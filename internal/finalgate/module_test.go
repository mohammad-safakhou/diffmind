package finalgate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"diffmind/internal/audit"
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
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "a", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
		{"id": "b", "path": "/products/mapper/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{{"id": "q1", "question": "what services?", "endpoint": "/products/docs/g1"}}})
	mustWriteJSON(t, graphIndex, map[string]any{"graphs": []map[string]any{{"graph_id": "g1", "tenant_id": "default", "fingerprint": "abc"}}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--graph-index", graphIndex, "--out-report", report, "--out-decision", decision})
	if err != nil {
		data, _ := os.ReadFile(report)
		t.Fatalf("attest failed: %v report=%s", err, string(data))
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
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "a", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{{"id": "q1", "question": "what services?", "endpoint": "/products/docs/g1"}}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--out-report", report, "--out-decision", decision, "--signers", "engineering,platform"})
	if err == nil {
		t.Fatalf("expected attest failure when security signer is missing")
	}
}

func TestAttestFailsWhenQuestionCatalogEndpointNotCoveredByTemplates(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "docs", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{
		{"id": "q1", "question": "docs", "endpoint": "/products/docs/{graph_id}"},
		{"id": "q2", "question": "governance", "endpoint": "/products/governance/{graph_id}"},
	}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--out-report", report, "--out-decision", decision})
	if err == nil {
		t.Fatalf("expected attest failure when catalog endpoints are not fully covered by templates")
	}
}

func TestAttestFailsWhenRuntimeQualityGateFails(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "runtime_reconciliation": map[string]any{"runtime_quality_passed": false}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "docs", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{
		{"id": "q1", "question": "docs", "endpoint": "/products/docs/{graph_id}"},
	}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--out-report", report, "--out-decision", decision})
	if err == nil {
		t.Fatalf("expected attest failure when runtime quality gate fails in slo artifact")
	}
}

func TestCloseoutGeneratesAllReports(t *testing.T) {
	tmp := t.TempDir()

	qualityGate := filepath.Join(tmp, "quality", "gate_result.json")
	sloGate := filepath.Join(tmp, "ops", "slo_report.json")
	templates := filepath.Join(tmp, "docs", "m15_query_templates.json")
	catalog := filepath.Join(tmp, "docs", "m17_question_catalog.json")
	graphIndex := filepath.Join(tmp, "graph", "index.json")
	readiness := filepath.Join(tmp, "final", "readiness_report.json")
	decision := filepath.Join(tmp, "final", "gate_decision.md")
	milestones := filepath.Join(tmp, "final", "milestone_closure_report.json")
	benchmark := filepath.Join(tmp, "final", "benchmark_evidence_report.json")
	security := filepath.Join(tmp, "final", "security_validation_report.json")
	opsReport := filepath.Join(tmp, "final", "operations_drill_report.json")

	qualityReport := filepath.Join(tmp, "quality", "report.json")
	corpusReport := filepath.Join(tmp, "corpus", "report.json")
	perfPolicy := filepath.Join(tmp, "docs", "graph_performance_baseline.md")
	auditRoot := filepath.Join(tmp, "auditroot")
	drillSource := filepath.Join(tmp, "source")
	drillOut := filepath.Join(tmp, "drills")

	mustWriteJSON(t, qualityGate, map[string]any{"passed": true})
	mustWriteJSON(t, sloGate, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{
		"templates": []map[string]any{
			{"id": "docs-service", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
		},
	})
	mustWriteJSON(t, catalog, map[string]any{
		"questions": []map[string]any{
			{"id": "q1", "question": "services", "endpoint": "/products/docs/{graph_id}"},
		},
	})
	mustWriteJSON(t, graphIndex, map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "tenant_id": "default", "fingerprint": "fp1"},
		},
	})
	mustWriteJSON(t, qualityReport, map[string]any{"metrics": map[string]any{"pass_rate": 0.99}})
	mustWriteJSON(t, corpusReport, map[string]any{"status": "passed"})
	if err := os.MkdirAll(filepath.Dir(perfPolicy), 0o755); err != nil {
		t.Fatalf("mkdir perf policy dir: %v", err)
	}
	if err := os.WriteFile(perfPolicy, []byte("# perf baseline\n"), 0o644); err != nil {
		t.Fatalf("write perf policy: %v", err)
	}
	if err := audit.AppendEvent(auditRoot, audit.Event{
		Timestamp: time.Now().UTC(),
		Action:    "query_graph",
		TenantID:  "default",
		Principal: "tester",
		Method:    "GET",
		Path:      "/graphs/g1",
		Decision:  "allow",
	}); err != nil {
		t.Fatalf("append audit event: %v", err)
	}
	if err := os.MkdirAll(drillSource, 0o755); err != nil {
		t.Fatalf("mkdir drill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(drillSource, "sample.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write drill source file: %v", err)
	}

	args := []string{
		"closeout",
		"--quality-gate", qualityGate,
		"--slo", sloGate,
		"--templates", templates,
		"--catalog", catalog,
		"--graph-index", graphIndex,
		"--out-report", readiness,
		"--out-decision", decision,
		"--out-milestones", milestones,
		"--out-benchmark", benchmark,
		"--out-security", security,
		"--out-ops", opsReport,
		"--quality-report", qualityReport,
		"--corpus-report", corpusReport,
		"--performance-policy", perfPolicy,
		"--audit-root", auditRoot,
		"--drill-source", drillSource,
		"--drill-out", drillOut,
		"--signers", "engineering,platform,security",
	}
	if err := Run(context.Background(), args); err != nil {
		t.Fatalf("closeout failed: %v", err)
	}

	required := []string{readiness, decision, milestones, benchmark, security, opsReport}
	for _, path := range required {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected closeout artifact %s: %v", path, err)
		}
	}

	var milestonePayload struct {
		OverallPassed bool `json:"overall_passed"`
	}
	data, err := os.ReadFile(milestones)
	if err != nil {
		t.Fatalf("read milestone report: %v", err)
	}
	if err := json.Unmarshal(data, &milestonePayload); err != nil {
		t.Fatalf("decode milestone report: %v", err)
	}
	if !milestonePayload.OverallPassed {
		t.Fatalf("expected milestone closure report overall_passed=true")
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
