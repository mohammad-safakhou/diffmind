// Package knowledge implements deterministic knowledge packs. Packs teach
// DiffMind organization-specific conventions without requiring an LLM.
package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"gopkg.in/yaml.v3"
)

const (
	APIVersion = "diffmind.dev/v1alpha1"
	Kind       = "KnowledgePack"
)

// Pack is a versioned, portable set of deterministic extraction rules.
type Pack struct {
	APIVersion      string           `json:"api_version" yaml:"api_version"`
	Kind            string           `json:"kind" yaml:"kind"`
	ID              string           `json:"id" yaml:"id"`
	Name            string           `json:"name" yaml:"name"`
	Description     string           `json:"description,omitempty" yaml:"description,omitempty"`
	Version         string           `json:"version" yaml:"version"`
	License         string           `json:"license" yaml:"license"`
	Compatibility   string           `json:"compatibility" yaml:"compatibility"`
	Priority        int              `json:"priority,omitempty" yaml:"priority,omitempty"`
	AppliesTo       AppliesTo        `json:"applies_to" yaml:"applies_to"`
	Ignore          []string         `json:"ignore,omitempty" yaml:"ignore,omitempty"`
	Extractions     []Extraction     `json:"extractions" yaml:"extractions"`
	ResolutionRules []ResolutionRule `json:"resolution_rules,omitempty" yaml:"resolution_rules,omitempty"`
	Detectors       []Detector       `json:"detectors,omitempty" yaml:"detectors,omitempty"`
	Tests           []TestCase       `json:"tests,omitempty" yaml:"tests,omitempty"`
	GraphTests      []GraphTest      `json:"graph_tests,omitempty" yaml:"graph_tests,omitempty"`
	SourcePath      string           `json:"-" yaml:"-"`
}

type AppliesTo struct {
	Kind  string      `json:"kind" yaml:"kind"`
	Match MatchConfig `json:"match,omitempty" yaml:"match,omitempty"`
}

type MatchConfig struct {
	HasPath  string `json:"has_path,omitempty" yaml:"has_path,omitempty"`
	HasFile  string `json:"has_file,omitempty" yaml:"has_file,omitempty"`
	NameLike string `json:"name_like,omitempty" yaml:"name_like,omitempty"`
}

type Extraction struct {
	Name        string           `json:"name" yaml:"name"`
	Description string           `json:"description,omitempty" yaml:"description,omitempty"`
	Source      ExtractionSource `json:"source" yaml:"source"`
	Strategy    string           `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	Extract     []ExtractField   `json:"extract" yaml:"extract"`
}

type ExtractionSource struct {
	Glob string `json:"glob" yaml:"glob"`
}

type ExtractField struct {
	Field   string `json:"field,omitempty" yaml:"field,omitempty"`
	Pattern string `json:"pattern,omitempty" yaml:"pattern,omitempty"`
	MapsTo  string `json:"maps_to" yaml:"maps_to"`
}

// ResolutionRule maps a dependency target pattern to a registered service.
// TargetService may use regular-expression expansions such as "$1-service".
type ResolutionRule struct {
	Name           string  `json:"name" yaml:"name"`
	DependencyType string  `json:"dependency_type,omitempty" yaml:"dependency_type,omitempty"`
	TargetPattern  string  `json:"target_pattern" yaml:"target_pattern"`
	TargetService  string  `json:"target_service" yaml:"target_service"`
	Confidence     float64 `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	PackID         string  `json:"-" yaml:"-"`
	PackPriority   int     `json:"-" yaml:"-"`
}

// TestCase points at a synthetic repository fixture and its expected identity.
type TestCase struct {
	Name         string              `json:"name" yaml:"name"`
	Fixture      string              `json:"fixture" yaml:"fixture"`
	RepoKind     string              `json:"repo_kind,omitempty" yaml:"repo_kind,omitempty"`
	Expected     ExpectedIdentity    `json:"expected" yaml:"expected"`
	Dependencies []ExpectedDetection `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	Exposures    []ExpectedDetection `json:"exposures,omitempty" yaml:"exposures,omitempty"`
}

// Detector emits one evidence-backed relationship per scalar field or regex
// match. It never executes repository code or substitutes environment variables.
type Detector struct {
	Name     string           `json:"name" yaml:"name"`
	Type     string           `json:"type" yaml:"type"`
	Source   ExtractionSource `json:"source" yaml:"source"`
	Strategy string           `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	Field    string           `json:"field,omitempty" yaml:"field,omitempty"`
	Pattern  string           `json:"pattern,omitempty" yaml:"pattern,omitempty"`
	Platform string           `json:"platform,omitempty" yaml:"platform,omitempty"`
}

type ExpectedDetection struct {
	Type   string `json:"type" yaml:"type"`
	Target string `json:"target" yaml:"target"`
	File   string `json:"file" yaml:"file"`
	Line   int    `json:"line" yaml:"line"`
}

// GraphTest uses several synthetic repositories and asserts the complete edge
// set, including external/resource edges (not merely a subset of matches).
type GraphTest struct {
	Name         string                `json:"name" yaml:"name"`
	Repositories []GraphTestRepository `json:"repositories" yaml:"repositories"`
	Edges        []ExpectedEdge        `json:"edges" yaml:"edges"`
}

type GraphTestRepository struct {
	Name    string `json:"name" yaml:"name"`
	Fixture string `json:"fixture" yaml:"fixture"`
}

type ExpectedEdge struct {
	From string `json:"from" yaml:"from"`
	To   string `json:"to" yaml:"to"`
	Type string `json:"type" yaml:"type"`
}

type ExpectedIdentity struct {
	ServiceName string                `json:"service_name" yaml:"service_name"`
	Aliases     []model.IdentityAlias `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Resources   []model.OwnedResource `json:"resources,omitempty" yaml:"resources,omitempty"`
	Metadata    map[string]string     `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type ExtractionResult struct {
	PackID         string         `json:"pack_id"`
	PackVersion    string         `json:"pack_version"`
	PackPriority   int            `json:"pack_priority"`
	ExtractionName string         `json:"extraction_name"`
	SourceFile     string         `json:"source_file"`
	Values         map[string]any `json:"values"`
}

func LoadPack(path string) (*Pack, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pack %s: %w", path, err)
	}
	pack, validation := ValidatePack(body, filepath.Ext(path))
	if len(validation) > 0 {
		return nil, fmt.Errorf("validate pack %s: %s", path, FormatValidationErrors(validation))
	}
	pack.SourcePath, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve pack %s: %w", path, err)
	}
	return pack, nil
}

// LoadPacksFromDirs recursively loads manifests. Missing directories are
// optional, but malformed manifests are always reported rather than skipped.
func LoadPacksFromDirs(dirs []string) ([]*Pack, error) {
	var paths []string
	var errs []error
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			name := strings.ToLower(entry.Name())
			if name == "pack.yaml" || name == "pack.yml" || name == "pack.json" {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("scan %s: %w", dir, err))
		}
	}
	sort.Strings(paths)
	packs := make([]*Pack, 0, len(paths))
	for _, path := range paths {
		pack, err := LoadPack(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		packs = append(packs, pack)
	}
	if setErrs := ValidateSet(packs); len(setErrs) > 0 {
		errs = append(errs, errors.New(FormatValidationErrors(setErrs)))
	}
	sort.SliceStable(packs, func(i, j int) bool {
		if packs[i].Priority != packs[j].Priority {
			return packs[i].Priority > packs[j].Priority
		}
		return packs[i].ID < packs[j].ID
	})
	return packs, errors.Join(errs...)
}

func MarshalYAML(pack *Pack) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(pack); err != nil {
		return nil, err
	}
	return out.Bytes(), enc.Close()
}

// ToIdentity deterministically merges extraction output by priority.
func ToIdentity(serviceName, repoPath string, results []ExtractionResult) (model.ServiceIdentity, error) {
	id := model.ServiceIdentity{ServiceName: serviceName, RepoPath: repoPath, Metadata: map[string]string{}}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].PackPriority != results[j].PackPriority {
			return results[i].PackPriority > results[j].PackPriority
		}
		if results[i].PackID != results[j].PackID {
			return results[i].PackID < results[j].PackID
		}
		return results[i].ExtractionName < results[j].ExtractionName
	})
	seen := map[string]bool{}
	metadataPriority := map[string]int{}
	metadataPack := map[string]string{}
	servicePriority := -int(^uint(0)>>1) - 1
	servicePack := "fallback"
	for _, result := range results {
		for mapsTo, value := range result.Values {
			switch mapsTo {
			case "service_name":
				candidate := firstString(value)
				if candidate == "" {
					continue
				}
				if result.PackPriority == servicePriority && candidate != id.ServiceName {
					return id, fmt.Errorf("knowledge pack conflict: %s and %s have priority %d but derive different service names %q and %q", servicePack, result.PackID, result.PackPriority, id.ServiceName, candidate)
				}
				if result.PackPriority > servicePriority {
					id.ServiceName, servicePriority, servicePack = candidate, result.PackPriority, result.PackID
				}
			case "dns_aliases", "http_paths", "iam_role":
				kind := map[string]string{"dns_aliases": "dns", "http_paths": "http_path", "iam_role": "iam_role"}[mapsTo]
				for _, alias := range toStringSlice(value) {
					key := kind + ":" + alias
					if !seen[key] {
						seen[key] = true
						id.Aliases = append(id.Aliases, model.IdentityAlias{Kind: kind, Value: alias})
					}
				}
			case "database_connection", "queue_identifiers":
				kind := map[string]string{"database_connection": "database", "queue_identifiers": "queue"}[mapsTo]
				for _, resource := range toStringSlice(value) {
					key := "resource:" + kind + ":" + resource
					if !seen[key] {
						seen[key] = true
						id.Resources = append(id.Resources, model.OwnedResource{Kind: kind, Identifier: resource, Role: "owner"})
					}
				}
			case "queue_ownership":
				// Infra ownership is handled by the orchestrator.
			default:
				metadataKey := strings.TrimPrefix(mapsTo, "metadata.")
				candidate := firstString(value)
				if candidate == "" {
					continue
				}
				priority, exists := metadataPriority[metadataKey]
				if exists && priority == result.PackPriority && id.Metadata[metadataKey] != candidate {
					return id, fmt.Errorf("knowledge pack conflict: %s and %s have priority %d but derive different metadata.%s values %q and %q",
						metadataPack[metadataKey], result.PackID, priority, metadataKey, id.Metadata[metadataKey], candidate)
				}
				if !exists || result.PackPriority > priority {
					id.Metadata[metadataKey] = candidate
					metadataPriority[metadataKey] = result.PackPriority
					metadataPack[metadataKey] = result.PackID
				}
			}
		}
	}
	return id, nil
}

func firstString(value any) string {
	values := toStringSlice(value)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func toStringSlice(value any) []string {
	var values []string
	switch value := value.(type) {
	case []string:
		values = value
	case string:
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, "[") {
			var items []any
			if json.Unmarshal([]byte(trimmed), &items) == nil {
				return toStringSlice(items)
			}
		}
		values = []string{trimmed}
	case []any:
		for _, item := range value {
			values = append(values, toStringSlice(item)...)
		}
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool, json.Number:
		values = []string{fmt.Sprint(value)}
	}
	seen := map[string]bool{}
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "[]" || value == "{}" || value == "null" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
