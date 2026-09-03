package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// Both protocols must return the same pinned evidence to a read-only company
// viewer. A newer snapshot must not silently change the baseline query.
func TestGraphHistoryHTTPAndMCPAsViewer(t *testing.T) {
	srv := newAuthTestServer(t)
	srv.SetAuthToken("admin-secret")
	srv.SetTrustedProxySecret("proxy-secret")
	p, err := srv.store.CreateProject(store.Project{Name: "History"})
	if err != nil {
		t.Fatal(err)
	}
	writeRun := func(g *archgraph.ArchGraph, started time.Time) string {
		t.Helper()
		run, err := srv.store.CreateRun(p.ID, store.RunManifest{Status: store.RunCompleted, StartedAt: started})
		if err != nil {
			t.Fatal(err)
		}
		g.RunID = run.ID
		body, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srv.store.RunDir(p.ID, run.ID), "graph.json"), body, 0o644); err != nil {
			t.Fatal(err)
		}
		return run.ID
	}
	dependency := archgraph.EntitySummary{ID: "call", Name: "GET /items", Details: map[string]any{"evidence": map[string]any{"file": "api.go", "line": 8}}}
	g := &archgraph.ArchGraph{Services: []*archgraph.ServiceNode{
		{Name: "gateway", HTTPRoutes: []archgraph.EntitySummary{{ID: "entry", Name: "GET /shop"}}, Dependencies: []archgraph.EntitySummary{dependency}, Connections: []archgraph.ConnectionSummary{{FromID: "entry", ToID: "call", FlowID: "flow"}}},
		{Name: "catalog"},
	}, Edges: []*archgraph.GraphEdge{{From: "gateway", To: "catalog", Type: "http", Details: []archgraph.EntitySummary{dependency}}}}
	old := writeRun(g, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	g.Services[1].Team = "commerce"
	g.Edges = nil
	newID := writeRun(g, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	viewer := &http.Client{Transport: headerRoundTripper{headers: map[string]string{proxySecretHeader: "proxy-secret", proxyUserHeader: "viewer@example.test", proxyRoleHeader: "viewer"}, base: http.DefaultTransport}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "history-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: viewer, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	base := httpServer.URL + "/api/v1/projects/" + p.ID + "/graph/"
	tests := []struct {
		name, path string
		args       map[string]any
		want       map[string]any
	}{
		{"list_graph_runs", "runs?limit=1", map[string]any{"limit": 1}, map[string]any{"total": float64(2), "next_offset": float64(1)}},
		{"compare_graphs", "compare?from=" + old + "&to=" + newID + "&limit=1", map[string]any{"from": old, "to": newID, "limit": 1}, map[string]any{"total": float64(2), "next_offset": float64(1)}},
		{"compare_graphs", "compare?from=" + old + "&to=" + newID + "&offset=1&limit=1", map[string]any{"from": old, "to": newID, "offset": 1, "limit": 1}, map[string]any{"total": float64(2)}},
		{"find_dependency_path", "path?from=gateway&to=catalog&run=" + old, map[string]any{"from": "gateway", "to": "catalog", "run": old}, map[string]any{"status": "found", "run_id": old}},
		{"find_dependency_path", "path?from=gateway&to=catalog&run=" + newID, map[string]any{"from": "gateway", "to": "catalog", "run": newID}, map[string]any{"status": "not_found", "run_id": newID}},
		{"get_object_trace", "trace?service=gateway&object_id=entry&run=" + old, map[string]any{"service": "gateway", "object_id": "entry", "run": old}, map[string]any{"connection_count": float64(1), "edge_count": float64(1), "run_id": old}},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			unauthorized, err := http.Get(base + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			unauthorized.Body.Close()
			if unauthorized.StatusCode != http.StatusUnauthorized {
				t.Fatalf("unauthed status=%d", unauthorized.StatusCode)
			}
			response, err := viewer.Get(base + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var httpData map[string]any
			if err := json.NewDecoder(response.Body).Decode(&httpData); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("HTTP: %d %+v", response.StatusCode, httpData)
			}
			tc.args["project"] = p.ID
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("MCP error: %+v", result.Content)
			}
			if !reflect.DeepEqual(httpData, result.StructuredContent) {
				t.Fatalf("protocols differ: HTTP=%+v MCP=%+v", httpData, result.StructuredContent)
			}
			for key, want := range tc.want {
				if !reflect.DeepEqual(httpData[key], want) {
					t.Errorf("%s=%v want %v", key, httpData[key], want)
				}
			}
		})
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"runs?offset=nope", 400}, {"runs?offset=-1", 400}, {"runs?limit=501", 400},
		{"compare?from=" + old, 400}, {"compare?from=" + old + "&to=" + newID + "&limit=NaN", 400},
		{"compare?from=" + old + "&to=unknown", 404}, {"compare?from=" + url.QueryEscape("../escape") + "&to=" + newID, 404},
		{"path?from=gateway&to=catalog&depth=1.5", 400}, {"path?from=gateway&to=catalog&depth=21", 400},
		{"path?from=missing&to=catalog", 404}, {"trace?service=gateway&object_id=missing", 404}, {"trace?service=gateway", 400},
	} {
		resp, err := viewer.Get(base + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status %d want %d: %s", tc.path, resp.StatusCode, tc.status, body)
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"compare_graphs", map[string]any{"from": old}},
		{"compare_graphs", map[string]any{"from": old, "to": "unknown"}},
		{"list_graph_runs", map[string]any{"limit": 501}},
		{"find_dependency_path", map[string]any{"from": "gateway", "to": "catalog", "depth": 21}},
		{"get_object_trace", map[string]any{"service": "gateway", "object_id": "GET /shop"}},
	} {
		tc.args["project"] = p.ID
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err == nil && !result.IsError {
			t.Errorf("invalid MCP input accepted: %s %+v", tc.name, tc.args)
		}
	}
}
