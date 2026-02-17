package graphschema

import "time"

type Graph struct {
	GraphID     string     `json:"graph_id"`
	GeneratedAt time.Time  `json:"generated_at"`
	Mode        string     `json:"mode"`
	Nodes       []Node     `json:"nodes"`
	Edges       []Edge     `json:"edges"`
	Stats       GraphStats `json:"stats"`
	Meta        GraphMeta  `json:"meta"`
}

type GraphMeta struct {
	TenantID   string          `json:"tenant_id,omitempty"`
	Services   []ServiceMeta   `json:"services"`
	Provenance GraphProvenance `json:"provenance,omitempty"`
	Freshness  GraphFreshness  `json:"freshness,omitempty"`
}

type ServiceMeta struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	RepoPath       string            `json:"repo_path,omitempty"`
	BundlePath     string            `json:"bundle_path"`
	AnalyzerBundle string            `json:"analyzer_bundle_path,omitempty"`
	BaseURLs       []string          `json:"base_urls,omitempty"`
	QueuePublishes []string          `json:"queue_publishes,omitempty"`
	QueueConsumes  []string          `json:"queue_consumes,omitempty"`
	DBReads        []string          `json:"db_reads,omitempty"`
	DBWrites       []string          `json:"db_writes,omitempty"`
	Provenance     ServiceProvenance `json:"provenance,omitempty"`
}

type GraphProvenance struct {
	Tool        string `json:"tool,omitempty"`
	GeneratedBy string `json:"generated_by,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type GraphFreshness struct {
	EvaluatedAt time.Time `json:"evaluated_at,omitempty"`
	MaxAgeHours int       `json:"max_age_hours,omitempty"`
	AgeSeconds  int64     `json:"age_seconds,omitempty"`
	IsStale     bool      `json:"is_stale"`
}

type ServiceProvenance struct {
	OutputRoot           string `json:"output_root,omitempty"`
	RunReportPath        string `json:"run_report_path,omitempty"`
	RunSource            string `json:"run_source,omitempty"`
	RunRef               string `json:"run_ref,omitempty"`
	SnapshotID           string `json:"snapshot_id,omitempty"`
	SnapshotPath         string `json:"snapshot_path,omitempty"`
	SnapshotRepoLocator  string `json:"snapshot_repo_locator,omitempty"`
	SnapshotRef          string `json:"snapshot_ref,omitempty"`
	SnapshotCommitSHA    string `json:"snapshot_commit_sha,omitempty"`
	SnapshotSourceType   string `json:"snapshot_source_type,omitempty"`
	BundleSHA256         string `json:"bundle_sha256,omitempty"`
	AnalyzerBundleSHA256 string `json:"analyzer_bundle_sha256,omitempty"`
}

type GraphStats struct {
	NodeCount int            `json:"node_count"`
	EdgeCount int            `json:"edge_count"`
	ByNode    map[string]int `json:"by_node_type"`
	ByEdge    map[string]int `json:"by_edge_type"`
}

type Node struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Label      string         `json:"label"`
	ServiceID  string         `json:"service_id,omitempty"`
	Attributes map[string]any `json:"attributes"`
	Confidence float64        `json:"confidence"`
	Inferred   bool           `json:"inferred"`
}

type Edge struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	SourceID     string         `json:"source_id"`
	TargetID     string         `json:"target_id"`
	Attributes   map[string]any `json:"attributes"`
	Confidence   float64        `json:"confidence"`
	Inferred     bool           `json:"inferred"`
	EvidenceRefs []EvidenceRef  `json:"evidence_refs"`
}

type EvidenceRef struct {
	SnapshotID string `json:"snapshot_id,omitempty"`
	FilePath   string `json:"file_path,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	StartCol   int    `json:"start_col,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	EndCol     int    `json:"end_col,omitempty"`
	FactID     string `json:"fact_id,omitempty"`
	EvidenceID string `json:"evidence_id,omitempty"`
}

type Index struct {
	Graphs []Summary `json:"graphs"`
}

type Summary struct {
	GraphID     string    `json:"graph_id"`
	GeneratedAt time.Time `json:"generated_at"`
	TenantID    string    `json:"tenant_id,omitempty"`
	Mode        string    `json:"mode"`
	NodeCount   int       `json:"node_count"`
	EdgeCount   int       `json:"edge_count"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Path        string    `json:"path"`
}
