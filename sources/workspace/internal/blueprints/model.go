// Package blueprints implements the extraction blueprint system.
// Blueprints teach DiffMind how to extract service identity information
// from repository files (Helm values, Terraform, Docker Compose, etc.).
package blueprints

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// Blueprint is a declarative rule set that tells DiffMind how to extract
// identity information from repositories matching certain criteria.
type Blueprint struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Version     string       `json:"version"`
	AppliesTo   AppliesTo    `json:"applies_to"`
	Extractions []Extraction `json:"extractions"`
}

// AppliesTo describes which repositories this blueprint applies to.
type AppliesTo struct {
	Kind  string      `json:"kind"` // service_repo, infra_repo, any
	Match MatchConfig `json:"match"`
}

// MatchConfig defines the matching criteria.
type MatchConfig struct {
	HasPath  string `json:"has_path,omitempty"`  // repo must contain this path
	HasFile  string `json:"has_file,omitempty"`  // repo must contain this file
	NameLike string `json:"name_like,omitempty"` // repo name matches glob
}

// Extraction describes a single piece of information to extract.
type Extraction struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Source      ExtractionSource `json:"source"`
	Strategy    string           `json:"strategy,omitempty"`    // field_path (default), regex, llm
	PromptHint  string           `json:"prompt_hint,omitempty"` // hint for LLM strategy
	Extract     []ExtractField   `json:"extract"`
}

// ExtractionSource describes where to look for data.
type ExtractionSource struct {
	Glob string `json:"glob"` // file glob pattern relative to repo root
}

// ExtractField describes a single field to extract from the source.
type ExtractField struct {
	Field   string `json:"field,omitempty"`   // YAML/JSON field path (e.g. "iamRole", "ingress.hosts")
	Pattern string `json:"pattern,omitempty"` // regex pattern for regex strategy
	MapsTo  string `json:"maps_to"`           // what this field represents: service_name, dns_aliases, etc.
}

// ExtractionResult holds the output of running one extraction.
type ExtractionResult struct {
	BlueprintName  string         `json:"blueprint_name"`
	ExtractionName string         `json:"extraction_name"`
	SourceFile     string         `json:"source_file"`
	Values         map[string]any `json:"values"` // maps_to → extracted value
}

// LoadBlueprint reads a single blueprint from a JSON file.
func LoadBlueprint(path string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read blueprint %s: %w", path, err)
	}
	var bp Blueprint
	if err := json.Unmarshal(data, &bp); err != nil {
		return nil, fmt.Errorf("parse blueprint %s: %w", path, err)
	}
	if bp.Name == "" {
		return nil, fmt.Errorf("blueprint %s has no name", path)
	}
	return &bp, nil
}

// LoadBlueprintsFromDirs scans directories for .json blueprint files.
func LoadBlueprintsFromDirs(dirs []string) ([]*Blueprint, error) {
	var bps []*Blueprint
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			continue // directory may not exist yet
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			bp, err := LoadBlueprint(filepath.Join(abs, e.Name()))
			if err != nil {
				continue // skip bad blueprints
			}
			bps = append(bps, bp)
		}
	}
	return bps, nil
}

// ToIdentity converts a set of extraction results into a ServiceIdentity.
func ToIdentity(serviceName, repoPath string, results []ExtractionResult) model.ServiceIdentity {
	id := model.ServiceIdentity{
		ServiceName: serviceName,
		RepoPath:    repoPath,
		Metadata:    make(map[string]string),
	}

	seen := make(map[string]bool) // dedup aliases

	for _, r := range results {
		for mapsTo, val := range r.Values {
			switch mapsTo {
			case "service_name":
				if s := firstString(val); s != "" {
					id.ServiceName = s
				}
			case "dns_aliases":
				for _, alias := range toStringSlice(val) {
					key := "dns:" + alias
					if !seen[key] {
						seen[key] = true
						id.Aliases = append(id.Aliases, model.IdentityAlias{Kind: "dns", Value: alias})
					}
				}
			case "http_paths":
				for _, p := range toStringSlice(val) {
					key := "http_path:" + p
					if !seen[key] {
						seen[key] = true
						id.Aliases = append(id.Aliases, model.IdentityAlias{Kind: "http_path", Value: p})
					}
				}
			case "iam_role":
				if s := firstString(val); s != "" {
					key := "iam_role:" + s
					if !seen[key] {
						seen[key] = true
						id.Aliases = append(id.Aliases, model.IdentityAlias{Kind: "iam_role", Value: s})
					}
				}
			case "database_connection":
				for _, s := range toStringSlice(val) {
					id.Resources = append(id.Resources, model.OwnedResource{Kind: "database", Identifier: s, Role: "owner"})
				}
			case "queue_identifiers":
				for _, q := range toStringSlice(val) {
					id.Resources = append(id.Resources, model.OwnedResource{Kind: "queue", Identifier: q, Role: "owner"})
				}
			case "queue_ownership":
				// LLM-extracted: expect map[string]string of queue_name → service_name
				// handled at a higher level
			default:
				if s := firstString(val); s != "" {
					id.Metadata[mapsTo] = s
				}
			}
		}
	}
	return id
}

func firstString(v any) string {
	items := toStringSlice(v)
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func toStringSlice(v any) []string {
	switch val := v.(type) {
	case []string:
		return cleanStringSlice(val)
	case string:
		val = strings.TrimSpace(val)
		if strings.HasPrefix(val, "[") {
			var arr []any
			if err := json.Unmarshal([]byte(val), &arr); err == nil {
				return toStringSlice(arr)
			}
		}
		return cleanStringSlice([]string{val})
	case []any:
		var out []string
		for _, item := range val {
			out = append(out, toStringSlice(item)...)
		}
		return cleanStringSlice(out)
	}
	return nil
}

func cleanStringSlice(items []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || item == "[]" || item == "{}" || item == "null" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
