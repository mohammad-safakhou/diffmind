// Package extractors implements deterministic knowledge-pack extraction.
package extractors

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExtractFieldPath reads a JSON or YAML file and traverses a dot-separated
// path. Numeric components index arrays (for example ingress.hosts.0). A '*'
// component traverses every mapping value or sequence element and returns the
// flattened matches, matching the relationship-detector field-path contract.
func ExtractFieldPath(filePath, fieldPath string) (any, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", filePath, err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		if err := yaml.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("parse %s as JSON or YAML: %w", filePath, err)
		}
	}
	return extractPath(document, strings.Split(fieldPath, "."))
}

func extractPath(current any, parts []string) (any, error) {
	if len(parts) == 0 {
		return current, nil
	}
	part := parts[0]
	if part == "*" {
		var values []any
		switch value := current.(type) {
		case map[string]any:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				values = append(values, value[key])
			}
		case []any:
			values = append(values, value...)
		default:
			return nil, fmt.Errorf("wildcard cannot navigate into %T", current)
		}
		out := make([]any, 0, len(values))
		for _, value := range values {
			match, err := extractPath(value, parts[1:])
			if err != nil {
				return nil, err
			}
			if nested, ok := match.([]any); ok {
				out = append(out, nested...)
			} else {
				out = append(out, match)
			}
		}
		return out, nil
	}
	switch value := current.(type) {
	case map[string]any:
		next, exists := value[part]
		if !exists {
			return nil, fmt.Errorf("field %q not found", part)
		}
		return extractPath(next, parts[1:])
	case []any:
		index, err := strconv.Atoi(part)
		if err != nil || index < 0 || index >= len(value) {
			return nil, fmt.Errorf("invalid array index %q", part)
		}
		return extractPath(value[index], parts[1:])
	default:
		return nil, fmt.Errorf("cannot navigate into %T at %q", current, part)
	}
}
