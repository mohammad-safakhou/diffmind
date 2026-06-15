package pipeline

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// TestSeedsToEntitiesPreservesIdentityAndGatesConfidence verifies the
// --skip-detail conversion path: verified seeds become model entities directly
// (no LLM enrichment), their identity-bearing details survive untouched, and a
// seed below the confidence floor is diverted to unresolved rather than emitted.
func TestSeedsToEntitiesPreservesIdentityAndGatesConfidence(t *testing.T) {
	o := &orchestrator{repoPath: "repo", cfg: config.Default()} // MinConfidence 0.70

	httpObj := objectives.Objective{ID: "exposure.http_route", Kind: model.KindExposure, Type: "http_route"}
	dbObj := objectives.Objective{ID: "dependency.db_operation", Kind: model.KindDependency, Type: "db_operation"}

	jobs := []detailJob{
		{Objective: httpObj, Seed: llmEntity{
			Type: "http_route", Name: "GET /orders", Confidence: 0.95,
			Details:   map[string]any{"method": "GET", "path": "/orders"},
			Locations: []llmLocation{{File: "src/OrderController.java", StartLine: 10, EndLine: 12}},
		}},
		{Objective: dbObj, Seed: llmEntity{
			Type: "db_operation", Name: "read orders", Confidence: 0.90,
			Details:   map[string]any{"table": "orders", "operation": "read"},
			Locations: []llmLocation{{File: "src/OrderRepo.java", StartLine: 5, EndLine: 5}},
		}},
		// Below the 0.70 floor → must be diverted to unresolved, not emitted.
		{Objective: dbObj, Seed: llmEntity{
			Type: "db_operation", Name: "write payments", Confidence: 0.10,
			Details:   map[string]any{"table": "payments", "operation": "write"},
			Locations: []llmLocation{{File: "src/PayRepo.java", StartLine: 7, EndLine: 7}},
		}},
	}

	var unresolved []model.UnresolvedItem
	exposures, dependencies := o.seedsToEntities(jobs, &unresolved)

	if len(exposures) != 1 {
		t.Fatalf("exposures = %d, want 1", len(exposures))
	}
	if len(dependencies) != 1 {
		t.Fatalf("dependencies = %d, want 1", len(dependencies))
	}
	if len(unresolved) != 1 {
		t.Fatalf("unresolved = %d, want 1 (low-confidence seed)", len(unresolved))
	}
	if rc := unresolved[0].ReasonCode; rc != "low_confidence" {
		t.Fatalf("unresolved reason = %q, want low_confidence", rc)
	}

	// Identity-bearing details survive the conversion with no detail LLM pass.
	if m, _ := exposures[0].Details["method"].(string); m != "GET" {
		t.Fatalf("exposure method = %q, want GET", m)
	}
	if p, _ := exposures[0].Details["path"].(string); p != "/orders" {
		t.Fatalf("exposure path = %q, want /orders", p)
	}
	if tbl, _ := dependencies[0].Details["table"].(string); tbl != "orders" {
		t.Fatalf("dependency table = %q, want orders", tbl)
	}
}
