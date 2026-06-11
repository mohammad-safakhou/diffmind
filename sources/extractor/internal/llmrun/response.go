package llmrun

import (
	"encoding/json"
	"sort"
	"strings"
)

// ScrapeJSONObject recovers the first JSON object from a provider's free-text
// response. Providers may return the object directly, inside a markdown fence,
// or surrounded by explanatory prose.
func ScrapeJSONObject(text string) map[string]any {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if object, ok := tryJSONObject(text); ok {
		return object
	}
	for _, block := range extractFencedBlocks(text) {
		if object, ok := tryJSONObject(block); ok {
			return object
		}
	}
	if candidate, ok := scanBalancedObject(text); ok {
		if object, ok := tryJSONObject(candidate); ok {
			return object
		}
	}
	return nil
}

func IsNoStructuredPayload(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no structured payload")
}

func PreviewBytes(value []byte, limit int) string {
	return PreviewString(string(value), limit)
}

func PreviewString(value string, limit int) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\t", "\\t")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\u2026"
}

func MapKeys(value map[string]any) []string {
	if value == nil {
		return nil
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func tryJSONObject(value string) (map[string]any, bool) {
	var object map[string]any
	if err := json.Unmarshal([]byte(value), &object); err != nil {
		return nil, false
	}
	return object, true
}

func extractFencedBlocks(value string) []string {
	var blocks []string
	for _, fence := range []string{"```json", "```"} {
		offset := 0
		for {
			start := strings.Index(value[offset:], fence)
			if start < 0 {
				break
			}
			start += offset + len(fence)
			if start < len(value) && value[start] == '\n' {
				start++
			}
			end := strings.Index(value[start:], "```")
			if end < 0 {
				break
			}
			blocks = append(blocks, strings.TrimSpace(value[start:start+end]))
			offset = start + end + 3
		}
		if len(blocks) > 0 {
			break
		}
	}
	return blocks
}

func scanBalancedObject(value string) (string, bool) {
	start := strings.Index(value, "{")
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(value); index++ {
		character := value[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return value[start : index+1], true
			}
		}
	}
	return "", false
}
