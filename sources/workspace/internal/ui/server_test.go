package ui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/store"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	beRuns := t.TempDir()
	mgr := runmgr.New(st, util.NewLogger(util.LevelInfo), beRuns)
	srv := New(st, mgr, beRuns, "127.0.0.1", 0, util.NewLogger(util.LevelInfo))
	return httptest.NewServer(srv.Handler()), st
}

func doJSON(t *testing.T, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rdr)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func TestProjectsAPI(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	// Empty list.
	resp, data := doJSON(t, "GET", srv.URL+"/api/projects", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list projects = %d: %s", resp.StatusCode, data)
	}

	// Create.
	resp, data = doJSON(t, "POST", srv.URL+"/api/projects", map[string]any{"name": "DEFAULT"})
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d: %s", resp.StatusCode, data)
	}
	var p store.Project
	json.Unmarshal(data, &p)
	if p.ID != "default" {
		t.Fatalf("project id = %q", p.ID)
	}

	// Patch.
	resp, data = doJSON(t, "PATCH", srv.URL+"/api/projects/"+p.ID, map[string]any{"instruction": "hi"})
	if resp.StatusCode != 200 {
		t.Fatalf("patch = %d: %s", resp.StatusCode, data)
	}

	// Get.
	resp, _ = doJSON(t, "GET", srv.URL+"/api/projects/"+p.ID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get = %d", resp.StatusCode)
	}

	// 404 on unknown.
	resp, _ = doJSON(t, "GET", srv.URL+"/api/projects/nope", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("get unknown = %d, want 404", resp.StatusCode)
	}

	// Delete.
	resp, _ = doJSON(t, "DELETE", srv.URL+"/api/projects/"+p.ID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
}

func TestStarterBlueprintsSeeded(t *testing.T) {
	srv, st := newTestServer(t)
	defer srv.Close()

	body := map[string]any{
		"name": "p",
		"starter_blueprints": []map[string]any{
			{"name": "Helm Identity", "body": map[string]any{
				"name":        "Helm Identity",
				"applies_to":  map[string]any{"kind": "service_repo"},
				"extractions": []map[string]any{{"name": "x", "source": map[string]any{"glob": "v.yaml"}, "strategy": "field_path", "extract": []map[string]any{{"field": "a", "maps_to": "service_name"}}}},
			}},
		},
	}
	resp, data := doJSON(t, "POST", srv.URL+"/api/projects", body)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d: %s", resp.StatusCode, data)
	}
	var p store.Project
	json.Unmarshal(data, &p)
	bps, _ := st.ListBlueprints(p.ID)
	if len(bps) != 1 {
		t.Fatalf("expected 1 starter blueprint, got %d", len(bps))
	}
}

func TestReposAndSuggestions(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	_, data := doJSON(t, "POST", srv.URL+"/api/projects", map[string]any{"name": "p"})
	var p store.Project
	json.Unmarshal(data, &p)

	// A search root with one git repo and one non-repo dir.
	rootsDir := t.TempDir()
	repoDir := filepath.Join(rootsDir, "svc-a")
	os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755)
	os.MkdirAll(filepath.Join(rootsDir, "not-a-repo"), 0o755)

	doJSON(t, "PATCH", srv.URL+"/api/projects/"+p.ID, map[string]any{"search_roots": []string{rootsDir}})

	resp, data := doJSON(t, "GET", srv.URL+"/api/projects/"+p.ID+"/repo-suggestions", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("suggestions = %d: %s", resp.StatusCode, data)
	}
	var sresp struct {
		Suggestions []repoSuggestion `json:"suggestions"`
	}
	json.Unmarshal(data, &sresp)
	if len(sresp.Suggestions) != 1 || sresp.Suggestions[0].Name != "svc-a" {
		t.Fatalf("suggestions = %+v", sresp.Suggestions)
	}

	// Create repo + override.
	resp, data = doJSON(t, "POST", srv.URL+"/api/projects/"+p.ID+"/repos", map[string]any{"path": repoDir, "blueprint_ids": []string{"x"}, "instruction": "ovr"})
	if resp.StatusCode != 201 {
		t.Fatalf("create repo = %d: %s", resp.StatusCode, data)
	}
	var repo store.Repo
	json.Unmarshal(data, &repo)
	if len(repo.BlueprintIDs) != 1 || repo.Instruction != "ovr" {
		t.Fatalf("overrides not persisted: %+v", repo)
	}

	// Suggestions now exclude the added repo.
	_, data = doJSON(t, "GET", srv.URL+"/api/projects/"+p.ID+"/repo-suggestions", nil)
	json.Unmarshal(data, &sresp)
	if len(sresp.Suggestions) != 0 {
		t.Fatalf("expected added repo excluded from suggestions, got %+v", sresp.Suggestions)
	}
}

func TestBlueprintValidationAPI(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	_, data := doJSON(t, "POST", srv.URL+"/api/projects", map[string]any{"name": "p"})
	var p store.Project
	json.Unmarshal(data, &p)

	// Invalid JSON.
	req, _ := http.NewRequest("POST", srv.URL+"/api/projects/"+p.ID+"/blueprints", bytes.NewReader([]byte("{not json")))
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 422 {
		t.Fatalf("invalid json blueprint = %d, want 422", resp.StatusCode)
	}
	resp.Body.Close()

	// Structurally invalid (missing extractions).
	req, _ = http.NewRequest("POST", srv.URL+"/api/projects/"+p.ID+"/blueprints", bytes.NewReader([]byte(`{"name":"x","applies_to":{"kind":"bogus"}}`)))
	resp, _ = http.DefaultClient.Do(req)
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Fatalf("invalid blueprint = %d, want 422: %s", resp.StatusCode, bodyBytes)
	}
	var verr struct {
		Validation []map[string]any `json:"validation"`
	}
	json.Unmarshal(bodyBytes, &verr)
	if len(verr.Validation) == 0 {
		t.Fatal("expected structured validation errors")
	}

	// Valid.
	valid := []byte(`{"name":"Good","applies_to":{"kind":"service_repo"},"extractions":[{"name":"e","source":{"glob":"v.yaml"},"strategy":"field_path","extract":[{"field":"a","maps_to":"service_name"}]}]}`)
	req, _ = http.NewRequest("POST", srv.URL+"/api/projects/"+p.ID+"/blueprints", bytes.NewReader(valid))
	resp, _ = http.DefaultClient.Do(req)
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("valid blueprint = %d: %s", resp.StatusCode, bodyBytes)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(bodyBytes, &created)
	if created.ID != "good" {
		t.Fatalf("blueprint id = %q", created.ID)
	}

	// Fetch raw + delete.
	resp, _ = doJSON(t, "GET", srv.URL+"/api/projects/"+p.ID+"/blueprints/"+created.ID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get blueprint = %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, "DELETE", srv.URL+"/api/projects/"+p.ID+"/blueprints/"+created.ID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete blueprint = %d", resp.StatusCode)
	}
}

func TestDiffMindRunsDiscoveryAPI(t *testing.T) {
	st, _ := store.New(t.TempDir())
	beRuns := t.TempDir()
	// Seed one diffmind run manifest.
	runDir := filepath.Join(beRuns, "20260101T000000Z")
	os.MkdirAll(runDir, 0o755)
	os.WriteFile(filepath.Join(runDir, "run_manifest.json"), []byte(`{"run_id":"20260101T000000Z","repo_path":"/repo/x","started_at":"2026-01-01T00:00:00Z"}`), 0o644)

	mgr := runmgr.New(st, util.NewLogger(util.LevelInfo), beRuns)
	srv := httptest.NewServer(New(st, mgr, beRuns, "127.0.0.1", 0, util.NewLogger(util.LevelInfo)).Handler())
	defer srv.Close()

	resp, data := doJSON(t, "GET", srv.URL+"/api/diffmind-runs", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("discovery = %d: %s", resp.StatusCode, data)
	}
	var out struct {
		Runs []map[string]any `json:"runs"`
	}
	json.Unmarshal(data, &out)
	if len(out.Runs) != 1 {
		t.Fatalf("expected 1 discovered run, got %d", len(out.Runs))
	}

	// Filter by repo_path.
	resp, data = doJSON(t, "GET", srv.URL+"/api/diffmind-runs?repo_path=/repo/x", nil)
	json.Unmarshal(data, &out)
	if len(out.Runs) != 1 {
		t.Fatalf("filtered discovery returned %d", len(out.Runs))
	}

}

func TestWorkspaceReposUseStoredDiffMindRunIDFallback(t *testing.T) {
	st, _ := store.New(t.TempDir())
	beRuns := t.TempDir()
	runDir := filepath.Join(beRuns, "20260101T000000Z")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "run_manifest.json"), []byte(`{"run_id":"20260101T000000Z","repo_path":"/old/checkout/path","team":"platform","started_at":"2026-01-01T00:00:00Z","repo_metrics":{"total_loc":42}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := st.CreateProject(store.Project{Name: "fallback"})
	if _, err := st.CreateRepo(p.ID, store.Repo{
		Name:              "svc",
		Path:              "/current/checkout/path",
		Kind:              "service_repo",
		LastDiffMindRunID: "20260101T000000Z",
		Team:              "default",
	}); err != nil {
		t.Fatal(err)
	}

	mgr := runmgr.New(st, util.NewLogger(util.LevelInfo), beRuns)
	srv := New(st, mgr, beRuns, "127.0.0.1", 0, util.NewLogger(util.LevelInfo))
	repos, err := srv.workspaceRepos(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("repos = %d, want 1", len(repos))
	}
	if repos[0].LatestDiffMindRun == nil || repos[0].LatestDiffMindRun.RunID != "20260101T000000Z" {
		t.Fatalf("latest fallback not populated: %+v", repos[0].LatestDiffMindRun)
	}
	if repos[0].EffectiveTeam != "platform" {
		t.Fatalf("effective team = %q, want platform", repos[0].EffectiveTeam)
	}
	if repos[0].RepoMetrics == nil || repos[0].RepoMetrics.TotalLOC != 42 {
		t.Fatalf("repo metrics not copied from fallback run: %+v", repos[0].RepoMetrics)
	}
}
