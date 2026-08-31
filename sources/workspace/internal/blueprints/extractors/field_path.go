// Package extractors implements the deterministic extraction strategies
// for blueprints (field_path, regex).
package extractors

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ExtractFieldPath reads a JSON/YAML file and extracts a value
// at the given dot-separated field path.
func ExtractFieldPath(filePath, fieldPath string) (any, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", filePath, err)
	}

	// Try JSON first.
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		// Try to parse as YAML.
		doc = parseYAML(data)
		if doc == nil {
			return nil, fmt.Errorf("cannot parse %s as JSON or YAML: %w", filePath, err)
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

// parseYAML is a recursive YAML parser that handles arbitrary nesting depth.
// It supports maps, lists (both - item and [inline] forms), and scalar values.
func parseYAML(data []byte) map[string]any {
	lines := strings.Split(string(data), "\n")
	result, _ := parseYAMLLines(lines, 0, 0)
	if len(result) == 0 {
		return nil
	}
	return result
}

func parseYAMLLines(lines []string, startIdx, minIndent int) (map[string]any, int) {
	result := make(map[string]any)
	i := startIdx

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimRight(line, "\r\n")

		// Skip empty lines and comments
		stripped := strings.TrimSpace(trimmed)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			i++
			continue
		}

		// Calculate indent
		indent := countIndent(trimmed)
		if indent < minIndent {
			// Back to parent scope
			return result, i
		}

		// List items at this level (- key: value)
		if strings.HasPrefix(stripped, "- ") {
			i++
			continue
		}

		// Key: value pairs
		colonIdx := strings.Index(stripped, ":")
		if colonIdx < 0 {
			i++
			continue
		}

		key := strings.TrimSpace(stripped[:colonIdx])
		value := strings.TrimSpace(stripped[colonIdx+1:])

		if key == "" {
			i++
			continue
		}

		if value == "" {
			// This is a nested map or list — look at next lines
			nextIndent := -1
			for j := i + 1; j < len(lines); j++ {
				nextLine := strings.TrimRight(lines[j], "\r\n")
				nextStripped := strings.TrimSpace(nextLine)
				if nextStripped == "" || strings.HasPrefix(nextStripped, "#") {
					continue
				}
				nextIndent = countIndent(nextLine)
				break
			}

			if nextIndent > indent {
				// Check if it's a list
				for j := i + 1; j < len(lines); j++ {
					nextLine := strings.TrimRight(lines[j], "\r\n")
					nextStripped := strings.TrimSpace(nextLine)
					if nextStripped == "" || strings.HasPrefix(nextStripped, "#") {
						continue
					}
					if strings.HasPrefix(nextStripped, "- ") {
						// It's a list
						list, newI := parseYAMLList(lines, i+1, nextIndent)
						result[key] = list
						i = newI
					} else {
						// It's a nested map
						nested, newI := parseYAMLLines(lines, i+1, nextIndent)
						result[key] = nested
						i = newI
					}
					break
				}
			} else {
				result[key] = ""
				i++
			}
		} else {
			// Scalar value
			result[key] = resolveValue(value)
			i++
		}
	}

	return result, i
}

func parseYAMLList(lines []string, startIdx, minIndent int) ([]any, int) {
	var items []any
	i := startIdx

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimRight(line, "\r\n")
		stripped := strings.TrimSpace(trimmed)

		if stripped == "" || strings.HasPrefix(stripped, "#") {
			i++
			continue
		}

		indent := countIndent(trimmed)
		if indent < minIndent {
			return items, i
		}

		if strings.HasPrefix(stripped, "- ") {
			itemContent := strings.TrimPrefix(stripped, "- ")
			itemContent = strings.TrimSpace(itemContent)
			if strings.Contains(itemContent, ":") {
				// It's a map item in a list
				innerMap := map[string]any{}
				colonIdx := strings.Index(itemContent, ":")
				k := strings.TrimSpace(itemContent[:colonIdx])
				v := strings.TrimSpace(itemContent[colonIdx+1:])
				if v != "" {
					innerMap[k] = resolveValue(v)
				}
				items = append(items, innerMap)
			} else {
				items = append(items, resolveValue(itemContent))
			}
			i++
		} else {
			return items, i
		}
	}

	return items, i
}

func countIndent(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
		} else if ch == '\t' {
			count += 2
		} else {
			break
		}
	}
	return count
}

func resolveValue(v string) any {
	v = strings.TrimSpace(v)
	// Handle inline list
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		inner := strings.TrimPrefix(strings.TrimSuffix(v, "]"), "[")
		items := strings.Split(inner, ",")
		var out []any
		for _, item := range items {
			out = append(out, strings.TrimSpace(strings.Trim(strings.TrimSpace(item), `"'`)))
		}
		return out
	}
	// Strip quotes
	if (strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`)) ||
		(strings.HasPrefix(v, `'`) && strings.HasSuffix(v, `'`)) {
		v = v[1 : len(v)-1]
	}
	// Boolean/null
	switch strings.ToLower(v) {
	case "true":
		return true
	case "false":
		return false
	case "null", "~":
		return nil
	}
	return v
}
