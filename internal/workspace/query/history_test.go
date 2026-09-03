package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func saveGraph(t *testing.T, q *Service, pid, rid string, g *archgraph.ArchGraph) {
	t.Helper()
	body, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(q.store.RunDir(pid, rid), "graph.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDependencyPathDeterministicTiesAndSearchBudgets(t *testing.T) {
	q, pid, rid := testQueryService(t)
	ctx := context.Background()
	g := &archgraph.ArchGraph{RunID: rid, Services: []*archgraph.ServiceNode{{Name: "start"}, {Name: "a"}, {Name: "b"}, {Name: "end"}}, Edges: []*archgraph.GraphEdge{
		{From: "start", To: "b", Type: "http"}, {From: "b", To: "end", Type: "http"},
		{From: "start", To: "a", Type: "http", Confidence: 0.9}, {From: "start", To: "a", Type: "http", Confidence: 0.8}, {From: "a", To: "end", Type: "http"},
	}}
	saveGraph(t, q, pid, rid, g)
	a, err := q.FindPath(ctx, pid, rid, "start", "end", 3)
	if err != nil || !reflect.DeepEqual(a.Nodes, []string{"start", "a", "end"}) {
		t.Fatalf("diamond path: %+v %v", a, err)
	}
	slices.Reverse(g.Edges)
	saveGraph(t, q, pid, rid, g)
	b, err := q.FindPath(ctx, pid, rid, "start", "end", 3)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("unstable path: %+v %+v %v", a, b, err)
	}
	g.Edges = nil
	for i := 0; i < 10001; i++ {
		name := fmt.Sprintf("node-%05d", i)
		g.Services = append(g.Services, &archgraph.ServiceNode{Name: name})
		g.Edges = append(g.Edges, &archgraph.GraphEdge{From: "start", To: name, Type: "http"})
	}
	saveGraph(t, q, pid, rid, g)
	limited, err := q.FindPath(ctx, pid, rid, "start", "end", 3)
	if err != nil || limited.Status != "limited" || limited.Visited != 10000 {
		t.Fatalf("node budget: %+v %v", limited, err)
	}
	g.Edges = make([]*archgraph.GraphEdge, 100001)
	for i := range g.Edges {
		g.Edges[i] = &archgraph.GraphEdge{From: "start", To: "end", Type: "http"}
	}
	saveGraph(t, q, pid, rid, g)
	limited, err = q.FindPath(ctx, pid, rid, "start", "end", 3)
	if err != nil || limited.Status != "limited" || len(limited.Edges) != 0 {
		t.Fatalf("edge budget: %+v %v", limited, err)
	}
}

func TestObjectTraceStableTruncationAndCancellation(t *testing.T) {
	q, pid, rid := testQueryService(t)
	ctx := context.Background()
	_, g, err := q.Load(pid, rid)
	if err != nil {
		t.Fatal(err)
	}
	svc := g.Services[1]
	svc.Connections = nil
	g.Edges = nil
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("dep-%03d", i)
		svc.Connections = append(svc.Connections, archgraph.ConnectionSummary{FlowID: id, FromID: "entry", ToID: id})
		g.Edges = append(g.Edges, &archgraph.GraphEdge{From: "checkout", To: "catalog", Type: "http", Details: []archgraph.EntitySummary{{ID: id}}})
	}
	saveGraph(t, q, pid, rid, g)
	a, err := q.TraceObject(ctx, pid, rid, "checkout", "entry")
	if err != nil || !a.Truncated || a.ConnectionCount != 205 || a.EdgeCount != 205 || len(a.Connections) != 200 || len(a.RelatedEdges) != 200 {
		t.Fatalf("truncation: %+v %v", a, err)
	}
	slices.Reverse(svc.Connections)
	slices.Reverse(g.Edges)
	saveGraph(t, q, pid, rid, g)
	b, err := q.TraceObject(ctx, pid, rid, "checkout", "entry")
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("unstable truncation: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := q.TraceObject(cancelled, pid, rid, "checkout", "entry"); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestHistoryMissingArtifactsNullAndLegacyGraph(t *testing.T) {
	q, pid, rid := testQueryService(t)
	_, g, err := q.Load(pid, rid)
	if err != nil {
		t.Fatal(err)
	}
	g.RunID = ""
	saveGraph(t, q, pid, rid, g)
	_, loaded, err := q.Load(pid, rid)
	if err != nil || loaded.RunID != rid {
		t.Fatalf("legacy ID: %+v %v", loaded, err)
	}
	filename := filepath.Join(q.store.RunDir(pid, rid), "graph.json")
	if err := os.WriteFile(filename, []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.Load(pid, rid); err == nil {
		t.Fatal("null graph accepted")
	}
	// Presence discovery is intentionally cheaper than validating each snapshot.
	runs, err := q.GraphRuns(pid, 0, 0)
	if err != nil || !runs.Runs[0].GraphAvailable {
		t.Fatalf("presence listing: %+v %v", runs, err)
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	runs, err = q.GraphRuns(pid, 0, 100)
	if err != nil || runs.Runs[0].GraphAvailable {
		t.Fatalf("missing artifact listing: %+v %v", runs, err)
	}
	if _, err := q.CompareGraphs(context.Background(), pid, rid, rid, 0, 100); !errors.Is(err, ErrNoCompletedGraph) {
		t.Fatalf("missing graph comparison: %v", err)
	}
}

func TestGraphHistoryComparisonAndPagination(t *testing.T) {
	q, pid, oldID := testQueryService(t)
	old, g, err := q.Load(pid, oldID)
	if err != nil {
		t.Fatal(err)
	}
	old.Options = map[string]any{"knowledge_pack_set_digest": "sha256:old"}
	old.Repos = []store.RunRepoRef{{RepoID: "catalog", DiffMindRunID: "analysis-1"}}
	if err := q.store.SaveRun(pid, *old); err != nil {
		t.Fatal(err)
	}
	newRun, err := q.store.CreateRun(pid, store.RunManifest{Status: store.RunCompleted, StartedAt: old.StartedAt.Add(time.Hour), Repos: []store.RunRepoRef{{RepoID: "catalog", DiffMindRunID: "analysis-2"}}, Options: map[string]any{"knowledge_pack_set_digest": "sha256:new"}})
	if err != nil {
		t.Fatal(err)
	}
	g.RunID = newRun.ID
	g.Services[0].Team = "new-team"
	g.Edges = g.Edges[1:]
	saveGraph(t, q, pid, newRun.ID, g)
	list, err := q.GraphRuns(pid, 0, 1)
	if err != nil || len(list.Runs) != 1 || list.Runs[0].ID != newRun.ID || list.NextOffset == nil {
		t.Fatalf("runs: %+v %v", list, err)
	}
	page, err := q.GraphRuns(pid, *list.NextOffset, 1)
	if err != nil || page.Runs[0].ID != oldID || page.NextOffset != nil {
		t.Fatalf("runs page: %+v %v", page, err)
	}
	result, err := q.CompareGraphs(context.Background(), pid, oldID, newRun.ID, 0, 1)
	if err != nil || result.Total != 2 || result.Counts["removed"] != 1 || result.Counts["modified"] != 1 || result.NextOffset == nil {
		t.Fatalf("comparison: %+v %v", result, err)
	}
	if !reflect.DeepEqual(result.RepositoryArtifactsChanged, []string{"catalog"}) || len(result.Notes) != 3 {
		t.Fatalf("missing provenance context: %+v", result)
	}
	last, err := q.CompareGraphs(context.Background(), pid, oldID, newRun.ID, *result.NextOffset, 1)
	if err != nil || len(last.Changes) != 1 || last.NextOffset != nil {
		t.Fatalf("page: %+v %v", last, err)
	}
	if result.Changes[0].Key == last.Changes[0].Key {
		t.Fatal("pagination repeated change")
	}
	empty, err := q.CompareGraphs(context.Background(), pid, oldID, oldID, 999, 1)
	if err != nil || empty.Total != 0 || len(empty.Changes) != 0 {
		t.Fatalf("same run: %+v %v", empty, err)
	}
	_, original, err := q.Load(pid, oldID)
	if err != nil || original.Services[0].Team != "commerce" || len(original.Edges) != 2 {
		t.Fatal("historical snapshot modified")
	}
}

func TestHistoryRejectsInvalidSelectionsAndCorruption(t *testing.T) {
	q, pid, rid := testQueryService(t)
	for _, tc := range []struct {
		from, to      string
		offset, limit int
	}{{"", rid, 0, 1}, {rid, "", 0, 1}, {rid, rid, -1, 1}, {rid, rid, 0, 501}, {"../outside", rid, 0, 1}, {"missing", rid, 0, 1}} {
		if _, err := q.CompareGraphs(context.Background(), pid, tc.from, tc.to, tc.offset, tc.limit); err == nil {
			t.Fatalf("invalid selection accepted: %+v", tc)
		}
	}
	other, err := q.store.CreateProject(store.Project{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CompareGraphs(context.Background(), other.ID, rid, rid, 0, 1); err == nil {
		t.Fatal("cross project run accepted")
	}
	failed, err := q.store.CreateRun(pid, store.RunManifest{Status: store.RunFailed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CompareGraphs(context.Background(), pid, rid, failed.ID, 0, 1); err == nil {
		t.Fatal("failed run accepted")
	}
	for _, body := range []string{"not-json", `{"run_id":"different","services":[]}`} {
		if err := os.WriteFile(filepath.Join(q.store.RunDir(pid, rid), "graph.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := q.CompareGraphs(context.Background(), pid, rid, rid, 0, 1); err == nil {
			t.Fatal("corruption accepted")
		}
	}
}

func TestDependencyPathsCyclesDirectionAndLimits(t *testing.T) {
	q, pid, rid := testQueryService(t)
	ctx := context.Background()
	out, err := q.FindPath(ctx, pid, rid, "checkout", "db:products", 2)
	if err != nil || out.Status != "found" || !reflect.DeepEqual(out.Nodes, []string{"checkout", "catalog", "db:products"}) || len(out.Edges) != 2 {
		t.Fatalf("path: %+v %v", out, err)
	}
	out, err = q.FindPath(ctx, pid, rid, "checkout", "db:products", 1)
	if err != nil || out.Status != "limited" {
		t.Fatalf("depth: %+v %v", out, err)
	}
	out, err = q.FindPath(ctx, pid, rid, "db:products", "checkout", 20)
	if err != nil || out.Status != "not_found" {
		t.Fatalf("reverse: %+v %v", out, err)
	}
	out, err = q.FindPath(ctx, pid, rid, "checkout", "checkout", 1)
	if err != nil || len(out.Edges) != 0 || out.Status != "found" {
		t.Fatalf("self: %+v %v", out, err)
	}
	_, g, _ := q.Load(pid, rid)
	g.Edges = append(g.Edges, &archgraph.GraphEdge{From: "catalog", To: "checkout", Type: "http"})
	saveGraph(t, q, pid, rid, g)
	out, err = q.FindPath(ctx, pid, rid, "catalog", "db:products", 20)
	if err != nil || len(out.Edges) != 1 {
		t.Fatalf("cycle: %+v %v", out, err)
	}
	if _, err := q.FindPath(ctx, pid, rid, "missing", "catalog", 1); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("unknown node: %v", err)
	}
	if _, err := q.FindPath(ctx, pid, rid, "checkout", "catalog", 21); err == nil {
		t.Fatal("unbounded depth accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := q.FindPath(cancelled, pid, rid, "checkout", "catalog", 1); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
}

func TestObjectTraceExactIDsEvidenceAndNoInventedFlow(t *testing.T) {
	q, pid, rid := testQueryService(t)
	ctx := context.Background()
	_, g, _ := q.Load(pid, rid)
	svc := g.Services[1]
	svc.HTTPRoutes = []archgraph.EntitySummary{{ID: "entry", Name: "POST /checkout"}}
	svc.Dependencies[0].Details = map[string]any{"evidence": []any{map[string]any{"file": "checkout.go", "start_line": 9}}}
	svc.Connections = []archgraph.ConnectionSummary{{FlowID: "flow", FromID: "entry", ToID: "call-catalog", Summary: "calls catalog", Nodes: []any{map[string]any{"id": "step"}}, Edges: []any{map[string]any{"from": "a", "to": "b"}}}}
	g.Edges[0].Details = []archgraph.EntitySummary{svc.Dependencies[0], {ID: "unrelated", Name: "also catalog"}}
	g.Edges = append(g.Edges, &archgraph.GraphEdge{From: "checkout", To: "catalog", Type: "rpc", Label: "calls catalog", Details: []archgraph.EntitySummary{{ID: "different"}}})
	saveGraph(t, q, pid, rid, g)
	for _, id := range []string{"entry", "flow", "call-catalog"} {
		trace, err := q.TraceObject(ctx, pid, rid, "checkout", id)
		if err != nil || trace.Status != "local_flow_available" || len(trace.Connections) != 1 || len(trace.RelatedEdges) != 1 || len(trace.RelatedEdges[0].Details) != 1 {
			t.Fatalf("trace %s: %+v %v", id, trace, err)
		}
		if trace.RelatedEdges[0].Details[0].Details["evidence"] == nil {
			t.Fatal("lost evidence")
		}
	}
	if _, err := q.TraceObject(ctx, pid, rid, "checkout", "catalog"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("name matched as ID: %v", err)
	}
	trace, err := q.TraceObject(ctx, pid, rid, "catalog", "get-product")
	if err != nil || trace.Status != "partial" || len(trace.RelatedEdges) != 0 {
		t.Fatalf("unrelated adjacency leaked: %+v %v", trace, err)
	}
	if _, err := q.TraceObject(ctx, pid, rid, "missing", "entry"); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("service: %v", err)
	}
	if _, err := q.TraceObject(ctx, pid, rid, "checkout", ""); err == nil {
		t.Fatal("empty selector accepted")
	}
}
