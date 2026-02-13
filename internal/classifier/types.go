package classifier

import "time"

type Options struct {
	Source string
	OutDir string
}

type ScanReport struct {
	GeneratedAt   time.Time        `json:"generated_at"`
	SourceRoot    string           `json:"source_root"`
	Profile       RepoProfile      `json:"profile"`
	Capabilities  RepoCapabilities `json:"capabilities"`
	Stats         RepoStats        `json:"stats"`
	ToolVersion   string           `json:"tool_version"`
	AnalyzerID    string           `json:"analyzer_id"`
	AnalyzerStage string           `json:"analyzer_stage"`
}

type RepoProfile struct {
	Labels []LabelScore `json:"labels"`
}

type LabelScore struct {
	Label      string   `json:"label"`
	Confidence float64  `json:"confidence"`
	Evidence   []string `json:"evidence"`
}

type RepoCapabilities struct {
	Languages  []LanguageCount `json:"languages"`
	BuildTools []Capability    `json:"build_tools"`
	CI         []Capability    `json:"ci"`
	IaC        []Capability    `json:"iac"`
	APISpecs   []Capability    `json:"api_specs"`
	Migrations []Capability    `json:"migrations"`
	Containers []Capability    `json:"containers"`
}

type LanguageCount struct {
	Language string   `json:"language"`
	Count    int      `json:"count"`
	Evidence []string `json:"evidence"`
}

type Capability struct {
	Name       string   `json:"name"`
	Evidence   []string `json:"evidence"`
	Confidence float64  `json:"confidence"`
}

type RepoStats struct {
	TotalFiles     int            `json:"total_files"`
	SourceFiles    int            `json:"source_files"`
	ConfigFiles    int            `json:"config_files"`
	ExtensionCount map[string]int `json:"extension_count"`
}

type ScannedFile struct {
	Path string
	Ext  string
}
