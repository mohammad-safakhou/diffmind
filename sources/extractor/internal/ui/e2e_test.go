package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// TestServerEndToEnd boots a real ui.Server (its HTTP routes), points it at
// a stub OpenCode httptest server, posts a run via the JSON API, then
// consumes the SSE stream and asserts that we observe the full sequence of
// run / stage / job / llm-call events. This covers the entire backend half
// of the dashboard in a single test so regressions in any wiring layer
// surface here.
func TestServerEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(stubOpencodeHandler))
	defer srv.Close()

	repo := copyRepoForTest(t)
	baseDir := t.TempDir()

	uiServer := New(baseDir, "127.0.0.1", 0)
	mux := http.NewServeMux()
	uiServer.routes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	// 1. Post a run.
	body, err := json.Marshal(map[string]any{
		"repo_path": repo,
		"opencode": map[string]any{
			"base_url":        srv.URL,
			"provider_id":     "test",
			"model_id":        "test",
			"timeout_seconds": 30,
		},
		"runtime": map[string]any{
			"workers":           4,
			"max_catalog_items": 10,
		},
		"quality": map[string]any{"min_confidence": 0.7},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(httpSrv.URL+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(b))
	}
	var startResp struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&startResp); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if startResp.RunID == "" {
		t.Fatalf("missing run id")
	}

	// 2. Open the SSE stream and collect events for up to 30s.
	collected, eofSeen := drainSSE(t, httpSrv.URL+"/api/runs/"+startResp.RunID+"/events", 30*time.Second)

	if !eofSeen {
		t.Logf("note: no eof seen, but %d events collected", len(collected))
	}

	// 3. Assertions on the event sequence. We require at least:
	//    - run_started
	//    - stage_started for each of the six stages
	//    - llm_call_started + llm_call_completed
	//    - run_completed
	want := []string{
		"run_started",
		"stage_started", // there are several; count > 0
		"job_started",
		"job_completed",
		"llm_call_started",
		"llm_call_completed",
		"stage_completed",
		"run_completed",
	}
	for _, kind := range want {
		if !containsKind(collected, kind) {
			t.Fatalf("missing event kind %q in collected stream of %d events", kind, len(collected))
		}
	}

	// 4. Wait for the runner to finalize and check state via the API.
	uiServer.runner.Wait()
	statusResp, err := http.Get(httpSrv.URL + "/api/runs/active")
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var st struct {
		Status string `json:"status"`
		RunID  string `json:"run_id"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.Status != "completed" {
		t.Fatalf("expected status=completed, got %s", st.Status)
	}

	// 5. Job detail endpoint should serve a captured prompt + response.
	httpRouteJob := "discover.exposure.http_route"
	jobResp, err := http.Get(httpSrv.URL + "/api/runs/" + startResp.RunID + "/job/" + httpRouteJob)
	if err != nil {
		t.Fatal(err)
	}
	defer jobResp.Body.Close()
	var job struct {
		Prompt   string `json:"prompt"`
		Response string `json:"response"`
	}
	if err := json.NewDecoder(jobResp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.Prompt == "" {
		t.Fatalf("expected non-empty prompt for %s", httpRouteJob)
	}
	if job.Response == "" {
		t.Fatalf("expected non-empty response for %s", httpRouteJob)
	}

	// 6. events.jsonl must exist on disk.
	jsonlPath := filepath.Join(baseDir, startResp.RunID, "events.jsonl")
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Fatalf("events.jsonl missing: %v", err)
	}
}

// drainSSE consumes an SSE stream into a slice of parsed events. It returns
// when it sees an "event: eof" marker or when the deadline expires.
func drainSSE(t *testing.T, url string, total time.Duration) ([]events.Event, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), total)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sse got %d", resp.StatusCode)
	}
	out := []events.Event{}
	eof := false
	br := bufio.NewReader(resp.Body)
	currentEvent := ""
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return out, eof
			}
			return out, eof
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			currentEvent = ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			if currentEvent == "eof" {
				eof = true
				return out, eof
			}
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			var e events.Event
			if err := json.Unmarshal([]byte(payload), &e); err == nil {
				out = append(out, e)
			}
		}
	}
}

func containsKind(events []events.Event, kind string) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------------------
// Stub OpenCode server (a copy of internal/app/end_to_end_test.go's stub).
// We duplicate it here so the ui package doesn't depend on internal test
// helpers from another package.
// ----------------------------------------------------------------------------

var stubOnce sync.Once

var (
	orderExposureID string
	scheduledExpID  string
	dbDepID         string
	httpDepID       string
	cmdExecDepID    string
)

func ensureStubIDs() {
	stubOnce.Do(func() {
		orderExposureID = util.StableID("exposure", "http_route", "POST /orders", "cmd/api.go", "12:18")
		scheduledExpID = util.StableID("exposure", "scheduled_job", "StartCronJob", "internal/worker.go", "5:8")
		dbDepID = util.StableID("dependency", "db_operation", "orders_db_open", "cmd/api.go", "17:17")
		httpDepID = util.StableID("dependency", "outbound_http", "POST billing/charge", "cmd/api.go", "18:18")
		cmdExecDepID = util.StableID("dependency", "command_exec", "shell_exec_echo_run", "internal/worker.go", "7:7")
	})
}

func stubOpencodeHandler(w http.ResponseWriter, r *http.Request) {
	ensureStubIDs()
	switch {
	case r.URL.Path == "/global/health":
		w.WriteHeader(http.StatusOK)
		return
	case r.URL.Path == "/session" && r.Method == http.MethodPost:
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "session-1"})
		return
	case strings.HasPrefix(r.URL.Path, "/session/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message"):
		handleStubMessage(w, r)
		return
	case r.URL.Path == "/permission" || r.URL.Path == "/question":
		_ = json.NewEncoder(w).Encode([]any{})
		return
	case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/abort"):
		w.WriteHeader(http.StatusOK)
		return
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func handleStubMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	prompt := ""
	for _, p := range body.Parts {
		if p.Type == "text" {
			prompt += p.Text
		}
	}
	payload := stubResponseFor(prompt)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"info": map[string]any{"structured": payload},
	})
}

func stubResponseFor(prompt string) map[string]any {
	switch {
	case strings.Contains(prompt, "AGENT ROLE: repo-facts"):
		return map[string]any{"service_name": "sample"}
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "POST /orders", "summary": "order handler", "confidence": 0.95,
			"details":          map[string]any{"method": "POST", "path": "/orders"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 12, "end_line": 18}},
		}}}
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"items": []any{map[string]any{
			"type": "db_operation", "name": "orders_db_open", "summary": "Opens postgres connection", "confidence": 0.9,
			"details":          map[string]any{"operation": "connect", "database_type": "postgres"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 17, "end_line": 17}},
		}}}
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor"):
		return map[string]any{"items": []any{}}
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "POST /orders", "summary": "Handles new order POST requests", "confidence": 0.97,
			"details":          map[string]any{"method": "POST", "path": "/orders"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 12, "end_line": 18}},
		}}
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"item": map[string]any{
			"type": "db_operation", "name": "orders_db_open", "summary": "Opens postgres connection", "confidence": 0.92,
			"details":          map[string]any{"operation": "connect", "database_type": "postgres"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 17, "end_line": 17}},
		}}
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor"):
		return map[string]any{"item": nil}
	case strings.Contains(prompt, "AGENT ROLE: connection-extractor") && strings.Contains(prompt, "EXPOSURE_ID: "+orderExposureID):
		return map[string]any{"items": []any{map[string]any{
			"from_exposure_id": orderExposureID,
			"to_dependency_id": dbDepID,
			"summary":          "handler opens postgres",
			"confidence":       0.9,
			"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
			"path_signature":   "orders->db",
		}}}
	case strings.Contains(prompt, "AGENT ROLE: connection-extractor"):
		return map[string]any{"items": []any{}}
	case strings.Contains(prompt, "AGENT ROLE: reexaminer"):
		return map[string]any{"items": []any{}}
	}
	return map[string]any{"items": []any{}}
}

func copyRepoForTest(t *testing.T) string {
	t.Helper()
	srcRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample_repo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(srcRoot); err != nil {
		t.Fatalf("missing sample repo: %v", err)
	}
	dst := t.TempDir()
	err = filepath.WalkDir(srcRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcRoot, p)
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// Avoid unused-imports lint when we need fmt only for assertions in the
// future.
var _ = fmt.Sprintf
