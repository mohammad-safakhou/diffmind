package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Central storage layout. DiffMind keeps all project data under a single home
// directory so projects, repos, blueprints, and graph runs are discoverable
// independent of the working directory.
//
//	~/.diffmind/
//	  config.json                       (global defaults: search roots)
//	  projects/<project_id>/
//	    project.json
//	    blueprints/<blueprint_id>.json
//	    repos/<repo_id>/repo.json
//	    runs/<run_id>/{manifest.json,graph.json,events.jsonl,identities/}
//
// The location can be overridden with the DIFFMIND_HOME environment variable
// (handy for tests).

// Home returns the DiffMind home directory ($DIFFMIND_HOME or ~/.diffmind).
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

// ProjectsDir returns ~/.diffmind/projects.
func ProjectsDir() string { return filepath.Join(Home(), "projects") }

// GlobalConfigPath returns ~/.diffmind/config.json.
func GlobalConfigPath() string { return filepath.Join(Home(), "config.json") }

// DiffMindRunsDir returns the central DiffMind runs directory that DiffMind
// discovers runs from. It honours DIFFMIND_HOME (matching DiffMind's own
// resolution) and falls back to ~/.diffmind/runs.
func DiffMindRunsDir() string {
	if v := os.Getenv("DIFFMIND_HOME"); v != "" {
		return filepath.Join(v, "runs")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".diffmind", "runs")
	}
	return filepath.Join(home, ".diffmind", "runs")
}

// GlobalConfig is the optional ~/.diffmind/config.json. It holds defaults shared
// across projects, currently global repo search roots.
type GlobalConfig struct {
	SearchRoots []string `json:"search_roots,omitempty"`
}

// LoadGlobal reads ~/.diffmind/config.json if present. A missing file yields a
// zero-value GlobalConfig (not an error).
func LoadGlobal() (*GlobalConfig, error) {
	path := GlobalConfigPath()
	gc := &GlobalConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, gc); err != nil {
			return gc, err
		}
	} else if !os.IsNotExist(err) {
		return gc, err
	}
	return gc, nil
}
