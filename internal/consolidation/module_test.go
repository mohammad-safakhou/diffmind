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

func TestConsolidateCountsDependencyFamilies(t *testing.T) {
	ev := facts.NewEvidence("snap1", "package.json", 1, 1, 1, 20, "deps")
	dep := facts.NewFact("Dependency", map[string]any{
		"ecosystem": "npm", "name": "axios", "version": "^1.7.0", "source_file": "package.json",
	}, []string{ev.ID}, 0.9, facts.Provenance{AnalyzerID: "a", AnalyzerVersion: "1", Deterministic: true})
	own := facts.NewFact("OwnershipRule", map[string]any{
		"pattern": "/package.json", "owner": "@frontend-team", "source_file": "CODEOWNERS",
	}, []string{ev.ID}, 0.9, facts.Provenance{AnalyzerID: "a", AnalyzerVersion: "1", Deterministic: true})
	risk := facts.NewFact("DependencyRisk", map[string]any{
		"ecosystem": "npm", "name": "axios", "version": "^1.7.0", "risk_type": "version_drift",
	}, []string{ev.ID}, 0.8, facts.Provenance{AnalyzerID: "a", AnalyzerVersion: "1", Deterministic: true})

	intel, report, err := consolidate(facts.Bundle{
		Evidence: []facts.Evidence{ev},
		Facts:    []facts.Fact{dep, own, risk},
	}, "snap1")
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if len(intel.Entities) != 3 {
		t.Fatalf("expected 3 entities, got %d", len(intel.Entities))
	}
	if report.Dependencies != 1 || report.OwnershipRules != 1 || report.DependencyRisks != 1 {
		t.Fatalf("unexpected dependency report counters: %+v", report)
	}
}

func TestConsolidateCreatesConflictEntityForContradictingAttributes(t *testing.T) {
	ev1 := facts.NewEvidence("snap1", "main.go", 10, 1, 10, 30, "timeout 1000")
	ev2 := facts.NewEvidence("snap1", "main.go", 11, 1, 11, 30, "timeout 2000")
	f1 := facts.NewFact("ExternalCall", map[string]any{
		"protocol": "http", "method": "GET", "target": "https://svc", "library": "go-net-http", "timeout_ms": 1000,
	}, []string{ev1.ID}, 0.9, facts.Provenance{AnalyzerID: "a", AnalyzerVersion: "1", Deterministic: true})
	f2 := facts.NewFact("ExternalCall", map[string]any{
		"protocol": "http", "method": "GET", "target": "https://svc", "library": "go-net-http", "timeout_ms": 2000,
	}, []string{ev2.ID}, 0.9, facts.Provenance{AnalyzerID: "a", AnalyzerVersion: "1", Deterministic: true})

	intel, report, err := consolidate(facts.Bundle{
		Evidence: []facts.Evidence{ev1, ev2},
		Facts:    []facts.Fact{f1, f2},
	}, "snap1")
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if report.Conflicts == 0 {
		t.Fatalf("expected non-zero conflicts in report: %+v", report)
	}
	foundConflict := false
	for _, e := range intel.Entities {
		if e.Type == "Conflict" {
			foundConflict = true
			if e.Attributes["status"] != "unresolved" {
				t.Fatalf("expected unresolved conflict status")
			}
			if _, ok := e.Attributes["conflict_keys"]; !ok {
				t.Fatalf("expected conflict_keys in conflict entity")
			}
		}
	}
	if !foundConflict {
		t.Fatalf("expected conflict entity in output")
	}
}
