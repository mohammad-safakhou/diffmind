package archgraph

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func comparisonFixture() *ArchGraph {
	return &ArchGraph{RunID: "old", Services: []*ServiceNode{
		{Name: "gateway", Known: true, Team: "core", RepoPath: "/old/checkout", Dependencies: []EntitySummary{{ID: "generated-1", Kind: "outbound_http", Name: "catalog", Details: map[string]any{"source_locations": []any{map[string]any{"file": "main.go", "start_line": 4}}, "method": "GET"}}}},
		{Name: "catalog", Known: true, HTTPRoutes: []EntitySummary{{ID: "route", Name: "GET /items", Kind: "http_route"}}},
	}, Edges: []*GraphEdge{{From: "gateway", To: "catalog", Type: "http", Label: "GET", Confidence: 0.9}}, ResourceNodes: []*ResourceNode{{ID: "q", GraphID: "queue:q", Name: "orders", Kind: "queue"}}, ExternalNodes: []*ExternalNode{{Name: "external.test", Kind: "service"}}}
}

func cloneComparison(g *ArchGraph) *ArchGraph {
	body, _ := json.Marshal(g)
	var clone ArchGraph
	_ = json.Unmarshal(body, &clone)
	return &clone
}

func TestComparisonIgnoresPresentationAndGeneratedIDs(t *testing.T) {
	a := comparisonFixture()
	b := cloneComparison(a)
	b.RunID = "new"
	b.Services[0].RepoPath = "/another/checkout"
	b.Services[0].DiffMindFreshness = "stale"
	b.Services[0].Dependencies[0].ID = "regenerated"
	b.Services[0].EntrypointCount = 42
	b.Edges[0].FromPort = "new-port"
	b.Services[0], b.Services[1] = b.Services[1], b.Services[0]
	before, _ := json.Marshal(a)
	changes, err := Compare(context.Background(), a, b)
	if err != nil || len(changes) != 0 {
		t.Fatalf("noise produced changes: %+v %v", changes, err)
	}
	after, _ := json.Marshal(a)
	if string(before) != string(after) {
		t.Fatal("comparison mutated source graph")
	}
}

func TestComparisonExactKindsAndEvidenceChanges(t *testing.T) {
	a := comparisonFixture()
	b := cloneComparison(a)
	b.Services[0].Team = "platform"
	b.Services[0].Dependencies[0].Details["source_locations"] = []any{map[string]any{"file": "main.go", "start_line": 8}}
	b.Services[1].HTTPRoutes[0].Name = "GET /products"
	b.Edges[0].Confidence = 0.7
	b.ResourceNodes[0].OwnerService = "catalog"
	b.ExternalNodes = nil
	b.Services = append(b.Services, &ServiceNode{Name: "billing", Known: true})
	changes, err := Compare(context.Background(), a, b)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, change := range changes {
		got = append(got, change.Kind+":"+change.Change)
	}
	want := []string{"external:removed", "object:removed", "object:added", "object:modified", "relationship:modified", "resource:modified", "service:added", "service:modified"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes: %v", got)
	}
	encoded, _ := json.Marshal(changes)
	if !strings.Contains(string(encoded), "start_line") {
		t.Fatal("lost evidence")
	}
	reverse, err := Compare(context.Background(), b, a)
	if err != nil || len(reverse) != len(changes) {
		t.Fatalf("reverse: %+v %v", reverse, err)
	}
}

func TestComparisonDuplicateOccurrencesAndReordering(t *testing.T) {
	a := comparisonFixture()
	a.Services[0].Dependencies = append(a.Services[0].Dependencies, EntitySummary{Name: "catalog", Kind: "outbound_http", Details: map[string]any{"method": "POST"}})
	b := cloneComparison(a)
	b.Services[0].Dependencies[0], b.Services[0].Dependencies[1] = b.Services[0].Dependencies[1], b.Services[0].Dependencies[0]
	changes, err := Compare(context.Background(), a, b)
	if err != nil || len(changes) != 0 {
		t.Fatalf("multiset reordered: %+v %v", changes, err)
	}
	b.Services[0].Dependencies = b.Services[0].Dependencies[:1]
	changes, err = Compare(context.Background(), a, b)
	if err != nil || len(changes) != 1 || changes[0].Change != "modified" {
		t.Fatalf("occurrence removal lost: %+v %v", changes, err)
	}
}

func TestComparisonCancellationAndInvalidDuplicateServices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compare(ctx, comparisonFixture(), comparisonFixture()); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
	g := comparisonFixture()
	g.Services = append(g.Services, g.Services[0])
	if _, err := Compare(context.Background(), g, g); err == nil {
		t.Fatal("ambiguous duplicate accepted")
	}
	for _, duplicate := range []*ArchGraph{
		{ResourceNodes: []*ResourceNode{{GraphID: "queue:q"}, {GraphID: "queue:q"}}},
		{QueueNodes: []*QueueNode{{ID: "q"}, {ID: "q"}}},
		{ExternalNodes: []*ExternalNode{{Name: "external.test"}, {Name: "external.test"}}},
	} {
		if _, err := Compare(context.Background(), duplicate, duplicate); err == nil {
			t.Fatal("duplicate resource/external identity accepted")
		}
	}
}

func TestComparisonFlowsLegacyResourcesAndEvidenceOrder(t *testing.T) {
	a := comparisonFixture()
	a.Services[0].Connections = []ConnectionSummary{{FromName: "GET /shop", ToName: "catalog", FlowID: "generated", Reachability: "conditional", Nodes: []any{map[string]any{"step": "first"}, map[string]any{"step": "second"}}}}
	a.QueueNodes = []*QueueNode{{ID: "legacy", Name: "orders", FIFO: true}}
	b := cloneComparison(a)
	b.Services[0].Connections[0].FlowID = "regenerated"
	changes, err := Compare(context.Background(), a, b)
	if err != nil || len(changes) != 0 {
		t.Fatalf("flow identity noise: %+v %v", changes, err)
	}
	nodes := b.Services[0].Connections[0].Nodes.([]any)
	nodes[0], nodes[1] = nodes[1], nodes[0]
	b.QueueNodes[0].FIFO = false
	changes, err = Compare(context.Background(), a, b)
	if err != nil || len(changes) != 2 || changes[0].Kind != "flow" || changes[1].Kind != "resource" {
		t.Fatalf("flow/resource facts lost: %+v %v", changes, err)
	}
	empty, err := Compare(context.Background(), nil, &ArchGraph{Services: []*ServiceNode{nil}, Edges: []*GraphEdge{nil}})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty/nil entries: %+v %v", empty, err)
	}
}
