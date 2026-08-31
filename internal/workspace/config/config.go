// Package config handles DiffMind configuration loading.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the top-level DiffMind configuration.
type Config struct {
	DiffMind   DiffMindConfig   `json:"diffmind"`
	Repos      ReposConfig      `json:"repos"`
	Blueprints BlueprintsConfig `json:"blueprints"`
	Artifacts  ArtifactsConfig  `json:"artifacts"`
}

// DiffMindConfig configures how DiffMind invokes DiffMind.
type DiffMindConfig struct {
	BinaryPath    string `json:"binary_path"`
	DefaultConfig string `json:"default_config"`
}

// RepoEntry describes a single repository.
type RepoEntry struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	DiffMindArtifacts string `json:"diffmind_artifacts,omitempty"` // direct path to artifacts
}

// ReposConfig lists all known repositories.
type ReposConfig struct {
	ServiceRepos []RepoEntry `json:"service_repos"`
	InfraRepos   []RepoEntry `json:"infra_repos"`
}

// BlueprintsConfig tells DiffMind where to find blueprints.
type BlueprintsConfig struct {
	Dirs []string `json:"dirs"`
}

// ArtifactsConfig controls output location.
type ArtifactsConfig struct {
	BaseDir string `json:"base_dir"`
}

// Defaults applies default values to the config.
func (c *Config) Defaults() {
	if c.DiffMind.BinaryPath == "" {
		c.DiffMind.BinaryPath = "diffmind"
	}
	if c.Artifacts.BaseDir == "" {
		c.Artifacts.BaseDir = ".diffmind/runs"
	}
	if len(c.Blueprints.Dirs) == 0 {
		c.Blueprints.Dirs = []string{".diffmind/blueprints", "blueprints"}
	}
}

// LoadFromFile reads a JSON config file and returns a Config with defaults applied.
func LoadFromFile(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	cfg.Defaults()
	return &cfg, nil
}

// NewDefault returns a Config with all defaults applied (no file).
func NewDefault() *Config {
	cfg := &Config{}
	cfg.Defaults()
	return cfg
}
