package opencode

import (
	"encoding/json"
	"regexp"
	"strings"
)

func previewBody(raw []byte, maxLen int) string {
	if len(raw) == 0 {
		return "<empty>"
	}
	s := string(raw)
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	if len(s) > maxLen {
		s = s[:maxLen] + "\u2026"
	}
	return s
}

func parseStructuredResponse(raw []byte) (map[string]any, string) {
	m, detail, _ := parseStructuredResponseVerbose(raw)
	return m, detail
}

func parseStructuredResponseVerbose(raw []byte) (map[string]any, string, string) {
	var out struct {
		Info struct {
			Structured any `json:"structured"`
			Error      struct {
				Name    string         `json:"name"`
				Message string         `json:"message"`
				Data    map[string]any `json:"data"`
			} `json:"error"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", ""
	}
	var texts []string
	for _, part := range out.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	textBody := strings.Join(texts, "\n")
	if out.Info.Structured != nil {
		if mapped, ok := toMap(out.Info.Structured); ok {
			return mapped, "", textBody
		}
	}
	for _, part := range out.Parts {
		if part.Type != "text" || strings.TrimSpace(part.Text) == "" {
			continue
		}
		if mapped, ok := parseAnyJSONMap(part.Text); ok {
			return mapped, "", textBody
		}
	}
	if strings.TrimSpace(out.Info.Error.Name) != "" || strings.TrimSpace(out.Info.Error.Message) != "" || len(out.Info.Error.Data) > 0 {
		detail := strings.TrimSpace(out.Info.Error.Name + ": " + out.Info.Error.Message)
		if message, ok := out.Info.Error.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
			if detail == "" {
				detail = strings.TrimSpace(message)
			} else {
				detail += " (" + strings.TrimSpace(message) + ")"
			}
		}
		return nil, detail, textBody
	}
	return nil, "", textBody
}

func toMap(value any) (map[string]any, bool) {
	if mapped, ok := value.(map[string]any); ok {
		return mapped, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var mapped map[string]any
	if err := json.Unmarshal(data, &mapped); err != nil {
		return nil, false
	}
	return mapped, true
}

func parseAnyJSONMap(value string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(value)
	if mapped, ok := tryJSONMap(trimmed); ok {
		return mapped, true
	}
	for _, block := range extractCodeFenceBlocks(trimmed) {
		if mapped, ok := tryJSONMap(block); ok {
			return mapped, true
		}
	}
	if candidate, ok := extractFirstJSONObject(trimmed); ok {
		if mapped, ok := tryJSONMap(candidate); ok {
			return mapped, true
		}
	}
	return nil, false
}

func tryJSONMap(value string) (map[string]any, bool) {
	var mapped map[string]any
	if err := json.Unmarshal([]byte(value), &mapped); err == nil {
		return mapped, true
	}
	return nil, false
}

var fencedJSONRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

func extractCodeFenceBlocks(value string) []string {
	matches := fencedJSONRe.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) >= 2 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	return out
}

func extractFirstJSONObject(value string) (string, bool) {
	start := strings.Index(value, "{")
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(value); i++ {
		ch := value[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '{' {
			depth++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return value[start : i+1], true
			}
		}
	}
	return "", false
}
