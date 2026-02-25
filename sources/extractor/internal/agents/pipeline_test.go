package agents

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type fakeOpenCode struct {
	mu sync.Mutex
}

func (f *fakeOpenCode) Enabled() bool { return true }

func (f *fakeOpenCode) CreateSession(ctx context.Context, directory string) (string, error) {
	return "s", nil
}

func (f *fakeOpenCode) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}

func (f *fakeOpenCode) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	exposureID := util.StableID("exposure", "http_route", "GET /users/{id}", "api.go", "10:30")
	dependencyID := util.StableID("dependency", "db_operation", "users_table_select", "repo.go", "40:60")

	switch {
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "HTTP endpoint", "confidence": 0.95,
			"inputs":           []any{map[string]any{"name": "id", "type": "string", "required": true}},
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
			"evidence":         []any{map[string]any{"file": "api.go", "start_line": 12, "end_line": 12, "snippet": "router.GET(\"/users/{id}\", getUser)", "source": "opencode"}},
		}}}, nil
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"items": []any{map[string]any{
			"type": "db_operation", "name": "users_table_select", "summary": "Reads users table", "confidence": 0.94,
			"inputs":           []any{map[string]any{"name": "id", "type": "string", "required": true}},
			"details":          map[string]any{"table": "users", "operation": "read"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
			"evidence":         []any{map[string]any{"file": "repo.go", "start_line": 45, "end_line": 45, "snippet": "SELECT * FROM users WHERE id = ?", "source": "opencode"}},
		}}}, nil
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor"):
		return map[string]any{"items": []any{}}, nil

	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "Validates id and returns user", "confidence": 0.96,
			"key_actions":      []any{"validate path param", "query user repository", "return JSON"},
			"inputs":           []any{map[string]any{"name": "id", "type": "string", "required": true, "description": "user identifier"}},
			"details":          map[string]any{"method": "GET", "path": "/users/{id}", "auth": "required"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
			"evidence":         []any{map[string]any{"file": "api.go", "start_line": 20, "end_line": 20, "snippet": "userRepo.GetByID(ctx, id)", "source": "opencode"}},
		}}, nil
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"item": map[string]any{
			"type": "db_operation", "name": "users_table_select", "summary": "Select by id on users table", "confidence": 0.95,
			"key_actions":      []any{"prepare query", "execute read"},
			"inputs":           []any{map[string]any{"name": "id", "type": "string", "required": true}},
			"details":          map[string]any{"table": "users", "operation": "read", "transaction": "none"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
			"evidence":         []any{map[string]any{"file": "repo.go", "start_line": 45, "end_line": 45, "snippet": "SELECT * FROM users WHERE id = ?", "source": "opencode"}},
		}}, nil
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor"):
		return map[string]any{"item": nil}, nil

	case strings.Contains(prompt, "AGENT ROLE: connection-extractor") && strings.Contains(prompt, "EXPOSURE_ID: "+exposureID):
		return map[string]any{"items": []any{map[string]any{
			"from_exposure_id": exposureID,
			"to_dependency_id": dependencyID,
			"summary":          "Reads users table for endpoint",
			"confidence":       0.93,
			"path_signature":   "main",
			"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
			"paths": []any{map[string]any{
				"id":        "p1",
				"summary":   "happy path",
				"condition": map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
				"steps": []any{map[string]any{
					"order": 1, "action": "query users", "operation": "read", "from": "http_route", "to": "db_operation",
					"condition": map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
					"location":  map[string]any{"file": "api.go", "start_line": 20, "end_line": 20},
					"evidence":  []any{map[string]any{"file": "api.go", "start_line": 20, "end_line": 20, "snippet": "userRepo.GetByID(ctx, id)", "source": "opencode"}},
				}},
			}},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 20, "end_line": 20}},
			"evidence":         []any{map[string]any{"file": "api.go", "start_line": 20, "end_line": 20, "snippet": "userRepo.GetByID(ctx, id)", "source": "opencode"}},
		}}}, nil
	case strings.Contains(prompt, "AGENT ROLE: connection-extractor"):
		return map[string]any{"items": []any{}}, nil
	default:
		return map[string]any{"items": []any{}}, nil
	}
}

type fakeOpenCodeLowConfidence struct{}

func (f *fakeOpenCodeLowConfidence) Enabled() bool { return true }
func (f *fakeOpenCodeLowConfidence) CreateSession(ctx context.Context, directory string) (string, error) {
	return "s", nil
}
func (f *fakeOpenCodeLowConfidence) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeOpenCodeLowConfidence) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	switch {
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /foo", "summary": "foo", "confidence": 0.2,
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 1, "end_line": 2}},
		}}}, nil
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /foo", "summary": "foo", "confidence": 0.2,
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 1, "end_line": 2}},
		}}, nil
	default:
		return map[string]any{"items": []any{}}, nil
	}
}

func TestRunDeterministicPipelineBuildsEntitiesAndConnections(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7

	result, err := Run(context.Background(), cfg, "/repo", &fakeOpenCode{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(result.Exposures) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(result.Exposures))
	}
	if len(result.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Dependencies))
	}
	if len(result.Connections) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(result.Connections))
	}
	if result.Exposures[0].Type != "http_route" {
		t.Fatalf("unexpected exposure type: %s", result.Exposures[0].Type)
	}
	if result.Dependencies[0].Type != "db_operation" {
		t.Fatalf("unexpected dependency type: %s", result.Dependencies[0].Type)
	}
	if result.Dependencies[0].Details["table"] != "users" {
		t.Fatalf("expected dependency table details")
	}
	if len(result.Connections[0].Paths) != 1 {
		t.Fatalf("expected one connection path")
	}
	if len(result.Connections[0].Paths[0].Steps) != 1 {
		t.Fatalf("expected one path step")
	}
	if result.Connections[0].Paths[0].Steps[0].Operation != "read" {
		t.Fatalf("unexpected path operation: %s", result.Connections[0].Paths[0].Steps[0].Operation)
	}
}

func TestRunDropsLowConfidenceEntities(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 2
	cfg.Quality.MinConfidence = 0.7

	result, err := Run(context.Background(), cfg, "/repo", &fakeOpenCodeLowConfidence{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(result.Exposures) != 0 {
		t.Fatalf("expected 0 exposures, got %d", len(result.Exposures))
	}
	found := false
	for _, u := range result.Unresolved {
		if u.ReasonCode == "low_confidence" && u.Type == "http_route" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected low_confidence unresolved item for http_route")
	}
}
