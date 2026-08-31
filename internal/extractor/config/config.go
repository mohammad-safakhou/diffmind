package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const PipelineDeterministic = "deterministic"

type Quality struct {
	MinConfidence float64 `json:"min_confidence"`
}

type Runtime struct {
	// Pipeline is retained only to reject stale saved configs that still ask for
	// the removed LLM pipeline. New callers should not set it.
	Pipeline string `json:"pipeline,omitempty"`
	Workers  int    `json:"workers"`
}

type Artifacts struct {
	BaseDir string `json:"base_dir"`
}

// Indexer holds AST analysis configuration. The tree-sitter engine
// auto-detects languages from file extensions; Languages can be set to give a
// primary-language hint for repos where detection is ambiguous.
type Indexer struct {
	Languages []string `json:"languages"`
}

type Config struct {
	Quality   Quality   `json:"quality"`
	Runtime   Runtime   `json:"runtime"`
	Artifacts Artifacts `json:"artifacts"`
	Indexer   Indexer   `json:"indexer"`
}

func Default() Config {
	return Config{
		Quality: Quality{MinConfidence: 0.70},
		Runtime: Runtime{
			Pipeline: PipelineDeterministic,
			Workers:  6,
		},
		Artifacts: Artifacts{BaseDir: RunsDir()},
		Indexer:   Indexer{Languages: nil},
	}
}

func NormalizePipeline(value string) string {
	if strings.TrimSpace(value) == "" {
		return PipelineDeterministic
	}
	if strings.EqualFold(strings.TrimSpace(value), PipelineDeterministic) {
		return PipelineDeterministic
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func (c Config) Pipeline() string { return PipelineDeterministic }

func (c Config) IsDeterministicPipeline() bool { return true }

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	switch NormalizePipeline(c.Runtime.Pipeline) {
	case "", PipelineDeterministic:
		return nil
	case "llm":
		return fmt.Errorf("runtime.pipeline=llm is no longer supported; DiffMind is deterministic-only")
	default:
		return fmt.Errorf("runtime.pipeline=%q is unsupported; DiffMind only supports deterministic runs", c.Runtime.Pipeline)
	}
}

// SanitizationFix records a single defensive correction made by Sanitize.
type SanitizationFix struct {
	Field    string `json:"field"`
	Was      int    `json:"was"`
	Adjusted int    `json:"adjusted"`
	Reason   string `json:"reason"`
}

// Sanitize normalizes non-sensical deterministic runtime values. It is kept as
// an explicit step because run events and tests surface these corrections.
func (c *Config) Sanitize() []SanitizationFix {
	var fixes []SanitizationFix
	def := Default()
	if c.Runtime.Workers <= 0 {
		fixes = append(fixes, SanitizationFix{
			Field: "runtime.workers", Was: c.Runtime.Workers, Adjusted: def.Runtime.Workers,
			Reason: "worker count must be positive; reset to deterministic default",
		})
		c.Runtime.Workers = def.Runtime.Workers
	}
	c.Runtime.Pipeline = PipelineDeterministic
	if c.Quality.MinConfidence < 0 {
		c.Quality.MinConfidence = 0
	}
	if c.Quality.MinConfidence > 1 {
		c.Quality.MinConfidence = 1
	}
	return fixes
}
