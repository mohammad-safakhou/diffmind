package util

import "testing"

func TestContentHash_Deterministic(t *testing.T) {
	h1 := ContentHash("service", "order-service")
	h2 := ContentHash("service", "order-service")
	if h1 != h2 {
		t.Errorf("expected same hash, got %s and %s", h1, h2)
	}
}

func TestContentHash_OrderIndependent(t *testing.T) {
	h1 := ContentHash("a", "b", "c")
	h2 := ContentHash("c", "a", "b")
	if h1 != h2 {
		t.Errorf("expected same hash for different order, got %s and %s", h1, h2)
	}
}

func TestContentHash_CaseInsensitive(t *testing.T) {
	h1 := ContentHash("Service", "Order-Service")
	h2 := ContentHash("service", "order-service")
	if h1 != h2 {
		t.Errorf("expected same hash for different case, got %s and %s", h1, h2)
	}
}

func TestContentHash_SkipsEmpty(t *testing.T) {
	h1 := ContentHash("a", "", "b")
	h2 := ContentHash("a", "b")
	if h1 != h2 {
		t.Errorf("expected same hash when skipping empty, got %s and %s", h1, h2)
	}
}

func TestContentHash_DifferentInputs(t *testing.T) {
	h1 := ContentHash("service", "order")
	h2 := ContentHash("service", "billing")
	if h1 == h2 {
		t.Error("expected different hashes for different inputs")
	}
}
