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

// fakeRetryAware fails the detail stage on the first run, then
// succeeds on the second. The role-aware switch lets us reuse one fake
// across both invocations of RunWith via a shared atomic counter.
//
// Originally this fake targeted the connections stage; that stage is
// now deterministic and cannot fail with an LLM error. The detail
// stage is the new "LLM stage that can fail and be resumed", so we
// exercise resume against it instead.
type fakeRetryAware struct {
	mu               sync.Mutex
	detailFailedOnce atomic.Bool
	httpRouteID      string
	dbDepID          string
	connectionCalls  atomic.Int32
	discoveryCalls   atomic.Int32
	detailCalls      atomic.Int32
	repoFactsCalls   atomic.Int32
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
			// Name the dep so the shallow connection matcher pairs them
			// when this fake's detail succeeds on the resumed run.
			"details": map[string]any{
				"method":       "GET",
				"path":         "/users/{id}",
				"dependencies": []any{map[string]any{"name": "users_select", "type": "db_operation"}},
			},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}, nil
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		f.detailCalls.Add(1)
		// Fail once for the dep detail prompt; succeed thereafter.
		if !f.detailFailedOnce.Load() {
			f.detailFailedOnce.Store(true)
			return nil, errors.New("detail 401 Unauthorized: scripted failure")
		}
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
		// The new connections stage is deterministic — no LLM prompts
		// should reach this branch in a normal run. We keep the case
		// here so older tests' assertions about the counter still hold.
		return map[string]any{"items": []any{}}, nil
	}
	return map[string]any{"items": []any{}}, nil
}

// End-to-end resume: a DETAIL-stage failure halts the run, leaving
// state/*.json + run_failure.json + a retained snapshot. A second
// RunWith using ResumeFromDir + SnapshotPath replays the detail stage
// (since the failed entity is not yet checkpointed), the new
// deterministic connections stage runs, and the overall result is
// success.
//
// Originally this test targeted the connections stage. With the SCIP
// rewrite that stage is deterministic and cannot fail with an LLM
// error; the meaningful "LLM-stage-failure → resume" path now lives
// in detail. The fake fails the dep detail prompt once, then succeeds.
func TestResumeAfterDetailFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.SkipReexamination = true
	cfg.Indexer.Disabled = true // unit tests don't have Docker; skip SCIP
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "20260101T000000Z")
	repoDir := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	f := newFakeRetryAware()

	// Run #1: should fail on detail.
	res1, err := RunWith(context.Background(), cfg, repoDir, f, RunOptions{
		RunDir: runDir,
	})
	if err == nil {
		t.Fatalf("expected first run to fail on detail")
	}
	if res1.Failure == nil || res1.Failure.Stage != "detail" {
		t.Fatalf("expected failure in detail stage; got %+v", res1.Failure)
	}
	if res1.SnapshotPath == "" {
		t.Fatalf("expected retained snapshot path on failure")
	}
	beforeRetryRepoFacts := f.repoFactsCalls.Load()
	beforeRetryDiscovery := f.discoveryCalls.Load()

	// Run #2: resume from state. Detail now succeeds and connections
	// completes successfully.
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

	// Confirm earlier non-detail stages were NOT re-run.
	if got := f.repoFactsCalls.Load(); got != beforeRetryRepoFacts {
		t.Errorf("repo_facts was re-run on resume: before=%d after=%d", beforeRetryRepoFacts, got)
	}
	if got := f.discoveryCalls.Load(); got != beforeRetryDiscovery {
		t.Errorf("discovery was re-run on resume: before=%d after=%d", beforeRetryDiscovery, got)
	}
	_ = os.RemoveAll(res1.SnapshotPath)
}
