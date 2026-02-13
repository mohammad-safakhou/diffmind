package consolidation

import (
	"testing"

	"diffmind/internal/facts"
)

func TestConsolidateMergesDuplicatesAndKeepsStableIDs(t *testing.T) {
	ev1 := facts.NewEvidence("snap1", "api/routes.js", 10, 1, 10, 20, "app.get('/health')")
	ev2 := facts.NewEvidence("snap1", "api/routes.js", 14, 1, 14, 20, "app.get('/health')")

	f1 := facts.NewFact("Endpoint", map[string]any{
		"direction": "inbound", "method": "GET", "path": "/health", "framework": "express-like",
	}, []string{ev1.ID}, 0.7, facts.Provenance{AnalyzerID: "a", AnalyzerVersion: "1", Deterministic: true})
	f2 := facts.NewFact("Endpoint", map[string]any{
		"direction": "inbound", "method": "GET", "path": "/health", "framework": "express-like",
	}, []string{ev2.ID}, 0.9, facts.Provenance{AnalyzerID: "a", AnalyzerVersion: "1", Deterministic: true})

	bundle := facts.Bundle{Evidence: []facts.Evidence{ev1, ev2}, Facts: []facts.Fact{f1, f2}}

	intelA, reportA, err := consolidate(bundle, "snap1")
	if err != nil {
		t.Fatalf("consolidate returned error: %v", err)
	}
	intelB, _, err := consolidate(bundle, "snap1")
	if err != nil {
		t.Fatalf("consolidate returned error: %v", err)
	}

	if reportA.OutputEntities != 1 {
		t.Fatalf("expected 1 output entity, got %d", reportA.OutputEntities)
	}
	if reportA.DuplicatesMerged != 1 {
		t.Fatalf("expected 1 merged duplicate, got %d", reportA.DuplicatesMerged)
	}
	if len(intelA.Entities) != 1 {
		t.Fatalf("expected one entity")
	}
	if intelA.Entities[0].ID != intelB.Entities[0].ID {
		t.Fatalf("entity IDs must be stable across runs")
	}
	if len(intelA.Entities[0].EvidenceIDs) != 2 {
		t.Fatalf("expected merged evidence ids")
	}
	if intelA.Entities[0].Confidence != 0.9 {
		t.Fatalf("expected max confidence 0.9, got %f", intelA.Entities[0].Confidence)
	}
}
