package consolidation

import "time"

type Options struct {
	InBundle   string
	OutDir     string
	SnapshotID string
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

type IntelligenceBundle struct {
	SnapshotID  string    `json:"snapshot_id"`
	GeneratedAt time.Time `json:"generated_at"`
	Entities    []Entity  `json:"entities"`
}

type Report struct {
	GeneratedAt      time.Time `json:"generated_at"`
	SnapshotID       string    `json:"snapshot_id"`
	InputFacts       int       `json:"input_facts"`
	OutputEntities   int       `json:"output_entities"`
	DuplicatesMerged int       `json:"duplicates_merged"`
	RuntimeUnits     int       `json:"runtime_units"`
	Endpoints        int       `json:"endpoints"`
	ExternalCalls    int       `json:"external_calls"`
	ConfigKeys       int       `json:"config_keys"`
	PipelineSteps    int       `json:"pipeline_steps"`
	InfraResources   int       `json:"infra_resources"`
}
