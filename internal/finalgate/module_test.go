package finalgate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"diffmind/internal/audit"
)

func TestAttestPassesWhenAllGatesPass(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	mergeQuality := filepath.Join(tmp, "merge_quality_report.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	graphIndex := filepath.Join(tmp, "index.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{"passed": true})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "a", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
		{"id": "b", "path": "/products/mapper/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{{"id": "q1", "question": "what services?", "endpoint": "/products/docs/g1"}}})
	mustWriteJSON(t, graphIndex, map[string]any{"graphs": []map[string]any{{"graph_id": "g1", "tenant_id": "default", "fingerprint": "abc"}}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--merge-quality", mergeQuality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--graph-index", graphIndex, "--out-report", report, "--out-decision", decision})
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
	mergeQuality := filepath.Join(tmp, "merge_quality_report.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{"passed": true})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "a", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{{"id": "q1", "question": "what services?", "endpoint": "/products/docs/g1"}}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--merge-quality", mergeQuality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--out-report", report, "--out-decision", decision, "--signers", "engineering,platform"})
	if err == nil {
		t.Fatalf("expected attest failure when security signer is missing")
	}
}

func TestAttestFailsWhenQuestionCatalogEndpointNotCoveredByTemplates(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	mergeQuality := filepath.Join(tmp, "merge_quality_report.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{"passed": true})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "docs", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{
		{"id": "q1", "question": "docs", "endpoint": "/products/docs/{graph_id}"},
		{"id": "q2", "question": "governance", "endpoint": "/products/governance/{graph_id}"},
	}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--merge-quality", mergeQuality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--out-report", report, "--out-decision", decision})
	if err == nil {
		t.Fatalf("expected attest failure when catalog endpoints are not fully covered by templates")
	}
}

func TestAttestFailsWhenRuntimeQualityGateFails(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	mergeQuality := filepath.Join(tmp, "merge_quality_report.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{"passed": true})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "runtime_reconciliation": map[string]any{"runtime_quality_passed": false}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "docs", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{
		{"id": "q1", "question": "docs", "endpoint": "/products/docs/{graph_id}"},
	}})

	err := Run(context.Background(), []string{"attest", "--quality-gate", quality, "--merge-quality", mergeQuality, "--slo", slo, "--templates", templates, "--catalog", catalog, "--out-report", report, "--out-decision", decision})
	if err == nil {
		t.Fatalf("expected attest failure when runtime quality gate fails in slo artifact")
	}
}

func TestAttestFailsWhenMergeQualityGateFails(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	mergeQuality := filepath.Join(tmp, "merge_quality_report.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{"passed": false})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "docs", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{
		{"id": "q1", "question": "docs", "endpoint": "/products/docs/{graph_id}"},
	}})

	err := Run(context.Background(), []string{
		"attest",
		"--quality-gate", quality,
		"--merge-quality", mergeQuality,
		"--slo", slo,
		"--templates", templates,
		"--catalog", catalog,
		"--out-report", report,
		"--out-decision", decision,
	})
	if err == nil {
		t.Fatalf("expected attest failure when merge quality gate fails")
	}
}

func TestAttestFailsWhenMergeQualityReportMissing(t *testing.T) {
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
	}})

	err := Run(context.Background(), []string{
		"attest",
		"--quality-gate", quality,
		"--merge-quality", filepath.Join(tmp, "missing-merge-quality.json"),
		"--slo", slo,
		"--templates", templates,
		"--catalog", catalog,
		"--out-report", report,
		"--out-decision", decision,
	})
	if err == nil {
		t.Fatalf("expected attest failure when merge quality report is missing")
	}
}

func TestAttestFailsWhenMergeQualityBenchmarkMissingWithExpectedLinks(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	mergeQuality := filepath.Join(tmp, "merge_quality_report.json")
	expectLinks := filepath.Join(tmp, "expected_links.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{"passed": true})
	mustWriteJSON(t, expectLinks, map[string]any{"service_calls_service": []map[string]any{
		{"source_service_id": "a", "source_repo_path": "/repos/a", "target_service_id": "b", "target_repo_path": "/repos/b"},
	}})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "docs", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{
		{"id": "q1", "question": "docs", "endpoint": "/products/docs/{graph_id}"},
	}})

	err := Run(context.Background(), []string{
		"attest",
		"--quality-gate", quality,
		"--merge-quality", mergeQuality,
		"--merge-quality-expect-links", expectLinks,
		"--slo", slo,
		"--templates", templates,
		"--catalog", catalog,
		"--out-report", report,
		"--out-decision", decision,
	})
	if err == nil {
		t.Fatalf("expected attest failure when merge quality benchmark is missing with expected links")
	}
}

func TestAttestPassesWhenMergeQualityBenchmarkPassesWithExpectedLinks(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	mergeQuality := filepath.Join(tmp, "merge_quality_report.json")
	expectLinks := filepath.Join(tmp, "expected_links.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	graphIndex := filepath.Join(tmp, "index.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{
		"passed": true,
		"benchmark": map[string]any{
			"passed": true,
		},
	})
	mustWriteJSON(t, expectLinks, map[string]any{"service_calls_service": []map[string]any{
		{"source_service_id": "a", "source_repo_path": "/repos/a", "target_service_id": "b", "target_repo_path": "/repos/b"},
	}})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "a", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{{"id": "q1", "question": "what services?", "endpoint": "/products/docs/g1"}}})
	mustWriteJSON(t, graphIndex, map[string]any{"graphs": []map[string]any{{"graph_id": "g1", "tenant_id": "default", "fingerprint": "abc"}}})

	err := Run(context.Background(), []string{
		"attest",
		"--quality-gate", quality,
		"--merge-quality", mergeQuality,
		"--merge-quality-expect-links", expectLinks,
		"--slo", slo,
		"--templates", templates,
		"--catalog", catalog,
		"--graph-index", graphIndex,
		"--out-report", report,
		"--out-decision", decision,
	})
	if err != nil {
		t.Fatalf("attest failed: %v", err)
	}
}

func TestAttestBenchmarkFailureIncludesMismatchDetails(t *testing.T) {
	tmp := t.TempDir()
	quality := filepath.Join(tmp, "quality.gate.json")
	mergeQuality := filepath.Join(tmp, "merge_quality_report.json")
	expectLinks := filepath.Join(tmp, "expected_links.json")
	slo := filepath.Join(tmp, "slo.json")
	templates := filepath.Join(tmp, "templates.json")
	catalog := filepath.Join(tmp, "catalog.json")
	report := filepath.Join(tmp, "out", "readiness.json")
	decision := filepath.Join(tmp, "out", "decision.md")

	mustWriteJSON(t, quality, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{
		"passed": true,
		"benchmark": map[string]any{
			"passed": false,
			"service_calls_service": map[string]any{
				"false_positive_samples": []string{"checkout@/repos/checkout -> orders@/repos/orders"},
				"false_negative_samples": []string{"billing@/repos/billing -> payments@/repos/payments"},
			},
			"canonical_service_aliases": map[string]any{
				"false_positive_samples": []string{"orders-svc@/repos/orders -> canonical:orders[prod]"},
				"false_negative_samples": []string{"payments-svc@/repos/payments -> canonical:payments[prod]"},
			},
		},
	})
	mustWriteJSON(t, expectLinks, map[string]any{"service_calls_service": []map[string]any{
		{"source_service_id": "a", "source_repo_path": "/repos/a", "target_service_id": "b", "target_repo_path": "/repos/b"},
	}})
	mustWriteJSON(t, slo, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "docs", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{
		{"id": "q1", "question": "docs", "endpoint": "/products/docs/{graph_id}"},
	}})

	err := Run(context.Background(), []string{
		"attest",
		"--quality-gate", quality,
		"--merge-quality", mergeQuality,
		"--merge-quality-expect-links", expectLinks,
		"--slo", slo,
		"--templates", templates,
		"--catalog", catalog,
		"--out-report", report,
		"--out-decision", decision,
	})
	if err == nil {
		t.Fatalf("expected attest failure when merge quality benchmark fails")
	}

	data, readErr := os.ReadFile(report)
	if readErr != nil {
		t.Fatalf("read readiness report: %v", readErr)
	}
	var readiness readinessReport
	if err := json.Unmarshal(data, &readiness); err != nil {
		t.Fatalf("decode readiness report: %v", err)
	}
	var m8Detail string
	for _, c := range readiness.Checks {
		if c.Name == "m8_merge_quality_gate" {
			m8Detail = c.Detail
			break
		}
	}
	if m8Detail == "" {
		t.Fatalf("expected m8_merge_quality_gate check detail")
	}
	if !strings.Contains(m8Detail, "linkage") || !strings.Contains(m8Detail, "identity") {
		t.Fatalf("expected benchmark mismatch detail to include linkage and identity samples, got %q", m8Detail)
	}
}

func TestCloseoutGeneratesAllReports(t *testing.T) {
	tmp := t.TempDir()

	qualityGate := filepath.Join(tmp, "quality", "gate_result.json")
	mergeQuality := filepath.Join(tmp, "graph", "merge_quality_report.json")
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
	closureRules := filepath.Join(tmp, "final", "closure_rules_report.json")

	qualityReport := filepath.Join(tmp, "quality", "report.json")
	corpusReport := filepath.Join(tmp, "corpus", "report.json")
	perfPolicy := filepath.Join(tmp, "docs", "graph_performance_baseline.md")
	contractReport := filepath.Join(tmp, "graph", "contract_report.json")
	auditRoot := filepath.Join(tmp, "auditroot")
	drillSource := filepath.Join(tmp, "source")
	drillOut := filepath.Join(tmp, "drills")

	mustWriteJSON(t, qualityGate, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{"passed": true})
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
	mustWriteJSON(t, contractReport, map[string]any{
		"passed": true,
		"surfaces": map[string]any{
			"endpoints": map[string]any{
				"evidence_samples": []map[string]any{
					{"value": "GET /health", "links": []string{"graph://node/endpoint:health"}},
				},
			},
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
		"--merge-quality", mergeQuality,
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
		"--out-closure-rules", closureRules,
		"--contract-report", contractReport,
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

	required := []string{readiness, decision, milestones, benchmark, security, opsReport, closureRules}
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
	var closureRulesPayload struct {
		OverallPassed bool `json:"overall_passed"`
	}
	data, err = os.ReadFile(closureRules)
	if err != nil {
		t.Fatalf("read closure rules report: %v", err)
	}
	if err := json.Unmarshal(data, &closureRulesPayload); err != nil {
		t.Fatalf("decode closure rules report: %v", err)
	}
	if !closureRulesPayload.OverallPassed {
		t.Fatalf("expected closure rules report overall_passed=true")
	}
}

func TestCloseoutAutoGeneratesMergeQualityReportWhenMissing(t *testing.T) {
	tmp := t.TempDir()

	qualityGate := filepath.Join(tmp, "quality", "gate_result.json")
	mergeQuality := filepath.Join(tmp, "graph", "merge_quality_report.json")
	expectLinks := filepath.Join(tmp, "graph", "expected_links.json")
	sloGate := filepath.Join(tmp, "ops", "slo_report.json")
	templates := filepath.Join(tmp, "docs", "m15_query_templates.json")
	catalog := filepath.Join(tmp, "docs", "m17_question_catalog.json")
	graphIndex := filepath.Join(tmp, "graph", "index.json")
	graphJSON := filepath.Join(tmp, "graph", "g1", "graph.json")
	readiness := filepath.Join(tmp, "final", "readiness_report.json")
	decision := filepath.Join(tmp, "final", "gate_decision.md")
	milestones := filepath.Join(tmp, "final", "milestone_closure_report.json")
	benchmark := filepath.Join(tmp, "final", "benchmark_evidence_report.json")
	security := filepath.Join(tmp, "final", "security_validation_report.json")
	opsReport := filepath.Join(tmp, "final", "operations_drill_report.json")
	closureRules := filepath.Join(tmp, "final", "closure_rules_report.json")

	qualityReport := filepath.Join(tmp, "quality", "report.json")
	corpusReport := filepath.Join(tmp, "corpus", "report.json")
	perfPolicy := filepath.Join(tmp, "docs", "graph_performance_baseline.md")
	contractReport := filepath.Join(tmp, "graph", "contract_report.json")
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
	mustWriteJSON(t, graphJSON, map[string]any{
		"graph_id": "g1",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e1", "type": "service_calls_service", "source_id": "svc:a", "target_id": "svc:b", "attributes": map[string]any{
				"source_service_id": "a",
				"source_repo_path":  "/repos/a",
				"target_service_id": "b",
				"target_repo_path":  "/repos/b",
			}, "confidence": 0.9, "inferred": false},
		},
		"stats": map[string]any{"node_count": 2, "edge_count": 1, "by_node_type": map[string]any{"service": 2}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	})
	mustWriteJSON(t, graphIndex, map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "tenant_id": "default", "fingerprint": "fp1", "path": graphJSON},
		},
	})
	mustWriteJSON(t, expectLinks, map[string]any{
		"service_calls_service": []map[string]any{
			{
				"source_service_id": "a",
				"source_repo_path":  "/repos/a",
				"target_service_id": "b",
				"target_repo_path":  "/repos/b",
			},
		},
	})
	mustWriteJSON(t, contractReport, map[string]any{
		"passed": true,
		"surfaces": map[string]any{
			"dependencies": map[string]any{
				"evidence_samples": []map[string]any{
					{"value": "orders->payments", "links": []string{"graph://edge/e1"}},
				},
			},
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
		"--merge-quality", mergeQuality,
		"--merge-quality-expect-links", expectLinks,
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
		"--out-closure-rules", closureRules,
		"--contract-report", contractReport,
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
	if _, err := os.Stat(mergeQuality); err != nil {
		t.Fatalf("expected merge quality report to be auto-generated: %v", err)
	}
	data, err := os.ReadFile(mergeQuality)
	if err != nil {
		t.Fatalf("read merge quality report: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode merge quality report: %v", err)
	}
	if _, ok := payload["benchmark"].(map[string]any); !ok {
		t.Fatalf("expected merge quality report benchmark section when expected links are provided")
	}
}

func TestCloseoutFailsWhenContractEvidenceIsMissing(t *testing.T) {
	tmp := t.TempDir()
	qualityGate := filepath.Join(tmp, "quality", "gate_result.json")
	mergeQuality := filepath.Join(tmp, "graph", "merge_quality_report.json")
	sloGate := filepath.Join(tmp, "ops", "slo_report.json")
	templates := filepath.Join(tmp, "docs", "m15_query_templates.json")
	catalog := filepath.Join(tmp, "docs", "m17_question_catalog.json")
	graphIndex := filepath.Join(tmp, "graph", "index.json")
	contractReport := filepath.Join(tmp, "graph", "contract_report.json")
	qualityReport := filepath.Join(tmp, "quality", "report.json")
	corpusReport := filepath.Join(tmp, "corpus", "report.json")
	perfPolicy := filepath.Join(tmp, "docs", "graph_performance_baseline.md")
	auditRoot := filepath.Join(tmp, "auditroot")
	drillSource := filepath.Join(tmp, "source")
	drillOut := filepath.Join(tmp, "drills")

	mustWriteJSON(t, qualityGate, map[string]any{"passed": true})
	mustWriteJSON(t, mergeQuality, map[string]any{"passed": true})
	mustWriteJSON(t, sloGate, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	mustWriteJSON(t, templates, map[string]any{"templates": []map[string]any{
		{"id": "docs-service", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
	}})
	mustWriteJSON(t, catalog, map[string]any{"questions": []map[string]any{
		{"id": "q1", "question": "services", "endpoint": "/products/docs/{graph_id}"},
	}})
	mustWriteJSON(t, graphIndex, map[string]any{"graphs": []map[string]any{
		{"graph_id": "g1", "tenant_id": "default", "fingerprint": "fp1"},
	}})
	// Contract report exists but lacks graph evidence links, so closure rule must fail.
	mustWriteJSON(t, contractReport, map[string]any{
		"passed": true,
		"surfaces": map[string]any{
			"endpoints": map[string]any{
				"evidence_samples": []map[string]any{
					{"value": "GET /health", "links": []string{"https://example.com/evidence"}},
				},
			},
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

	err := Run(context.Background(), []string{
		"closeout",
		"--quality-gate", qualityGate,
		"--merge-quality", mergeQuality,
		"--slo", sloGate,
		"--templates", templates,
		"--catalog", catalog,
		"--graph-index", graphIndex,
		"--quality-report", qualityReport,
		"--corpus-report", corpusReport,
		"--performance-policy", perfPolicy,
		"--contract-report", contractReport,
		"--audit-root", auditRoot,
		"--drill-source", drillSource,
		"--drill-out", drillOut,
		"--signers", "engineering,platform,security",
	})
	if err == nil {
		t.Fatalf("expected closeout failure when contract evidence sampling gate fails")
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
