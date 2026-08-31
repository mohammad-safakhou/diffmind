package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func ValidateRun(baseDir, runID string) error {
	manifestPath := filepath.Join(baseDir, runID, "run_manifest.json")
	util.Info("app.validate", "validating run", map[string]any{"run_id": runID, "manifest": manifestPath})
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		util.Error("app.validate", "failed to read manifest", map[string]any{"manifest": manifestPath, "error": err})
		return err
	}
	var m model.RunManifest
	if err := json.Unmarshal(b, &m); err != nil {
		util.Error("app.validate", "failed to parse manifest", map[string]any{"manifest": manifestPath, "error": err})
		return err
	}
	if m.RunID == "" || m.SchemaVersion == "" || m.RepoPath == "" {
		util.Error("app.validate", "manifest invalid", map[string]any{"run_id": runID})
		return fmt.Errorf("invalid manifest fields")
	}
	if len(m.StageFailures) > 0 {
		// Surface degraded stages so the operator sees them at a
		// glance without having to grep the manifest. Sorted for
		// stable output across runs.
		stages := make([]string, 0, len(m.StageFailures))
		for k := range m.StageFailures {
			stages = append(stages, k)
		}
		sort.Strings(stages)
		summary := make(map[string]any, len(stages))
		for _, k := range stages {
			summary[k] = m.StageFailures[k]
		}
		util.Warn("app.validate", "run completed with stage failures", map[string]any{
			"run_id":         runID,
			"stage_failures": summary,
		})
	}
	util.Info("app.validate", "run is valid", map[string]any{"run_id": runID, "schema": m.SchemaVersion})
	return nil
}

func ListRuns(baseDir string) ([]string, error) {
	util.Info("app.list_runs", "listing runs", map[string]any{"base_dir": baseDir})
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			util.Debug("app.list_runs", "base directory does not exist", map[string]any{"base_dir": baseDir})
			return nil, nil
		}
		util.Error("app.list_runs", "failed to list base directory", map[string]any{"base_dir": baseDir, "error": err})
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	util.Info("app.list_runs", "list runs complete", map[string]any{"count": len(out), "base_dir": baseDir})
	return out, nil
}
