package graphschema

import (
	"strings"
	"testing"
	"time"
)

func TestValidateGraphAcceptsValidGraph(t *testing.T) {
	graph := Graph{
		GraphID:     "g1",
		GeneratedAt: time.Now().UTC(),
		Mode:        "multi",
		Nodes: []Node{
			{ID: "svc:a", Type: "service", Label: "Service A", Confidence: 1.0},
			{ID: "ep:a:get:/users", Type: "endpoint", Label: "GET /users", ServiceID: "service-a", Confidence: 0.9},
		},
		Edges: []Edge{
			{
				ID:         "e1",
				Type:       "service_calls_endpoint",
				SourceID:   "svc:a",
				TargetID:   "ep:a:get:/users",
				Confidence: 0.92,
				EvidenceRefs: []EvidenceRef{
					{EvidenceID: "ev-1"},
				},
			},
		},
		Stats: GraphStats{
			NodeCount: 2,
			EdgeCount: 1,
			ByNode:    map[string]int{"service": 1, "endpoint": 1},
			ByEdge:    map[string]int{"service_calls_endpoint": 1},
		},
		Meta: GraphMeta{TenantID: "default"},
	}
	if err := ValidateGraph(graph); err != nil {
		t.Fatalf("expected valid graph, got error: %v", err)
	}
}

func TestValidateGraphRejectsDanglingEdge(t *testing.T) {
	graph := Graph{
		GraphID:     "g1",
		GeneratedAt: time.Now().UTC(),
		Mode:        "single",
		Nodes: []Node{
			{ID: "svc:a", Type: "service", Label: "Service A", Confidence: 1.0},
		},
		Edges: []Edge{
			{ID: "e1", Type: "service_calls_service", SourceID: "svc:a", TargetID: "svc:b", Confidence: 0.8},
		},
		Stats: GraphStats{
			NodeCount: 1,
			EdgeCount: 1,
			ByNode:    map[string]int{"service": 1},
			ByEdge:    map[string]int{"service_calls_service": 1},
		},
		Meta: GraphMeta{TenantID: "default"},
	}
	err := ValidateGraph(graph)
	if err == nil {
		t.Fatalf("expected dangling edge error")
	}
	if !strings.Contains(err.Error(), "target_id") {
		t.Fatalf("expected target_id error, got: %v", err)
	}
}

func TestValidateGraphRejectsStatsMismatch(t *testing.T) {
	graph := Graph{
		GraphID:     "g1",
		GeneratedAt: time.Now().UTC(),
		Mode:        "single",
		Nodes: []Node{
			{ID: "svc:a", Type: "service", Label: "Service A", Confidence: 1.0},
			{ID: "q:orders", Type: "queue", Label: "orders", Confidence: 1.0},
		},
		Edges: []Edge{
			{ID: "e1", Type: "service_publishes_queue", SourceID: "svc:a", TargetID: "q:orders", Confidence: 0.9},
		},
		Stats: GraphStats{
			NodeCount: 2,
			EdgeCount: 1,
			ByNode:    map[string]int{"service": 2}, // invalid on purpose
			ByEdge:    map[string]int{"service_publishes_queue": 1},
		},
		Meta: GraphMeta{TenantID: "default"},
	}
	err := ValidateGraph(graph)
	if err == nil {
		t.Fatalf("expected stats mismatch error")
	}
	if !strings.Contains(err.Error(), "stats.by_node") {
		t.Fatalf("expected stats.by_node error, got: %v", err)
	}
}
