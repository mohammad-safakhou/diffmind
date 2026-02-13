package facts

import "time"

type Fact struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Attributes   map[string]any `json:"attributes"`
	EvidenceIDs  []string       `json:"evidence_ids"`
	Confidence   float64        `json:"confidence"`
	Provenance   Provenance     `json:"provenance"`
	CreatedAtUTC time.Time      `json:"created_at_utc"`
}

type Evidence struct {
	ID           string    `json:"id"`
	SnapshotID   string    `json:"snapshot_id"`
	FilePath     string    `json:"file_path"`
	StartLine    int       `json:"start_line"`
	StartCol     int       `json:"start_col"`
	EndLine      int       `json:"end_line"`
	EndCol       int       `json:"end_col"`
	SnippetHash  string    `json:"snippet_hash"`
	ASTNodeID    string    `json:"ast_node_id,omitempty"`
	QueryName    string    `json:"query_name,omitempty"`
	CreatedAtUTC time.Time `json:"created_at_utc"`
}

type Provenance struct {
	AnalyzerID      string `json:"analyzer_id"`
	AnalyzerVersion string `json:"analyzer_version"`
	Deterministic   bool   `json:"deterministic"`
	Inferred        bool   `json:"inferred"`
}

type Bundle struct {
	Facts     []Fact     `json:"facts"`
	Evidence  []Evidence `json:"evidence"`
	Generated time.Time  `json:"generated"`
}
