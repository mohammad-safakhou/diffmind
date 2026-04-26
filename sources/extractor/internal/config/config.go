package config

import (
	"encoding/json"
	"os"
)

type OpenCode struct {
	BaseURL      string `json:"base_url"`
	ProviderID   string `json:"provider_id"`
	ModelID      string `json:"model_id"`
	ModelVariant string `json:"model_variant"`
	TimeoutSec   int    `json:"timeout_seconds"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

type Quality struct {
	MinConfidence float64 `json:"min_confidence"`
}

type Runtime struct {
	Workers                 int  `json:"workers"`
	MaxCatalogItems         int  `json:"max_catalog_items"`
	CleanupOpenCodeSessions bool `json:"cleanup_opencode_sessions"`
	OpenCodeDeleteDelaySec  int  `json:"opencode_delete_delay_seconds"`
	ReuseOpenCodeSession    bool `json:"reuse_opencode_session"`
	SkipReexamination       bool `json:"skip_reexamination"`
}

type Artifacts struct {
	BaseDir string `json:"base_dir"`
}

type Config struct {
	OpenCode  OpenCode  `json:"opencode"`
	Quality   Quality   `json:"quality"`
	Runtime   Runtime   `json:"runtime"`
	Artifacts Artifacts `json:"artifacts"`
}

func Default() Config {
	return Config{
		OpenCode: OpenCode{TimeoutSec: 300},
		Quality: Quality{
			MinConfidence: 0.70,
		},
		Runtime: Runtime{
			Workers:                 16,
			MaxCatalogItems:         80,
			CleanupOpenCodeSessions: false,
			OpenCodeDeleteDelaySec:  5,
			ReuseOpenCodeSession:    false,
		},
		Artifacts: Artifacts{BaseDir: ".diffmind/runs"},
	}
}

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
	return cfg, nil
}
