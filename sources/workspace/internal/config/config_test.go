package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewDefault(t *testing.T) {
	cfg := NewDefault()
	if cfg.OpenCode.BaseURL != "http://localhost:3000" {
		t.Errorf("expected default base URL, got %s", cfg.OpenCode.BaseURL)
	}
	if cfg.OpenCode.Timeout != 120 {
		t.Errorf("expected default timeout 120, got %d", cfg.OpenCode.Timeout)
	}
	if cfg.Artifacts.BaseDir != ".diffmind/runs" {
		t.Errorf("expected default artifacts dir, got %s", cfg.Artifacts.BaseDir)
	}
	if len(cfg.Blueprints.Dirs) != 2 {
		t.Errorf("expected 2 default blueprint dirs, got %d", len(cfg.Blueprints.Dirs))
	}
}

func TestLoadFromFile(t *testing.T) {
	// Find testdata relative to project root.
	// The test config is at the project root's testdata directory.
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	configPath := filepath.Join(projectRoot, "testdata", "diffmind-test-config.json")

	cfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.Repos.ServiceRepos) != 2 {
		t.Errorf("expected 2 service repos, got %d", len(cfg.Repos.ServiceRepos))
	}
	if len(cfg.Repos.InfraRepos) != 1 {
		t.Errorf("expected 1 infra repo, got %d", len(cfg.Repos.InfraRepos))
	}
	if cfg.Repos.ServiceRepos[0].Name != "order-service" {
		t.Errorf("expected first service to be order-service, got %s", cfg.Repos.ServiceRepos[0].Name)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	os.Setenv("OPENCODE_SERVER_USERNAME", "testuser")
	os.Setenv("OPENCODE_SERVER_PASSWORD", "testpass")
	defer os.Unsetenv("OPENCODE_SERVER_USERNAME")
	defer os.Unsetenv("OPENCODE_SERVER_PASSWORD")

	cfg := NewDefault()
	if cfg.OpenCode.Username != "testuser" {
		t.Errorf("expected username from env, got %s", cfg.OpenCode.Username)
	}
	if cfg.OpenCode.Password != "testpass" {
		t.Errorf("expected password from env, got %s", cfg.OpenCode.Password)
	}
}
