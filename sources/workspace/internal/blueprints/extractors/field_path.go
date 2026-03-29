// Package extractors implements the deterministic extraction strategies
// for blueprints (field_path, regex).
package extractors

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ExtractFieldPath reads a JSON/YAML file (as JSON) and extracts a value
// at the given dot-separated field path.
// For example, "ingress.hosts" on {"ingress": {"hosts": ["a.com"]}} returns ["a.com"].
func ExtractFieldPath(filePath, fieldPath string) (any, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", filePath, err)
	}

	// Try JSON first.
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		// Try to parse as YAML-like (simple key: value).
		doc = parseSimpleYAML(data)
		if doc == nil {
			return nil, fmt.Errorf("cannot parse %s as JSON or simple YAML: %w", filePath, err)
		}
	}

	return navigateFieldPath(doc, fieldPath)
}

// navigateFieldPath traverses a nested map using a dot-separated path.
func navigateFieldPath(doc map[string]any, path string) (any, error) {
	parts := strings.Split(path, ".")
	var current any = doc
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field %q not found", part)
			}
			current = val
		default:
			return nil, fmt.Errorf("cannot navigate into non-map at %q", part)
		}
	}
	return current, nil
}

// parseSimpleYAML does a very basic key:value parse for flat and one-level
// nested YAML files. This is intentionally minimal — complex YAML should
// use the LLM strategy or a proper YAML parser (added as needed).
func parseSimpleYAML(data []byte) map[string]any {
	result := make(map[string]any)
	lines := strings.Split(string(data), "\n")
	var currentSection string

	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" || strings.HasPrefix(strings.TrimSpace(trimmed), "#") {
			continue
		}

		indent := len(trimmed) - len(strings.TrimLeft(trimmed, " \t"))
		trimmed = strings.TrimSpace(trimmed)

		if !strings.Contains(trimmed, ":") {
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if indent == 0 {
			if value == "" {
				// Start of a section.
				currentSection = key
				if _, ok := result[currentSection]; !ok {
					result[currentSection] = make(map[string]any)
				}
			} else {
				result[key] = resolveValue(value)
				currentSection = ""
			}
		} else if currentSection != "" {
			section, ok := result[currentSection].(map[string]any)
			if !ok {
				section = make(map[string]any)
				result[currentSection] = section
			}
			section[key] = resolveValue(value)
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

func resolveValue(v string) any {
	// Handle simple YAML list items.
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		inner := strings.TrimPrefix(strings.TrimSuffix(v, "]"), "[")
		items := strings.Split(inner, ",")
		var out []any
		for _, item := range items {
			out = append(out, strings.TrimSpace(strings.Trim(strings.TrimSpace(item), `"'`)))
		}
		return out
	}
	// Strip quotes.
	v = strings.Trim(v, `"'`)
	return v
}
