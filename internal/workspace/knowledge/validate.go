package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

var (
	packIDPattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
	semverPattern   = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	knownStrategies = map[string]bool{"": true, "field_path": true, "regex": true}
	knownKinds      = map[string]bool{"service_repo": true, "infra_repo": true, "any": true}
	knownMappings   = map[string]bool{
		"service_name": true, "dns_aliases": true, "http_paths": true,
		"iam_role": true, "database_connection": true,
		"queue_identifiers": true, "queue_ownership": true,
	}
)

// ValidatePack strictly parses JSON or YAML and validates every executable
// field, including regex compilation and path traversal protection.
func ValidatePack(body []byte, extension string) (*Pack, []ValidationError) {
	var pack Pack
	var err error
	if strings.EqualFold(extension, ".json") || firstNonSpace(body) == '{' {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		err = dec.Decode(&pack)
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		err = dec.Decode(&pack)
	}
	if err != nil {
		return nil, []ValidationError{{Field: "", Message: "invalid pack document: " + err.Error()}}
	}
	errs := validateStructure(&pack)
	if len(errs) > 0 {
		return nil, errs
	}
	return &pack, nil
}

func firstNonSpace(body []byte) byte {
	for _, b := range body {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return b
		}
	}
	return 0
}

func validateStructure(pack *Pack) []ValidationError {
	var errs []ValidationError
	required := []struct{ field, value string }{
		{"api_version", pack.APIVersion}, {"kind", pack.Kind}, {"id", pack.ID},
		{"name", pack.Name}, {"version", pack.Version}, {"license", pack.License},
		{"compatibility", pack.Compatibility}, {"applies_to.kind", pack.AppliesTo.Kind},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			errs = append(errs, ValidationError{Field: item.field, Message: item.field + " is required"})
		}
	}
	if pack.APIVersion != "" && pack.APIVersion != APIVersion {
		errs = append(errs, ValidationError{Field: "api_version", Message: fmt.Sprintf("unsupported API version %q (want %s)", pack.APIVersion, APIVersion)})
	}
	if pack.Kind != "" && pack.Kind != Kind {
		errs = append(errs, ValidationError{Field: "kind", Message: fmt.Sprintf("unsupported kind %q (want %s)", pack.Kind, Kind)})
	}
	if pack.ID != "" && !packIDPattern.MatchString(pack.ID) {
		errs = append(errs, ValidationError{Field: "id", Message: "must contain only lowercase letters, digits, dots, and hyphens"})
	}
	if pack.Version != "" && !semverPattern.MatchString(pack.Version) {
		errs = append(errs, ValidationError{Field: "version", Message: "must be a semantic version such as 1.0.0"})
	}
	if pack.Compatibility != "" {
		compatible, err := Compatible(pack.Compatibility, RuntimeVersion)
		if err != nil {
			errs = append(errs, ValidationError{Field: "compatibility", Message: err.Error()})
		} else if !compatible {
			errs = append(errs, ValidationError{Field: "compatibility", Message: fmt.Sprintf("requires %s but this runtime implements %s", pack.Compatibility, RuntimeVersion)})
		}
	}
	if pack.AppliesTo.Kind != "" && !knownKinds[pack.AppliesTo.Kind] {
		errs = append(errs, ValidationError{Field: "applies_to.kind", Message: fmt.Sprintf("unknown kind %q (want service_repo, infra_repo, or any)", pack.AppliesTo.Kind)})
	}
	for field, value := range map[string]string{
		"applies_to.match.has_path": pack.AppliesTo.Match.HasPath,
		"applies_to.match.has_file": pack.AppliesTo.Match.HasFile,
	} {
		if value != "" && !safeRelativePath(value) {
			errs = append(errs, ValidationError{Field: field, Message: "must be a safe relative path"})
		}
	}
	if len(pack.Extractions) == 0 {
		errs = append(errs, ValidationError{Field: "extractions", Message: "at least one extraction is required"})
	}
	names := map[string]bool{}
	for i, extraction := range pack.Extractions {
		base := fmt.Sprintf("extractions[%d]", i)
		if strings.TrimSpace(extraction.Name) == "" {
			errs = append(errs, ValidationError{Field: base + ".name", Message: "extraction name is required"})
		} else if names[extraction.Name] {
			errs = append(errs, ValidationError{Field: base + ".name", Message: "extraction name must be unique"})
		}
		names[extraction.Name] = true
		if !knownStrategies[extraction.Strategy] {
			errs = append(errs, ValidationError{Field: base + ".strategy", Message: fmt.Sprintf("unknown strategy %q (want field_path or regex)", extraction.Strategy)})
		}
		if extraction.Source.Glob == "" {
			errs = append(errs, ValidationError{Field: base + ".source.glob", Message: "source.glob is required"})
		} else if !safeRelativePath(extraction.Source.Glob) {
			errs = append(errs, ValidationError{Field: base + ".source.glob", Message: "must be a safe relative glob"})
		}
		if len(extraction.Extract) == 0 {
			errs = append(errs, ValidationError{Field: base + ".extract", Message: "at least one extract field is required"})
		}
		for j, field := range extraction.Extract {
			fieldBase := fmt.Sprintf("%s.extract[%d]", base, j)
			if field.MapsTo == "" {
				errs = append(errs, ValidationError{Field: fieldBase + ".maps_to", Message: "maps_to is required"})
			} else if !knownMappings[field.MapsTo] && !strings.HasPrefix(field.MapsTo, "metadata.") {
				errs = append(errs, ValidationError{Field: fieldBase + ".maps_to", Message: "unknown mapping; custom values must use metadata.<name>"})
			}
			if extraction.Strategy == "regex" {
				if field.Pattern == "" {
					errs = append(errs, ValidationError{Field: fieldBase + ".pattern", Message: "regex strategy requires a pattern"})
				} else if _, err := regexp.Compile(field.Pattern); err != nil {
					errs = append(errs, ValidationError{Field: fieldBase + ".pattern", Message: "invalid regular expression: " + err.Error()})
				}
			} else if field.Field == "" {
				errs = append(errs, ValidationError{Field: fieldBase + ".field", Message: "field_path strategy requires a field"})
			}
		}
	}
	for i, ignored := range pack.Ignore {
		if !safeRelativePath(ignored) {
			errs = append(errs, ValidationError{Field: fmt.Sprintf("ignore[%d]", i), Message: "must be a safe relative glob"})
		}
	}
	for i, test := range pack.Tests {
		base := fmt.Sprintf("tests[%d]", i)
		if test.Name == "" {
			errs = append(errs, ValidationError{Field: base + ".name", Message: "test name is required"})
		}
		if test.Fixture == "" || !safeRelativePath(test.Fixture) {
			errs = append(errs, ValidationError{Field: base + ".fixture", Message: "fixture must be a safe relative path"})
		}
		if test.RepoKind != "" && !knownKinds[test.RepoKind] {
			errs = append(errs, ValidationError{Field: base + ".repo_kind", Message: "unknown repository kind"})
		}
	}
	ruleNames := map[string]bool{}
	for i, rule := range pack.ResolutionRules {
		base := fmt.Sprintf("resolution_rules[%d]", i)
		if rule.Name == "" {
			errs = append(errs, ValidationError{Field: base + ".name", Message: "rule name is required"})
		} else if ruleNames[rule.Name] {
			errs = append(errs, ValidationError{Field: base + ".name", Message: "rule name must be unique"})
		}
		ruleNames[rule.Name] = true
		if rule.TargetPattern == "" {
			errs = append(errs, ValidationError{Field: base + ".target_pattern", Message: "target_pattern is required"})
		} else if _, err := regexp.Compile(rule.TargetPattern); err != nil {
			errs = append(errs, ValidationError{Field: base + ".target_pattern", Message: "invalid regular expression: " + err.Error()})
		}
		if rule.DependencyType != "" {
			if _, err := filepath.Match(rule.DependencyType, "dependency"); err != nil {
				errs = append(errs, ValidationError{Field: base + ".dependency_type", Message: "invalid glob: " + err.Error()})
			}
		}
		if rule.TargetService == "" {
			errs = append(errs, ValidationError{Field: base + ".target_service", Message: "target_service is required"})
		}
		if rule.Confidence < 0 || rule.Confidence > 1 {
			errs = append(errs, ValidationError{Field: base + ".confidence", Message: "confidence must be between 0 and 1"})
		}
	}
	return errs
}

func ResolutionRules(packs []*Pack) []ResolutionRule {
	var rules []ResolutionRule
	for _, pack := range packs {
		for _, rule := range pack.ResolutionRules {
			rule.PackID = pack.ID
			rule.PackPriority = pack.Priority
			if rule.Confidence == 0 {
				rule.Confidence = 0.98
			}
			rules = append(rules, rule)
		}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].PackPriority != rules[j].PackPriority {
			return rules[i].PackPriority > rules[j].PackPriority
		}
		if rules[i].PackID != rules[j].PackID {
			return rules[i].PackID < rules[j].PackID
		}
		return rules[i].Name < rules[j].Name
	})
	return rules
}

func safeRelativePath(path string) bool {
	if filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// ValidateSet prevents ambiguous installations and runtime order.
func ValidateSet(packs []*Pack) []ValidationError {
	seen := map[string]string{}
	var errs []ValidationError
	for _, pack := range packs {
		key := pack.ID + "@" + pack.Version
		if source, exists := seen[key]; exists {
			errs = append(errs, ValidationError{Field: pack.ID, Message: fmt.Sprintf("duplicate pack %s (also loaded from %s)", key, source)})
		}
		seen[key] = pack.SourcePath
	}
	return errs
}

func FormatValidationErrors(errs []ValidationError) string {
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	parts := make([]string, len(errs))
	for i, err := range errs {
		if err.Field == "" {
			parts[i] = err.Message
		} else {
			parts[i] = err.Field + ": " + err.Message
		}
	}
	return strings.Join(parts, "; ")
}
