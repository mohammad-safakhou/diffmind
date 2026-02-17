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
	Extractors  string
	LLMAugment  bool
	LLMModel    string
	LLMTask     string
	LLMMaxFiles int
	LLMMaxChars int
}

type Report struct {
	GeneratedAt       time.Time `json:"generated_at"`
	SourceRoot        string    `json:"source_root"`
	SnapshotID        string    `json:"snapshot_id"`
	FactsCount        int       `json:"facts_count"`
	EvidenceCount     int       `json:"evidence_count"`
	RuntimeUnits      int       `json:"runtime_units"`
	Endpoints         int       `json:"endpoints"`
	ExternalCalls     int       `json:"external_calls"`
	ConfigKeys        int       `json:"config_keys"`
	SensitiveSurfaces int       `json:"sensitive_surfaces"`
	PipelineSteps     int       `json:"pipeline_steps"`
	InfraResources    int       `json:"infra_resources"`
	BuildArtifacts    int       `json:"build_artifacts"`
	Deployments       int       `json:"deployments"`
	Dependencies      int       `json:"dependencies"`
	OwnershipRules    int       `json:"ownership_rules"`
	DependencyRisks   int       `json:"dependency_risks"`
	CodeSymbols       int       `json:"code_symbols"`
	CodeCalls         int       `json:"code_calls"`
	Extractors        []string  `json:"extractors,omitempty"`
	LLMEnabled        bool      `json:"llm_enabled"`
	LLMFactsAdded     int       `json:"llm_facts_added"`
	LLMTracePath      string    `json:"llm_trace_path,omitempty"`
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
