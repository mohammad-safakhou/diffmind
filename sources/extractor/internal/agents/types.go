package agents

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// openCodeAPI is the narrow interface the agents package depends on from the
// opencode client. Keeping it private means the test suite can easily swap in
// fakes and the orchestrator never touches HTTP concerns directly.
type openCodeAPI interface {
	Enabled() bool
	CreateSession(ctx context.Context, directory string) (string, error)
	DeleteSession(ctx context.Context, sessionID, directory string) error
	PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error)
	PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error)
}

// Result is the final output of the agents pipeline.
type Result struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
	Unresolved   []model.UnresolvedItem
	Warnings     []string
}

// ----------------------------------------------------------------------------
// LLM DTOs (shape-compatible with the prompts/schemas in schemas.go).
// ----------------------------------------------------------------------------

type llmLocation struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type llmEvidence struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Snippet   string `json:"snippet"`
	Source    string `json:"source"`
}

type llmInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type llmEntity struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Summary    string         `json:"summary"`
	Inputs     []llmInput     `json:"inputs"`
	Actions    []string       `json:"key_actions"`
	Confidence float64        `json:"confidence"`
	Tags       []string       `json:"tags"`
	Details    map[string]any `json:"details"`
	Locations  []llmLocation  `json:"source_locations"`
	Evidence   []llmEvidence  `json:"evidence"`
}

type llmConnection struct {
	FromExposureID string          `json:"from_exposure_id"`
	ToDependencyID string          `json:"to_dependency_id"`
	Summary        string          `json:"summary"`
	Confidence     float64         `json:"confidence"`
	PathSignature  string          `json:"path_signature"`
	Condition      model.Condition `json:"condition"`
	Paths          []llmPath       `json:"paths"`
	Locations      []llmLocation   `json:"source_locations"`
	Evidence       []llmEvidence   `json:"evidence"`
}

type llmPath struct {
	ID        string          `json:"id"`
	Summary   string          `json:"summary"`
	Condition model.Condition `json:"condition"`
	Steps     []llmPathStep   `json:"steps"`
}

type llmPathStep struct {
	Order     int             `json:"order"`
	Action    string          `json:"action"`
	Operation string          `json:"operation"`
	From      string          `json:"from"`
	To        string          `json:"to"`
	Condition model.Condition `json:"condition"`
	Location  llmLocation     `json:"location"`
	Evidence  []llmEvidence   `json:"evidence"`
}

// ----------------------------------------------------------------------------
// Stage-internal result types.
// ----------------------------------------------------------------------------

// repoFacts is the Stage 0 output, cached for the whole run and injected into
// every downstream prompt.
type repoFacts struct {
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
}

// discoveryResult is the output of stage 1 for a single objective.
type discoveryResult struct {
	Objective  objectives.Objective
	Items      []llmEntity
	Err        error
	Unresolved []model.UnresolvedItem
}

// detailJob pairs a verified seed entity with its objective for stage 3.
type detailJob struct {
	Objective objectives.Objective
	Seed      llmEntity
}

// detailResult is the output of stage 3 for a single seed.
type detailResult struct {
	Objective objectives.Objective
	Item      *llmEntity
	Err       error
}

// connectionResult is the output of stage 4 for a single exposure.
type connectionResult struct {
	ExposureID string
	Items      []llmConnection
	Err        error
}
