package archfile

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// Phase 4: the resolved InstanceRef from the deterministic client pass is the
// authority for resource grouping. Two db ops on different tables that go
// through one client (one resolved InstanceRef) must derive the SAME resource
// node — "same client ⇒ same instance ⇒ one node" — and its platform/instance
// come from the InstanceRef, not the per-op table/class detail.
func TestDerivedResourceGroupsByInstanceRef(t *testing.T) {
	ref := &model.InstanceRef{Kind: "postgres", LogicalName: "orders_db", Database: "orders_db"}
	opA := model.BaseEntity{
		Type:        "db_operation",
		Name:        "write orders",
		Details:     map[string]any{"table": "orders", "operation": "write"},
		InstanceRef: ref,
	}
	opB := model.BaseEntity{
		Type:        "db_operation",
		Name:        "read customers",
		Details:     map[string]any{"table": "customers", "operation": "read"},
		InstanceRef: ref,
	}

	ra := derivedResource(opA)
	rb := derivedResource(opB)

	if ra.ID != rb.ID {
		t.Fatalf("ops sharing one InstanceRef must group under one node: %q vs %q", ra.ID, rb.ID)
	}
	if ra.Platform != "postgres" {
		t.Errorf("platform should come from InstanceRef.Kind, got %q", ra.Platform)
	}
	if ra.Instance != "orders_db" {
		t.Errorf("instance should come from InstanceRef, got %q", ra.Instance)
	}
	// The per-op table is the resource detail, never the grouping instance.
	if ra.Instance == "orders" || rb.Instance == "customers" {
		t.Errorf("per-op table leaked into the grouping instance: a=%q b=%q", ra.Instance, rb.Instance)
	}
}

// Without an InstanceRef (hand-authored facts), derivedResource is unchanged —
// it falls back to the detail-derived instance. This guards the no-op promise.
func TestDerivedResourceWithoutInstanceRefUnchanged(t *testing.T) {
	op := model.BaseEntity{
		Type:    "db_operation",
		Name:    "write orders",
		Details: map[string]any{"database": "orders_db", "table": "orders", "operation": "write"},
	}
	r := derivedResource(op)
	if r.Instance != "orders_db" {
		t.Errorf("without InstanceRef the authored database detail drives grouping, got %q", r.Instance)
	}
}
