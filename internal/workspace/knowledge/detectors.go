package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
	"gopkg.in/yaml.v3"
)

const maxDetectorFileBytes = 2 << 20
const maxDetections = 10000

type DetectionResult struct {
	Dependencies []model.Dependency    `json:"dependencies"`
	Exposures    []model.Exposure      `json:"exposures"`
	Skipped      []DetectionDiagnostic `json:"skipped,omitempty"`
}

type DetectionDiagnostic struct {
	Detector string `json:"detector"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Reason   string `json:"reason"`
}

// Detect evaluates only explicit opt-in rules, returning errors rather than a
// deceptively complete graph when selected inputs cannot be read or parsed.
func Detect(ctx context.Context, pack *Pack, repoPath, service string) (DetectionResult, error) {
	var out DetectionResult
	for _, rule := range pack.Detectors {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		files, err := detectorFiles(ctx, repoPath, rule.Source.Glob, pack.Ignore)
		if err != nil {
			return out, fmt.Errorf("pack %s detector %s: %w", pack.ID, rule.Name, err)
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			body, err := readDetectorFile(file)
			if err != nil {
				return out, err
			}
			values, err := detectValues(body, rule)
			rel, _ := filepath.Rel(repoPath, file)
			rel = filepath.ToSlash(rel)
			if err != nil {
				return out, fmt.Errorf("pack %s detector %s file %s: %w", pack.ID, rule.Name, rel, err)
			}
			for _, value := range values {
				if !literalTarget(value.target) {
					out.Skipped = append(out.Skipped, DetectionDiagnostic{rule.Name, rel, value.line, "empty or non-literal target"})
					continue
				}
				if len(out.Dependencies)+len(out.Exposures) >= maxDetections {
					return out, fmt.Errorf("pack %s exceeds %d detections", pack.ID, maxDetections)
				}
				entity, ok := detectionEntity(pack, rule, service, rel, value)
				if !ok {
					out.Skipped = append(out.Skipped, DetectionDiagnostic{rule.Name, rel, value.line, "unsupported or potentially sensitive target"})
					continue
				}
				if rule.Type == "queue_consumer" {
					out.Exposures = append(out.Exposures, model.Exposure{BaseEntity: entity})
				} else {
					out.Dependencies = append(out.Dependencies, model.Dependency{BaseEntity: entity})
				}
			}
		}
	}
	return out, ctx.Err()
}

type detectedValue struct {
	target string
	line   int
}

func detectValues(body []byte, rule Detector) ([]detectedValue, error) {
	var out []detectedValue
	if rule.Strategy == "regex" {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, err
		}
		group := re.SubexpIndex("target")
		if group < 1 {
			return nil, fmt.Errorf("missing target capture")
		}
		last, line := 0, 1
		for _, match := range re.FindAllSubmatchIndex(body, maxDetections+1) {
			start, end := match[2*group], match[2*group+1]
			if start >= 0 {
				line += bytes.Count(body[last:start], []byte{'\n'})
				last = start
				out = append(out, detectedValue{strings.TrimSpace(string(body[start:end])), line})
			}
		}
		return out, nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc yaml.Node
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid YAML/JSON")
		}
		if len(doc.Content) == 0 {
			continue
		}
		// Decode once for duplicate-key/type validation, but retain Nodes for lines.
		var check any
		if err := doc.Decode(&check); err != nil {
			return nil, fmt.Errorf("invalid YAML/JSON mapping")
		}
		nodes := fieldNodes(doc.Content[0], strings.Split(rule.Field, "."))
		for _, node := range nodes {
			if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
				continue
			}
			if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
				return nil, fmt.Errorf("field %s must select strings (aliases and objects are not targets)", rule.Field)
			}
			out = append(out, detectedValue{strings.TrimSpace(node.Value), node.Line})
			if len(out) > maxDetections {
				return nil, fmt.Errorf("too many selected fields")
			}
		}
	}
	return out, nil
}

// Wildcards traverse mappings/arrays in document order. A terminal sequence is
// a list of scalar targets; YAML aliases are deliberately not followed.
func fieldNodes(node *yaml.Node, parts []string) []*yaml.Node {
	if len(parts) == 0 {
		if node.Kind == yaml.SequenceNode {
			return node.Content
		}
		return []*yaml.Node{node}
	}
	var out []*yaml.Node
	part := parts[0]
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if part == "*" || node.Content[i].Value == part {
				out = append(out, fieldNodes(node.Content[i+1], parts[1:])...)
			}
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			if part == "*" || part == strconv.Itoa(i) {
				out = append(out, fieldNodes(child, parts[1:])...)
			}
		}
	}
	return out
}

func literalTarget(target string) bool {
	return target != "" && !strings.ContainsAny(target, "$\r\n\t {}<>`")
}

func detectionEntity(pack *Pack, rule Detector, service, file string, value detectedValue) (model.BaseEntity, bool) {
	location := model.Location{File: file, StartLine: value.line, EndLine: value.line}
	provenance := "knowledge_pack:" + pack.ID + "@" + pack.Version + "/" + rule.Name
	details := map[string]any{
		"detection_confidence": 0.9,
		"pack_id":              pack.ID, "pack_version": pack.Version, "detector": rule.Name,
		"target": value.target, "match_basis": "knowledge_pack", "source_locations": []model.Location{location},
		"evidence": []model.Evidence{{Location: location, Source: provenance}},
	}
	switch rule.Type {
	case "outbound_http":
		if strings.Contains(value.target, "://") {
			u, err := url.Parse(value.target)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return model.BaseEntity{}, false
			}
			details["host"], details["target_service"], details["url_template"], details["path"] = u.Hostname(), u.Hostname(), value.target, u.EscapedPath()
		} else {
			if strings.ContainsAny(value.target, "/:@?&#") {
				return model.BaseEntity{}, false
			}
			details["target_service"] = value.target
		}
	case "outbound_rpc":
		if strings.ContainsAny(value.target, "/@?&#") {
			return model.BaseEntity{}, false
		}
		details["target_service"] = value.target
	case "queue_publish", "queue_consumer":
		if strings.Contains(value.target, "://") || strings.ContainsAny(value.target, "@?&#") {
			return model.BaseEntity{}, false
		}
		details["queue"], details["destination"] = value.target, value.target
	default:
		return model.BaseEntity{}, false
	}
	if rule.Platform != "" {
		details["platform"] = rule.Platform
	}
	return model.BaseEntity{
		ID:   util.ContentHash("pack", pack.ID, pack.Version, rule.Name, service, file, strconv.Itoa(value.line), value.target),
		Type: rule.Type, Name: rule.Name + ": " + value.target, Service: service,
		Platform: rule.Platform, Instance: value.target, Summary: "Declared by knowledge pack " + pack.ID + " (not runtime reachability)",
		Locations: []model.Location{location}, Evidence: []model.Evidence{{Location: location, Source: provenance}},
		Confidence: 0.9, PluginSource: provenance, Details: details,
	}, true
}

func readDetectorFile(file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("detector input is not a regular file")
	}
	body, err := io.ReadAll(io.LimitReader(f, maxDetectorFileBytes+1))
	if len(body) > maxDetectorFileBytes {
		return nil, fmt.Errorf("detector input %s exceeds %d bytes", filepath.Base(file), maxDetectorFileBytes)
	}
	return body, err
}

func detectorFiles(ctx context.Context, root, pattern string, ignored []string) ([]string, error) {
	if !safeRelativePath(pattern) {
		return nil, fmt.Errorf("unsafe source glob")
	}
	var files []string
	// Restrict the walk to the literal prefix. Exact paths do not require a
	// traversal of build caches, vendored dependencies, or the whole repository.
	prefix := pattern
	if at := strings.IndexAny(pattern, "*?"); at >= 0 {
		prefix = pattern[:at]
		if slash := strings.LastIndex(prefix, "/"); slash >= 0 {
			prefix = prefix[:slash]
		} else {
			prefix = ""
		}
	}
	base := root
	if prefix != "" {
		for _, part := range strings.Split(prefix, "/") {
			base = filepath.Join(base, part)
			info, err := os.Lstat(base)
			if os.IsNotExist(err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("detector input path must not contain symlinks")
			}
		}
	}
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		for _, ignore := range ignored {
			if matchesGlob(rel, ignore) || (entry.IsDir() && matchesGlob(rel+"/", ignore)) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if !entry.IsDir() && matchesGlob(rel, pattern) {
			if !entry.Type().IsRegular() {
				return fmt.Errorf("detector input %s must be a regular file, not a symlink", rel)
			}
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// FixturePath confines tests to real directories inside the pack. This also
// prevents a symlink in a contributed fixture from reading a developer's files.
func FixturePath(pack *Pack, fixture string) (string, error) {
	if !safeRelativePath(fixture) {
		return "", fmt.Errorf("unsafe fixture path")
	}
	root := filepath.Dir(pack.SourcePath)
	path := root
	for _, part := range strings.Split(fixture, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("fixture %s must be a real directory", fixture)
		}
	}
	if _, err := ContentDigest(path); err != nil {
		return "", err
	}
	return path, nil
}
