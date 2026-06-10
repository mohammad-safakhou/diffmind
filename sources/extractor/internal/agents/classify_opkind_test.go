package agents

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// C5: the emitted operation_kind for db/cache ops must be the canonical
// read/write fold (not a raw verb like delete/insert/saveAll), while the raw
// verb is preserved in details["operation"].
func TestEnrichEntityGroupingCanonicalOperationKind(t *testing.T) {
	cases := []struct {
		rawOp    string
		wantKind string
	}{
		{"SELECT", "read"},
		{"INSERT then UPDATE", "write"},
		{"DELETE (bulk hard delete, no WHERE clause)", "write"},
		{"saveAll", "write"},
		{"findByCampaignIdInAndTargetDate", "read"},
		{"UPDATE", "write"},
	}
	for _, c := range cases {
		b := model.BaseEntity{
			Type:    "db_operation",
			Name:    "op orders",
			Details: map[string]any{"table": "orders", "operation": c.rawOp},
		}
		EnrichEntityGrouping(&b)
		if b.OperationKind != c.wantKind {
			t.Errorf("op %q: OperationKind=%q, want %q", c.rawOp, b.OperationKind, c.wantKind)
		}
		if got := b.Details["operation_kind"]; got != c.wantKind {
			t.Errorf("op %q: details.operation_kind=%v, want %q", c.rawOp, got, c.wantKind)
		}
		// raw verb preserved
		if got := b.Details["operation"]; got != c.rawOp {
			t.Errorf("op %q: raw operation not preserved, got %v", c.rawOp, got)
		}
	}
}

// Cache eviction verbs are not read/write and must pass through (not be folded).
func TestEnrichEntityGroupingCacheEvictPassesThrough(t *testing.T) {
	b := model.BaseEntity{Type: "cache_operation", Name: "evict sessions", Details: map[string]any{"cache": "sessions", "operation": "evict"}}
	EnrichEntityGrouping(&b)
	if b.OperationKind != "evict" {
		t.Errorf("cache evict should pass through, got %q", b.OperationKind)
	}
}
