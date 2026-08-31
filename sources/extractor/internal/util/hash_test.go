package util

import "testing"

func TestStableID(t *testing.T) {
	a := StableID("Exposure", "Endpoint", "Orders")
	b := StableID(" exposure ", "endpoint", "orders")
	if a != b {
		t.Fatalf("expected normalized deterministic id, got %s != %s", a, b)
	}
}
