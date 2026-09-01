// Package model defines the core domain types shared across DiffMind.
// DiffMind-compatible types live here so the artifact reader can
// deserialise run output directly.
package model

import (
	"time"

	"github.com/mohammad-safakhou/diffmind/protocol"
)

// ---------------------------------------------------------------------------
// DiffMind-compatible types (mirror of diffmind/internal/model)
// ---------------------------------------------------------------------------

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
	Details        map[string]any   `json:"details,omitempty"`
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
	RunID             string                 `json:"run_id"`
	StartedAt         time.Time              `json:"started_at"`
	FinishedAt        time.Time              `json:"finished_at"`
	RepoPath          string                 `json:"repo_path"`
	Team              string                 `json:"team,omitempty"`
	RepoGitSHA        string                 `json:"repo_git_sha,omitempty"`
	RepoGitBranch     string                 `json:"repo_git_branch,omitempty"`
	RepoGitRemoteURL  string                 `json:"repo_git_remote_url,omitempty"`
	RepoGitDirty      bool                   `json:"repo_git_dirty,omitempty"`
	SchemaVersion     string                 `json:"schema_version"`
	ConfidenceMinimum float64                `json:"confidence_minimum"`
	Counts            map[string]int         `json:"counts"`
	RepoMetrics       *RepoMetrics           `json:"repo_metrics,omitempty"`
	Warnings          []string               `json:"warnings,omitempty"`
	Metadata          map[string]string      `json:"metadata,omitempty"`
	StageFailures     map[string]int         `json:"stage_failures,omitempty"`
	TokenTotals       map[string]TokenBucket `json:"token_totals,omitempty"`
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

type Resource struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Platform string         `json:"platform,omitempty"`
	Name     string         `json:"name"`
	Instance string         `json:"instance,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
	Status   string         `json:"status,omitempty"`
	Source   string         `json:"source,omitempty"`
}

// ---------------------------------------------------------------------------
// DiffMind-specific types
// ---------------------------------------------------------------------------

// ServiceArchitecture holds the full DiffMind output for one service.
type ServiceArchitecture struct {
	ServiceName  string
	RepoPath     string
	Manifest     *RunManifest
	Protocol     *protocol.Document
	Resources    []Resource
	Exposures    []Exposure
	Dependencies []Dependency
	Connections  []Connection
	Unresolved   []UnresolvedItem
}

// ServiceIdentity holds the extracted identity of a single service,
// derived from running extraction packs against the repo.
type ServiceIdentity struct {
	ServiceName string            `json:"service_name"`
	RepoPath    string            `json:"repo_path"`
	Aliases     []IdentityAlias   `json:"aliases"`
	Resources   []OwnedResource   `json:"resources,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// IdentityAlias is a single network-resolvable name for a service.
type IdentityAlias struct {
	Kind  string `json:"kind"`  // dns, k8s_service, iam_role, etc.
	Value string `json:"value"` // e.g. "billing.internal"
}

// OwnedResource is a resource owned/used by the service.
type OwnedResource struct {
	Kind       string `json:"kind"`       // database, queue, s3_bucket, lambda, etc.
	Identifier string `json:"identifier"` // e.g. "order-events", "orders-db"
	Role       string `json:"role"`       // owner, consumer, publisher, reader, writer
}

// ---------------------------------------------------------------------------
// Cross-service graph types
// ---------------------------------------------------------------------------

// GraphNode represents a service in the cross-service graph.
type GraphNode struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	RepoPath        string          `json:"repo_path,omitempty"`
	Identity        ServiceIdentity `json:"identity"`
	ExposuresCount  int             `json:"exposures_count"`
	DependencyCount int             `json:"dependencies_count"`
}

// GraphEdge represents a connection between two services.
type GraphEdge struct {
	ID             string      `json:"id"`
	FromService    string      `json:"from_service"`
	ToService      string      `json:"to_service"`
	Type           string      `json:"type"` // http, queue, rpc, shared_db, grpc
	FromDependency string      `json:"from_dependency,omitempty"`
	ToExposure     string      `json:"to_exposure,omitempty"`
	Label          string      `json:"label,omitempty"`
	Conditions     []Condition `json:"conditions,omitempty"`
	Evidence       []Evidence  `json:"evidence,omitempty"`
	Confidence     float64     `json:"confidence"`
}

// SharedResource represents a resource accessed by multiple services.
type SharedResource struct {
	Kind       string   `json:"kind"`
	Identifier string   `json:"identifier"`
	Services   []string `json:"services"`
}

// CrossServiceGraph is the final output of DiffMind.
type CrossServiceGraph struct {
	Version         string           `json:"version"`
	GeneratedAt     time.Time        `json:"generated_at"`
	Services        []GraphNode      `json:"services"`
	Edges           []GraphEdge      `json:"edges"`
	SharedResources []SharedResource `json:"shared_resources,omitempty"`
	Unresolved      []UnresolvedEdge `json:"unresolved,omitempty"`
}

// UnresolvedEdge is a dependency that could not be matched to any service.
type UnresolvedEdge struct {
	Service        string `json:"service"`
	DependencyID   string `json:"dependency_id"`
	DependencyName string `json:"dependency_name"`
	Type           string `json:"type"`
	Target         string `json:"target,omitempty"` // raw target URL/host/queue name
	Reason         string `json:"reason"`
}
