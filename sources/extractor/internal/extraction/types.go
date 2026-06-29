// Package extraction contains the domain contracts passed between pipeline
// stages. It has no transport, persistence, or orchestration responsibilities.
package extraction

import (
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

type Location struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type Evidence struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Snippet   string `json:"snippet"`
	Source    string `json:"source"`
}

type Input struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// Candidate is the stage-level entity shape emitted by deterministic detectors
// before conversion to the canonical model/DiffMind protocol objects.
type Candidate struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Summary    string         `json:"summary"`
	Inputs     []Input        `json:"inputs"`
	Actions    []string       `json:"key_actions"`
	Confidence float64        `json:"confidence"`
	Tags       []string       `json:"tags"`
	Details    map[string]any `json:"details"`
	Locations  []Location     `json:"source_locations"`
	Evidence   []Evidence     `json:"evidence"`
}

type RepoFacts struct {
	ServiceName       string              `json:"service_name"`
	Languages         []string            `json:"languages"`
	Frameworks        []string            `json:"frameworks"`
	BuildFiles        []string            `json:"build_files"`
	ConfigFiles       []string            `json:"config_files"`
	MonorepoSubdir    string              `json:"monorepo_subdir"`
	ModuleMap         []map[string]string `json:"module_map"`
	ProbableTechHints []string            `json:"probable_tech_hints"`
	DeploymentHints   []string            `json:"deployment_hints"`
	ExtraObservations []string            `json:"extra_observations"`
	LanguageFacts     []LanguageFact      `json:"language_facts,omitempty"`
}

type LanguageFact struct {
	Language         string   `json:"language"`
	Version          string   `json:"version,omitempty"`
	BuildTool        string   `json:"build_tool,omitempty"`
	BuildToolVersion string   `json:"build_tool_version,omitempty"`
	Sources          []string `json:"sources,omitempty"`
}

type DiscoveryResult struct {
	Objective     objectives.Objective
	Items         []Candidate
	Err           error
	PeerCancelled bool
	Unresolved    []model.UnresolvedItem
}

type DetailJob struct {
	Objective objectives.Objective
	Seed      Candidate
}

type Request struct {
	RepoPath      string
	CaptureDir    string
	RunDir        string
	RunID         string
	ResumeFromDir string
}

type Result struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
	Clients      []model.ConnectionClient
	Unresolved   []model.UnresolvedItem
	Warnings     []string
	Failure      *Failure
	SourceRoot   string
	Intermediate IntermediateState
}

type Failure struct {
	Stage       string         `json:"stage"`
	JobID       string         `json:"job_id"`
	ObjectiveID string         `json:"objective_id"`
	EntityName  string         `json:"entity_name"`
	Error       string         `json:"error"`
	ErrorClass  string         `json:"error_class"`
	HTTPStatus  int            `json:"http_status,omitempty"`
	SourceRoot  string         `json:"source_root,omitempty"`
	OccurredAt  time.Time      `json:"occurred_at"`
	Extra       map[string]any `json:"extra,omitempty"`
	Cancelled   bool           `json:"cancelled,omitempty"`
}

type IntermediateState struct {
	RepoFacts      *RepoFacts               `json:"repo_facts,omitempty"`
	DiscoverySeeds []DetailJob              `json:"discovery_seeds,omitempty"`
	Exposures      []model.Exposure         `json:"entities_exposures,omitempty"`
	Dependencies   []model.Dependency       `json:"entities_dependencies,omitempty"`
	Connections    []model.Connection       `json:"connections,omitempty"`
	Clients        []model.ConnectionClient `json:"connection_clients,omitempty"`
	ExposureObjs   map[string]string        `json:"exposure_objectives,omitempty"`
}
