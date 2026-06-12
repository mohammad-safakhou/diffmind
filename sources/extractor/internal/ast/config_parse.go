package ast

import (
	"bytes"
	"fmt"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

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

// parseYAMLEntries flattens YAML into dotted (key, value) entries using a real
// YAML decoder. The previous indentation-walker collapsed sequence-of-mapping
// items onto one shared key, dropped block lists entirely, and re-parented
// mis-indented keys (V3b/V3c/V3e) — all of which corrupted ${...} resolution
// and dedup identity. yaml.Node (not plain unmarshal) keeps each entry's
// source line and lets Spring multi-document files attribute entries to the
// profile document that activates them, so cross-file profile precedence
// (V3a) also holds within one file.
func parseYAMLEntries(src []byte) []ConfigEntry {
	var entries []ConfigEntry
	for _, doc := range splitYAMLDocuments(src) {
		var node yaml.Node
		if err := yaml.Unmarshal(doc.src, &node); err != nil {
			continue // a malformed document degrades to nothing; siblings are kept
		}
		root := &node
		if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
			root = node.Content[0]
		}
		var docEntries []ConfigEntry
		flattenYAMLNode(root, "", &docEntries)
		profile := yamlDocProfile(docEntries)
		for i := range docEntries {
			docEntries[i].Line += doc.startLine
			docEntries[i].Profile = profile
		}
		entries = append(entries, docEntries...)
	}
	return entries
}

type yamlDocument struct {
	src       []byte
	startLine int // 0-based line of the document's first line in the file
}

// splitYAMLDocuments cuts a multi-doc stream on "---" separator lines. Each
// document is decoded independently so one malformed overlay cannot discard
// the whole file's config.
func splitYAMLDocuments(src []byte) []yamlDocument {
	lines := bytes.Split(src, []byte("\n"))
	var docs []yamlDocument
	start := 0
	flush := func(end int) {
		if end <= start {
			return
		}
		docs = append(docs, yamlDocument{
			src:       bytes.Join(lines[start:end], []byte("\n")),
			startLine: start,
		})
	}
	for i, line := range lines {
		t := bytes.TrimSpace(line)
		if bytes.Equal(t, []byte("---")) {
			flush(i)
			start = i + 1
		}
	}
	flush(len(lines))
	return docs
}

// flattenYAMLNode walks a YAML node, joining mapping keys with "." and
// sequence indices as "[i]" (Spring's relaxed-binding list syntax), so each
// item of a sequence of mappings keeps its own key space.
func flattenYAMLNode(n *yaml.Node, prefix string, out *[]ConfigEntry) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.AliasNode:
		flattenYAMLNode(n.Alias, prefix, out)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := strings.TrimSpace(n.Content[i].Value)
			if key == "" {
				continue
			}
			child := key
			if prefix != "" {
				child = prefix + "." + key
			}
			flattenYAMLNode(n.Content[i+1], child, out)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			flattenYAMLNode(item, fmt.Sprintf("%s[%d]", prefix, i), out)
		}
	case yaml.ScalarNode:
		if prefix == "" || n.Tag == "!!null" {
			return
		}
		*out = append(*out, ConfigEntry{Key: prefix, Value: strings.TrimSpace(n.Value), Line: n.Line})
	}
}

// yamlDocProfile returns the Spring profile a multi-doc overlay activates on
// ("spring.config.activate.on-profile", legacy "spring.profiles"), "" for the
// base document. Expressions ("prod & cloud") pass through verbatim — they
// will simply never equal a pinned active profile, which is the conservative
// outcome.
func yamlDocProfile(entries []ConfigEntry) string {
	for _, e := range entries {
		switch strings.ToLower(e.Key) {
		case "spring.config.activate.on-profile", "spring.profiles":
			return strings.TrimSpace(e.Value)
		}
	}
	return ""
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
