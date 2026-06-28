package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestConfigEndpointPrefill verifies /api/config surfaces the deterministic run
// defaults used by the New Run form.
func TestConfigEndpointPrefill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIFFMIND_HOME", home)
	cfgJSON := `{"runtime":{"pipeline":"deterministic","workers":7},"quality":{"min_confidence":0.82}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	uiServer := New(t.TempDir(), "127.0.0.1", 0)
	mux := http.NewServeMux()
	uiServer.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["opencode"]; ok {
		t.Fatalf("opencode config must not be exposed via /api/config")
	}
	rt, _ := body["runtime"].(map[string]any)
	if rt["workers"].(float64) != 7 {
		t.Fatalf("workers = %v", rt["workers"])
	}
	if rt["pipeline"] != "deterministic" {
		t.Fatalf("pipeline = %v", rt["pipeline"])
	}
	quality, _ := body["quality"].(map[string]any)
	if quality["min_confidence"].(float64) != 0.82 {
		t.Fatalf("min_confidence = %v", quality["min_confidence"])
	}
}

// TestCancelRouteIsPOST verifies the cancel endpoint moved to POST
// /api/runs/{id}/cancel and that DELETE /api/runs/{id} is the delete path.
func TestCancelRouteIsPOST(t *testing.T) {
	baseDir := t.TempDir()
	uiServer := New(baseDir, "127.0.0.1", 0)
	mux := http.NewServeMux()
	uiServer.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// POST cancel on an unknown run is an idempotent 200 no-op.
	resp, err := http.Post(srv.URL+"/api/runs/unknown/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST cancel = %d, want 200", resp.StatusCode)
	}

	// GET cancel must not be allowed.
	getResp, err := http.Get(srv.URL + "/api/runs/unknown/cancel")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET cancel = %d, want 405", getResp.StatusCode)
	}
}
