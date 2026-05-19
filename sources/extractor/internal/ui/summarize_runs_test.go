package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// REGRESSION: the sidebar's "click on the latest run" surfaced
// every failed/cancelled run as "running" (and later "completed")
// because summarizeRun only ever set status to "completed" or
// "unknown". The user expected "failed" / "cancelled" depending on
// what actually happened.
//
// The on-disk evidence is: a successful run produces
// run_manifest.json; a failed/cancelled run produces
// run_failure.json (and skips the manifest until a successful
// retry). The new logic reads both files and picks the right
// status, with Failure.Cancelled distinguishing user-cancelled from
// provider-failed.
func TestSummarizeRun_FailedRun(t *testing.T) {
	srv, baseDir := newTestServer(t)
	runID := "20260601T120000Z"
	writeFailureReport(t, baseDir, runID, map[string]any{
		"stage":       "detail",
		"error":       "schema problem",
		"error_class": "schema",
		"cancelled":   false,
		"occurred_at": time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	// No run_manifest.json — failure path doesn't write one.

	got := srv.summarizeRun(runID)
	if got["status"] != "failed" {
		t.Fatalf("status = %q, want failed", got["status"])
	}
	if got["failed_stage"] != "detail" {
		t.Errorf("failed_stage = %q, want detail", got["failed_stage"])
	}
	if got["error_class"] != "schema" {
		t.Errorf("error_class = %q, want schema", got["error_class"])
	}
}

// A run whose failure report has cancelled=true must surface as
// "cancelled", not "failed". Lets the SPA pick the right banner /
// pill colour.
func TestSummarizeRun_CancelledRun(t *testing.T) {
	srv, baseDir := newTestServer(t)
	runID := "20260601T120100Z"
	writeFailureReport(t, baseDir, runID, map[string]any{
		"stage":       "discovery",
		"error":       "context canceled",
		"error_class": "cancelled",
		"cancelled":   true,
		"occurred_at": time.Now().UTC(),
	})

	got := srv.summarizeRun(runID)
	if got["status"] != "cancelled" {
		t.Fatalf("status = %q, want cancelled", got["status"])
	}
	if got["failed_stage"] != "discovery" {
		t.Errorf("failed_stage = %q, want discovery", got["failed_stage"])
	}
}

// A successful run has a manifest and no failure report.
func TestSummarizeRun_CompletedRun(t *testing.T) {
	srv, baseDir := newTestServer(t)
	runID := "20260601T120200Z"
	writeManifest(t, baseDir, runID, map[string]any{
		"run_id":         runID,
		"schema_version": "v1alpha1",
		"repo_path":      "/repo/x",
		"started_at":     "2026-06-01T12:00:00Z",
		"finished_at":    "2026-06-01T12:05:00Z",
		"counts":         map[string]int{"exposures": 3},
	})

	got := srv.summarizeRun(runID)
	if got["status"] != "completed" {
		t.Fatalf("status = %q, want completed", got["status"])
	}
	if got["repo_path"] != "/repo/x" {
		t.Errorf("repo_path = %q, want /repo/x", got["repo_path"])
	}
}

// A directory with neither manifest nor failure report (pre-watchdog
// runs, half-cleaned runs, etc.) reports status=unknown so the UI
// can dim it appropriately without falsely claiming success.
func TestSummarizeRun_NoArtifactsAtAll(t *testing.T) {
	srv, baseDir := newTestServer(t)
	runID := "20260601T120300Z"
	if err := os.MkdirAll(filepath.Join(baseDir, runID), 0o755); err != nil {
		t.Fatal(err)
	}

	got := srv.summarizeRun(runID)
	if got["status"] != "unknown" {
		t.Fatalf("status = %q, want unknown", got["status"])
	}
}

// A retry that succeeded after a previous failure may briefly leave
// both files behind (the orchestrator removes the failure report
// only after the successful manifest write completes). The failure
// report should NOT win in this case — the run did succeed. We
// keep the current behaviour (failure wins) for now because the
// retry command explicitly removes both files on success, so this
// case shouldn't appear in practice; but the test documents the
// trade-off.
//
// Note: this test asserts the CURRENT behaviour (failure-wins) so
// any future change to "manifest-wins-when-both-present" is a
// deliberate decision, not an accident.
func TestSummarizeRun_BothFilesPresent_FailureWins(t *testing.T) {
	srv, baseDir := newTestServer(t)
	runID := "20260601T120400Z"
	writeFailureReport(t, baseDir, runID, map[string]any{
		"stage":       "detail",
		"error_class": "auth",
		"cancelled":   false,
	})
	writeManifest(t, baseDir, runID, map[string]any{
		"run_id":         runID,
		"schema_version": "v1alpha1",
		"repo_path":      "/repo/y",
		"counts":         map[string]int{"exposures": 1},
	})

	got := srv.summarizeRun(runID)
	if got["status"] != "failed" {
		t.Fatalf("status = %q, want failed (failure report wins when both exist)", got["status"])
	}
	// We still fill in repo_path from the manifest as a bonus.
	if got["repo_path"] != "/repo/y" {
		t.Errorf("repo_path = %q, want /repo/y (filled from manifest)", got["repo_path"])
	}
}

// ---------- helpers ----------

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	baseDir := t.TempDir()
	s := New(baseDir, "127.0.0.1", 0)
	return s, baseDir
}

func writeManifest(t *testing.T, baseDir, runID string, body map[string]any) {
	t.Helper()
	dir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run_manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFailureReport(t *testing.T, baseDir, runID string, body map[string]any) {
	t.Helper()
	dir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run_failure.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
