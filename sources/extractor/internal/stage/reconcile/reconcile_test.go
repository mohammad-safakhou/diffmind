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
	mk := func(id, name, file, table, op, platform, instance string) model.Dependency {
		return model.Dependency{BaseEntity: model.BaseEntity{
			ID: id, Type: "db_operation", Name: name, Confidence: 0.9,
			Platform: platform, Instance: instance,
			Locations: []model.Location{{File: file, StartLine: 1}},
			Details:   map[string]any{"table": table, "operation": op},
		}}
	}
	in := []model.Dependency{
		// Same logical (orders, read) reported four ways: different files,
		// names, verb variants, singular/plural, and noisy platform/instance —
		// but all on the ONE postgres datastore. Must collapse to one row.
		mk("1", "OrderRepository.findById", "a/Svc.java", "orders", "read", "postgres", "spring.datasource.url"),
		mk("2", "read orders", "b/Repo.java", "order", "SELECT", "database", "unknown"), // generic platform merges in
		mk("3", "OrderRepository.findByStatus", "c/X.java", "orders", "find", "postgres", "instance dump…"),
		// Distinct op on same table.
		mk("4", "OrderRepository.save", "a/Svc.java", "orders", "DELETE", "database", "unknown"),
		// Distinct table.
		mk("5", "find customers", "c/Cust.java", "customers", "read", "database", ""),
		// MULTI-DATASTORE: the same table name read from two genuinely
		// different engines must stay as two distinct dependencies.
		mk("6", "read events from pg", "p/Pg.java", "events", "read", "postgres", ""),
		mk("7", "read events from dynamo", "d/Dyn.java", "events", "read", "dynamodb", ""),
	}
	out := DedupeDependencies(in)
	// orders:read, orders:write, customers:read, events:read@postgres, events:read@dynamodb
	if len(out) != 5 {
		rows := make([]string, len(out))
		for i, d := range out {
			rows[i] = d.Details["table"].(string) + ":" + d.Details["operation"].(string) + "@" + d.Platform
		}
		t.Fatalf("expected 5 deduped db ops, got %d: %v", len(out), rows)
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
