package parser

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/go-ini/ini"
	"go.yaml.in/yaml/v3"
)

func parseStructured(path string, content []byte) (map[string]any, bool, error) {
	lower := strings.ToLower(path)
	ext := strings.ToLower(filepath.Ext(lower))

	switch ext {
	case ".json":
		var out any
		if err := json.Unmarshal(content, &out); err != nil {
			return nil, true, fmt.Errorf("json parse: %w", err)
		}
		return map[string]any{"format": "json", "data": out}, true, nil
	case ".yaml", ".yml":
		var out any
		if err := yaml.Unmarshal(content, &out); err != nil {
			return nil, true, fmt.Errorf("yaml parse: %w", err)
		}
		return map[string]any{"format": "yaml", "data": normalizeYAML(out)}, true, nil
	case ".toml":
		out := map[string]any{}
		if err := toml.Unmarshal(content, &out); err != nil {
			return nil, true, fmt.Errorf("toml parse: %w", err)
		}
		return map[string]any{"format": "toml", "data": out}, true, nil
	case ".ini", ".properties", ".env", ".conf":
		cfg, err := ini.Load(content)
		if err != nil {
			return nil, true, fmt.Errorf("ini parse: %w", err)
		}
		return map[string]any{"format": "ini", "data": iniToMap(cfg)}, true, nil
	case ".xml":
		node, err := parseXML(content)
		if err != nil {
			return nil, true, fmt.Errorf("xml parse: %w", err)
		}
		return map[string]any{"format": "xml", "data": node}, true, nil
	default:
		base := strings.ToLower(filepath.Base(lower))
		if base == "dockerfile" {
			return map[string]any{"format": "dockerfile", "data": map[string]any{"text": string(content)}}, true, nil
		}
	}

	return nil, false, nil
}

func normalizeYAML(in any) any {
	switch v := in.(type) {
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[fmt.Sprint(key)] = normalizeYAML(value)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = normalizeYAML(value)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = normalizeYAML(v[i])
		}
		return out
	default:
		return v
	}
}

func iniToMap(cfg *ini.File) map[string]any {
	out := map[string]any{}
	for _, sec := range cfg.Sections() {
		section := map[string]any{}
		for _, key := range sec.Keys() {
			section[key.Name()] = key.Value()
		}
		out[sec.Name()] = section
	}
	return out
}

type xmlNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Nodes   []xmlNode  `xml:",any"`
	Text    string     `xml:",chardata"`
}

func parseXML(content []byte) (map[string]any, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(content)))
	for {
		tok, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				return map[string]any{}, nil
			}
			return nil, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			n, err := decodeElement(decoder, start)
			if err != nil {
				return nil, err
			}
			return nodeToMap(n), nil
		}
	}
}

func decodeElement(decoder *xml.Decoder, start xml.StartElement) (xmlNode, error) {
	n := xmlNode{XMLName: start.Name, Attrs: start.Attr}
	for {
		tok, err := decoder.Token()
		if err != nil {
			return xmlNode{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := decodeElement(decoder, t)
			if err != nil {
				return xmlNode{}, err
			}
			n.Nodes = append(n.Nodes, child)
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return n, nil
			}
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text != "" {
				n.Text = text
			}
		}
	}
}

func nodeToMap(n xmlNode) map[string]any {
	attrs := map[string]any{}
	for _, a := range n.Attrs {
		attrs[a.Name.Local] = a.Value
	}
	children := make([]any, 0, len(n.Nodes))
	for _, child := range n.Nodes {
		children = append(children, nodeToMap(child))
	}
	return map[string]any{
		"name":     n.XMLName.Local,
		"attrs":    attrs,
		"text":     n.Text,
		"children": children,
	}
}
