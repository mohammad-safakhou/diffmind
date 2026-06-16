package archfile

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// parsed holds both representations of one file: the decoded struct (for logic)
// and the document node (for comment/format-preserving write-back).
type parsed struct {
	path string
	doc  *yaml.Node // document node; doc.Content[0] is the mapping
	raw  rawFile
}

// parse reads one YAML file, validates its schema header, and returns both the
// retained node tree and the decoded struct. Variables are NOT yet expanded.
func parse(path string) (*parsed, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, fmt.Errorf("parse %s: empty document", path)
	}
	var raw rawFile
	if err := doc.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if raw.Schema != "" && raw.Schema != Schema {
		return nil, fmt.Errorf("parse %s: unsupported schema %q (want %q)", path, raw.Schema, Schema)
	}
	return &parsed{path: path, doc: &doc, raw: raw}, nil
}

// rootMapping returns the top-level mapping node of a parsed document.
func (p *parsed) rootMapping() *yaml.Node {
	return p.doc.Content[0]
}

// Validate checks that raw bytes parse as a discovery file (schema header + YAML
// shape). It is used to gate inline-editor saves before they touch disk.
func Validate(b []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return fmt.Errorf("empty document")
	}
	var raw rawFile
	if err := doc.Decode(&raw); err != nil {
		return err
	}
	if raw.Schema != "" && raw.Schema != Schema {
		return fmt.Errorf("unsupported schema %q (want %q)", raw.Schema, Schema)
	}
	return nil
}
