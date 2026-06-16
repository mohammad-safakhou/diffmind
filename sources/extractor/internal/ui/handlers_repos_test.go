package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestReposAndFileEndpoints(t *testing.T) {
	base := t.TempDir()
	repo := t.TempDir() // acts as an existing repo directory
	mainPath := filepath.Join(repo, "diffmind.yaml")
	if err := os.WriteFile(mainPath, []byte(archfileBody), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(base, "127.0.0.1", 8080)
	mux := http.NewServeMux()
	s.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Register a repository.
	status, body := postJSON(t, srv, "/api/repos", `{"path":"`+repo+`","file_path":"`+mainPath+`"}`)
	if status != http.StatusOK {
		t.Fatalf("register repo status %d: %v", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("no repo id returned: %v", body)
	}

	// List shows it, enriched and file-present.
	resp, err := http.Get(srv.URL + "/api/repos")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Repos []map[string]any `json:"repos"`
	}
	decodeJSON(t, resp, &list)
	if len(list.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(list.Repos))
	}
	if list.Repos[0]["file_present"] != true {
		t.Fatalf("expected file_present true, got %v", list.Repos[0]["file_present"])
	}

	// GET file content.
	resp, err = http.Get(srv.URL + "/api/architecture/file?path=" + mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var file map[string]any
	decodeJSON(t, resp, &file)
	if file["exists"] != true || file["valid"] != true {
		t.Fatalf("unexpected file status: %v", file)
	}

	// PUT invalid content is rejected.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/architecture/file", strings.NewReader(`{"path":"`+mainPath+`","content":"schema: wrong\n"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid yaml, got %d", resp.StatusCode)
	}

	// fs list of the repo dir surfaces the yaml file.
	resp, err = http.Get(srv.URL + "/api/fs/list?path=" + repo)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var fs struct {
		Files []string `json:"files"`
	}
	decodeJSON(t, resp, &fs)
	found := false
	for _, f := range fs.Files {
		if f == "diffmind.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fs list did not include diffmind.yaml: %v", fs.Files)
	}

	// Delete the registration.
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/repos/"+id, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
}
