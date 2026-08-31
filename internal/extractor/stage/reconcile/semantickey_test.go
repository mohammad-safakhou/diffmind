package reconcile

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

// TestSemanticKeyMatchesDedupGrouping is the regression lock that keeps the
// exported SemanticKey from drifting away from the actual dedup behaviour: two
// items that Dedupe* collapses into one MUST share a SemanticKey, and two items
// it keeps separate MUST NOT. The eval harness relies on this equivalence.
func TestSemanticKeyMatchesDedupGrouping(t *testing.T) {
	dep := func(typ, name, platform, op string, details map[string]any) model.Dependency {
		return model.Dependency{BaseEntity: model.BaseEntity{
			ID: util(name, platform, op), Type: typ, Name: name, Platform: platform, Operation: op,
			Confidence: 1, Details: details,
			Locations: []model.Location{{File: "a.go", StartLine: 1, EndLine: 2}},
		}}
	}

	t.Run("plural and singular table collapse", func(t *testing.T) {
		a := dep("db_operation", "read orders", "postgres", "select", map[string]any{"table": "orders", "operation": "read"})
		b := dep("db_operation", "find order", "postgres", "find", map[string]any{"table": "order", "operation": "select"})
		if SemanticKey(a.BaseEntity) != SemanticKey(b.BaseEntity) {
			t.Fatalf("expected same key: %q vs %q", SemanticKey(a.BaseEntity), SemanticKey(b.BaseEntity))
		}
		got := DedupeDependencies([]model.Dependency{a, b})
		if len(got) != 1 {
			t.Fatalf("expected dedup to 1, got %d: %+v", len(got), got)
		}
	})

	t.Run("distinct datastores stay separate", func(t *testing.T) {
		a := dep("db_operation", "read orders", "postgres", "read", map[string]any{"table": "orders", "operation": "read"})
		b := dep("db_operation", "read orders", "dynamodb", "read", map[string]any{"table": "orders", "operation": "read"})
		if SemanticKey(a.BaseEntity) == SemanticKey(b.BaseEntity) {
			t.Fatalf("expected distinct keys for postgres vs dynamodb, both %q", SemanticKey(a.BaseEntity))
		}
		got := DedupeDependencies([]model.Dependency{a, b})
		if len(got) != 2 {
			t.Fatalf("expected 2 distinct datastores preserved, got %d", len(got))
		}
	})

	t.Run("read vs write are separate facts", func(t *testing.T) {
		a := dep("db_operation", "read orders", "postgres", "select", map[string]any{"table": "orders", "operation": "read"})
		b := dep("db_operation", "write orders", "postgres", "insert", map[string]any{"table": "orders", "operation": "write"})
		if SemanticKey(a.BaseEntity) == SemanticKey(b.BaseEntity) {
			t.Fatalf("read and write must differ, both %q", SemanticKey(a.BaseEntity))
		}
	})

	t.Run("loose drops file, strict keeps it for generic", func(t *testing.T) {
		a := model.BaseEntity{Type: "outbound_http", Name: "GET /x", Platform: "http", Locations: []model.Location{{File: "a.go"}}}
		b := model.BaseEntity{Type: "outbound_http", Name: "GET /x", Platform: "http", Locations: []model.Location{{File: "b.go"}}}
		if SemanticKey(a) == SemanticKey(b) {
			t.Fatalf("strict key should differ by file")
		}
		if SemanticKeyLoose(a) != SemanticKeyLoose(b) {
			t.Fatalf("loose key should ignore file: %q vs %q", SemanticKeyLoose(a), SemanticKeyLoose(b))
		}
	})
}

// util builds a deterministic-but-unique id so the byID dedup phase never
// collapses two semantically-distinct rows on a shared empty id.
func util(parts ...string) string {
	s := ""
	for _, p := range parts {
		s += p + "|"
	}
	return s
}
