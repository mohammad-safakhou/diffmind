package analyzers

import "time"

type llmOptions struct {
	Enabled        bool
	Model          string
	Task           string
	MaxFiles       int
	MaxChars       int
	DefaultConf    float64
	TraceOutputDir string
}

type llmTrace struct {
	TimestampUTC      time.Time       `json:"timestamp_utc"`
	Model             string          `json:"model"`
	Task              string          `json:"task"`
	MaxFiles          int             `json:"max_files"`
	MaxChars          int             `json:"max_chars"`
	EvidenceCount     int             `json:"evidence_count"`
	EvidenceFileCount int             `json:"evidence_file_count"`
	Prompt            string          `json:"prompt"`
	RawResponse       string          `json:"raw_response"`
	ParsedFacts       []llmFactOutput `json:"parsed_facts"`
}

type llmFactOutput struct {
	Type        string         `json:"type"`
	Attributes  map[string]any `json:"attributes"`
	EvidenceIDs []string       `json:"evidence_ids"`
	Confidence  float64        `json:"confidence,omitempty"`
}

type llmResponsePayload struct {
	Facts []llmFactOutput `json:"facts"`
}
