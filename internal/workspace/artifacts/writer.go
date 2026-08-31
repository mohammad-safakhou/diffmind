package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
)

// WriteGraph writes the cross-service graph to a fresh timestamped run
// directory under baseDir. Retained for the standalone CLI pipeline.
func WriteGraph(baseDir string, graph *model.CrossServiceGraph) (string, error) {
	runID := time.Now().UTC().Format("20060102T150405Z")
	runDir := filepath.Join(baseDir, runID)
	if err := WriteGraphTo(runDir, graph); err != nil {
		return "", err
	}
	// Standalone runs also get a small manifest for the legacy viewer.
	manifest := map[string]any{
		"run_id":       runID,
		"generated_at": graph.GeneratedAt,
		"version":      graph.Version,
		"counts": map[string]int{
			"services":         len(graph.Services),
			"edges":            len(graph.Edges),
			"shared_resources": len(graph.SharedResources),
			"unresolved":       len(graph.Unresolved),
		},
	}
	if err := writeJSON(filepath.Join(runDir, "manifest.json"), manifest); err != nil {
		return "", err
	}
	return runDir, nil
}

// WriteGraphTo writes graph.json and the per-service identities into the given
// directory (no extra subdirectory). The DiffMind run manager uses this to land
// graph output directly in a project's run directory, which already holds the
// manifest.json that tracks run state.
func WriteGraphTo(runDir string, graph *model.CrossServiceGraph) error {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	if err := writeJSON(filepath.Join(runDir, "graph.json"), graph); err != nil {
		return err
	}
	identDir := filepath.Join(runDir, "identities")
	if err := os.MkdirAll(identDir, 0o755); err != nil {
		return fmt.Errorf("create identities dir: %w", err)
	}
	for _, svc := range graph.Services {
		fname := sanitizeFilename(svc.Name) + ".json"
		if err := writeJSON(filepath.Join(identDir, fname), svc.Identity); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func sanitizeFilename(name string) string {
	safe := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			safe = append(safe, c)
		} else {
			safe = append(safe, '_')
		}
	}
	return string(safe)
}
