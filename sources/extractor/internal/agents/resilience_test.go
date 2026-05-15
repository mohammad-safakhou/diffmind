package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// fakeFlaky triggers a controllable set of failures to exercise the
// pipeline's error-reporting and resilience behavior.
type fakeFlaky struct {
	mu               sync.Mutex
	failRepoFacts    bool
	failObjectiveID  string
	failDetailID     string
	failConnectionID string
}

func (f *fakeFlaky) Enabled() bool { return true }
func (f *fakeFlaky) CreateSession(ctx context.Context, directory string) (string, error) {
	return "s", nil
}
func (f *fakeFlaky) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeFlaky) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	m, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}
func (f *fakeFlaky) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	f.mu.Lock()
	failFacts := f.failRepoFacts
	failObj := f.failObjectiveID
	failDet := f.failDetailID
	failConn := f.failConnectionID
	f.mu.Unlock()

	exposureID := util.StableID("exposure", "http_route", "GET /users/{id}", "api.go", "10:30")
	dependencyID := util.StableID("dependency", "db_operation", "users_table_select", "repo.go", "40:60")

	switch {
	case role == "repo_facts":
		if failFacts {
			return nil, fmt.Errorf("repo facts boom")
		}
		return map[string]any{}, nil
	case role == "discovery" && failObj != "" && strings.Contains(prompt, "OBJECTIVE_ID: "+failObj):
		return nil, fmt.Errorf("discovery boom for %s", failObj)
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"items": []any{map[string]any{
			"type": "db_operation", "name": "users_table_select", "summary": "x", "confidence": 0.94,
			"details":          map[string]any{"operation": "read", "table": "users"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
		}}}, nil
	case role == "discovery":
		return map[string]any{"items": []any{}}, nil
	case role == "detail" && failDet != "" && strings.Contains(prompt, "OBJECTIVE_ID: "+failDet):
		return nil, fmt.Errorf("detail boom for %s", failDet)
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "detailed", "confidence": 0.96,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}", "auth": "required"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}, nil
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"item": map[string]any{
			"type": "db_operation", "name": "users_table_select", "summary": "detailed", "confidence": 0.95,
			"details":          map[string]any{"operation": "read", "table": "users"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
		}}, nil
	case role == "detail":
		return map[string]any{"item": nil}, nil
	case role == "connection" && failConn != "" && strings.Contains(prompt, "EXPOSURE_ID: "+failConn):
		return nil, fmt.Errorf("connection boom for %s", failConn)
	case role == "connection":
		return map[string]any{"items": []any{map[string]any{
			"from_exposure_id": exposureID,
			"to_dependency_id": dependencyID,
			"summary":          "mapped",
			"confidence":       0.9,
			"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
		}}}, nil
	default:
		return map[string]any{"items": []any{}}, nil
	}
}

// Fail-fast policy: a Stage 0 (repo_facts) failure halts the run with a
// hard error. The Result still contains a populated Failure and a
// retained snapshot path so the operator can inspect the captured prompt
// and response and call `diffmind retry` once the underlying problem is
// fixed.
func TestRunHaltsOnRepoFactsFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	f := &fakeFlaky{failRepoFacts: true}
	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	if err == nil {
		t.Fatalf("repo_facts failure must surface as a hard error under fail-fast policy")
	}
	if res.Failure == nil {
		t.Fatalf("Result.Failure must be populated; got %+v", res)
	}
	if res.Failure.Stage != "repo_facts" {
		t.Errorf("Failure.Stage = %q, want repo_facts", res.Failure.Stage)
	}
	if res.SnapshotPath == "" {
		t.Errorf("Failure must retain SnapshotPath so retry can re-attach")
	}
	// Make sure the snapshot dir actually still exists on disk.
	if _, statErr := os.Stat(res.SnapshotPath); statErr != nil {
		t.Errorf("snapshot dir was removed even though run failed: %v", statErr)
	}
	// Cleanup so we don't litter /var/folders.
	_ = os.RemoveAll(res.SnapshotPath)
}

// Fail-fast policy: a Stage 1 (discovery) failure on any single
// objective halts the entire run. No partial entities are returned.
func TestRunHaltsOnDiscoveryFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	f := &fakeFlaky{failObjectiveID: "exposure.webhook"}
	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	if err == nil {
		t.Fatalf("discovery failure must surface as a hard error under fail-fast policy")
	}
	if res.Failure == nil || res.Failure.Stage != "discovery" {
		t.Fatalf("Failure.Stage must be 'discovery'; got %+v", res.Failure)
	}
	if res.Failure.ObjectiveID != "exposure.webhook" {
		t.Errorf("Failure.ObjectiveID = %q, want exposure.webhook", res.Failure.ObjectiveID)
	}
	// Even though some other objectives may have completed in parallel,
	// fail-fast does NOT return partial entities.
	if len(res.Exposures) != 0 || len(res.Dependencies) != 0 {
		t.Errorf("fail-fast must return zero entities; got %d/%d", len(res.Exposures), len(res.Dependencies))
	}
	_ = os.RemoveAll(res.SnapshotPath)
}

// Fail-fast policy: a Stage 4 (connections) failure halts the run. The
// Failure carries the exposure name so the operator can locate the
// relevant prompt/response files.
func TestRunHaltsOnConnectionFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	exposureID := util.StableID("exposure", "http_route", "GET /users/{id}", "api.go", "10:30")
	f := &fakeFlaky{failConnectionID: exposureID}
	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	if err == nil {
		t.Fatalf("connection failure must surface as a hard error under fail-fast policy")
	}
	if res.Failure == nil || res.Failure.Stage != "connections" {
		t.Fatalf("Failure.Stage must be 'connections'; got %+v", res.Failure)
	}
	if !strings.Contains(res.Failure.EntityName, exposureID) {
		t.Errorf("Failure.EntityName should reference the failing exposure %q; got %q", exposureID, res.Failure.EntityName)
	}
	_ = os.RemoveAll(res.SnapshotPath)
}
