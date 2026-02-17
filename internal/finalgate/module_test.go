package finalgate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAttestPassesWhenAllGatesPass(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	graphIndex := filepath.Join(tmp, "index.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, slo, map[string]any{"passed": true})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{{"id": "a", "query": map[string]any{"explain": true}}, {"id": "b", "query": map[string]any{"explain": true}}}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{{"id": "q1", "question": "what services?", "endpoint": "/products/docs/g1"}}})
	mustWriteJSON(t, graphIndex, map[string]any{"graphs": []map[string]any{{"graph_id": "g1", "tenant_id": "default", "fingerprint": "abc"}}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--graph-index", graphIndex, "--out-report", report, "--out-decision", decision})
	if err != nil {
		t.Fatalf("attest failed: %v", err)
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatalf("missing readiness report: %v", err)
	}
	if _, err := os.Stat(decision); err != nil {
		t.Fatalf("missing decision doc: %v", err)
	}
}

func TestAttestFailsWhenMissingSignature(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, slo, map[string]any{"passed": true})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{{"id": "a", "query": map[string]any{"explain": true}}}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{{"id": "q1", "question": "what services?", "endpoint": "/products/docs/g1"}}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--out-report", report, "--out-decision", decision, "--signers", "engineering,platform"})
	if err == nil {
		t.Fatalf("expected attest failure when security signer is missing")
	}
}

func mustWriteJSON(t *testing.T, path string, payload map[string]any) {
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
