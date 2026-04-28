package agents

import (
	"context"
	"encoding/json"
	"fmt"
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

// Stage 0 failure is non-fatal: the pipeline continues with nil repo facts.
func TestRunSurvivesRepoFactsFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	f := &fakeFlaky{failRepoFacts: true}
	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	if err != nil {
		t.Fatalf("pipeline should not fail on repo facts error: %v", err)
	}
	if len(res.Exposures) == 0 || len(res.Dependencies) == 0 {
		t.Fatalf("expected extraction to continue despite repo facts failure")
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "repo_facts extraction failed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected repo_facts warning, got %+v", res.Warnings)
	}
}

// Stage 1 per-objective failure surfaces as a warning + unresolved, but does
// not stop the rest of the pipeline.
func TestRunSurvivesObjectiveFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	f := &fakeFlaky{failObjectiveID: "exposure.webhook"}
	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	if err != nil {
		t.Fatalf("pipeline should not fail on single objective error: %v", err)
	}
	if len(res.Exposures) == 0 || len(res.Dependencies) == 0 {
		t.Fatalf("expected http_route exposure and db dep to still come through")
	}
	found := false
	for _, u := range res.Unresolved {
		if u.ReasonCode == "agent_failure" && u.Type == "webhook" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected agent_failure unresolved for webhook, got %+v", res.Unresolved)
	}
}

// Stage 4 per-exposure failure surfaces as an unresolved item; the rest of
// the run still completes and writes the entities that did succeed.
func TestRunSurvivesConnectionFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	exposureID := util.StableID("exposure", "http_route", "GET /users/{id}", "api.go", "10:30")
	f := &fakeFlaky{failConnectionID: exposureID}
	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	if err != nil {
		t.Fatalf("pipeline should not fail on single connection error: %v", err)
	}
	if len(res.Connections) != 0 {
		t.Fatalf("expected 0 connections (the only exposure's mapping failed), got %d", len(res.Connections))
	}
	found := false
	for _, u := range res.Unresolved {
		if u.ReasonCode == "agent_failure" && u.Type == "connection" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected connection agent_failure unresolved, got %+v", res.Unresolved)
	}
}
