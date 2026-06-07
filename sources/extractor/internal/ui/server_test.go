package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleRunsAndRun(t *testing.T) {
	base := t.TempDir()
	runDir := filepath.Join(base, "20260225T000000Z")
	for _, sub := range []string{"exposures", "dependencies", "connections", "unresolved"} {
		if err := os.MkdirAll(filepath.Join(runDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteJSON(t, filepath.Join(runDir, "run_manifest.json"), map[string]any{
		"run_id":         "20260225T000000Z",
		"schema_version": "v1alpha1",
		"repo_path":      "/repo",
		"counts": map[string]any{
			"exposures": 1, "dependencies": 1, "connections": 1, "unresolved": 1,
		},
	})
	mustWriteJSON(t, filepath.Join(runDir, "exposures", "http_route.json"), []map[string]any{{"id": "e1"}})
	mustWriteJSON(t, filepath.Join(runDir, "dependencies", "db_operation.json"), []map[string]any{{"id": "d1"}})
	mustWriteJSON(t, filepath.Join(runDir, "connections", "http_route__to__db_operation.json"), []map[string]any{{"id": "c1"}})
	mustWriteJSON(t, filepath.Join(runDir, "unresolved", "x.json"), []map[string]any{{"id": "u1"}})

	s := New(base, "127.0.0.1", 8080)

	rrRuns := httptest.NewRecorder()
	reqRuns := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	s.handleRuns(rrRuns, reqRuns)
	if rrRuns.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrRuns.Code)
	}
	var runsResp struct {
		Runs []string `json:"runs"`
	}
	if err := json.Unmarshal(rrRuns.Body.Bytes(), &runsResp); err != nil {
		t.Fatal(err)
	}
	if len(runsResp.Runs) != 1 || runsResp.Runs[0] != "20260225T000000Z" {
		t.Fatalf("unexpected runs response: %+v", runsResp.Runs)
	}

	rrRun := httptest.NewRecorder()
	reqRun := httptest.NewRequest(http.MethodGet, "/api/run/latest", nil)
	s.handleRun(rrRun, reqRun)
	if rrRun.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rrRun.Code, rrRun.Body.String())
	}
	var runResp RunData
	if err := json.Unmarshal(rrRun.Body.Bytes(), &runResp); err != nil {
		t.Fatal(err)
	}
	if runResp.RunID != "20260225T000000Z" {
		t.Fatalf("unexpected run id: %s", runResp.RunID)
	}
	if runResp.Manifest.Counts["exposures"] != 1 {
		t.Fatalf("expected manifest exposures count 1")
	}
	if len(runResp.Exposures["http_route"]) != 1 {
		t.Fatalf("expected one exposure item")
	}
}

func TestHandleRunGraphAttachesDeterministicReport(t *testing.T) {
	base := t.TempDir()
	runID := "20260225T010000Z"
	stateDir := filepath.Join(base, runID, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteJSON(t, filepath.Join(stateDir, "graph.v1.json"), map[string]any{
		"schema_version": "graph.v1",
		"run_id":         runID,
	})
	mustWriteJSON(t, filepath.Join(stateDir, "deterministic_frameworks.json"), map[string]any{
		"accepted": []map[string]any{{"kind": "http_handler"}},
		"rejected": []map[string]any{{"rejection_reason": "spring_mapping_without_controller_context"}},
		"route_manifest": []map[string]any{{
			"method": "GET", "path": "/orders", "handler": "OrdersController.list", "file": "Orders.java", "line": 12,
		}},
	})
	mustWriteJSON(t, filepath.Join(stateDir, "discovery_evaluation.json"), map[string]any{
		"mode": "shadow_compare",
	})

	s := New(base, "127.0.0.1", 8080)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/graph", nil)
	s.handleRunGraph(rr, req, runID)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	det, _ := body["deterministic"].(map[string]any)
	if det == nil {
		t.Fatalf("deterministic report missing: %#v", body)
	}
	eval, _ := det["evaluation"].(map[string]any)
	if eval["mode"] != "shadow_compare" {
		t.Fatalf("evaluation mode = %v", eval["mode"])
	}
	frameworks, _ := det["frameworks"].(map[string]any)
	manifest, _ := frameworks["route_manifest"].([]any)
	if len(manifest) != 1 {
		t.Fatalf("route manifest len = %d", len(manifest))
	}
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
