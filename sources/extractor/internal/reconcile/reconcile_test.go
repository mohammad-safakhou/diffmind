package reconcile

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func TestDedupeExposuresKeepsHighestConfidenceAndMergesFields(t *testing.T) {
	a := model.Exposure{BaseEntity: model.BaseEntity{
		ID: "x", Type: "http_route", Name: "GET /", Confidence: 0.8,
		Summary: "original", Details: map[string]any{"method": "GET"},
	}}
	b := model.Exposure{BaseEntity: model.BaseEntity{
		ID: "x", Type: "http_route", Name: "GET /", Confidence: 0.9,
		Summary: "better", Details: map[string]any{"path": "/"},
	}}
	out := DedupeExposures([]model.Exposure{a, b})
	if len(out) != 1 {
		t.Fatalf("expected 1 entity after dedup, got %d", len(out))
	}
	if out[0].Summary != "better" {
		t.Fatalf("higher confidence summary should win, got %q", out[0].Summary)
	}
	// Both details should be merged.
	if out[0].Details["method"] != "GET" || out[0].Details["path"] != "/" {
		t.Fatalf("expected merged details, got %+v", out[0].Details)
	}
}

// TestDedupeDependenciesCollapsesDbOpsByResourceAndOperation verifies that db
// operations on the same (table, operation-kind) collapse to one row even when
// they came from different files / methods / sources, while a different
// operation on the same table stays separate.
func TestDedupeDependenciesCollapsesDbOpsByResourceAndOperation(t *testing.T) {
	mk := func(id, name, file, table, op string) model.Dependency {
		return model.Dependency{BaseEntity: model.BaseEntity{
			ID: id, Type: "db_operation", Name: name, Confidence: 0.9,
			Locations: []model.Location{{File: file, StartLine: 1}},
			Details:   map[string]any{"table": table, "operation": op},
		}}
	}
	in := []model.Dependency{
		mk("1", "OrderRepository.findById", "a/Svc.java", "orders", "read"),   // LLM, file A
		mk("2", "read orders", "b/Repo.java", "orders", "SELECT"),             // det floor, file B, verb variant
		mk("3", "OrderRepository.save", "a/Svc.java", "orders", "write"),      // distinct op, same table
		mk("4", "find customers", "c/Cust.java", "customers", "read"),         // distinct table
	}
	out := DedupeDependencies(in)
	if len(out) != 3 {
		names := make([]string, len(out))
		for i, d := range out {
			names[i] = d.Name
		}
		t.Fatalf("expected 3 deduped db ops (orders:read, orders:write, customers:read), got %d: %v", len(out), names)
	}
}

func TestFilterConnectionsDropsOrphans(t *testing.T) {
	exp := []model.Exposure{{BaseEntity: model.BaseEntity{ID: "e1"}}}
	dep := []model.Dependency{{BaseEntity: model.BaseEntity{ID: "d1"}}}
	conns := []model.Connection{
		{ID: "c1", FromExposureID: "e1", ToDependencyID: "d1"},
		{ID: "c2", FromExposureID: "e1", ToDependencyID: "UNKNOWN"},
		{ID: "c3", FromExposureID: "UNKNOWN", ToDependencyID: "d1"},
	}
	kept, dropped := FilterConnections(conns, exp, dep)
	if len(kept) != 1 || kept[0].ID != "c1" {
		t.Fatalf("expected only c1 to survive, got %+v", kept)
	}
	if len(dropped) != 2 {
		t.Fatalf("expected 2 orphan unresolved items, got %d", len(dropped))
	}
}

func TestDedupeUnresolvedCollapsesByKey(t *testing.T) {
	items := []model.UnresolvedItem{
		{Kind: model.KindExposure, Type: "http_route", Name: "GET /", ReasonCode: "low_confidence"},
		{Kind: model.KindExposure, Type: "http_route", Name: "GET /", ReasonCode: "low_confidence"},
		{Kind: model.KindDependency, Type: "db_operation", Name: "users", ReasonCode: "no_source_location"},
	}
	out := DedupeUnresolved(items)
	if len(out) != 2 {
		t.Fatalf("expected 2 unique unresolved items, got %d", len(out))
	}
}

func TestDedupeWarningsKeepsOrderAndDropsDups(t *testing.T) {
	in := []string{"a", "b", "a", " ", "c"}
	out := DedupeWarnings(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 unique non-empty warnings, got %+v", out)
	}
	if out[0] != "a" || out[1] != "b" || out[2] != "c" {
		t.Fatalf("unexpected order: %+v", out)
	}
}
