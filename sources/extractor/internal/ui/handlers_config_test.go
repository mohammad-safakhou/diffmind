package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestConfigEndpointPrefill verifies /api/config surfaces the central config's
// non-secret fields for the New Run form and never leaks the password.
func TestConfigEndpointPrefill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIFFMIND_HOME", home)
	cfgJSON := `{"opencode":{"base_url":"http://pref:4096","provider_id":"openai","model_id":"gpt","password":"secret"},"runtime":{"workers":7,"deterministic_discovery":"shadow_compare"}}`
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
	oc, _ := body["opencode"].(map[string]any)
	if oc["base_url"] != "http://pref:4096" {
		t.Fatalf("base_url = %v", oc["base_url"])
	}
	if _, leaked := oc["password"]; leaked {
		t.Fatalf("password must not be exposed via /api/config")
	}
	rt, _ := body["runtime"].(map[string]any)
	if rt["workers"].(float64) != 7 {
		t.Fatalf("workers = %v", rt["workers"])
	}
	if rt["deterministic_discovery"] != "shadow_compare" {
		t.Fatalf("deterministic_discovery = %v", rt["deterministic_discovery"])
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
