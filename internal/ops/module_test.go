package ops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"diffmind/internal/audit"
)

func TestSLOEvaluateAndBackupRestore(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".diffmind")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := audit.AppendEvent(root, audit.Event{Timestamp: time.Now().UTC(), Action: "query_graph", Decision: "allow", Metadata: map[string]any{"duration_ms": 15.0}}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if err := audit.AppendEvent(root, audit.Event{Timestamp: time.Now().UTC(), Action: "query_graph", Decision: "allow", Metadata: map[string]any{"duration_ms": 20.0}}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	qualityPath := filepath.Join(root, "quality", "report.json")
	if err := os.MkdirAll(filepath.Dir(qualityPath), 0o755); err != nil {
		t.Fatalf("mkdir quality dir: %v", err)
	}
	if err := os.WriteFile(qualityPath, []byte(`{"metrics":{"pass_rate":1.0}}`), 0o644); err != nil {
		t.Fatalf("write quality report: %v", err)
	}

	sloOut := filepath.Join(root, "ops", "slo.json")
	if err := Run(context.Background(), []string{"slo", "--audit-root", root, "--quality", qualityPath, "--out", sloOut}); err != nil {
		t.Fatalf("ops slo failed: %v", err)
	}
	data, err := os.ReadFile(sloOut)
	if err != nil {
		t.Fatalf("read slo report: %v", err)
	}
	var rep map[string]any
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode slo report: %v", err)
	}
	if passed, ok := rep["passed"].(bool); !ok || !passed {
		t.Fatalf("expected passed slo report, got %v", rep["passed"])
	}

	filePath := filepath.Join(root, "graph", "index.json")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(`{"graphs":[]}`), 0o644); err != nil {
		t.Fatalf("write graph index: %v", err)
	}
	archivePath := filepath.Join(tmp, "backup.tar.gz")
	if err := Run(context.Background(), []string{"backup", "--source", root, "--out", archivePath}); err != nil {
		t.Fatalf("ops backup failed: %v", err)
	}
	restoreTarget := filepath.Join(tmp, "restore")
	if err := Run(context.Background(), []string{"restore", "--archive", archivePath, "--target", restoreTarget}); err != nil {
		t.Fatalf("ops restore failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoreTarget, "graph", "index.json")); err != nil {
		t.Fatalf("expected restored file: %v", err)
	}
}

func TestRolloutPlan(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "rollout.json")
	if err := Run(context.Background(), []string{"rollout", "--component", "schema", "--candidate", "v2.0.0", "--current", "v1.9.0", "--out", out}); err != nil {
		t.Fatalf("ops rollout failed: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected rollout plan: %v", err)
	}
}
