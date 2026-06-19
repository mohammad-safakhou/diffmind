// Package artifacts handles reading DiffMind output and writing DiffMind output.
package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// ReadDiffMindRun reads the latest DiffMind run artifacts from a service repo.
// It looks for .diffmind/runs/<runid>/ directories and picks the latest one.
func ReadDiffMindRun(repoPath string) (*model.ServiceArchitecture, error) {
	if HasRepoArchfile(repoPath) {
		return ReadDiffMindArchfile(RepoArchfilePath(repoPath))
	}
	runsDir := filepath.Join(repoPath, ".diffmind", "runs")
	if _, err := os.Stat(runsDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("no DiffMind runs found at %s", runsDir)
	}
	return readLatestRun(repoPath, runsDir)
}

// ReadDiffMindArtifacts reads DiffMind artifacts from a specific directory.
func ReadDiffMindArtifacts(artifactsDir string) (*model.ServiceArchitecture, error) {
	if isYAMLFile(artifactsDir) {
		return ReadDiffMindArchfile(artifactsDir)
	}
	// Check if this is a runs directory or a specific run.
	if _, err := os.Stat(filepath.Join(artifactsDir, "run_manifest.json")); err == nil {
		return readRunDir("", artifactsDir)
	}
	// Maybe it's the runs parent dir.
	return readLatestRun("", artifactsDir)
}

func readLatestRun(repoPath, runsDir string) (*model.ServiceArchitecture, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil, fmt.Errorf("read runs dir: %w", err)
	}

	var runDirs []string
	for _, e := range entries {
		if e.IsDir() {
			runDirs = append(runDirs, e.Name())
		}
	}
	if len(runDirs) == 0 {
		return nil, fmt.Errorf("no run directories found in %s", runsDir)
	}

	// Sort descending to get latest first (run IDs are timestamp-based).
	sort.Sort(sort.Reverse(sort.StringSlice(runDirs)))
	latestDir := filepath.Join(runsDir, runDirs[0])

	return readRunDir(repoPath, latestDir)
}

func readRunDir(repoPath, runDir string) (*model.ServiceArchitecture, error) {
	arch := &model.ServiceArchitecture{
		RepoPath: repoPath,
	}

	// Read manifest if present.
	manifestPath := filepath.Join(runDir, "run_manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m model.RunManifest
		if err := json.Unmarshal(data, &m); err == nil {
			arch.Manifest = &m
			if arch.RepoPath == "" {
				arch.RepoPath = m.RepoPath
			}
		}
	}

	// Read exposures.
	arch.Exposures = readEntityDir[model.Exposure](filepath.Join(runDir, "exposures"))

	// Read dependencies.
	arch.Dependencies = readEntityDir[model.Dependency](filepath.Join(runDir, "dependencies"))

	// Read connections.
	arch.Connections = readEntityDir[model.Connection](filepath.Join(runDir, "connections"))

	// Read unresolved items.
	arch.Unresolved = readEntityDir[model.UnresolvedItem](filepath.Join(runDir, "unresolved"))

	return arch, nil
}

// readEntityDir reads all JSON files in a directory and deserialises them.
// Each file may contain a single entity or an array of entities.
func readEntityDir[T any](dir string) []T {
	var items []T
	entries, err := os.ReadDir(dir)
	if err != nil {
		return items
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		// Try array first.
		var arr []T
		if err := json.Unmarshal(data, &arr); err == nil {
			items = append(items, arr...)
			continue
		}
		// Try single entity.
		var single T
		if err := json.Unmarshal(data, &single); err == nil {
			items = append(items, single)
		}
	}
	return items
}
