// Package extractors implements deterministic knowledge-pack extraction.
package extractors

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExtractFieldPath reads a JSON or YAML file and traverses a dot-separated
// path. Numeric components index arrays (for example ingress.hosts.0).
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
	current := document
	for _, part := range strings.Split(fieldPath, ".") {
		switch value := current.(type) {
		case map[string]any:
			next, exists := value[part]
			if !exists {
				return nil, fmt.Errorf("field %q not found", part)
			}
			current = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return nil, fmt.Errorf("invalid array index %q", part)
			}
			current = value[index]
		default:
			return nil, fmt.Errorf("cannot navigate into %T at %q", current, part)
		}
	}
	return current, nil
}
