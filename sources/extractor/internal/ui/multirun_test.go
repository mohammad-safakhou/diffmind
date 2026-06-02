package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConcurrentRunsNoConflict verifies the multi-run backend: a second run
// started while the first is still going no longer returns 409, and both runs
// are reported as active.
func TestConcurrentRunsNoConflict(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(stubOpencodeHandler))
	defer stub.Close()

	baseDir := t.TempDir()
	uiServer := New(baseDir, "127.0.0.1", 0)
	mux := http.NewServeMux()
	uiServer.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()
	defer func() {
		uiServer.runner.CancelAll()
		uiServer.runner.Wait()
	}()

	start := func() string {
		body, _ := json.Marshal(map[string]any{
			"repo_path": copyRepoForTest(t),
			"opencode":  map[string]any{"base_url": stub.URL, "provider_id": "test", "model_id": "test", "timeout_seconds": 30},
			"runtime":   map[string]any{"workers": 2, "max_catalog_items": 5},
			"quality":   map[string]any{"min_confidence": 0.7},
		})
		resp, err := http.Post(httpSrv.URL+"/api/runs", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(b))
		}
		var out struct {
			RunID string `json:"run_id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if out.RunID == "" {
			t.Fatal("missing run id")
		}
		return out.RunID
	}

	id1 := start()
	id2 := start()
	if id1 == id2 {
		t.Fatalf("two runs got the same id %q", id1)
	}

	// Both runs complete independently.
	uiServer.runner.Wait()

	for _, id := range []string{id1, id2} {
		st, ok := uiServer.runner.State(id)
		if !ok {
			t.Fatalf("run %s not tracked", id)
		}
		if st.Status != "completed" {
			t.Fatalf("run %s status = %s, want completed", id, st.Status)
		}
	}
}

// TestDeleteRunRemovesArtifacts verifies DELETE /api/runs/{id} wipes the run
// directory from disk.
func TestDeleteRunRemovesArtifacts(t *testing.T) {
	baseDir := t.TempDir()
	runID := "20260101T000000Z"
	runDir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(filepath.Join(runDir, "exposures"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteJSON(t, filepath.Join(runDir, "run_manifest.json"), map[string]any{"run_id": runID, "repo_path": "/r"})

	uiServer := New(baseDir, "127.0.0.1", 0)
	mux := http.NewServeMux()
	uiServer.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/runs/"+runID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete returned %d: %s", resp.StatusCode, string(b))
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("run dir still present after delete: %v", err)
	}

	// Deleting again is a 404.
	req2, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/runs/"+runID, nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", resp2.StatusCode)
	}
}

// TestAggregateEventsStream verifies the homepage SSE endpoint emits lifecycle
// events (created/started/finished) for a run.
func TestAggregateEventsStream(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(stubOpencodeHandler))
	defer stub.Close()

	baseDir := t.TempDir()
	uiServer := New(baseDir, "127.0.0.1", 0)
	mux := http.NewServeMux()
	uiServer.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	// Open the aggregate stream first so we don't miss the created event.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpSrv.URL+"/api/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse get: %v", err)
	}
	defer resp.Body.Close()

	types := make(chan string, 16)
	go func() {
		br := bufio.NewReader(resp.Body)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				close(types)
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "event: ") {
				types <- strings.TrimPrefix(line, "event: ")
			}
		}
	}()

	body, _ := json.Marshal(map[string]any{
		"repo_path": copyRepoForTest(t),
		"opencode":  map[string]any{"base_url": stub.URL, "provider_id": "test", "model_id": "test", "timeout_seconds": 30},
		"runtime":   map[string]any{"workers": 2, "max_catalog_items": 5},
		"quality":   map[string]any{"min_confidence": 0.7},
	})
	postResp, err := http.Post(httpSrv.URL+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	postResp.Body.Close()

	seen := map[string]bool{}
	deadline := time.After(30 * time.Second)
	for {
		select {
		case et, ok := <-types:
			if !ok {
				t.Fatalf("stream closed before finished event; saw %v", seen)
			}
			seen[et] = true
			if seen["created"] && seen["started"] && seen["finished"] {
				uiServer.runner.Wait()
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for lifecycle events; saw %v", seen)
		}
	}
}
