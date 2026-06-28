// Package store is the filesystem-backed persistence layer for DiffMind
// projects, repositories, blueprints, and graph runs. Everything lives under
// the DiffMind home directory (see config.Home); the store owns the on-disk
// layout and exposes CRUD operations the HTTP API and run manager build on.
package store

import "time"

// Project is a top-level workspace grouping repositories, blueprints, and
// graph runs. SearchRoots provide project-scoped repository discovery roots;
// Instruction is the project-level default extraction instruction that repos may
// override.
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	SearchRoots []string  `json:"search_roots,omitempty"`
	Instruction string    `json:"instruction,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Repo is a project-scoped repository reference. Path points at the source repo
// on disk (DiffMind never mutates it). Kind is service_repo or infra_repo.
// BlueprintIDs and Instruction are repo-level overrides of the project
// defaults: a non-empty value wins over the project's.
type Repo struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Path              string    `json:"path"`
	Kind              string    `json:"kind"` // service_repo | infra_repo
	SourceType        string    `json:"source_type,omitempty"`
	GitURL            string    `json:"git_url,omitempty"`
	GitProvider       string    `json:"git_provider,omitempty"`
	ClonePath         string    `json:"clone_path,omitempty"`
	DefaultBranch     string    `json:"default_branch,omitempty"`
	HeadSHA           string    `json:"head_sha,omitempty"`
	RemoteHeadSHA     string    `json:"remote_head_sha,omitempty"`
	SyncStatus        string    `json:"sync_status,omitempty"`
	SyncError         string    `json:"sync_error,omitempty"`
	LastSyncedAt      time.Time `json:"last_synced_at,omitempty"`
	Team              string    `json:"team,omitempty"`
	LastDiffMindRunID string    `json:"last_diffmind_run_id,omitempty"`
	DiffMindFreshness string    `json:"diffmind_freshness,omitempty"`
	BlueprintIDs      []string  `json:"blueprint_ids,omitempty"`
	Instruction       string    `json:"instruction,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// BlueprintMeta is the listing projection of a stored blueprint. The full
// blueprint body is the raw JSON file on disk; the store returns it verbatim so
// the in-UI editor round-trips exactly what the user typed.
type BlueprintMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunStatus values for a DiffMind graph run.
const (
	RunRunning    = "running"
	RunCancelling = "cancelling"
	RunCompleted  = "completed"
	RunFailed     = "failed"
	RunCancelled  = "cancelled"
)

// RunRepoRef binds a project repo to the specific DiffMind run whose artifacts
// should feed this graph run.
type RunRepoRef struct {
	RepoID        string `json:"repo_id"`
	DiffMindRunID string `json:"diffmind_run_id"`
}

// RunManifest is the persisted state of a single graph run.
type RunManifest struct {
	ID           string         `json:"id"`
	ProjectID    string         `json:"project_id"`
	Status       string         `json:"status"`
	Repos        []RunRepoRef   `json:"repos"`
	Options      map[string]any `json:"options,omitempty"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at,omitempty"`
	Error        string         `json:"error,omitempty"`
	ServiceCount int            `json:"service_count"`
	EdgeCount    int            `json:"edge_count"`
	GraphQuality *GraphQuality  `json:"graph_quality,omitempty"`
}

// GraphQuality stores deterministic graph quality counters surfaced after a
// graph build. These are warnings, not hard failures: they tell the user where
// configuration aliases or detectors should be improved.
type GraphQuality struct {
	UnresolvedExternalServices int      `json:"unresolved_external_services"`
	PathShapedExternalNodes    int      `json:"path_shaped_external_nodes"`
	MissingEvidenceObjects     int      `json:"missing_evidence_objects"`
	StaleRepos                 int      `json:"stale_repos"`
	DirtyRepos                 int      `json:"dirty_repos"`
	Warnings                   []string `json:"warnings,omitempty"`
}
