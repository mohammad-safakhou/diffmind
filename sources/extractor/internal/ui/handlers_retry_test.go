package ui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// REGRESSION: the dashboard had no UI surface for the retry CLI for
// weeks. When a user's OpenCode credits ran out mid-run (real
// scenario: run 20260518T122739Z) they had to drop to a terminal to
// resume. POST /api/runs/{id}/retry fixes that. Wire-up regression
// catches the case where the route is registered but the wrong
// handler is dispatched.
func TestRetryEndpoint_RegisteredAndAccepted(t *testing.T) {
	baseDir := t.TempDir()
	runID := "20260601T120000Z"
	runDir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(filepath.Join(runDir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal failure report + manifest so RetryRun's pre-flight
	// checks don't bail before the handler even hands the runner
	// the params we care about.
	mustWriteJSON(t, filepath.Join(runDir, "run_failure.json"), map[string]any{
		"stage":         "detail",
		"job_id":        "detail.x.y",
		"error":         "synthetic",
		"snapshot_path": filepath.Join(baseDir, "snap"),
	})
	mustWriteJSON(t, filepath.Join(runDir, "run_manifest.json"), map[string]any{
		"run_id":         runID,
		"schema_version": "v1alpha1",
		"repo_path":      t.TempDir(),
	})
	// snapshot dir referenced by failure report; RetryRun would
	// stat it before opening the prompt POST. The handler under
	// test never reaches that point — it just queues a goroutine —
	// but we create the dir anyway so the goroutine doesn't crash
	// while we're cleaning up.
	if err := os.MkdirAll(filepath.Join(baseDir, "snap"), 0o755); err != nil {
		t.Fatal(err)
	}

	uiServer := New(baseDir, "127.0.0.1", 0)
	mux := http.NewServeMux()
	uiServer.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// POST /api/runs/{id}/retry with an empty body — the simplest
	// "just resume from where you stopped" call.
	resp, err := http.Post(srv.URL+"/api/runs/"+runID+"/retry", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted; got %d body=%s", resp.StatusCode, string(body))
	}
	var out struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, string(body))
	}
	if out.RunID != runID {
		t.Errorf("retry returned run_id=%q, want %q", out.RunID, runID)
	}
	if out.Status != "running" {
		t.Errorf("retry status=%q, want running", out.Status)
	}

	// The retry goroutine is launched but will quickly fail because
	// the manifest / config we wrote is minimal. We don't care about
	// the eventual failure — we only assert the HTTP handshake.
	// Cancel any background work the runner might still be doing
	// so the test process can exit cleanly.
	_ = uiServer.runner.Cancel()
	uiServer.runner.Wait()
}

// Retry MUST require POST. GET / DELETE / etc. should be rejected
// so we don't accidentally trigger a retry on a routine GET probe.
func TestRetryEndpoint_RequiresPOST(t *testing.T) {
	baseDir := t.TempDir()
	uiServer := New(baseDir, "127.0.0.1", 0)
	mux := http.NewServeMux()
	uiServer.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/runs/some-id/retry", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /retry must return 405; got %d", resp.StatusCode)
	}
}
