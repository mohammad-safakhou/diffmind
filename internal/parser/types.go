package parser

import "time"

const parserVersion = "v1"

type Options struct {
	Source     string
	OutDir     string
	SnapshotID string
}

type ParseReport struct {
	GeneratedAt      time.Time `json:"generated_at"`
	SourceRoot       string    `json:"source_root"`
	SnapshotID       string    `json:"snapshot_id"`
	ParserVersion    string    `json:"parser_version"`
	TotalFiles       int       `json:"total_files"`
	ArtifactsCreated int       `json:"artifacts_created"`
	StructuredCount  int       `json:"structured_count"`
	TreeSitterCount  int       `json:"tree_sitter_count"`
	FallbackCount    int       `json:"fallback_count"`
	FailedCount      int       `json:"failed_count"`
}

type ParseArtifact struct {
	SnapshotID    string         `json:"snapshot_id"`
	FilePath      string         `json:"file_path"`
	FileHash      string         `json:"file_hash"`
	ParserVersion string         `json:"parser_version"`
	ArtifactType  string         `json:"artifact_type"`
	Language      string         `json:"language,omitempty"`
	ParserName    string         `json:"parser_name"`
	LineCount     int            `json:"line_count"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Symbols       []Symbol       `json:"symbols,omitempty"`
	Tree          *TreeSummary   `json:"tree,omitempty"`
	Structured    map[string]any `json:"structured,omitempty"`
	Error         string         `json:"error,omitempty"`
}

type TreeSummary struct {
	RootType  string `json:"root_type"`
	NodeCount int    `json:"node_count"`
}

type Symbol struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	StartLine int    `json:"start_line"`
	StartCol  int    `json:"start_col"`
	EndLine   int    `json:"end_line"`
	EndCol    int    `json:"end_col"`
}
