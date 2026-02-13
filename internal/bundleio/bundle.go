package bundleio

import (
	"encoding/json"
	"fmt"
	"os"
)

type Bundle struct {
	SnapshotID string   `json:"snapshot_id"`
	Entities   []Entity `json:"entities"`
}

type Entity struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	NaturalKey  string         `json:"natural_key"`
	Attributes  map[string]any `json:"attributes"`
	EvidenceIDs []string       `json:"evidence_ids"`
	FactIDs     []string       `json:"fact_ids"`
	Confidence  float64        `json:"confidence"`
}

func Load(path string) (Bundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Bundle{}, fmt.Errorf("read bundle %s: %w", path, err)
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return Bundle{}, fmt.Errorf("decode bundle %s: %w", path, err)
	}
	return b, nil
}
