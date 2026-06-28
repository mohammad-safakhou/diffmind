package protocol

import (
	"encoding/json"
	"io"

	"gopkg.in/yaml.v3"
)

func DecodeJSON(r io.Reader) (*Document, error) {
	var doc Document
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, Validate(&doc)
}

func EncodeJSON(w io.Writer, doc *Document) error {
	if err := Validate(doc); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func DecodeYAML(r io.Reader) (*Document, error) {
	var doc Document
	if err := yaml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, Validate(&doc)
}

func EncodeYAML(w io.Writer, doc *Document) error {
	if err := Validate(doc); err != nil {
		return err
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(doc)
}
