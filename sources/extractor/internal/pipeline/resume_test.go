package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// fakeRetryAware fails the re-examination stage on the first run, then
// succeeds on the second. The role-aware switch lets us reuse one fake
// across both invocations of RunWith via a shared atomic counter.
//
// This fake has targeted, in turn, the connections stage and then the
// detail stage as "the LLM stage that can fail and be resumed". Both are
// now gone (connections is deterministic; detail was removed), so the
// resumable LLM stage is re-examination — the fake fails its prompt once,
// then succeeds.
type fakeRetryAware struct {
	reexamFailedOnce atomic.Bool
	httpRouteID      string
	dbDepID          string
	connectionCalls  atomic.Int32
	discoveryCalls   atomic.Int32
	reexamCalls      atomic.Int32
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

// rescuedRoute is the corrected http_route the re-examination returns. It
// carries the dependencies hint so the shallow connection matcher (no SCIP
// index in tests) pairs it with the db dependency on the resumed run.
func rescuedRoute() map[string]any {
	return map[string]any{
		"type": "http_route", "name": "GET /users/{id}", "summary": "x (re-verified)", "confidence": 0.95,
		"details": map[string]any{
			"method": "GET", "path": "/users/{id}", "auth": "none",
			"dependencies": []any{map[string]any{"name": "users_select", "type": "db_operation"}},
		},
		"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
	}
}

func (f *fakeRetryAware) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	switch {
	case role == "repo_facts":
		f.repoFactsCalls.Add(1)
		return map[string]any{}, nil

	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		f.discoveryCalls.Add(1)
		// Low confidence → this seed is a re-examination suspect.
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "x", "confidence": 0.2,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}", "auth": "none"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		f.discoveryCalls.Add(1)
		// Complete + high confidence → clean, never a suspect.
		return map[string]any{"items": []any{map[string]any{
			"type": "db_operation", "name": "users_select", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"operation": "read", "table": "users", "datasource": "primary", "client": "userRepo"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
		}}}, nil
	case role == "discovery":
		f.discoveryCalls.Add(1)
		return map[string]any{"items": []any{}}, nil

	case role == "reexamination":
		f.reexamCalls.Add(1)
		// Fail once; succeed (rescue the suspect) thereafter.
		if !f.reexamFailedOnce.Load() {
			f.reexamFailedOnce.Store(true)
			return nil, errors.New("reexamination 401 Unauthorized: scripted failure")
		}
		return map[string]any{"items": []any{rescuedRoute()}}, nil

	case role == "connection":
		f.connectionCalls.Add(1)
		return map[string]any{"items": []any{}}, nil
	}
	return map[string]any{"items": []any{}}, nil
}

// End-to-end resume: a RE-EXAMINATION-stage failure halts the run, leaving
// state/*.json + run_failure.json + a retained snapshot. A second RunWith
// using ResumeFromDir + SnapshotPath replays re-examination (the suspect was
// not checkpointed because its prompt errored), converts the rescued seed to
// an entity, runs the deterministic connections stage, and succeeds — without
// re-running repo_facts or discovery.
func TestResumeAfterReexaminationFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "20260101T000000Z")
	repoDir := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	f := newFakeRetryAware()

	// Run #1: should fail on re-examination.
	res1, err := RunWith(context.Background(), cfg, repoDir, f, RunOptions{
		RunDir: runDir,
	})
	if err == nil {
		t.Fatalf("expected first run to fail on reexamination")
	}
	if res1.Failure == nil || res1.Failure.Stage != "reexamination" {
		t.Fatalf("expected failure in reexamination stage; got %+v", res1.Failure)
	}
	if res1.SnapshotPath == "" {
		t.Fatalf("expected retained snapshot path on failure")
	}
	beforeRetryRepoFacts := f.repoFactsCalls.Load()
	beforeRetryDiscovery := f.discoveryCalls.Load()

	// Run #2: resume from state. Re-examination now succeeds and the run
	// completes.
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

	// Confirm earlier stages were NOT re-run on resume.
	if got := f.repoFactsCalls.Load(); got != beforeRetryRepoFacts {
		t.Errorf("repo_facts was re-run on resume: before=%d after=%d", beforeRetryRepoFacts, got)
	}
	if got := f.discoveryCalls.Load(); got != beforeRetryDiscovery {
		t.Errorf("discovery was re-run on resume: before=%d after=%d", beforeRetryDiscovery, got)
	}
	_ = os.RemoveAll(res1.SnapshotPath)
}
