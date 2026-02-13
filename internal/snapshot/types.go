package snapshot

import "time"

const ToolVersion = "dev"

type Options struct {
	Source  string
	Ref     string
	OutDir  string
	Persist bool
}

type PreparedSource struct {
	RepoLocator string
	Ref         string
	CommitSHA   string
	Workdir     string
	SourceType  string
}

type FileEntry struct {
	Path           string `json:"path"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256"`
	FileType       string `json:"file_type"`
	Classification string `json:"classification"`
}

type Snapshot struct {
	SnapshotID  string    `json:"snapshot_id"`
	RepoLocator string    `json:"repo_locator"`
	Ref         string    `json:"ref"`
	CommitSHA   string    `json:"commit_sha,omitempty"`
	SourceType  string    `json:"source_type"`
	FileCount   int       `json:"file_count"`
	GeneratedAt time.Time `json:"generated_at"`
	ToolVersion string    `json:"tool_version"`
}
