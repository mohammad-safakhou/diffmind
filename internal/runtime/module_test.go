package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPlanWritesDisabledContract(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "runtime", "plan.json")
	if err := Run(context.Background(), []string{"plan", "--out", out}); err != nil {
		t.Fatalf("runtime plan failed: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if payload["enabled"] != false {
		t.Fatalf("expected enabled=false, got %v", payload["enabled"])
	}
	if payload["publish_blocking"] != false {
		t.Fatalf("expected publish_blocking=false, got %v", payload["publish_blocking"])
	}
}

func TestRunReconcileMatchesClaims(t *testing.T) {
	tmp := t.TempDir()
	claimsPath := filepath.Join(tmp, "claims.json")
	observationsPath := filepath.Join(tmp, "observations.json")
	out := filepath.Join(tmp, "result.json")

	claims := []map[string]any{
		{"graph_id": "g1", "edge_id": "e1"},
		{"graph_id": "g1", "node_id": "n1"},
	}
	observations := []map[string]any{
		{"source_system": "gateway", "signal_type": "http", "attributes": map[string]any{"edge_id": "e1"}},
		{"source_system": "broker", "signal_type": "queue", "attributes": map[string]any{"node_id": "n1", "contradicts": "true"}},
		{"source_system": "gateway", "signal_type": "http", "attributes": map[string]any{"edge_id": "e404"}},
	}
	mustWriteJSON(t, claimsPath, claims)
	mustWriteJSON(t, observationsPath, observations)

	if err := Run(context.Background(), []string{"reconcile", "--graph-id", "g1", "--claims", claimsPath, "--observations", observationsPath, "--out", out}); err != nil {
		t.Fatalf("runtime reconcile failed: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read reconcile result: %v", err)
	}
	var payload struct {
		GraphID      string   `json:"graph_id"`
		Confirmed    []string `json:"confirmed"`
		Contradicted []string `json:"contradicted"`
		Unmapped     []string `json:"runtime_only_unmapped"`
		NeedsReview  []string `json:"needs_review"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode reconcile result: %v", err)
	}
	if payload.GraphID != "g1" {
		t.Fatalf("expected graph_id g1, got %q", payload.GraphID)
	}
	if len(payload.Confirmed) != 1 || payload.Confirmed[0] != "edge:e1" {
		t.Fatalf("expected confirmed edge:e1, got %+v", payload.Confirmed)
	}
	if len(payload.Contradicted) != 1 || payload.Contradicted[0] != "node:n1" {
		t.Fatalf("expected contradicted node:n1, got %+v", payload.Contradicted)
	}
	if len(payload.Unmapped) != 1 || payload.Unmapped[0] != "observation:2" {
		t.Fatalf("expected one unmapped observation, got %+v", payload.Unmapped)
	}
	if len(payload.NeedsReview) != 0 {
		t.Fatalf("expected no needs_review claims, got %+v", payload.NeedsReview)
	}
}

func mustWriteJSON(t *testing.T, path string, payload any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
