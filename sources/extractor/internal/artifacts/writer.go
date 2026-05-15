package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

const SchemaVersion = "v1alpha1"

type WriteInput struct {
	RunID         string
	BaseDir       string
	RepoPath      string
	OpenCodeURL   string
	MinConfidence float64
	Exposures     []model.Exposure
	Dependencies  []model.Dependency
	Connections   []model.Connection
	Unresolved    []model.UnresolvedItem
	Warnings      []string
	StartedAt     time.Time
	FinishedAt    time.Time
}

func Write(in WriteInput) (string, error) {
	runDir := filepath.Join(in.BaseDir, in.RunID)
	util.Info("artifacts", "writing artifacts", map[string]any{"run_dir": runDir, "run_id": in.RunID})
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		util.Error("artifacts", "failed creating run dir", map[string]any{"run_dir": runDir, "error": err})
		return "", err
	}
	if err := writeEntities(filepath.Join(runDir, "exposures"), exposuresByType(in.Exposures)); err != nil {
		return "", err
	}
	if err := writeDependencies(filepath.Join(runDir, "dependencies"), depsByType(in.Dependencies)); err != nil {
		return "", err
	}
	if err := writeConnections(filepath.Join(runDir, "connections"), connsByType(in.Connections)); err != nil {
		return "", err
	}
	if err := writeUnresolved(filepath.Join(runDir, "unresolved"), unresolvedByType(in.Unresolved)); err != nil {
		return "", err
	}
	manifest := model.RunManifest{
		RunID:             in.RunID,
		StartedAt:         in.StartedAt,
		FinishedAt:        in.FinishedAt,
		RepoPath:          in.RepoPath,
		SchemaVersion:     SchemaVersion,
		OpenCodeURL:       in.OpenCodeURL,
		ConfidenceMinimum: in.MinConfidence,
		Counts: map[string]int{
			"exposures":    len(in.Exposures),
			"dependencies": len(in.Dependencies),
			"connections":  len(in.Connections),
			"unresolved":   len(in.Unresolved),
		},
		Warnings:      in.Warnings,
		StageFailures: stageFailures(in.Unresolved),
	}
	if err := writeJSON(filepath.Join(runDir, "run_manifest.json"), manifest); err != nil {
		return "", err
	}
	util.Info("artifacts", "artifact write complete", map[string]any{
		"run_dir":      runDir,
		"exposures":    len(in.Exposures),
		"dependencies": len(in.Dependencies),
		"connections":  len(in.Connections),
		"unresolved":   len(in.Unresolved),
	})
	return runDir, nil
}

func writeEntities(dir string, grouped map[string][]model.Exposure) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := sortedKeys(grouped)
	used := map[string]struct{}{}
	for _, k := range keys {
		util.Trace("artifacts", "writing exposure file", map[string]any{"type": k, "count": len(grouped[k])})
		filename := safeFileBase(k, used) + ".json"
		if err := writeJSON(filepath.Join(dir, filename), grouped[k]); err != nil {
			return err
		}
	}
	return nil
}

func writeDependencies(dir string, grouped map[string][]model.Dependency) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := sortedKeys(grouped)
	used := map[string]struct{}{}
	for _, k := range keys {
		util.Trace("artifacts", "writing dependency file", map[string]any{"type": k, "count": len(grouped[k])})
		filename := safeFileBase(k, used) + ".json"
		if err := writeJSON(filepath.Join(dir, filename), grouped[k]); err != nil {
			return err
		}
	}
	return nil
}

func writeConnections(dir string, grouped map[string][]model.Connection) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := sortedKeys(grouped)
	used := map[string]struct{}{}
	for _, k := range keys {
		util.Trace("artifacts", "writing connection file", map[string]any{"type": k, "count": len(grouped[k])})
		filename := safeFileBase(k, used) + ".json"
		if err := writeJSON(filepath.Join(dir, filename), grouped[k]); err != nil {
			return err
		}
	}
	return nil
}

func writeUnresolved(dir string, grouped map[string][]model.UnresolvedItem) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := sortedKeys(grouped)
	used := map[string]struct{}{}
	for _, k := range keys {
		util.Trace("artifacts", "writing unresolved file", map[string]any{"type": k, "count": len(grouped[k])})
		filename := safeFileBase(k, used) + ".json"
		if err := writeJSON(filepath.Join(dir, filename), grouped[k]); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, data any) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func exposuresByType(in []model.Exposure) map[string][]model.Exposure {
	out := map[string][]model.Exposure{}
	for _, v := range in {
		out[v.Type] = append(out[v.Type], v)
	}
	return out
}

func depsByType(in []model.Dependency) map[string][]model.Dependency {
	out := map[string][]model.Dependency{}
	for _, v := range in {
		out[v.Type] = append(out[v.Type], v)
	}
	return out
}

func connsByType(in []model.Connection) map[string][]model.Connection {
	out := map[string][]model.Connection{}
	for _, v := range in {
		key := v.FromType + "__to__" + v.ToType
		out[key] = append(out[key], v)
	}
	return out
}

func unresolvedByType(in []model.UnresolvedItem) map[string][]model.UnresolvedItem {
	out := map[string][]model.UnresolvedItem{}
	for _, v := range in {
		key := string(v.Kind) + "_" + v.Type
		out[key] = append(out[key], v)
	}
	return out
}

// stageFailures groups unresolved diagnostics by the pipeline stage
// where they originated, using the ReasonCode as a coarse tag. The
// resulting map is what the dashboard's "stage health" badge reads to
// decide whether to flag a stage as "degraded".
//
// Reason codes that don't correspond to a stage (e.g.
// "missing_required_details" written by the assembler before any LLM
// call) are filed under "validation".
func stageFailures(in []model.UnresolvedItem) map[string]int {
	if len(in) == 0 {
		return nil
	}
	stageOf := map[string]string{
		// Hard agent failures (the LLM call itself errored after
		// retries) are filed under the stage that ran them.
		"discovery_failure":         "discovery",
		"detail_failure":            "detail",
		"connections_failure":       "connections",
		"reexamine_failure":         "reexamination",
		"rejected_on_reexamination": "reexamination",

		// Quality / validation diagnostics from the assembler.
		"missing_required_details": "validation",
		"low_confidence":           "validation",
		"no_source_location":       "validation",
		"invalid_entity":           "validation",
		"orphan_connection":        "reconcile",
		"unmatched_reference":      "connections",
	}
	out := map[string]int{}
	for _, u := range in {
		stage, ok := stageOf[u.ReasonCode]
		if !ok {
			stage = "other"
		}
		out[stage]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func safeFileBase(key string, used map[string]struct{}) string {
	base := sanitizeFileBase(key)
	if base == "" {
		base = "unknown"
	}
	candidate := base
	if candidate != key {
		candidate = base + "__" + util.StableID(key)[:8]
	}
	if _, exists := used[candidate]; exists {
		candidate = base + "__" + util.StableID(key)[:8]
	}
	used[candidate] = struct{}{}
	return candidate
}

func sanitizeFileBase(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(in))
	lastUnderscore := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		isAlpha := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		isSafePunct := c == '.' || c == '_' || c == '-'
		if isAlpha || isDigit || isSafePunct {
			b.WriteByte(c)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "._-")
	return out
}
