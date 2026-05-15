package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func TestWriteSanitizesUnresolvedFileNames(t *testing.T) {
	baseDir := t.TempDir()
	_, err := Write(WriteInput{
		RunID:         "run1",
		BaseDir:       baseDir,
		RepoPath:      "/repo",
		OpenCodeURL:   "http://127.0.0.1:4096",
		MinConfidence: 0.7,
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		Unresolved: []model.UnresolvedItem{
			{Kind: model.KindExposure, Type: "authentication/authorization_gap", Name: "x", ReasonCode: "gap", Reason: "missing"},
		},
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	unresolvedDir := filepath.Join(baseDir, "run1", "unresolved")
	entries, err := os.ReadDir(unresolvedDir)
	if err != nil {
		t.Fatalf("read unresolved dir failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 unresolved artifact file, got %d", len(entries))
	}
	if entries[0].IsDir() {
		t.Fatalf("expected file artifact, got directory")
	}
	if filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("expected .json unresolved artifact file, got %s", entries[0].Name())
	}
	if entries[0].Name() == "exposure_authentication/authorization_gap.json" {
		t.Fatalf("expected sanitized filename, got unsafe path segment")
	}
}

// stageFailures groups unresolved items by the pipeline stage that
// produced them. The dashboard reads this off the manifest to render a
// per-stage health badge, so it must be present and accurate even when
// no stage hard-failed.
func TestWriteManifestStageFailureSummary(t *testing.T) {
	baseDir := t.TempDir()
	_, err := Write(WriteInput{
		RunID:         "run42",
		BaseDir:       baseDir,
		RepoPath:      "/repo",
		MinConfidence: 0.7,
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		Unresolved: []model.UnresolvedItem{
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-a", ReasonCode: "discovery_failure", Reason: "boom"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-b", ReasonCode: "discovery_failure", Reason: "boom"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-c", ReasonCode: "detail_failure", Reason: "boom"},
			{Kind: model.KindDependency, Type: "connection", Name: "exp-1", ReasonCode: "connections_failure", Reason: "boom"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-d", ReasonCode: "reexamine_failure", Reason: "kept original seed"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-e", ReasonCode: "rejected_on_reexamination", Reason: "not real"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-f", ReasonCode: "missing_required_details", Reason: "no path"},
		},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(baseDir, "run42", "run_manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var got model.RunManifest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]int{
		"discovery":     2,
		"detail":        1,
		"connections":   1,
		"reexamination": 2,
		"validation":    1,
	}
	if len(got.StageFailures) != len(want) {
		t.Fatalf("stage_failures keys mismatch: got %v want %v", got.StageFailures, want)
	}
	for k, v := range want {
		if got.StageFailures[k] != v {
			t.Fatalf("stage_failures[%q] = %d, want %d (full: %v)", k, got.StageFailures[k], v, got.StageFailures)
		}
	}
}

// When there is no unresolved diagnostic the StageFailures field must
// be omitted (nil), so a successful run's manifest stays clean.
func TestWriteManifestNoStageFailuresWhenAllGreen(t *testing.T) {
	baseDir := t.TempDir()
	_, err := Write(WriteInput{
		RunID:         "run-green",
		BaseDir:       baseDir,
		RepoPath:      "/repo",
		MinConfidence: 0.7,
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(baseDir, "run-green", "run_manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var got model.RunManifest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.StageFailures != nil {
		t.Fatalf("expected stage_failures omitted for clean run; got %v", got.StageFailures)
	}
}
