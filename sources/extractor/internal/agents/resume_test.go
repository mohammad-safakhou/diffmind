package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// fakeRetryAware fails connections on the first run, then succeeds on
// the second. The role-aware switch lets us reuse one fake across both
// invocations of RunWith via a shared atomic counter.
type fakeRetryAware struct {
	mu              sync.Mutex
	connFailedOnce  atomic.Bool
	httpRouteID     string
	dbDepID         string
	connectionCalls atomic.Int32
	discoveryCalls  atomic.Int32
	detailCalls     atomic.Int32
	repoFactsCalls  atomic.Int32
}

func newFakeRetryAware() *fakeRetryAware {
	return &fakeRetryAware{
		httpRouteID: util.StableID("exposure", "http_route", "GET /users/{id}", "api.go", "10:30"),
		dbDepID:     util.StableID("dependency", "db_operation", "users_select", "repo.go", "40:60"),
	}
}

func (f *fakeRetryAware) Enabled() bool { return true }
func (f *fakeRetryAware) CreateSession(ctx context.Context, directory string) (string, error) {
	return "s", nil
}
func (f *fakeRetryAware) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeRetryAware) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	m, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}
func (f *fakeRetryAware) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	switch {
	case role == "repo_facts":
		f.repoFactsCalls.Add(1)
		return map[string]any{}, nil

	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		f.discoveryCalls.Add(1)
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		f.discoveryCalls.Add(1)
		return map[string]any{"items": []any{map[string]any{
			"type": "db_operation", "name": "users_select", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"operation": "read", "table": "users"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
		}}}, nil
	case role == "discovery":
		f.discoveryCalls.Add(1)
		return map[string]any{"items": []any{}}, nil

	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		f.detailCalls.Add(1)
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}, nil
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		f.detailCalls.Add(1)
		return map[string]any{"item": map[string]any{
			"type": "db_operation", "name": "users_select", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"operation": "read", "table": "users"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
		}}, nil
	case role == "detail":
		f.detailCalls.Add(1)
		return map[string]any{"item": nil}, nil

	case role == "connection":
		f.connectionCalls.Add(1)
		if !f.connFailedOnce.Load() {
			f.connFailedOnce.Store(true)
			return nil, errors.New("connection 401 Unauthorized: scripted failure")
		}
		return map[string]any{"items": []any{map[string]any{
			"from_exposure_id": f.httpRouteID,
			"to_dependency_id": f.dbDepID,
			"summary":          "ok",
			"confidence":       0.9,
			"path_signature":   "p",
			"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
		}}}, nil
	}
	return map[string]any{"items": []any{}}, nil
}

// End-to-end resume: a connection-stage failure halts the run, leaving
// state/*.json + run_failure.json + a retained snapshot. A second
// RunWith using ResumeFromDir + SnapshotPath skips Stages 0-3 and only
// re-runs connections, which now succeeds.
func TestResumeAfterConnectionFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.SkipReexamination = true
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "20260101T000000Z")
	repoDir := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	f := newFakeRetryAware()

	// Run #1: should fail on connections.
	res1, err := RunWith(context.Background(), cfg, repoDir, f, RunOptions{
		RunDir: runDir,
	})
	if err == nil {
		t.Fatalf("expected first run to fail on connections")
	}
	if res1.Failure == nil || res1.Failure.Stage != "connections" {
		t.Fatalf("expected failure in connections stage; got %+v", res1.Failure)
	}
	if res1.SnapshotPath == "" {
		t.Fatalf("expected retained snapshot path on failure")
	}
	beforeRetryRepoFacts := f.repoFactsCalls.Load()
	beforeRetryDiscovery := f.discoveryCalls.Load()
	beforeRetryDetail := f.detailCalls.Load()

	// Run #2: resume from state. Connection now succeeds.
	res2, err := RunWith(context.Background(), cfg, repoDir, f, RunOptions{
		RunDir:        runDir,
		ResumeFromDir: filepath.Join(runDir, "state"),
		SnapshotPath:  res1.SnapshotPath,
	})
	if err != nil {
		t.Fatalf("expected resume to succeed; got %v", err)
	}
	if res2.Failure != nil {
		t.Fatalf("resume should not have produced a Failure; got %+v", res2.Failure)
	}
	if len(res2.Connections) != 1 {
		t.Fatalf("expected 1 connection from the resumed run; got %d", len(res2.Connections))
	}

	// Confirm the resume actually skipped the earlier stages — the call
	// counts must NOT have changed across the two runs.
	if got := f.repoFactsCalls.Load(); got != beforeRetryRepoFacts {
		t.Errorf("repo_facts was re-run on resume: before=%d after=%d", beforeRetryRepoFacts, got)
	}
	if got := f.discoveryCalls.Load(); got != beforeRetryDiscovery {
		t.Errorf("discovery was re-run on resume: before=%d after=%d", beforeRetryDiscovery, got)
	}
	if got := f.detailCalls.Load(); got != beforeRetryDetail {
		t.Errorf("detail was re-run on resume: before=%d after=%d", beforeRetryDetail, got)
	}
	// Cleanup.
	_ = os.RemoveAll(res1.SnapshotPath)
}
