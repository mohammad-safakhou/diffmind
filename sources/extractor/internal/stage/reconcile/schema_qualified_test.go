package reconcile

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func dbOp(id, table, op string) model.Dependency {
	return model.Dependency{BaseEntity: model.BaseEntity{
		ID:      id,
		Type:    "db_operation",
		Name:    op + " " + table,
		Details: map[string]any{"table": table, "operation": op},
	}}
}

func countByResource(deps []model.Dependency) map[string]int {
	m := map[string]int{}
	for _, d := range deps {
		m[dataResource(d.BaseEntity)]++
	}
	return m
}

// C4: a bare resource merges with a single schema-qualified one (unambiguous).
func TestSchemaQualifiedUniqueMerges(t *testing.T) {
	out := DedupeDependencies([]model.Dependency{
		dbOp("1", "orders", "read"),
		dbOp("2", "public.orders", "read"),
	})
	if len(out) != 1 {
		t.Fatalf("expected bare+single-qualified to merge into 1, got %d: %+v", len(out), out)
	}
	if got := dataResource(out[0].BaseEntity); got != "public.order" {
		t.Errorf("merged resource should be qualified public.order, got %q", got)
	}
}

// C4: two distinct schemas are ambiguous → no over-merge; the bare one stays
// separate and the qualified ones stay distinct.
func TestSchemaQualifiedAmbiguousDoesNotMerge(t *testing.T) {
	out := DedupeDependencies([]model.Dependency{
		dbOp("1", "orders", "read"),
		dbOp("2", "public.orders", "read"),
		dbOp("3", "audit.orders", "read"),
	})
	res := countByResource(out)
	if res["public.order"] != 1 || res["audit.order"] != 1 || res["order"] != 1 {
		t.Fatalf("expected distinct order/public.order/audit.order, got %+v", res)
	}
}

// C4: different schemas for the same base must never merge (the over-merge guard).
func TestSchemaQualifiedDistinctSchemasStaySeparate(t *testing.T) {
	out := DedupeDependencies([]model.Dependency{
		dbOp("1", "public.orders", "read"),
		dbOp("2", "audit.orders", "read"),
	})
	if len(out) != 2 {
		t.Fatalf("public.orders and audit.orders must not merge; got %d: %+v", len(out), out)
	}
}

// All-bare resources are unchanged (the common case is not affected).
func TestSchemaQualifiedAllBareUnchanged(t *testing.T) {
	out := DedupeDependencies([]model.Dependency{
		dbOp("1", "orders", "read"),
		dbOp("2", "orders", "read"),
	})
	if len(out) != 1 {
		t.Fatalf("identical bare resources should dedup to 1, got %d", len(out))
	}
	if got := dataResource(out[0].BaseEntity); got != "order" {
		t.Errorf("resource should stay bare 'order', got %q", got)
	}
}

func TestSplitSchemaQualified(t *testing.T) {
	cases := []struct{ in, schema, base string }{
		{"public.orders", "public", "orders"},
		{"orders", "", "orders"},
		{"db.public.orders", "db.public", "orders"},
		{".orders", "", ".orders"},
		{"orders.", "", "orders."},
	}
	for _, c := range cases {
		s, b := splitSchemaQualified(c.in)
		if s != c.schema || b != c.base {
			t.Errorf("splitSchemaQualified(%q) = (%q,%q), want (%q,%q)", c.in, s, b, c.schema, c.base)
		}
	}
}
