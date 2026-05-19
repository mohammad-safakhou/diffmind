package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
)

// recorderSink captures every emitted event so tests can assert on
// the sequence the SPA would have seen.
type recorderSink struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *recorderSink) Emit(e events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorderSink) all() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]events.Event, len(r.events))
	copy(out, r.events)
	return out
}

// REGRESSION (run 20260518T122739Z): retrying a failed run used to
// die in 0.4ms with `retry: read manifest: open ... no such file or
// directory`. Failed runs never have a manifest. The fix: extract
// repo_path from the run_started event in events.jsonl when the
// manifest is missing.
func TestRetryRun_NoManifest_FallsBackToEventsLog(t *testing.T) {
	baseDir := t.TempDir()
	runID := "20260601T120000Z"
	runDir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Set up the disk state of a real failed run: events.jsonl with
	// run_started carrying repo + snapshot; run_failure.json with
	// snapshot_path; an actual snapshot directory.
	snapDir := filepath.Join(baseDir, "snap-retained")
	repo := t.TempDir()
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteJSONL(t, filepath.Join(runDir, "events.jsonl"), []map[string]any{
		{
			"run_id": runID, "seq": 1, "ts": "2026-06-01T12:00:00Z",
			"kind": "run_started",
			"payload": map[string]any{
				"repo":     repo,
				"snapshot": snapDir,
			},
		},
	})
	mustWriteJSON(t, filepath.Join(runDir, "run_failure.json"), map[string]any{
		"stage":         "detail",
		"error":         "synthetic",
		"error_class":   "schema",
		"snapshot_path": snapDir,
		"occurred_at":   "2026-06-01T12:30:00Z",
	})

	// Run the retry. It will fail at the opencode health check
	// (no server) — but that's the point: we want to confirm we
	// got PAST the manifest-not-found wall.
	sink := &recorderSink{}
	cfg := config.Default()
	cfg.Artifacts.BaseDir = baseDir
	cfg.OpenCode.BaseURL = "http://127.0.0.1:1" // guaranteed-dead port
	out, err := RetryRun(context.Background(), RetryInput{
		BaseDir: baseDir,
		RunID:   runID,
		Config:  cfg,
		Sink:    sink,
	})
	if err == nil {
		t.Fatalf("expected RetryRun to error against a dead opencode")
	}
	// The error must be the OpenCode health failure (proving we
	// got past manifest-reading), NOT a manifest error.
	if strings.Contains(err.Error(), "manifest") {
		t.Fatalf("retry should not depend on the manifest now; got: %v", err)
	}
	if !strings.Contains(err.Error(), "opencode health") {
		t.Fatalf("expected opencode health failure; got: %v", err)
	}
	_ = out

	// The sink must have received a synthetic run_failed event
	// before we exited — that's how the SPA learns the retry
	// pre-flight failed without waiting for a non-existent
	// orchestrator event.
	evs := sink.all()
	gotRunFailed := false
	for _, e := range evs {
		if e.Kind == events.KindRunFailed {
			gotRunFailed = true
			if !strings.Contains(e.Message, "opencode health") {
				t.Errorf("run_failed event has wrong message: %s", e.Message)
			}
		}
	}
	if !gotRunFailed {
		t.Fatalf("RetryRun did not emit a run_failed event on its sink (got %d events of kind %v)",
			len(evs), eventKinds(evs))
	}
}

// Sanity: when neither manifest nor events.jsonl can give us the
// repo path, we get a clear "run from scratch" error rather than a
// silent crash.
func TestRetryRun_NoRepoPathSurface_ClearError(t *testing.T) {
	baseDir := t.TempDir()
	runID := "20260601T120100Z"
	runDir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Failure report but no events.jsonl, no manifest.
	mustWriteJSON(t, filepath.Join(runDir, "run_failure.json"), map[string]any{
		"stage": "detail", "error": "x",
	})
	_, err := RetryRun(context.Background(), RetryInput{
		BaseDir: baseDir,
		RunID:   runID,
		Config:  config.Default(),
	})
	if err == nil {
		t.Fatalf("expected an error")
	}
	if !strings.Contains(err.Error(), "repo path") {
		t.Fatalf("expected 'repo path' wording; got: %v", err)
	}
}

// Sanity: when the retained snapshot directory is gone, the user
// gets a clear "run from scratch" message and a synthetic
// run_failed event.
func TestRetryRun_SnapshotMissing_ClearError(t *testing.T) {
	baseDir := t.TempDir()
	runID := "20260601T120200Z"
	runDir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	mustWriteJSON(t, filepath.Join(runDir, "run_failure.json"), map[string]any{
		"stage":         "detail",
		"snapshot_path": "/var/folders/nonexistent",
		"error":         "x",
	})
	mustWriteJSONL(t, filepath.Join(runDir, "events.jsonl"), []map[string]any{
		{
			"run_id": runID, "seq": 1, "ts": "2026-06-01T12:00:00Z",
			"kind": "run_started",
			"payload": map[string]any{
				"repo": repo, "snapshot": "/var/folders/nonexistent",
			},
		},
	})

	sink := &recorderSink{}
	_, err := RetryRun(context.Background(), RetryInput{
		BaseDir: baseDir,
		RunID:   runID,
		Config:  config.Default(),
		Sink:    sink,
	})
	if err == nil {
		t.Fatalf("expected an error")
	}
	if !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("expected 'snapshot' wording; got: %v", err)
	}
	gotRunFailed := false
	for _, e := range sink.all() {
		if e.Kind == events.KindRunFailed {
			gotRunFailed = true
		}
	}
	if !gotRunFailed {
		t.Fatalf("expected a synthetic run_failed event")
	}
}

// scanRunStartedFromEvents is a small helper but big enough to be
// worth a focused test. The event is always at line 1 in production
// but we feed a multi-line file to be safe.
func TestScanRunStartedFromEvents(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "events.jsonl")
	mustWriteJSONL(t, path, []map[string]any{
		{"kind": "run_started", "payload": map[string]any{"repo": "/r", "snapshot": "/s"}},
		{"kind": "stage_started"},
	})
	repo, snap := scanRunStartedFromEvents(path)
	if repo != "/r" {
		t.Errorf("repo = %q, want /r", repo)
	}
	if snap != "/s" {
		t.Errorf("snap = %q, want /s", snap)
	}
}

// Missing events log returns empty strings, no error, no panic.
func TestScanRunStartedFromEvents_MissingFile(t *testing.T) {
	repo, snap := scanRunStartedFromEvents("/nonexistent/path")
	if repo != "" || snap != "" {
		t.Errorf("expected empty strings; got repo=%q snap=%q", repo, snap)
	}
}

// ---- helpers ----

func mustWriteJSON(t *testing.T, path string, body any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustWriteJSONL(t *testing.T, path string, lines []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			t.Fatal(err)
		}
	}
}

func eventKinds(es []events.Event) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Kind)
	}
	return out
}

// Make sure my fail() helper sets up the right timing too.
func TestRetryRun_PreflightFailureContainsElapsedMs(t *testing.T) {
	baseDir := t.TempDir()
	sink := &recorderSink{}
	_, err := RetryRun(context.Background(), RetryInput{
		BaseDir: baseDir,
		RunID:   "nope",
		Config:  config.Default(),
		Sink:    sink,
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	// The first event should be run_failed with payload.elapsed_ms.
	if len(sink.all()) == 0 {
		t.Fatal("expected at least one event")
	}
	e := sink.all()[0]
	if e.Kind != events.KindRunFailed {
		t.Fatalf("first event = %s, want run_failed", e.Kind)
	}
	if e.Payload == nil {
		t.Fatal("payload missing")
	}
	if _, ok := e.Payload["elapsed_ms"]; !ok {
		t.Error("elapsed_ms missing from preflight fail payload")
	}
	// Just a sanity-keep on time package usage.
	_ = errors.New
	_ = time.Now
}
