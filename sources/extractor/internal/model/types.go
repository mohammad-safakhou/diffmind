package model

import "time"

type EntityKind string

const (
	KindExposure   EntityKind = "exposure"
	KindDependency EntityKind = "dependency"
	// KindClient is a connection backbone (see ConnectionClient). Clients are
	// neither an exposure nor a dependency: they are resolved to instance
	// identity and fanned out to the operations that use them, never emitted as
	// graph nodes themselves.
	KindClient EntityKind = "client"
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

// InstanceRef is the cross-service-matchable identity of the concrete external
// thing an entity talks to (a database, a broker queue, an HTTP service).
// Downstream graph builders join "service A ↔ service B" on these fields, so
// every value must be a config-derived fact: a resolved value or a verbatim
// ${ENV:default} template — never a config property name and never a rendered
// Go value. Fields are best-effort and individually optional; absence means
// "not resolvable from this repo's config", not "unknown instance".
type InstanceRef struct {
	Kind         string `json:"kind,omitempty"`          // concrete platform: postgres, sqs, kafka, http, ...
	LogicalName  string `json:"logical_name,omitempty"`  // queue name, database name, target service name
	URLTemplate  string `json:"url_template,omitempty"`  // config value verbatim, placeholders preserved
	ResolvedURL  string `json:"resolved_url,omitempty"`  // only when no unresolved placeholder remains
	Host         string `json:"host,omitempty"`          // only when the host part is placeholder-free
	Database     string `json:"database,omitempty"`      // database/schema name for datastores
	ConfigSource string `json:"config_source,omitempty"` // "<config file>: <property key>"
}

type BaseEntity struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Name          string         `json:"name"`
	Service       string         `json:"service"`
	Platform      string         `json:"platform,omitempty"`
	Instance      string         `json:"instance,omitempty"`
	InstanceRef   *InstanceRef   `json:"instance_ref,omitempty"`
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

// ConnectionClient is a shared connection backbone — the ORM/repository base,
// HTTP client bean, datasource, or messaging/cache client through which many
// operations reach one external system — paired with the config property that
// wires it. Discovery surfaces these once (instead of resolving an instance per
// operation); the deterministic propagation pass resolves each client to an
// InstanceRef from config and fans that identity to every operation using the
// client. Like InstanceRef it is a config-derived, cross-service-joinable fact;
// it is never emitted as an exposure/dependency graph node.
type ConnectionClient struct {
	ID           string       `json:"id"`
	LogicalName  string       `json:"logical_name"`            // bean/var/field name, e.g. "orderRepository", "sqsClient"
	Kind         string       `json:"kind"`                    // db | http | queue | cache | stream
	Symbol       string       `json:"symbol,omitempty"`        // qualified declared type/bean
	Framework    string       `json:"framework,omitempty"`     // spring-data, feign, aws-sdk, gorm, ...
	ConfigAnchor string       `json:"config_anchor,omitempty"` // config property key that configures it
	InstanceRef  *InstanceRef `json:"instance_ref,omitempty"`  // filled by deterministic propagation
	Locations    []Location   `json:"source_locations,omitempty"`
	Evidence     []Evidence   `json:"evidence,omitempty"`
	Source       string       `json:"source,omitempty"` // "ast" | "config" | "deterministic"
}

// Connection provenance values.
const (
	ConnectionSourceAST     = "ast"
	ConnectionSourceShallow = "shallow"
)

type Connection struct {
	ID             string `json:"id"`
	FromExposureID string `json:"from_exposure_id"`
	ToDependencyID string `json:"to_dependency_id"`
	Source         string `json:"source,omitempty"`
	// Status is the curation state carried from a discovery file ("verified",
	// "proposed", "needs_review"); empty means verified. Run artifacts omit it.
	Status        string           `json:"status,omitempty"`
	Condition     Condition        `json:"condition"`
	PathSignature string           `json:"path_signature"`
	Summary       string           `json:"summary"`
	Locations     []Location       `json:"source_locations"`
	Evidence      []Evidence       `json:"evidence"`
	Confidence    float64          `json:"confidence"`
	FromType      string           `json:"from_type"`
	ToType        string           `json:"to_type"`
	Paths         []ConnectionPath `json:"paths,omitempty"`
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
	RunID      string    `json:"run_id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	RepoPath   string    `json:"repo_path"`
	Team       string    `json:"team,omitempty"`
	// RepoGitSHA is the analyzed repo's HEAD commit (when it is a git repo), so a
	// run can be pinned to the exact target revision. DiffMindVersion records the
	// extractor build (set via -ldflags, else "dev") so output can be pinned to
	// the code that produced it.
	RepoGitSHA        string            `json:"repo_git_sha,omitempty"`
	RepoGitBranch     string            `json:"repo_git_branch,omitempty"`
	RepoGitRemoteURL  string            `json:"repo_git_remote_url,omitempty"`
	RepoGitDirty      bool              `json:"repo_git_dirty,omitempty"`
	DiffMindVersion   string            `json:"diffmind_version,omitempty"`
	SchemaVersion     string            `json:"schema_version"`
	Pipeline          string            `json:"pipeline,omitempty"`
	ConfidenceMinimum float64           `json:"confidence_minimum"`
	Counts            map[string]int    `json:"counts"`
	RepoMetrics       *RepoMetrics      `json:"repo_metrics,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`

	// StageFailures aggregates per-stage failure counts derived from
	// the unresolved diagnostics. It lets the dashboard and the
	// `validate` command surface partial-success runs without having
	// to grep the warnings array. Keys are stage names as seen in the
	// pipeline ("discovery", "reexamination", "connections")
	// and values are the count of items that failed in that stage.
	StageFailures map[string]int `json:"stage_failures,omitempty"`
}

type RepoMetrics struct {
	TotalLOC            int              `json:"total_loc"`
	FileCount           int              `json:"file_count"`
	Languages           []LanguageMetric `json:"languages,omitempty"`
	Frameworks          []string         `json:"frameworks,omitempty"`
	BuildTools          []string         `json:"build_tools,omitempty"`
	DetectedServiceName string           `json:"detected_service_name,omitempty"`
}

type LanguageMetric struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
	LOC      int    `json:"loc"`
}
