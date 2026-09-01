package ui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestV1QueryAPI(t *testing.T) {
	srv, st := newTestServer(t)
	defer srv.Close()
	project, err := st.CreateProject(store.Project{Name: "API Test"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateRun(project.ID, store.RunManifest{Status: store.RunCompleted, StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	graph := archgraph.ArchGraph{RunID: run.ID,
		Services: []*archgraph.ServiceNode{{Name: "orders api", Known: true, Team: "sales", HTTPRoutes: []archgraph.EntitySummary{{ID: "list-orders", Name: "GET /orders", Summary: "Lists orders"}}}, {Name: "frontend", Known: true, Team: "sales"}},
		Edges:    []*archgraph.GraphEdge{{From: "frontend", To: "orders api", Type: "http", Label: "GET /orders"}},
	}
	data, _ := json.Marshal(graph)
	if err := os.WriteFile(filepath.Join(st.RunDir(project.ID, run.ID), "graph.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path   string
		status int
	}{
		{"/api/v1/projects", http.StatusOK},
		{"/api/v1/projects/" + project.ID + "/graph/summary", http.StatusOK},
		{"/api/v1/projects/" + project.ID + "/services", http.StatusOK},
		{"/api/v1/projects/" + project.ID + "/services/" + url.PathEscape("orders api"), http.StatusOK},
		{"/api/v1/projects/" + project.ID + "/dependencies?service=" + url.QueryEscape("orders api") + "&direction=inbound", http.StatusOK},
		{"/api/v1/projects/" + project.ID + "/impact?target=" + url.QueryEscape("orders api"), http.StatusOK},
		{"/api/v1/projects/" + project.ID + "/search?q=orders", http.StatusOK},
		{"/api/v1/projects/" + project.ID + "/services/missing", http.StatusNotFound},
	}
	for _, tc := range tests {
		resp, body := doJSON(t, "GET", srv.URL+tc.path, nil)
		if resp.StatusCode != tc.status {
			t.Errorf("GET %s = %d, want %d: %s", tc.path, resp.StatusCode, tc.status, body)
		}
		if tc.status == http.StatusOK && !json.Valid(body) {
			t.Errorf("GET %s returned invalid JSON", tc.path)
		}
	}
}

func TestV1QueryAPIReportsGraphNotReady(t *testing.T) {
	srv, st := newTestServer(t)
	defer srv.Close()
	project, err := st.CreateProject(store.Project{Name: "Empty"})
	if err != nil {
		t.Fatal(err)
	}
	resp, _ := doJSON(t, "GET", srv.URL+"/api/v1/projects/"+project.ID+"/graph/summary", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d, want 409", resp.StatusCode)
	}
}
