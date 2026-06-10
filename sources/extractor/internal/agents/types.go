package agents

import (
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/agents/core"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// The shared DTOs and client interfaces live in internal/agents/core so every
// stage package can depend on them without depending on the orchestrator.
// These aliases keep the orchestrator's existing (lower-case) names working;
// stage packages reference the core.* names directly.
type (
	openCodeAPI     = core.OpenCodeAPI
	pauseHandler    = core.PauseHandler
	verbosePrompter = core.VerbosePrompter
	tokenReader     = core.TokenReader
	sessionState    = core.SessionState

	PendingPermission = core.PendingPermission
	PendingQuestion   = core.PendingQuestion

	llmLocation = core.LLMLocation
	llmEvidence = core.LLMEvidence
	llmInput    = core.LLMInput
	llmEntity   = core.LLMEntity

	repoFacts       = core.RepoFacts
	langFact        = core.LangFact
	discoveryResult = core.DiscoveryResult
	detailJob       = core.DetailJob
	detailResult    = core.DetailResult
)

// Result is the final output of the agents pipeline.
//
// On a clean run, Failure is nil and the entity slices contain whatever the
// pipeline produced. On a hard failure (any LLM call fails after the no-retry
// policy was applied) the orchestrator stops further work, populates Failure
// with everything the operator needs, and still returns the partial entity
// slices that did finalise before the failing stage.
//
// SnapshotPath is the path to the on-disk repo snapshot the run used. On a
// successful run the orchestrator removes this directory and SnapshotPath is
// empty. On a failed run the snapshot is retained so `diffmind retry <run-id>`
// can re-use the exact same byte-for-byte working tree the original run saw.
type Result struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
	Unresolved   []model.UnresolvedItem
	Warnings     []string

	Failure      *Failure
	SnapshotPath string

	// Intermediate is the per-stage state captured at successful stage
	// boundaries. `diffmind retry` reads this so it can fast-forward to the
	// failed stage instead of re-running everything from scratch.
	Intermediate IntermediateState

	// Tokens holds the per-stage token / cost totals collected from each
	// promptAgent call's final GET /session/{id}. Keys are stage names plus
	// the special "total" key. Nil when the OpenCode client doesn't expose
	// token reads (test fakes).
	Tokens map[string]model.TokenBucket
}

// Failure describes why the pipeline halted. Every field is optional so the
// report can be authored regardless of where the failure happened.
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

	// Cancelled is true when the halt was triggered by the user pressing
	// Cancel (parent context died) rather than by an underlying provider
	// error. It lets the runs-list endpoint report "cancelled" instead of
	// "failed" without consulting the in-memory runner state.
	Cancelled bool `json:"cancelled,omitempty"`
}

// IntermediateState is what a successful stage hands to the next one, captured
// so a manual retry can resume from the last clean boundary. Each field is
// nil/empty when the corresponding stage did not complete.
type IntermediateState struct {
	RepoFacts        *repoFacts         `json:"repo_facts,omitempty"`
	DiscoverySeeds   []detailJob        `json:"discovery_seeds,omitempty"`
	ReexamSeeds      []detailJob        `json:"reexam_seeds,omitempty"`
	DetailExposures  []model.Exposure   `json:"detail_exposures,omitempty"`
	DetailDependency []model.Dependency `json:"detail_dependencies,omitempty"`
	Connections      []model.Connection `json:"connections,omitempty"`
	ExposureObjs     map[string]string  `json:"exposure_objectives,omitempty"` // type -> objectiveID
}
