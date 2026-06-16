package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// LoadRun reads typed graph records from one completed extraction run. Keeping
// this adapter here makes the dependency direction explicit: run artifacts are
// an import format for the catalog, not the catalog itself.
func LoadRun(runDir, runID string) (ImportInput, error) {
	if _, err := os.Stat(filepath.Join(runDir, "run_manifest.json")); err != nil {
		return ImportInput{}, err
	}
	exposures, err := readArrays[model.Exposure](filepath.Join(runDir, "exposures"))
	if err != nil {
		return ImportInput{}, err
	}
	dependencies, err := readArrays[model.Dependency](filepath.Join(runDir, "dependencies"))
	if err != nil {
		return ImportInput{}, err
	}
	connections, err := readArrays[model.Connection](filepath.Join(runDir, "connections"))
	if err != nil {
		return ImportInput{}, err
	}
	return ImportInput{
		RunID:        runID,
		Exposures:    exposures,
		Dependencies: dependencies,
		Connections:  connections,
	}, nil
}

func readArrays[T any](dir string) ([]T, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []T{}, nil
		}
		return nil, err
	}
	var out []T
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var items []T
		if err := json.Unmarshal(b, &items); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		out = append(out, items...)
	}
	return out, nil
}
