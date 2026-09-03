// Package store is the filesystem-backed persistence layer for DiffMind
// projects, repositories, packs, and graph runs. Everything lives under
// the DiffMind home directory (see config.Home); the store owns the on-disk
// layout and exposes CRUD operations the HTTP API and run manager build on.
package store

import (
	"encoding/json"
	"time"
)

// Project is a top-level workspace grouping repositories, packs, and
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

const (
	IngestionRunning     = "running"
	IngestionCompleted   = "completed"
	IngestionPartial     = "partial"
	IngestionFailed      = "failed"
	IngestionCancelled   = "cancelled"
	IngestionInterrupted = "interrupted"
)

// Ingestion is the durable projection of the latest project bootstrap or
// refresh initiated from the UI. Repository-level detail remains on Repo;
// this record makes the whole import-to-graph operation observable.
type Ingestion struct {
	JobID            string          `json:"job_id,omitempty"`
	AttemptStartedAt time.Time       `json:"attempt_started_at,omitempty"`
	ID               string          `json:"id"`
	ProjectID        string          `json:"project_id"`
	Status           string          `json:"status"`
	Phase            string          `json:"phase"`
	Provider         string          `json:"provider,omitempty"`
	Source           string          `json:"source,omitempty"`
	Discovered       int             `json:"discovered"`
	Imported         int             `json:"imported"`
	Skipped          int             `json:"skipped"`
	Repositories     int             `json:"repositories"`
	Synced           int             `json:"synced"`
	Analyzed         int             `json:"analyzed"`
	Reused           int             `json:"reused"`
	Request          json.RawMessage `json:"request,omitempty"`
	ImportComplete   bool            `json:"import_complete,omitempty"`
	Attempt          int             `json:"attempt"`
	CancelRequested  bool            `json:"cancel_requested,omitempty"`
	RepoProgress     []IngestionRepo `json:"repo_progress,omitempty"`
	GraphRunID       string          `json:"graph_run_id,omitempty"`
	Errors           []string        `json:"errors,omitempty"`
	StartedAt        time.Time       `json:"started_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	FinishedAt       time.Time       `json:"finished_at,omitempty"`
}

// IngestionRepo is a persisted checkpoint, not a promise that inputs stay fresh.
// Resume revalidates the repository fingerprint before reusing its artifacts.
type IngestionRepo struct {
	RepoID string `json:"repo_id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	RunID  string `json:"run_id,omitempty"`
}

// Repo is a project-scoped repository reference. Path points at the source repo
// on disk (DiffMind never mutates it). Kind is service_repo or infra_repo.
// PackIDs and Instruction are repo-level overrides of the project
// defaults: a non-empty value wins over the project's.
type Repo struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Path                   string    `json:"path"`
	Kind                   string    `json:"kind"` // service_repo | infra_repo
	SourceType             string    `json:"source_type,omitempty"`
	GitURL                 string    `json:"git_url,omitempty"`
	GitProvider            string    `json:"git_provider,omitempty"`
	ClonePath              string    `json:"clone_path,omitempty"`
	DefaultBranch          string    `json:"default_branch,omitempty"`
	HeadSHA                string    `json:"head_sha,omitempty"`
	RemoteHeadSHA          string    `json:"remote_head_sha,omitempty"`
	SyncStatus             string    `json:"sync_status,omitempty"`
	SyncError              string    `json:"sync_error,omitempty"`
	LastSyncedAt           time.Time `json:"last_synced_at,omitempty"`
	Team                   string    `json:"team,omitempty"`
	LastDiffMindRunID      string    `json:"last_diffmind_run_id,omitempty"`
	AnalysisFingerprint    string    `json:"analysis_fingerprint,omitempty"`
	AnalysisArtifactDigest string    `json:"analysis_artifact_digest,omitempty"`
	DiffMindFreshness      string    `json:"diffmind_freshness,omitempty"`
	PackIDs                []string  `json:"pack_ids,omitempty"`
	Instruction            string    `json:"instruction,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// PackMeta is the listing projection of a stored pack. The full
// pack body is the raw JSON file on disk; the store returns it verbatim so
// the in-UI editor round-trips exactly what the user typed.
type PackMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Priority  int       `json:"priority"`
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
