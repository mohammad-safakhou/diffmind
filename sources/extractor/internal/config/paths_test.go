package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHomeHonoursEnv verifies DIFFMIND_HOME overrides the default location and
// that RunsDir/FilePath derive from it.
func TestHomeHonoursEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DIFFMIND_HOME", tmp)
	if Home() != tmp {
		t.Fatalf("Home() = %q, want %q", Home(), tmp)
	}
	if RunsDir() != filepath.Join(tmp, "runs") {
		t.Fatalf("RunsDir() = %q", RunsDir())
	}
	if FilePath() != filepath.Join(tmp, "config.json") {
		t.Fatalf("FilePath() = %q", FilePath())
	}
}

// TestDefaultBaseDirIsCentral verifies the built-in default now points at the
// central runs directory rather than a repo-local one.
func TestDefaultBaseDirIsCentral(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DIFFMIND_HOME", tmp)
	cfg := Default()
	if cfg.Artifacts.BaseDir != filepath.Join(tmp, "runs") {
		t.Fatalf("default base dir = %q, want central runs dir", cfg.Artifacts.BaseDir)
	}
}

// TestLoadCentralFallsBackToDefaults verifies a missing config file yields
// defaults, not an error.
func TestLoadCentralFallsBackToDefaults(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DIFFMIND_HOME", tmp)
	cfg, err := LoadCentral("")
	if err != nil {
		t.Fatalf("LoadCentral with no file should not error: %v", err)
	}
	if cfg.Artifacts.BaseDir != filepath.Join(tmp, "runs") {
		t.Fatalf("base dir = %q", cfg.Artifacts.BaseDir)
	}
}

// TestLoadCentralReadsHomeConfig verifies ~/.diffmind/config.json is picked up
// when present and its values override defaults.
func TestLoadCentralReadsHomeConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DIFFMIND_HOME", tmp)
	body := `{"runtime":{"workers":9}}`
	if err := os.WriteFile(FilePath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadCentral("")
	if err != nil {
		t.Fatalf("LoadCentral: %v", err)
	}
	if cfg.Runtime.Workers != 9 {
		t.Fatalf("workers = %d, want 9", cfg.Runtime.Workers)
	}
	// Unspecified base dir defaults to the central runs dir.
	if cfg.Artifacts.BaseDir != filepath.Join(tmp, "runs") {
		t.Fatalf("base dir = %q", cfg.Artifacts.BaseDir)
	}
}

// TestLoadCentralExplicitPathWins verifies an explicit path is honoured over
// the home config.
func TestLoadCentralExplicitPathWins(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DIFFMIND_HOME", tmp)
	explicit := filepath.Join(tmp, "explicit.json")
	if err := os.WriteFile(explicit, []byte(`{"runtime":{"workers":11}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadCentral(explicit)
	if err != nil {
		t.Fatalf("LoadCentral: %v", err)
	}
	if cfg.Runtime.Workers != 11 {
		t.Fatalf("workers = %d, want 11", cfg.Runtime.Workers)
	}
}
