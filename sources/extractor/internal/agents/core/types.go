// Package core holds the shared types and stage-agnostic helpers the pipeline
// orchestrator and every stage package depend on. It is the common seam: stages
// import core, never each other, so there are no inter-stage import cycles.
package core

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// OpenCodeAPI is the narrow interface the pipeline depends on from the opencode
// client. Keeping it small means the test suite can swap in fakes and the
// pipeline never touches HTTP concerns directly.
type OpenCodeAPI interface {
	Enabled() bool
	CreateSession(ctx context.Context, directory string) (string, error)
	DeleteSession(ctx context.Context, sessionID, directory string) error
	PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error)
	PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error)
}

// PauseHandler is an optional capability used by the watchdog to keep agents
// from blocking on permission/clarification prompts. Declared separately so
// test fakes that don't need it can stay slim; it is invoked only when the
// underlying client implements it.
type PauseHandler interface {
	AbortSession(ctx context.Context, sessionID, directory string) error
	ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error)
	RespondPermission(ctx context.Context, sessionID, permissionID, directory, response string) error
	ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error)
	RejectQuestion(ctx context.Context, requestID, directory string) error
	// LookupSessionParent returns the parentID of a session, or "" when the
	// session has no parent or cannot be found. The watchdog follows
	// task-spawned subagent sessions up to a parent it owns so subagent
	// permissions can be answered. Errors are non-fatal.
	LookupSessionParent(ctx context.Context, sessionID, directory string) (string, error)
}

// VerbosePrompter is the optional richer surface implemented by the real
// opencode client. It returns the raw response body (and any free-text parts)
// so the pipeline can persist it for diagnostics and use it as the input for a
// free-text JSON fallback when structured parsing fails.
type VerbosePrompter interface {
	PromptStructuredVerboseRaw(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (parsed map[string]any, raw []byte, text string, err error)
}

// TokenReader is the optional capability used to read final token/cost counters
// off a session AFTER a prompt POST completes. OpenCode reports cumulative
// session totals at /session/{id}; in per-call session mode those totals ARE
// that prompt's tokens. The real *opencode.Client implements this; bare fakes
// don't (and produce zero token reads, handled gracefully by the aggregator).
type TokenReader interface {
	GetSession(ctx context.Context, sessionID, directory string) (SessionState, error)
}

// SessionState is the minimal projection the pipeline needs from
// opencode.SessionState. Declared here so the pipeline stays free of the
// opencode package's full surface — tests can build values directly.
type SessionState struct {
	ID         string
	Cost       float64
	Input      int
	Output     int
	Reasoning  int
	CacheRead  int
	CacheWrite int
}

// totalTokens returns input + output + reasoning, the user-facing number.
// Cache reads/writes are reported separately because they're billed differently.
func (s SessionState) totalTokens() int {
	return s.Input + s.Output + s.Reasoning
}

// PendingPermission and PendingQuestion mirror the opencode client types so the
// watchdog interface can be treated in isolation. Plain structs (not aliases)
// so test fakes need not import the opencode package.
type PendingPermission struct {
	ID         string
	SessionID  string
	Title      string
	Type       string
	Permission string
	Patterns   []string
}

type PendingQuestion struct {
	ID        string
	SessionID string
	Question  string
}

// ----------------------------------------------------------------------------
// LLM DTOs (shape-compatible with the prompts/schemas).
// ----------------------------------------------------------------------------

type LLMLocation struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type LLMEvidence struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Snippet   string `json:"snippet"`
	Source    string `json:"source"`
}

type LLMInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type LLMEntity struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Summary    string         `json:"summary"`
	Inputs     []LLMInput     `json:"inputs"`
	Actions    []string       `json:"key_actions"`
	Confidence float64        `json:"confidence"`
	Tags       []string       `json:"tags"`
	Details    map[string]any `json:"details"`
	Locations  []LLMLocation  `json:"source_locations"`
	Evidence   []LLMEvidence  `json:"evidence"`
}

// ----------------------------------------------------------------------------
// Stage-internal result types.
// ----------------------------------------------------------------------------

// RepoFacts is the Stage 0 output, cached for the whole run and injected into
// every downstream prompt.
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
	// LanguageFacts carries the marker-file-detected language inventory the
	// index stage needs to plan its image build. Populated by RunRepoFacts
	// AFTER the LLM call, from internal/langdetect.Inspect(); the LLM's
	// "languages" field is kept for prompt continuity but the index stage
	// trusts the deterministic facts here.
	LanguageFacts []LangFact `json:"language_facts,omitempty"`
}

// LangFact mirrors langdetect.Fact for serialisation. We avoid importing the
// langdetect package here to keep the types layer free of cross-stage
// dependencies; the discovery stage does the conversion.
type LangFact struct {
	Language         string   `json:"language"`
	Version          string   `json:"version,omitempty"`
	BuildTool        string   `json:"build_tool,omitempty"`
	BuildToolVersion string   `json:"build_tool_version,omitempty"`
	Sources          []string `json:"sources,omitempty"`
}

// DiscoveryResult is the output of stage 1 for a single objective.
//
// PeerCancelled is set by workers that never issued the prompt because a
// sibling tripped fail-fast and cancelled the stage's child context. We need
// it because "parent context cancelled" and "per-call HTTP timeout" both wrap
// context.DeadlineExceeded at the orchestrator level; workers know which is
// which and declare it explicitly.
type DiscoveryResult struct {
	Objective     objectives.Objective
	Items         []LLMEntity
	Err           error
	PeerCancelled bool
	Unresolved    []model.UnresolvedItem
}

// DetailJob pairs a verified seed entity with its objective for stage 3.
type DetailJob struct {
	Objective objectives.Objective
	Seed      LLMEntity
}

// DetailResult is the output of stage 3 for a single seed.
//
// SeedName is required for accurate failure reporting: results arrive on the
// channel in completion order, not submission order, so the seed cannot be
// recovered from a slice index. PeerCancelled mirrors DiscoveryResult.
type DetailResult struct {
	Objective     objectives.Objective
	SeedName      string
	Item          *LLMEntity
	Err           error
	PeerCancelled bool
}
