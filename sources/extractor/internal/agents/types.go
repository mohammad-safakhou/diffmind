package agents

import (
	"context"
	"time"

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

// pauseHandler is an optional capability used by the orchestrator's watchdog
// to keep agents from blocking on permission/clarification prompts. We
// declare it as a separate interface so the test fakes that don't need it
// can stay slim; the orchestrator only invokes it when the underlying client
// implements it.
type pauseHandler interface {
	AbortSession(ctx context.Context, sessionID, directory string) error
	ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error)
	RespondPermission(ctx context.Context, sessionID, permissionID, directory, response string) error
	ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error)
	RejectQuestion(ctx context.Context, requestID, directory string) error
	// LookupSessionParent returns the parentID of a session, or "" when
	// the session has no parent (top-level session) OR cannot be found.
	// The watchdog uses this to follow `task`-spawned subagent sessions
	// up to one of the parent sessions it owns, so permissions raised
	// by subagents can be answered. Errors are non-fatal: callers
	// treat any error or empty string as "not a known sub-session" and
	// skip the permission, matching the pre-fix behaviour.
	LookupSessionParent(ctx context.Context, sessionID, directory string) (string, error)
}

// verbosePrompter is the optional richer surface implemented by the real
// opencode client. It returns the raw response body (and any free-text
// parts) so the orchestrator can persist it for diagnostics and use it as
// the input for a free-text JSON fallback when structured parsing fails.
// Test fakes that don't implement this stay on the legacy PromptStructured
// path with no fallback.
type verbosePrompter interface {
	PromptStructuredVerboseRaw(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (parsed map[string]any, raw []byte, text string, err error)
}

// tokenReader is the optional capability the orchestrator uses to
// read final token / cost counters off a session AFTER a prompt POST
// completes. OpenCode reports cumulative session totals at
// /session/{id}; because every promptAgent call (in the default
// per-call session mode) uses a fresh session, those cumulative
// totals ARE that prompt's tokens. Implementations: the real
// *opencode.Client implements this; bare test fakes don't (and
// produce zero token reads, which the aggregator handles gracefully).
type tokenReader interface {
	GetSession(ctx context.Context, sessionID, directory string) (sessionState, error)
}

// sessionState is the minimal projection the agents package needs
// from opencode.SessionState. Declared here as a separate type so
// the agents package stays free of the opencode package's full
// surface — tests can build sessionState values directly.
type sessionState struct {
	ID         string
	Cost       float64
	Input      int
	Output     int
	Reasoning  int
	CacheRead  int
	CacheWrite int
}

// totalTokens returns input + output + reasoning, the user-facing
// number we surface in the UI. Cache reads/writes are reported
// separately because they're billed differently (often free).
func (s sessionState) totalTokens() int {
	return s.Input + s.Output + s.Reasoning
}

// PendingPermission and PendingQuestion mirror the opencode client types so
// the orchestrator can treat the watchdog interface in isolation. We keep
// them as plain structs (not aliases) to avoid forcing every test fake to
// import the opencode package.
//
// Permission is the OpenCode "permission kind" string ("read", "edit",
// "bash", "external_directory", ...). Patterns is the list of paths/globs
// the agent wants access to. Either or both may be empty depending on the
// permission kind and the OpenCode version.
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

// Result is the final output of the agents pipeline.
//
// On a clean run, Failure is nil and the entity slices contain whatever
// the pipeline produced. On a hard failure (any LLM call fails after the
// no-retry policy was applied) the orchestrator stops further work,
// populates Failure with everything the operator needs to inspect the
// problem, and still returns the partial entity slices that did finalise
// before the failing stage.
//
// SnapshotPath is the path to the on-disk repo snapshot the run was
// using. On a successful run the orchestrator removes this directory
// before returning and SnapshotPath is empty. On a failed run the
// snapshot is intentionally retained so `diffmind retry <run-id>` can
// re-use the exact same byte-for-byte working tree the original run saw.
type Result struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
	Unresolved   []model.UnresolvedItem
	Warnings     []string

	Failure      *Failure
	SnapshotPath string

	// Intermediate is the per-stage state captured at successful stage
	// boundaries. `diffmind retry` reads this so it can fast-forward to
	// the failed stage instead of re-running everything from scratch.
	Intermediate IntermediateState

	// Tokens holds the per-stage token / cost totals collected from
	// each promptAgent call's final GET /session/{id}. Keys are
	// stage names (e.g. "discovery") plus the special "total" key
	// for the run-wide aggregate. Nil when the underlying OpenCode
	// client doesn't expose token reads (test fakes).
	Tokens map[string]model.TokenBucket
}

// Failure describes why the pipeline halted. Every field is optional so
// the report can be authored regardless of where the failure happened.
type Failure struct {
	Stage        string         `json:"stage"`        // discovery | reexamination | detail | connections | repo_facts
	JobID        string         `json:"job_id"`       // events.JobID, also used as prompt/response file prefix
	ObjectiveID  string         `json:"objective_id"` // pipeline objective id when applicable
	EntityName   string         `json:"entity_name"`  // seed name for detail/reexam failures
	Error        string         `json:"error"`        // err.Error()
	ErrorClass   string         `json:"error_class"`  // timeout | http_5xx | http_4xx | rate_limit | schema | cancelled | unknown
	HTTPStatus   int            `json:"http_status,omitempty"`
	SessionID    string         `json:"session_id,omitempty"`
	PromptPath   string         `json:"prompt_path,omitempty"`   // <runDir>/prompts/<jobID>.prompt.txt
	ResponsePath string         `json:"response_path,omitempty"` // <runDir>/prompts/<jobID>.response.{json|raw|text}
	SnapshotPath string         `json:"snapshot_path,omitempty"` // retained snapshot dir
	OccurredAt   time.Time      `json:"occurred_at"`
	Extra        map[string]any `json:"extra,omitempty"`

	// Cancelled is true when the halt was triggered by the user
	// pressing Cancel (parent context died) rather than by an
	// underlying provider error. It lets the runs-list endpoint
	// report a status of "cancelled" instead of "failed" without
	// needing to consult the in-memory runner state.
	Cancelled bool `json:"cancelled,omitempty"`
}

// IntermediateState is what a successful stage hands to the next one,
// captured so a manual retry can resume from the last clean boundary.
// Each field is nil/empty when the corresponding stage did not complete.
type IntermediateState struct {
	RepoFacts        *repoFacts         `json:"repo_facts,omitempty"`
	DiscoverySeeds   []detailJob        `json:"discovery_seeds,omitempty"`
	ReexamSeeds      []detailJob        `json:"reexam_seeds,omitempty"`
	DetailExposures  []model.Exposure   `json:"detail_exposures,omitempty"`
	DetailDependency []model.Dependency `json:"detail_dependencies,omitempty"`
	Connections      []model.Connection `json:"connections,omitempty"`
	ExposureObjs     map[string]string  `json:"exposure_objectives,omitempty"` // type -> objectiveID
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
	// LanguageFacts carries the marker-file-detected language
	// inventory the index stage needs to plan its image build.
	// Populated by runRepoFacts AFTER the LLM call completes,
	// from internal/langdetect.Inspect(); the LLM's "languages"
	// field above is kept for prompt continuity (downstream
	// stages still inject it) but the index stage trusts the
	// deterministic facts here.
	LanguageFacts []langFact `json:"language_facts,omitempty"`
}

// langFact mirrors langdetect.Fact for serialisation purposes.
// We avoid importing the langdetect package into types.go to
// keep the types layer free of cross-stage dependencies; the
// orchestrator does the conversion in repo_facts.go.
type langFact struct {
	Language         string   `json:"language"`
	Version          string   `json:"version,omitempty"`
	BuildTool        string   `json:"build_tool,omitempty"`
	BuildToolVersion string   `json:"build_tool_version,omitempty"`
	Sources          []string `json:"sources,omitempty"`
}

// discoveryResult is the output of stage 1 for a single objective.
//
// PeerCancelled is set by workers that never actually issued the
// prompt because a sibling worker had already tripped fail-fast and
// cancelled the stage's child context. We need this flag because we
// CANNOT reliably tell apart "parent context cancelled" from
// "per-call HTTP timeout" — both wrap context.DeadlineExceeded — at
// the orchestrator level. Workers know which is which, so they
// declare it explicitly.
type discoveryResult struct {
	Objective     objectives.Objective
	Items         []llmEntity
	Err           error
	PeerCancelled bool
	Unresolved    []model.UnresolvedItem
}

// detailJob pairs a verified seed entity with its objective for stage 3.
type detailJob struct {
	Objective objectives.Objective
	Seed      llmEntity
}

// detailResult is the output of stage 3 for a single seed.
//
// SeedName is the name of the seed entity the worker was asked to
// enrich. It is required for accurate failure reporting: results
// arrive on the channel in completion order, not submission order, so
// we cannot recover the seed from the result's slice index. Carrying
// the name in the result envelope is the only correct way to
// attribute a failure back to the entity that caused it.
//
// PeerCancelled mirrors discoveryResult: set when the worker exited
// before issuing its prompt because a sibling tripped fail-fast.
type detailResult struct {
	Objective     objectives.Objective
	SeedName      string
	Item          *llmEntity
	Err           error
	PeerCancelled bool
}


