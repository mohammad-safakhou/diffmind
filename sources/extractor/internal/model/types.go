package model

import "time"

type EntityKind string

const (
	KindExposure   EntityKind = "exposure"
	KindDependency EntityKind = "dependency"
)

type Location struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type Evidence struct {
	Location Location `json:"location"`
	Snippet  string   `json:"snippet"`
	Source   string   `json:"source"`
}

type InputSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type Condition struct {
	Kind        string            `json:"kind"`
	Expression  string            `json:"expression"`
	Variables   []string          `json:"variables,omitempty"`
	Operator    string            `json:"operator,omitempty"`
	Value       string            `json:"value,omitempty"`
	Negated     bool              `json:"negated,omitempty"`
	Explanation string            `json:"explanation"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type BaseEntity struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Name          string         `json:"name"`
	Service       string         `json:"service"`
	Platform      string         `json:"platform,omitempty"`
	Instance      string         `json:"instance,omitempty"`
	Operation     string         `json:"operation,omitempty"`
	OperationKind string         `json:"operation_kind,omitempty"`
	Inputs        []InputSpec    `json:"inputs,omitempty"`
	Summary       string         `json:"summary"`
	KeyActions    []string       `json:"key_actions,omitempty"`
	Locations     []Location     `json:"source_locations"`
	Evidence      []Evidence     `json:"evidence"`
	Confidence    float64        `json:"confidence"`
	Tags          []string       `json:"tags,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
	PluginSource  string         `json:"plugin_source,omitempty"`
}

type Exposure struct {
	BaseEntity
}

type Dependency struct {
	BaseEntity
}

type Connection struct {
	ID             string           `json:"id"`
	FromExposureID string           `json:"from_exposure_id"`
	ToDependencyID string           `json:"to_dependency_id"`
	Condition      Condition        `json:"condition"`
	PathSignature  string           `json:"path_signature"`
	Summary        string           `json:"summary"`
	Locations      []Location       `json:"source_locations"`
	Evidence       []Evidence       `json:"evidence"`
	Confidence     float64          `json:"confidence"`
	FromType       string           `json:"from_type"`
	ToType         string           `json:"to_type"`
	Paths          []ConnectionPath `json:"paths,omitempty"`
}

type ConnectionPath struct {
	ID        string               `json:"id"`
	Summary   string               `json:"summary"`
	Condition Condition            `json:"condition"`
	Steps     []ConnectionPathStep `json:"steps"`
}

type ConnectionPathStep struct {
	Order     int        `json:"order"`
	Action    string     `json:"action"`
	Operation string     `json:"operation"`
	From      string     `json:"from"`
	To        string     `json:"to"`
	Condition Condition  `json:"condition"`
	Location  Location   `json:"location"`
	Evidence  []Evidence `json:"evidence,omitempty"`
}

type UnresolvedItem struct {
	Kind       EntityKind `json:"kind"`
	Type       string     `json:"type"`
	Name       string     `json:"name"`
	ReasonCode string     `json:"reason_code"`
	Reason     string     `json:"reason"`
	Confidence float64    `json:"confidence"`
	Evidence   []Evidence `json:"evidence,omitempty"`
}

type RunManifest struct {
	RunID             string            `json:"run_id"`
	StartedAt         time.Time         `json:"started_at"`
	FinishedAt        time.Time         `json:"finished_at"`
	RepoPath          string            `json:"repo_path"`
	// RepoGitSHA is the analyzed repo's HEAD commit (when it is a git repo), so a
	// run can be pinned to the exact target revision. DiffMindVersion records the
	// extractor build (set via -ldflags, else "dev") so output can be pinned to
	// the code that produced it.
	RepoGitSHA        string            `json:"repo_git_sha,omitempty"`
	DiffMindVersion   string            `json:"diffmind_version,omitempty"`
	SchemaVersion     string            `json:"schema_version"`
	OpenCodeURL       string            `json:"opencode_url,omitempty"`
	ConfidenceMinimum float64           `json:"confidence_minimum"`
	Counts            map[string]int    `json:"counts"`
	Warnings          []string          `json:"warnings,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`

	// StageFailures aggregates per-stage failure counts derived from
	// the unresolved diagnostics. It lets the dashboard and the
	// `validate` command surface partial-success runs without having
	// to grep the warnings array. Keys are stage names as seen in the
	// pipeline ("discovery", "reexamination", "detail", "connections")
	// and values are the count of items that failed in that stage.
	StageFailures map[string]int `json:"stage_failures,omitempty"`

	// TokenTotals holds the per-stage token / cost totals reported
	// by OpenCode. Keys are stage names; the special key "total" is
	// the run-wide aggregate. Nil when the provider doesn't return
	// token counters or when token reads were disabled.
	TokenTotals map[string]TokenBucket `json:"token_totals,omitempty"`
}

// TokenBucket mirrors agents.tokenBucket in the model package so
// callers outside agents (artifacts writer, validate command, SPA
// JSON API) don't need to import the agents package just to read
// numbers off a manifest.
type TokenBucket struct {
	Calls      int     `json:"calls"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Reasoning  int     `json:"reasoning"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Total      int     `json:"total"`
	Cost       float64 `json:"cost"`
}
