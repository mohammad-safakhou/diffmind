package graphschema

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeGraphSemanticsDefaults(t *testing.T) {
	g := Graph{
		GraphID:     "g1",
		GeneratedAt: time.Now().UTC(),
		Mode:        "single",
		Nodes: []Node{
			{ID: "svc:a", Type: "service", Label: "A", Confidence: 1},
			{ID: "ep:a", Type: "endpoint", Label: "GET /x", Confidence: 0.9},
			{ID: "c1", Type: "conflict", Label: "conflict", Confidence: 0.6},
		},
		Edges: []Edge{
			{ID: "e1", Type: "service_calls_endpoint", SourceID: "svc:a", TargetID: "ep:a", Confidence: 0.9, EvidenceRefs: []EvidenceRef{{EvidenceID: "ev1"}}},
		},
		Stats: GraphStats{
			NodeCount: 3,
			EdgeCount: 1,
			ByNode:    map[string]int{"service": 1, "endpoint": 1, "conflict": 1},
			ByEdge:    map[string]int{"service_calls_endpoint": 1},
		},
		Meta: GraphMeta{TenantID: "default"},
	}

	NormalizeGraphSemantics(&g)

	if g.Meta.OntologyVersion != OntologyVersionV2 {
		t.Fatalf("expected ontology version %q, got %q", OntologyVersionV2, g.Meta.OntologyVersion)
	}

	ep := g.Nodes[1]
	if ep.Section != SectionExposure || ep.Class != "exposure_http_endpoint" || ep.VerificationState != VerificationVerified {
		t.Fatalf("unexpected endpoint semantics: section=%s class=%s verification=%s", ep.Section, ep.Class, ep.VerificationState)
	}

	conflict := g.Nodes[2]
	if conflict.VerificationState != VerificationDisputed {
		t.Fatalf("expected conflict verification state %q, got %q", VerificationDisputed, conflict.VerificationState)
	}

	edge := g.Edges[0]
	if edge.Section != SectionExposure || edge.Class != "dependency_api_call" || edge.VerificationState != VerificationVerified {
		t.Fatalf("unexpected edge semantics: section=%s class=%s verification=%s", edge.Section, edge.Class, edge.VerificationState)
	}
}

func TestNormalizeGraphSemanticsRespectsExplicitValues(t *testing.T) {
	g := Graph{
		GraphID:     "g1",
		GeneratedAt: time.Now().UTC(),
		Mode:        "single",
		Nodes: []Node{
			{
				ID:                "n1",
				Type:              "endpoint",
				Label:             "GET /x",
				Section:           SectionLogic,
				Class:             "custom_class",
				VerificationState: VerificationNeedsReview,
				Confidence:        1,
			},
		},
		Edges: []Edge{
			{
				ID:                "e1",
				Type:              "service_calls_endpoint",
				SourceID:          "n1",
				TargetID:          "n1",
				Section:           SectionDependencies,
				Class:             "custom_relation",
				VerificationState: VerificationNeedsReview,
				Confidence:        1,
				EvidenceRefs:      []EvidenceRef{{EvidenceID: "ev"}},
			},
		},
		Stats: GraphStats{
			NodeCount: 1,
			EdgeCount: 1,
			ByNode:    map[string]int{"endpoint": 1},
			ByEdge:    map[string]int{"service_calls_endpoint": 1},
		},
		Meta: GraphMeta{TenantID: "default", OntologyVersion: OntologyVersionV2},
	}
	NormalizeGraphSemantics(&g)

	if g.Nodes[0].Section != SectionLogic || g.Nodes[0].Class != "custom_class" || g.Nodes[0].VerificationState != VerificationNeedsReview {
		t.Fatalf("explicit node semantics were not preserved")
	}
	if g.Edges[0].Section != SectionDependencies || g.Edges[0].Class != "custom_relation" || g.Edges[0].VerificationState != VerificationNeedsReview {
		t.Fatalf("explicit edge semantics were not preserved")
	}
}

func TestValidateGraphRejectsInvalidOntologyFields(t *testing.T) {
	g := Graph{
		GraphID:     "g1",
		GeneratedAt: time.Now().UTC(),
		Mode:        "single",
		Nodes: []Node{
			{ID: "n1", Type: "service", Label: "S", Section: "bad", Class: "x", Confidence: 1},
		},
		Edges: []Edge{},
		Stats: GraphStats{
			NodeCount: 1,
			EdgeCount: 0,
			ByNode:    map[string]int{"service": 1},
			ByEdge:    map[string]int{},
		},
		Meta: GraphMeta{TenantID: "default", OntologyVersion: "v99"},
	}
	err := ValidateGraph(g)
	if err == nil {
		t.Fatalf("expected validation failure")
	}
	if !strings.Contains(err.Error(), "ontology_version") {
		t.Fatalf("expected ontology_version validation error, got %v", err)
	}
}
