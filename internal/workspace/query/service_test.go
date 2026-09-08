package query

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func testQueryService(t *testing.T) (*Service, string, string) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(store.Project{Name: "Example Platform"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateRun(project.ID, store.RunManifest{Status: store.RunCompleted, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	graph := &archgraph.ArchGraph{
		RunID: run.ID,
		Services: []*archgraph.ServiceNode{
			{Name: "catalog", Known: true, Team: "commerce", HTTPRoutes: []archgraph.EntitySummary{{ID: "get-product", Name: "GET /products/{id}", Summary: "Reads one product", Details: map[string]any{"request_fields": []any{map[string]any{"name": "experimentGroup", "type": "string", "required": false}}}}}, Dependencies: []archgraph.EntitySummary{{ID: "dep-db", Name: "products"}}},
			{Name: "checkout", Known: true, Team: "commerce", Dependencies: []archgraph.EntitySummary{{ID: "call-catalog", Name: "catalog", Summary: "Loads product prices"}}},
		},
		ResourceNodes: []*archgraph.ResourceNode{{ID: "db:products", GraphID: "db:products", Name: "products", Kind: "database", OwnerService: "catalog"}},
		Edges: []*archgraph.GraphEdge{
			{From: "checkout", To: "catalog", Type: "http", Label: "GET /products/{id}"},
			{From: "catalog", To: "db:products", Type: "database", Label: "read products"},
		},
	}
	data, _ := json.Marshal(graph)
	if err := os.WriteFile(filepath.Join(st.RunDir(project.ID, run.ID), "graph.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return New(st), project.ID, run.ID
}

func TestQueryDeveloperLoop(t *testing.T) {
	q, projectID, runID := testQueryService(t)
	projects, err := q.Projects()
	if err != nil || len(projects) != 1 || !projects[0].GraphReady {
		t.Fatalf("projects=%+v err=%v", projects, err)
	}
	summary, err := q.Summary(projectID, "")
	if err != nil || summary.RunID != runID || summary.ServiceCount != 2 || summary.EdgeCount != 2 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	services, err := q.Services(projectID, "")
	if err != nil || len(services) != 2 || services[0].Name != "catalog" || services[1].InboundEdges != 0 {
		t.Fatalf("services=%+v err=%v", services, err)
	}
	service, err := q.Service(projectID, "", "catalog")
	if err != nil || len(service.InboundEdges) != 1 || len(service.OutboundEdges) != 1 {
		t.Fatalf("service=%+v err=%v", service, err)
	}
	deps, err := q.Dependencies(projectID, "", "catalog", "inbound")
	if err != nil || len(deps.Edges) != 1 || deps.Edges[0].From != "checkout" {
		t.Fatalf("deps=%+v err=%v", deps, err)
	}
	impact, err := q.Impact(projectID, "", "catalog", 3)
	if err != nil || impact.Stats.ServiceCount != 2 {
		t.Fatalf("impact=%+v err=%v", impact, err)
	}
	search, err := q.Search(projectID, "", "product", 20)
	if err != nil || len(search.Results) < 2 {
		t.Fatalf("search=%+v err=%v", search, err)
	}
	fieldSearch, err := q.Search(projectID, "", "experimentGroup", 20)
	if err != nil || len(fieldSearch.Results) != 1 || fieldSearch.Results[0].Kind != "http_endpoint" {
		t.Fatalf("field search=%+v err=%v", fieldSearch, err)
	}
	contracts, err := q.Contracts(projectID, runID, "catalog")
	if err != nil || len(contracts.Fields) != 1 || contracts.Fields[0].Name != "experimentGroup" || contracts.Fields[0].Required {
		t.Fatalf("contracts=%+v err=%v", contracts, err)
	}
	nextRun, err := q.store.CreateRun(projectID, store.RunManifest{Status: store.RunCompleted, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	nextGraph := &archgraph.ArchGraph{RunID: nextRun.ID, Services: []*archgraph.ServiceNode{{Name: "catalog", Known: true, HTTPRoutes: []archgraph.EntitySummary{{ID: "get-product", Name: "GET /products/{id}", Details: map[string]any{"request_fields": []any{map[string]any{"name": "experimentGroup", "type": "string", "required": true}, map[string]any{"name": "market", "type": "string", "required": false}}}}}}}}
	nextData, _ := json.Marshal(nextGraph)
	if err := os.WriteFile(filepath.Join(q.store.RunDir(projectID, nextRun.ID), "graph.json"), nextData, 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := q.CompareContracts(projectID, runID, nextRun.ID, "catalog")
	if err != nil || len(diff.Changes) != 2 {
		t.Fatalf("contract diff=%+v err=%v", diff, err)
	}
	if diff.Changes[0].Compatibility != "potentially_breaking" || diff.Changes[1].Compatibility != "compatible" {
		t.Fatalf("classification=%+v", diff.Changes)
	}
}

func TestResolveProjectRequiresSelectionWhenAmbiguous(t *testing.T) {
	q, _, _ := testQueryService(t)
	if _, err := q.store.CreateProject(store.Project{Name: "Second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ResolveProject(""); err == nil {
		t.Fatal("expected ambiguous-project error")
	}
}

func TestQueryRejectsInvalidDirectionAndMissingService(t *testing.T) {
	q, projectID, _ := testQueryService(t)
	if _, err := q.Dependencies(projectID, "", "catalog", "sideways"); err == nil {
		t.Fatal("expected invalid direction")
	}
	if _, err := q.Service(projectID, "", "missing"); err == nil {
		t.Fatal("expected missing service")
	}
}
