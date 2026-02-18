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
	Offline     bool
	Adapters    string
	Extractors  string
	LLMAugment  bool
	LLMModel    string
	LLMTask     string
	LLMMaxFiles int
	LLMMaxChars int
}

type AdapterPlanItem struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities,omitempty"`
	Available    bool     `json:"available"`
	Selected     bool     `json:"selected"`
	Reason       string   `json:"reason,omitempty"`
	Extractors   []string `json:"extractors,omitempty"`
}

type AdapterRunItem struct {
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Extractors    []string `json:"extractors,omitempty"`
	FactsAdded    int      `json:"facts_added"`
	EvidenceAdded int      `json:"evidence_added"`
	ReplayKey     string   `json:"replay_key"`
}

type Report struct {
	GeneratedAt             time.Time         `json:"generated_at"`
	SourceRoot              string            `json:"source_root"`
	SnapshotID              string            `json:"snapshot_id"`
	FactsCount              int               `json:"facts_count"`
	EvidenceCount           int               `json:"evidence_count"`
	RuntimeUnits            int               `json:"runtime_units"`
	Endpoints               int               `json:"endpoints"`
	ExternalCalls           int               `json:"external_calls"`
	ConfigKeys              int               `json:"config_keys"`
	SensitiveSurfaces       int               `json:"sensitive_surfaces"`
	PipelineSteps           int               `json:"pipeline_steps"`
	InfraResources          int               `json:"infra_resources"`
	BuildArtifacts          int               `json:"build_artifacts"`
	Deployments             int               `json:"deployments"`
	Dependencies            int               `json:"dependencies"`
	OwnershipRules          int               `json:"ownership_rules"`
	DependencyRisks         int               `json:"dependency_risks"`
	CodeSymbols             int               `json:"code_symbols"`
	CodeCalls               int               `json:"code_calls"`
	Adapters                []string          `json:"adapters,omitempty"`
	AdapterPlan             []AdapterPlanItem `json:"adapter_plan,omitempty"`
	AdapterRuns             []AdapterRunItem  `json:"adapter_runs,omitempty"`
	Offline                 bool              `json:"offline"`
	ToolchainManifestPath   string            `json:"toolchain_manifest_path,omitempty"`
	ToolchainManifestSHA256 string            `json:"toolchain_manifest_sha256,omitempty"`
	Extractors              []string          `json:"extractors,omitempty"`
	LLMEnabled              bool              `json:"llm_enabled"`
	LLMFactsAdded           int               `json:"llm_facts_added"`
	LLMTracePath            string            `json:"llm_trace_path,omitempty"`
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
