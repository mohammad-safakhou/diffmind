package analyzers

import (
	"time"

	"diffmind/internal/facts"
)

const analyzerVersion = "v1"
const analyzerID = "analyzers.det.v1"

type Options struct {
	Source      string
	OutDir      string
	SnapshotID  string
	Persist     bool
	LLMAugment  bool
	LLMModel    string
	LLMTask     string
	LLMMaxFiles int
	LLMMaxChars int
}

type Report struct {
	GeneratedAt    time.Time `json:"generated_at"`
	SourceRoot     string    `json:"source_root"`
	SnapshotID     string    `json:"snapshot_id"`
	FactsCount     int       `json:"facts_count"`
	EvidenceCount  int       `json:"evidence_count"`
	RuntimeUnits   int       `json:"runtime_units"`
	Endpoints      int       `json:"endpoints"`
	ExternalCalls  int       `json:"external_calls"`
	ConfigKeys     int       `json:"config_keys"`
	PipelineSteps  int       `json:"pipeline_steps"`
	InfraResources int       `json:"infra_resources"`
	LLMEnabled     bool      `json:"llm_enabled"`
	LLMFactsAdded  int       `json:"llm_facts_added"`
	LLMTracePath   string    `json:"llm_trace_path,omitempty"`
}

type result struct {
	bundle facts.Bundle
	report Report
}

type sourceFile struct {
	Path    string
	AbsPath string
	Ext     string
	Lines   []string
	Text    string
}
