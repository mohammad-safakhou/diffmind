package config

import (
	"os"
	"path/filepath"
)

// Central storage layout. DiffMind keeps all run artifacts and its default
// configuration under a single home directory so runs are discoverable from
// anywhere (and by DiffMind), independent of which repository was scanned.
//
//	~/.diffmind/
//	  config.json   (CLI + UI defaults)
//	  runs/<run_id> (artifacts)
//
// The location can be overridden with the DIFFMIND_HOME environment variable,
// which is handy for tests and for users who keep their home directory on a
// small volume.

// Home returns the DiffMind home directory. It honours $DIFFMIND_HOME, then
// falls back to ~/.diffmind. If the user home cannot be resolved (rare, e.g.
// a stripped environment) it falls back to a repo-local .diffmind so the tool
// still functions.
func Home() string {
	if v := os.Getenv("DIFFMIND_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".diffmind"
	}
	return filepath.Join(home, ".diffmind")
}

// RunsDir returns the central run artifacts directory (~/.diffmind/runs).
func RunsDir() string { return filepath.Join(Home(), "runs") }

// FilePath returns the path to the central config file (~/.diffmind/config.json).
func FilePath() string { return filepath.Join(Home(), "config.json") }

// LoadCentral resolves the effective configuration for the CLI and UI.
//
//   - If explicitPath is non-empty, it is loaded directly (an error is returned
//     if it cannot be read/parsed).
//   - Otherwise, ~/.diffmind/config.json is loaded when present; if it is
//     absent, built-in Defaults are used.
//
// In all cases Artifacts.BaseDir is defaulted to the central runs directory
// when the loaded config leaves it blank, so runs always land somewhere
// predictable.
func LoadCentral(explicitPath string) (Config, error) {
	if explicitPath != "" {
		cfg, err := Load(explicitPath)
		if err != nil {
			return cfg, err
		}
		if cfg.Artifacts.BaseDir == "" {
			cfg.Artifacts.BaseDir = RunsDir()
		}
		return cfg, nil
	}

	central := FilePath()
	if _, err := os.Stat(central); err == nil {
		cfg, err := Load(central)
		if err != nil {
			return cfg, err
		}
		if cfg.Artifacts.BaseDir == "" {
			cfg.Artifacts.BaseDir = RunsDir()
		}
		return cfg, nil
	}
	return Default(), nil
}
