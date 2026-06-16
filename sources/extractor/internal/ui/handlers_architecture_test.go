package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestArchitectureAPIManualSaveAndRunImport(t *testing.T) {
	base := t.TempDir()
	runID := "20260615T120000Z"
	runDir := filepath.Join(base, runID)
	for _, sub := range []string{"exposures", "dependencies", "connections"} {
		if err := os.MkdirAll(filepath.Join(runDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteJSON(t, filepath.Join(runDir, "run_manifest.json"), map[string]any{
		"run_id": runID, "schema_version": "v1alpha1", "repo_path": "/repo", "counts": map[string]int{},
	})
	mustWriteJSON(t, filepath.Join(runDir, "exposures", "http_route.json"), []map[string]any{{
		"id": "e1", "type": "http_route", "name": "GET /orders", "service": "orders",
		"summary": "generated", "details": map[string]any{"method": "GET", "path": "/orders"},
	}})
	mustWriteJSON(t, filepath.Join(runDir, "dependencies", "db_operation.json"), []map[string]any{{
		"id": "d1", "type": "db_operation", "name": "read orders", "service": "orders",
		"summary": "generated", "details": map[string]any{"table": "orders", "operation": "read"},
	}})
	mustWriteJSON(t, filepath.Join(runDir, "connections", "http_route__to__db_operation.json"), []map[string]any{{
		"id": "c1", "from_exposure_id": "e1", "to_dependency_id": "d1", "path_signature": "orders->db",
	}})

	s := New(base, "127.0.0.1", 8080)
	mux := http.NewServeMux()
	s.routes(mux)

	importBody, _ := json.Marshal(map[string]string{"run_id": runID})
	importReq := httptest.NewRequest(http.MethodPost, "/api/architecture/import-run", bytes.NewReader(importBody))
	importRes := httptest.NewRecorder()
	mux.ServeHTTP(importRes, importReq)
	if importRes.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", importRes.Code, importRes.Body.String())
	}

	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, httptest.NewRequest(http.MethodGet, "/api/architecture", nil))
	if getRes.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", getRes.Code, getRes.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(getRes.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if got := int(doc["revision"].(float64)); got != 1 {
		t.Fatalf("revision = %d, want 1", got)
	}
	exposures := doc["exposures"].([]any)
	if len(exposures) != 1 {
		t.Fatalf("exposures = %d, want 1", len(exposures))
	}
	exposures[0].(map[string]any)["summary"] = "curated"

	saveBody, _ := json.Marshal(doc)
	saveRes := httptest.NewRecorder()
	mux.ServeHTTP(saveRes, httptest.NewRequest(http.MethodPut, "/api/architecture", bytes.NewReader(saveBody)))
	if saveRes.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", saveRes.Code, saveRes.Body.String())
	}

	staleRes := httptest.NewRecorder()
	mux.ServeHTTP(staleRes, httptest.NewRequest(http.MethodPut, "/api/architecture", bytes.NewReader(saveBody)))
	if staleRes.Code != http.StatusConflict {
		t.Fatalf("stale save = %d, want 409: %s", staleRes.Code, staleRes.Body.String())
	}
}

func TestArchitectureAPIRejectsIncompleteRunImport(t *testing.T) {
	base := t.TempDir()
	runID := "20260615T130000Z"
	runDir := filepath.Join(base, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteJSON(t, filepath.Join(runDir, "run_failure.json"), map[string]any{
		"stage": "discovery",
		"error": "provider unavailable",
	})

	s := New(base, "127.0.0.1", 8080)
	mux := http.NewServeMux()
	s.routes(mux)

	body, _ := json.Marshal(map[string]string{"run_id": runID})
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/architecture/import-run", bytes.NewReader(body)))
	if res.Code != http.StatusConflict {
		t.Fatalf("import = %d, want 409: %s", res.Code, res.Body.String())
	}
}
