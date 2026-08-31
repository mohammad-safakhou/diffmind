package ui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	ag "github.com/mohammad-safakhou/diffmind/internal/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/store"
)

// Seed a project + run with a persisted graph, then exercise the entrypoint
// search and cross-service flow endpoints end-to-end over HTTP.
func seedFlowGraph(t *testing.T, st *store.Store) (pid, rid string) {
	t.Helper()
	pid, rid = "p1", "r1"
	if _, err := st.CreateProject(store.Project{ID: pid, Name: "P1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRun(pid, store.RunManifest{ID: rid, ProjectID: pid, Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	graph := &ag.ArchGraph{
		RunID: rid,
		Services: []*ag.ServiceNode{
			{
				Name:       "orders-api",
				Known:      true,
				Team:       "checkout",
				HTTPRoutes: []ag.EntitySummary{{ID: "http.post_orders", Kind: "http_endpoint", Name: "POST /orders"}},
				Connections: []ag.ConnectionSummary{{
					FromID: "http.post_orders", FromName: "POST /orders", FromType: "http_endpoint",
					ToID: "httpcall.inventory", ToName: "PUT /inventory/{id}", ToType: "http_call",
					FlowID: "flow.orders", EntrypointID: "http.post_orders", Kind: "http", Reachability: "must",
					DataDependencies: []any{map[string]any{"from": "body.itemId", "to": "inventory.item_id"}},
				}},
			},
			{
				Name:       "inventory-api",
				Known:      true,
				HTTPRoutes: []ag.EntitySummary{{ID: "http.put_inventory", Kind: "http_endpoint", Name: "PUT /inventory/{id}"}},
			},
		},
		Edges: []*ag.GraphEdge{{
			From: "orders-api", To: "inventory-api", Type: "http", Label: "HTTP",
			Details: []ag.EntitySummary{{ID: "httpcall.inventory", Name: "PUT /inventory/{id}", Details: map[string]any{"method": "PUT", "matched_exposure_id": "http.put_inventory"}}},
		}},
	}
	data, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.RunDir(pid, rid), "graph.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return pid, rid
}

func TestEntrypointsEndpoint(t *testing.T) {
	ts, st := newTestServer(t)
	defer ts.Close()
	pid, rid := seedFlowGraph(t, st)

	resp, body := doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+pid+"/runs/"+rid+"/archgraph/entrypoints?q=orders", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var refs []ag.EntrypointRef
	if err := json.Unmarshal(body, &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Service != "orders-api" || refs[0].ID != "http.post_orders" {
		t.Fatalf("unexpected refs: %+v", refs)
	}

	// Empty query lists everything, capped.
	_, body = doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+pid+"/runs/"+rid+"/archgraph/entrypoints", nil)
	if err := json.Unmarshal(body, &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected both routes, got %+v", refs)
	}
}

// Impact walks against dependency edges: changing inventory-api must surface
// its caller orders-api in the blast radius.
func TestImpactEndpointFindsUpstreamCallers(t *testing.T) {
	ts, st := newTestServer(t)
	defer ts.Close()
	pid, rid := seedFlowGraph(t, st)

	resp, body := doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+pid+"/runs/"+rid+"/archgraph/impact?node=inventory-api&depth=4", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var view ag.FlowView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, svc := range view.Services {
		if svc.Name == "orders-api" && svc.Depth == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("orders-api must appear in inventory-api's impact: %+v", view.Services)
	}

	// Unknown target 404s.
	resp, _ = doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+pid+"/runs/"+rid+"/archgraph/impact?node=nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown target, got %d", resp.StatusCode)
	}
}

func TestFlowEndpointWalksAcrossServices(t *testing.T) {
	ts, st := newTestServer(t)
	defer ts.Close()
	pid, rid := seedFlowGraph(t, st)

	resp, body := doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+pid+"/runs/"+rid+"/archgraph/flow?service=orders-api&object_id=http.post_orders&depth=4", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var view ag.FlowView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	foundInventory := false
	for _, svc := range view.Services {
		if svc.Name == "inventory-api" {
			foundInventory = true
			if svc.Depth != 1 {
				t.Errorf("inventory-api should be at hop 1, got %d", svc.Depth)
			}
		}
	}
	if !foundInventory {
		t.Fatalf("flow did not cross into inventory-api: %+v", view.Services)
	}
	crossMatched := false
	for _, e := range view.Edges {
		if e.CrossService && e.MatchStatus == "exact_exposure" {
			crossMatched = true
		}
	}
	if !crossMatched {
		t.Fatalf("expected a cross-service edge anchored at an exposure: %+v", view.Edges)
	}
	if len(view.DataDependencies) != 1 || view.DataDependencies[0].Service != "orders-api" {
		t.Fatalf("expected the traversed connection's data dependencies, got %+v", view.DataDependencies)
	}
}
