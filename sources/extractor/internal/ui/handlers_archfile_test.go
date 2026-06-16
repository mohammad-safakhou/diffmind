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

const archfileBody = `schema: diffmind.discovery.v1
service: order-service
exposures:
  - type: http_route
    name: POST /v1/orders
    details:
      method: POST
      path: /v1/orders
dependencies:
  - id: orders_write
    type: db_operation
    name: OrderRepository.save
    details:
      table: orders
      operation: upsert
      platform: postgres
connections:
  - from: POST /v1/orders
    to: orders_write
`

func postJSON(t *testing.T, srv *httptest.Server, path, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestArchitectureFileEndpoints(t *testing.T) {
	base := t.TempDir()
	repo := t.TempDir()
	mainPath := filepath.Join(repo, "diffmind.yaml")
	if err := os.WriteFile(mainPath, []byte(archfileBody), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(base, "127.0.0.1", 8080)
	mux := http.NewServeMux()
	s.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// import-file: the human file lands as a manual source.
	status, body := postJSON(t, srv, "/api/architecture/import-file", `{"path":"`+mainPath+`"}`)
	if status != http.StatusOK {
		t.Fatalf("import-file status %d: %v", status, body)
	}
	summary, _ := body["summary"].(map[string]any)
	if summary["added"].(float64) != 3 { // 1 exposure + 1 dependency + 1 connection
		t.Fatalf("expected 3 added, got %v", summary)
	}

	// export-file: every fact is manual now, so nothing automation-owned to propose.
	status, body = postJSON(t, srv, "/api/architecture/export-file", `{"path":"`+mainPath+`"}`)
	if status != http.StatusOK {
		t.Fatalf("export-file status %d: %v", status, body)
	}
	if body["written"].(float64) != 0 {
		t.Fatalf("expected nothing to propose, got %v written", body["written"])
	}

	// merge-file with no proposal present is a no-op, not an error.
	status, body = postJSON(t, srv, "/api/architecture/merge-file", `{"path":"`+mainPath+`"}`)
	if status != http.StatusOK {
		t.Fatalf("merge-file status %d: %v", status, body)
	}

	// relative paths are rejected.
	status, _ = postJSON(t, srv, "/api/architecture/import-file", `{"path":"relative.yaml"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative path, got %d", status)
	}
}

func TestArchitectureRunProposalAndFileGraph(t *testing.T) {
	base := t.TempDir()
	repo := t.TempDir()
	mainPath := filepath.Join(repo, "diffmind.yaml")

	writeRunArtifacts(t, base, "20260616T100000Z", repo,
		[]map[string]any{{
			"id": "e1", "type": "http_route", "name": "POST /orders", "service": "orders",
			"details": map[string]any{"method": "POST", "path": "/orders"},
		}},
		[]map[string]any{{
			"id": "d1", "type": "db_operation", "name": "write orders", "service": "orders",
			"platform": "postgres", "details": map[string]any{"table": "orders", "operation": "write"},
		}},
		[]map[string]any{{
			"id": "c1", "from_exposure_id": "e1", "to_dependency_id": "d1", "path_signature": "orders-write",
		}},
	)

	s := New(base, "127.0.0.1", 8080)
	mux := http.NewServeMux()
	s.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	status, body := postJSON(t, srv, "/api/architecture/run-proposal", `{"path":"`+mainPath+`","run_id":"20260616T100000Z"}`)
	if status != http.StatusOK {
		t.Fatalf("run-proposal status %d: %v", status, body)
	}
	if got := len(body["append"].([]any)); got != 3 {
		t.Fatalf("first proposal append = %d, want 3: %v", got, body)
	}

	status, body = postJSON(t, srv, "/api/architecture/merge-file", `{"path":"`+mainPath+`"}`)
	if status != http.StatusOK {
		t.Fatalf("merge-file status %d: %v", status, body)
	}
	if body["merged"].(float64) != 3 {
		t.Fatalf("merged = %v, want 3", body["merged"])
	}
	merged, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(merged), "service: orders") {
		t.Fatalf("merged file lost root service:\n%s", string(merged))
	}

	resp, err := http.Get(srv.URL + "/api/architecture/file-graph?path=" + mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file-graph status %d", resp.StatusCode)
	}
	var graph map[string]any
	decodeJSON(t, resp, &graph)
	if got := len(graph["exposures"].([]any)); got != 1 {
		t.Fatalf("graph exposures = %d, want 1", got)
	}
	if got := len(graph["dependencies"].([]any)); got != 1 {
		t.Fatalf("graph dependencies = %d, want 1", got)
	}
	if got := len(graph["connections"].([]any)); got != 1 {
		t.Fatalf("graph connections = %d, want 1", got)
	}
	if got := len(graph["resources"].([]any)); got != 1 {
		t.Fatalf("graph resources = %d, want 1", got)
	}

	writeRunArtifacts(t, base, "20260616T110000Z", repo,
		[]map[string]any{{
			"id": "e2", "type": "http_route", "name": "POST /orders", "service": "orders",
			"details": map[string]any{"method": "POST", "path": "/orders"},
		}},
		[]map[string]any{
			{
				"id": "d2", "type": "db_operation", "name": "write orders", "service": "orders",
				"platform": "postgres", "details": map[string]any{"table": "orders", "operation": "write"},
			},
			{
				"id": "d3", "type": "queue_publish", "name": "publish orders", "service": "orders",
				"platform": "sqs", "details": map[string]any{"queue": "orders-events"},
			},
		},
		[]map[string]any{
			{"id": "c2", "from_exposure_id": "e2", "to_dependency_id": "d2", "path_signature": "orders-write"},
			{"id": "c3", "from_exposure_id": "e2", "to_dependency_id": "d3", "path_signature": "orders-publish"},
		},
	)

	status, body = postJSON(t, srv, "/api/architecture/run-proposal", `{"path":"`+mainPath+`","run_id":"20260616T110000Z"}`)
	if status != http.StatusOK {
		t.Fatalf("second run-proposal status %d: %v", status, body)
	}
	if got := len(body["append"].([]any)); got != 2 {
		t.Fatalf("second proposal append = %d, want 2: %v", got, body)
	}
	if got := len(body["skip"].([]any)); got != 3 {
		t.Fatalf("second proposal skip = %d, want 3: %v", got, body)
	}
}

func TestArchitectureFileDraftApply(t *testing.T) {
	base := t.TempDir()
	repo := t.TempDir()
	mainPath := filepath.Join(repo, "diffmind.yaml")
	if err := os.WriteFile(mainPath, []byte(`schema: diffmind.discovery.v1
service: orders
resources:
  - id: orders_db
    kind: datastore
    platform: postgres
    name: Orders DB
    instance: orders
dependencies:
  - type: db_operation
    name: OrderRepository.save
    resource: orders_db
    details: {table: orders, operation: write, platform: postgres}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(base, "127.0.0.1", 8080)
	mux := http.NewServeMux()
	s.routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/architecture/file?path=" + mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var file map[string]any
	decodeJSON(t, resp, &file)
	sha, _ := file["sha256"].(string)
	if sha == "" {
		t.Fatalf("file response missing sha: %+v", file)
	}

	before, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	status, body := postJSON(t, srv, "/api/architecture/file-draft", `{"path":"`+mainPath+`","base_sha":"`+sha+`","edits":{"resources":[{"id":"orders_db","name":"Orders Database"}]}}`)
	if status != http.StatusOK {
		t.Fatalf("file-draft status %d: %v", status, body)
	}
	if !strings.Contains(body["yaml"].(string), "Orders Database") {
		t.Fatalf("draft yaml missing edit: %v", body["yaml"])
	}
	afterDraft, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(afterDraft) {
		t.Fatal("draft endpoint wrote the main file")
	}

	status, _ = postJSON(t, srv, "/api/architecture/file-apply", `{"path":"`+mainPath+`","base_sha":"stale","yaml":`+jsonString(body["yaml"].(string))+`}`)
	if status != http.StatusConflict {
		t.Fatalf("stale apply status = %d, want 409", status)
	}
	status, body = postJSON(t, srv, "/api/architecture/file-apply", `{"path":"`+mainPath+`","base_sha":"`+sha+`","yaml":`+jsonString(body["yaml"].(string))+`}`)
	if status != http.StatusOK {
		t.Fatalf("file-apply status %d: %v", status, body)
	}
	afterApply, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(afterApply), "Orders Database") {
		t.Fatalf("apply did not write draft:\n%s", string(afterApply))
	}

	status, _ = postJSON(t, srv, "/api/architecture/file-apply", `{"path":"`+mainPath+`","base_sha":"`+body["sha256"].(string)+`","yaml":"schema: ["}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid yaml apply status = %d, want 400", status)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func writeRunArtifacts(t *testing.T, base, runID, repo string, exposures, dependencies, connections []map[string]any) {
	t.Helper()
	runDir := filepath.Join(base, runID)
	for _, sub := range []string{"exposures", "dependencies", "connections"} {
		if err := os.MkdirAll(filepath.Join(runDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteJSON(t, filepath.Join(runDir, "run_manifest.json"), map[string]any{
		"run_id": runID, "schema_version": "v1alpha1", "repo_path": repo, "counts": map[string]int{},
	})
	mustWriteJSON(t, filepath.Join(runDir, "exposures", "http_route.json"), exposures)
	mustWriteJSON(t, filepath.Join(runDir, "dependencies", "deps.json"), dependencies)
	mustWriteJSON(t, filepath.Join(runDir, "connections", "connections.json"), connections)
}
