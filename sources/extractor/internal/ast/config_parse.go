package ast

import "strings"

func extractConfigEntries(src []byte, format string) []ConfigEntry {
	switch format {
	case "yaml":
		return parseYAMLEntries(src)
	case "json":
		return parseJSONEntries(src)
	case "properties", "env":
		return parsePropertiesEntries(src)
	case "toml":
		return parseTOMLEntries(src)
	default:
		return nil
	}
}

func parseYAMLEntries(src []byte) []ConfigEntry {
	var entries []ConfigEntry
	lines := strings.Split(string(src), "\n")
	var prefixStack []string
	var indentStack []int
	for lineNum, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		indent := len(line) - len(trimmed)
		for len(indentStack) > 0 && indent <= indentStack[len(indentStack)-1] {
			indentStack = indentStack[:len(indentStack)-1]
			prefixStack = prefixStack[:len(prefixStack)-1]
		}
		colonIdx := strings.Index(trimmed, ":")
		if colonIdx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:colonIdx])
		value := strings.TrimSpace(trimmed[colonIdx+1:])
		if key == "-" || strings.HasPrefix(key, "- ") {
			continue
		}
		value = strings.Trim(value, `"'`)
		fullKey := strings.Join(append(prefixStack, key), ".")
		if value == "" || value == "{}" || value == "[]" {
			prefixStack = append(prefixStack, key)
			indentStack = append(indentStack, indent)
			continue
		}
		entries = append(entries, ConfigEntry{Key: fullKey, Value: value, Line: lineNum + 1})
	}
	return entries
}

func parsePropertiesEntries(src []byte) []ConfigEntry {
	var entries []ConfigEntry
	for lineNum, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		sep := strings.IndexAny(line, "=:")
		if sep < 0 {
			continue
		}
		key := strings.TrimSpace(line[:sep])
		value := strings.TrimSpace(line[sep+1:])
		if key == "" {
			continue
		}
		entries = append(entries, ConfigEntry{Key: key, Value: value, Line: lineNum + 1})
	}
	return entries
}

func parseJSONEntries(src []byte) []ConfigEntry {
	var entries []ConfigEntry
	for lineNum, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, ":") || !strings.HasPrefix(line, `"`) {
			continue
		}
		closeQuote := strings.Index(line[1:], `"`)
		if closeQuote < 0 {
			continue
		}
		key := line[1 : closeQuote+1]
		rest := strings.TrimSpace(line[closeQuote+2:])
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(rest, ":"))
		value = strings.TrimRight(value, ",")
		value = strings.Trim(value, `"`)
		if value == "" || value == "{" || value == "[" || value == "}" || value == "]" {
			continue
		}
		entries = append(entries, ConfigEntry{Key: key, Value: value, Line: lineNum + 1})
	}
	return entries
}

func parseTOMLEntries(src []byte) []ConfigEntry {
	var entries []ConfigEntry
	var section string
	for lineNum, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			end := strings.Index(line, "]")
			section = line[1:end] + "."
			continue
		}
		if !strings.Contains(line, "=") {
			continue
		}
		eqIdx := strings.Index(line, "=")
		key := strings.TrimSpace(line[:eqIdx])
		value := strings.TrimSpace(line[eqIdx+1:])
		value = strings.Trim(value, `"'`)
		entries = append(entries, ConfigEntry{Key: section + key, Value: value, Line: lineNum + 1})
	}
	return entries
}
