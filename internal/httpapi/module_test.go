package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	mux := newMux("")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestEntitiesEndpoint(t *testing.T) {
	tmp := t.TempDir()
	bundlePath := filepath.Join(tmp, "bundle.json")
	writeBundle(t, bundlePath, map[string]any{
		"snapshot_id": "s1",
		"entities": []map[string]any{
			{"id": "a", "type": "Endpoint", "natural_key": "GET|/a", "attributes": map[string]any{"method": "GET", "path": "/a"}, "evidence_ids": []string{"e1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
			{"id": "b", "type": "RuntimeUnit", "natural_key": "go|main|cmd/main.go", "attributes": map[string]any{"language": "go"}, "evidence_ids": []string{"e2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
		},
	})

	mux := newMux(bundlePath)
	req := httptest.NewRequest(http.MethodGet, "/entities?view=endpoints", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Count != 1 {
		t.Fatalf("expected 1 endpoint, got %d", payload.Count)
	}
}

func TestDiffEndpoint(t *testing.T) {
	tmp := t.TempDir()
	fromPath := filepath.Join(tmp, "from.json")
	toPath := filepath.Join(tmp, "to.json")

	writeBundle(t, fromPath, map[string]any{
		"snapshot_id": "s1",
		"entities": []map[string]any{
			{"id": "a", "type": "Endpoint", "natural_key": "GET|/a", "attributes": map[string]any{"path": "/a"}, "evidence_ids": []string{"e1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
		},
	})
	writeBundle(t, toPath, map[string]any{
		"snapshot_id": "s2",
		"entities": []map[string]any{
			{"id": "a2", "type": "Endpoint", "natural_key": "GET|/a", "attributes": map[string]any{"path": "/a"}, "evidence_ids": []string{"e1"}, "fact_ids": []string{"f1"}, "confidence": 0.8},
		},
	})

	mux := newMux("")
	req := httptest.NewRequest(http.MethodGet, "/diff?from="+fromPath+"&to="+toPath, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload struct {
		Changed int `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Changed != 1 {
		t.Fatalf("expected changed=1, got %d", payload.Changed)
	}
}

func writeBundle(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
}
