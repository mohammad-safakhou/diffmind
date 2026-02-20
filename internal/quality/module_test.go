package quality

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluateAndGate(t *testing.T) {
	tmp := t.TempDir()
	corpusPath := filepath.Join(tmp, "corpus.report.json")
	goldenPath := filepath.Join(tmp, "golden.summary.json")
	qualityPath := filepath.Join(tmp, "quality.report.json")
	policyPath := filepath.Join(tmp, "policy.json")
	dashboardPath := filepath.Join(tmp, "dashboard.md")
	triagePath := filepath.Join(tmp, "triage.md")

	corpusPayload := map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 2, "RuntimeUnit": 1}, "domain": "api", "language": "go", "framework": "gin", "framework_version": "v1", "duration_ms": 120, "tags": []string{"critical", "adversarial", "drift_expected", "drift_detected"}, "confidence": 0.95},
			{"name": "case-b", "status": "failed", "counts_by_type": map[string]any{"Endpoint": 1}, "domain": "api", "language": "go", "framework": "gin", "framework_version": "v1", "duration_ms": 180, "tags": []string{"sev1", "edge_case"}, "failures": []string{"required entity type missing: RuntimeUnit"}, "confidence": 0.9},
		},
	}
	goldenPayload := map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 2, "RuntimeUnit": 1}},
			{"name": "case-b", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1, "RuntimeUnit": 1}},
		},
	}
	policyPayload := map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                  0.5,
			"precision":                  0.5,
			"recall":                     0.5,
			"f1":                         0.5,
			"calibration_error_max":      0.6,
			"adversarial_pass_rate":      0.4,
			"framework_matrix_pass_rate": 0.5,
			"drift_precision":            0.5,
			"drift_recall":               0.5,
			"drift_f1":                   0.5,
			"benchmark_p95_ms_max":       1000,
		},
		"severity1": map[string]any{"regressions_max": 1},
	}
	mustWriteJSON(t, corpusPath, corpusPayload)
	mustWriteJSON(t, goldenPath, goldenPayload)
	mustWriteJSON(t, policyPath, policyPayload)

	if err := Run(context.Background(), []string{"evaluate", "--corpus", corpusPath, "--golden", goldenPath, "--out", qualityPath, "--dashboard", dashboardPath, "--triage", triagePath}); err != nil {
		t.Fatalf("quality evaluate failed: %v", err)
	}
	var evaluated map[string]any
	if data, err := os.ReadFile(qualityPath); err == nil {
		_ = json.Unmarshal(data, &evaluated)
	}
	if _, ok := evaluated["by_framework_version"]; !ok {
		t.Fatalf("expected by_framework_version in quality report")
	}
	if _, ok := evaluated["drift"]; !ok {
		t.Fatalf("expected drift metrics in quality report")
	}
	if _, ok := evaluated["benchmark"]; !ok {
		t.Fatalf("expected benchmark metrics in quality report")
	}
	if _, ok := evaluated["runtime_reconciliation"]; !ok {
		t.Fatalf("expected runtime_reconciliation plan in quality report")
	}
	if _, err := os.Stat(qualityPath); err != nil {
		t.Fatalf("quality report missing: %v", err)
	}
	if _, err := os.Stat(dashboardPath); err != nil {
		t.Fatalf("dashboard missing: %v", err)
	}
	if _, err := os.Stat(triagePath); err != nil {
		t.Fatalf("triage missing: %v", err)
	}

	if err := Run(context.Background(), []string{"gate", "--report", qualityPath, "--policy", policyPath, "--out", filepath.Join(tmp, "gate.result.json")}); err != nil {
		t.Fatalf("quality gate should pass but failed: %v", err)
	}
}

func TestGateFailsOnSev1Regression(t *testing.T) {
	tmp := t.TempDir()
	qualityPath := filepath.Join(tmp, "quality.report.json")
	policyPath := filepath.Join(tmp, "policy.json")

	qualityPayload := map[string]any{
		"metrics": map[string]any{
			"pass_rate":         1.0,
			"precision":         1.0,
			"recall":            1.0,
			"f1":                1.0,
			"calibration_error": 0.0,
		},
		"adversarial": map[string]any{"cases": 0, "passed": 0, "pass_rate": 0.0},
		"regressions": []map[string]any{{"case_name": "critical-case", "severity": "sev1", "was_status": "passed", "now_status": "failed"}},
	}
	policyPayload := map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                  0.9,
			"precision":                  0.9,
			"recall":                     0.9,
			"f1":                         0.9,
			"calibration_error_max":      0.1,
			"adversarial_pass_rate":      0.0,
			"framework_matrix_pass_rate": 0.0,
			"drift_precision":            0.0,
			"drift_recall":               0.0,
			"drift_f1":                   0.0,
			"benchmark_p95_ms_max":       0,
		},
		"severity1": map[string]any{"regressions_max": 0},
	}
	mustWriteJSON(t, qualityPath, qualityPayload)
	mustWriteJSON(t, policyPath, policyPayload)

	if err := Run(context.Background(), []string{"gate", "--report", qualityPath, "--policy", policyPath}); err == nil {
		t.Fatalf("expected gate failure on sev1 regression")
	}
}

func TestGateFailsOnDriftAndBenchmarkThresholds(t *testing.T) {
	tmp := t.TempDir()
	qualityPath := filepath.Join(tmp, "quality.report.json")
	policyPath := filepath.Join(tmp, "policy.json")

	qualityPayload := map[string]any{
		"metrics": map[string]any{
			"pass_rate":         1.0,
			"precision":         1.0,
			"recall":            1.0,
			"f1":                1.0,
			"calibration_error": 0.0,
		},
		"adversarial": map[string]any{"cases": 1, "passed": 1, "pass_rate": 1.0},
		"by_framework_version": []map[string]any{
			{"name": "spring@6", "cases": 2, "pass_rate": 1.0, "precision": 1.0, "recall": 1.0, "f1": 1.0, "calibration_error": 0.0},
		},
		"drift":       map[string]any{"cases": 1, "detected": 1, "precision": 0.6, "recall": 1.0, "f1": 0.75},
		"benchmark":   map[string]any{"cases": 2, "p95_duration_ms": 2500},
		"regressions": []map[string]any{},
	}
	policyPayload := map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                  0.9,
			"precision":                  0.9,
			"recall":                     0.9,
			"f1":                         0.9,
			"calibration_error_max":      0.1,
			"adversarial_pass_rate":      0.9,
			"framework_matrix_pass_rate": 0.95,
			"drift_precision":            0.95,
			"drift_recall":               0.9,
			"drift_f1":                   0.9,
			"benchmark_p95_ms_max":       2000,
		},
		"severity1": map[string]any{"regressions_max": 0},
	}
	mustWriteJSON(t, qualityPath, qualityPayload)
	mustWriteJSON(t, policyPath, policyPayload)

	if err := Run(context.Background(), []string{"gate", "--report", qualityPath, "--policy", policyPath}); err == nil {
		t.Fatalf("expected gate failure on drift/benchmark thresholds")
	}
}

func TestGateFailsWhenMergeQualityReportFails(t *testing.T) {
	tmp := t.TempDir()
	qualityPath := filepath.Join(tmp, "quality.report.json")
	policyPath := filepath.Join(tmp, "policy.json")

	qualityPayload := map[string]any{
		"metrics": map[string]any{
			"pass_rate":         1.0,
			"precision":         1.0,
			"recall":            1.0,
			"f1":                1.0,
			"calibration_error": 0.0,
		},
		"adversarial":   map[string]any{"cases": 0, "passed": 0, "pass_rate": 0.0},
		"drift":         map[string]any{"cases": 0, "detected": 0, "precision": 1.0, "recall": 1.0, "f1": 1.0},
		"benchmark":     map[string]any{"cases": 0, "p95_duration_ms": 0},
		"regressions":   []map[string]any{},
		"merge_quality": map[string]any{"present": true, "passed": false, "repo_provenance_coverage": 0.8, "unresolved_rate": 0.3, "ambiguous_rate": 0.1},
	}
	policyPayload := map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                  0.9,
			"precision":                  0.9,
			"recall":                     0.9,
			"f1":                         0.9,
			"calibration_error_max":      0.1,
			"adversarial_pass_rate":      0.0,
			"framework_matrix_pass_rate": 0.0,
			"drift_precision":            0.0,
			"drift_recall":               0.0,
			"drift_f1":                   0.0,
			"benchmark_p95_ms_max":       0,
		},
		"severity1": map[string]any{"regressions_max": 0},
	}
	mustWriteJSON(t, qualityPath, qualityPayload)
	mustWriteJSON(t, policyPath, policyPayload)

	if err := Run(context.Background(), []string{"gate", "--report", qualityPath, "--policy", policyPath}); err == nil {
		t.Fatalf("expected gate failure when merge quality report is present but failing")
	}
}

func TestGateFailsWhenMergeQualityBenchmarkIsRequiredButMissing(t *testing.T) {
	tmp := t.TempDir()
	qualityPath := filepath.Join(tmp, "quality.report.json")
	policyPath := filepath.Join(tmp, "policy.json")

	qualityPayload := map[string]any{
		"metrics": map[string]any{
			"pass_rate":         1.0,
			"precision":         1.0,
			"recall":            1.0,
			"f1":                1.0,
			"calibration_error": 0.0,
		},
		"adversarial": map[string]any{"cases": 0, "passed": 0, "pass_rate": 0.0},
		"drift":       map[string]any{"cases": 0, "detected": 0, "precision": 1.0, "recall": 1.0, "f1": 1.0},
		"benchmark":   map[string]any{"cases": 0, "p95_duration_ms": 0},
		"regressions": []map[string]any{},
		"merge_quality": map[string]any{
			"present":                  true,
			"passed":                   true,
			"repo_provenance_coverage": 1.0,
			"unresolved_rate":          0.0,
			"ambiguous_rate":           0.0,
			"benchmark_present":        false,
		},
	}
	policyPayload := map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                        0.9,
			"precision":                        0.9,
			"recall":                           0.9,
			"f1":                               0.9,
			"calibration_error_max":            0.1,
			"adversarial_pass_rate":            0.0,
			"framework_matrix_pass_rate":       0.0,
			"drift_precision":                  0.0,
			"drift_recall":                     0.0,
			"drift_f1":                         0.0,
			"benchmark_p95_ms_max":             0,
			"merge_quality_benchmark_required": true,
			"merge_quality_linkage_precision":  0.0,
			"merge_quality_linkage_recall":     0.0,
			"merge_quality_required":           true,
		},
		"severity1": map[string]any{"regressions_max": 0},
	}
	mustWriteJSON(t, qualityPath, qualityPayload)
	mustWriteJSON(t, policyPath, policyPayload)

	if err := Run(context.Background(), []string{"gate", "--report", qualityPath, "--policy", policyPath}); err == nil {
		t.Fatalf("expected gate failure when merge quality benchmark is required but missing")
	}
}

func TestGateFailsOnMergeQualityLinkageThresholds(t *testing.T) {
	tmp := t.TempDir()
	qualityPath := filepath.Join(tmp, "quality.report.json")
	policyPath := filepath.Join(tmp, "policy.json")

	qualityPayload := map[string]any{
		"metrics": map[string]any{
			"pass_rate":         1.0,
			"precision":         1.0,
			"recall":            1.0,
			"f1":                1.0,
			"calibration_error": 0.0,
		},
		"adversarial": map[string]any{"cases": 0, "passed": 0, "pass_rate": 0.0},
		"drift":       map[string]any{"cases": 0, "detected": 0, "precision": 1.0, "recall": 1.0, "f1": 1.0},
		"benchmark":   map[string]any{"cases": 0, "p95_duration_ms": 0},
		"regressions": []map[string]any{},
		"merge_quality": map[string]any{
			"present":                  true,
			"passed":                   true,
			"repo_provenance_coverage": 1.0,
			"unresolved_rate":          0.0,
			"ambiguous_rate":           0.0,
			"benchmark_present":        true,
			"benchmark_passed":         true,
			"linkage_precision":        0.80,
			"linkage_recall":           0.70,
			"linkage_f1":               0.75,
		},
	}
	policyPayload := map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                        0.9,
			"precision":                        0.9,
			"recall":                           0.9,
			"f1":                               0.9,
			"calibration_error_max":            0.1,
			"adversarial_pass_rate":            0.0,
			"framework_matrix_pass_rate":       0.0,
			"drift_precision":                  0.0,
			"drift_recall":                     0.0,
			"drift_f1":                         0.0,
			"benchmark_p95_ms_max":             0,
			"merge_quality_benchmark_required": true,
			"merge_quality_linkage_precision":  0.95,
			"merge_quality_linkage_recall":     0.95,
			"merge_quality_required":           true,
		},
		"severity1": map[string]any{"regressions_max": 0},
	}
	mustWriteJSON(t, qualityPath, qualityPayload)
	mustWriteJSON(t, policyPath, policyPayload)

	if err := Run(context.Background(), []string{"gate", "--report", qualityPath, "--policy", policyPath}); err == nil {
		t.Fatalf("expected gate failure on merge quality linkage thresholds")
	}
}

func TestGateFailsOnMergeQualityIdentityThresholds(t *testing.T) {
	tmp := t.TempDir()
	qualityPath := filepath.Join(tmp, "quality.report.json")
	policyPath := filepath.Join(tmp, "policy.json")

	qualityPayload := map[string]any{
		"metrics": map[string]any{
			"pass_rate":         1.0,
			"precision":         1.0,
			"recall":            1.0,
			"f1":                1.0,
			"calibration_error": 0.0,
		},
		"adversarial": map[string]any{"cases": 0, "passed": 0, "pass_rate": 0.0},
		"drift":       map[string]any{"cases": 0, "detected": 0, "precision": 1.0, "recall": 1.0, "f1": 1.0},
		"benchmark":   map[string]any{"cases": 0, "p95_duration_ms": 0},
		"regressions": []map[string]any{},
		"merge_quality": map[string]any{
			"present":                  true,
			"passed":                   true,
			"repo_provenance_coverage": 1.0,
			"unresolved_rate":          0.0,
			"ambiguous_rate":           0.0,
			"benchmark_present":        true,
			"benchmark_passed":         true,
			"linkage_precision":        1.0,
			"linkage_recall":           1.0,
			"identity_precision":       0.80,
			"identity_recall":          0.70,
		},
	}
	policyPayload := map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                        0.9,
			"precision":                        0.9,
			"recall":                           0.9,
			"f1":                               0.9,
			"calibration_error_max":            0.1,
			"adversarial_pass_rate":            0.0,
			"framework_matrix_pass_rate":       0.0,
			"drift_precision":                  0.0,
			"drift_recall":                     0.0,
			"drift_f1":                         0.0,
			"benchmark_p95_ms_max":             0,
			"merge_quality_benchmark_required": true,
			"merge_quality_linkage_precision":  0.95,
			"merge_quality_linkage_recall":     0.95,
			"merge_quality_identity_precision": 0.95,
			"merge_quality_identity_recall":    0.95,
			"merge_quality_required":           true,
		},
		"severity1": map[string]any{"regressions_max": 0},
	}
	mustWriteJSON(t, qualityPath, qualityPayload)
	mustWriteJSON(t, policyPath, policyPayload)

	if err := Run(context.Background(), []string{"gate", "--report", qualityPath, "--policy", policyPath}); err == nil {
		t.Fatalf("expected gate failure on merge quality identity thresholds")
	}
}

func TestEvaluateReadsMergeQualityBenchmarkSummary(t *testing.T) {
	tmp := t.TempDir()
	corpusPath := filepath.Join(tmp, "corpus.report.json")
	goldenPath := filepath.Join(tmp, "golden.summary.json")
	mergeQualityPath := filepath.Join(tmp, "graph", "merge_quality_report.json")
	qualityPath := filepath.Join(tmp, "quality.report.json")

	mustWriteJSON(t, corpusPath, map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1}, "confidence": 0.95},
		},
	})
	mustWriteJSON(t, goldenPath, map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1}},
		},
	})
	mustWriteJSON(t, mergeQualityPath, map[string]any{
		"graph_id": "g1",
		"passed":   true,
		"metrics": map[string]any{
			"repo_provenance_coverage": 1.0,
			"unresolved_rate":          0.0,
			"ambiguous_rate":           0.0,
		},
		"benchmark": map[string]any{
			"enabled": true,
			"passed":  true,
			"service_calls_service": map[string]any{
				"precision": 0.98,
				"recall":    0.99,
				"f1":        0.985,
			},
			"canonical_service_aliases": map[string]any{
				"precision": 0.97,
				"recall":    0.96,
				"f1":        0.965,
			},
		},
	})

	if err := Run(context.Background(), []string{
		"evaluate",
		"--corpus", corpusPath,
		"--golden", goldenPath,
		"--merge-quality", mergeQualityPath,
		"--merge-quality-auto=false",
		"--out", qualityPath,
		"--dashboard", filepath.Join(tmp, "dashboard.md"),
		"--triage", filepath.Join(tmp, "triage.md"),
	}); err != nil {
		t.Fatalf("quality evaluate failed: %v", err)
	}
	data, err := os.ReadFile(qualityPath)
	if err != nil {
		t.Fatalf("read quality report: %v", err)
	}
	var evaluated map[string]any
	if err := json.Unmarshal(data, &evaluated); err != nil {
		t.Fatalf("decode quality report: %v", err)
	}
	mq, _ := evaluated["merge_quality"].(map[string]any)
	if present, _ := mq["benchmark_present"].(bool); !present {
		t.Fatalf("expected merge_quality benchmark_present=true")
	}
	if got, _ := mq["linkage_precision"].(float64); got < 0.98 {
		t.Fatalf("expected linkage_precision from merge quality benchmark, got %v", got)
	}
	if got, _ := mq["identity_precision"].(float64); got < 0.97 {
		t.Fatalf("expected identity_precision from merge quality benchmark, got %v", got)
	}
}

func TestEvaluateAutoGeneratesMergeQualityReportFromGraphIndex(t *testing.T) {
	tmp := t.TempDir()
	corpusPath := filepath.Join(tmp, "corpus.report.json")
	goldenPath := filepath.Join(tmp, "golden.summary.json")
	qualityPath := filepath.Join(tmp, "quality.report.json")
	mergeQualityPath := filepath.Join(tmp, "graph", "merge_quality_report.json")
	expectLinksPath := filepath.Join(tmp, "graph", "expected_links.json")
	graphPath := filepath.Join(tmp, "graph", "g1", "graph.json")
	graphIndexPath := filepath.Join(tmp, "graph", "index.json")

	mustWriteJSON(t, corpusPath, map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1}, "confidence": 0.95},
		},
	})
	mustWriteJSON(t, goldenPath, map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1}},
		},
	})
	mustWriteJSON(t, graphPath, map[string]any{
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
	mustWriteJSON(t, graphIndexPath, map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "path": graphPath},
		},
	})
	mustWriteJSON(t, expectLinksPath, map[string]any{
		"service_calls_service": []map[string]any{
			{
				"source_service_id": "a",
				"source_repo_path":  "/repos/a",
				"target_service_id": "b",
				"target_repo_path":  "/repos/b",
			},
		},
	})

	if err := Run(context.Background(), []string{
		"evaluate",
		"--corpus", corpusPath,
		"--golden", goldenPath,
		"--merge-quality", mergeQualityPath,
		"--graph-index", graphIndexPath,
		"--merge-quality-expect-links", expectLinksPath,
		"--merge-quality-auto",
		"--out", qualityPath,
		"--dashboard", filepath.Join(tmp, "dashboard.md"),
		"--triage", filepath.Join(tmp, "triage.md"),
	}); err != nil {
		t.Fatalf("quality evaluate failed: %v", err)
	}

	if _, err := os.Stat(mergeQualityPath); err != nil {
		t.Fatalf("expected auto-generated merge quality report: %v", err)
	}
	var evaluated map[string]any
	data, err := os.ReadFile(qualityPath)
	if err != nil {
		t.Fatalf("read quality report: %v", err)
	}
	if err := json.Unmarshal(data, &evaluated); err != nil {
		t.Fatalf("decode quality report: %v", err)
	}
	mq, _ := evaluated["merge_quality"].(map[string]any)
	if present, _ := mq["present"].(bool); !present {
		t.Fatalf("expected merge_quality.present=true in evaluated report")
	}
	if benchmarkPresent, _ := mq["benchmark_present"].(bool); !benchmarkPresent {
		t.Fatalf("expected merge_quality.benchmark_present=true in evaluated report")
	}
}

func TestEvaluateAutoRefreshesMergeQualityBenchmarkWhenExpectedLinksProvided(t *testing.T) {
	tmp := t.TempDir()
	corpusPath := filepath.Join(tmp, "corpus.report.json")
	goldenPath := filepath.Join(tmp, "golden.summary.json")
	qualityPath := filepath.Join(tmp, "quality.report.json")
	mergeQualityPath := filepath.Join(tmp, "graph", "merge_quality_report.json")
	expectLinksPath := filepath.Join(tmp, "graph", "expected_links.json")
	graphPath := filepath.Join(tmp, "graph", "g1", "graph.json")
	graphIndexPath := filepath.Join(tmp, "graph", "index.json")

	mustWriteJSON(t, corpusPath, map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1}, "confidence": 0.95},
		},
	})
	mustWriteJSON(t, goldenPath, map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1}},
		},
	})
	// Stale merge-quality report without benchmark block.
	mustWriteJSON(t, mergeQualityPath, map[string]any{
		"graph_id": "stale",
		"passed":   true,
		"metrics": map[string]any{
			"repo_provenance_coverage": 1.0,
			"unresolved_rate":          0.0,
			"ambiguous_rate":           0.0,
		},
	})
	mustWriteJSON(t, graphPath, map[string]any{
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
	mustWriteJSON(t, graphIndexPath, map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "path": graphPath},
		},
	})
	mustWriteJSON(t, expectLinksPath, map[string]any{
		"service_calls_service": []map[string]any{
			{
				"source_service_id": "a",
				"source_repo_path":  "/repos/a",
				"target_service_id": "b",
				"target_repo_path":  "/repos/b",
			},
		},
	})

	if err := Run(context.Background(), []string{
		"evaluate",
		"--corpus", corpusPath,
		"--golden", goldenPath,
		"--merge-quality", mergeQualityPath,
		"--graph-index", graphIndexPath,
		"--merge-quality-expect-links", expectLinksPath,
		"--merge-quality-auto=true",
		"--out", qualityPath,
		"--dashboard", filepath.Join(tmp, "dashboard.md"),
		"--triage", filepath.Join(tmp, "triage.md"),
	}); err != nil {
		t.Fatalf("quality evaluate failed: %v", err)
	}

	mergeData, err := os.ReadFile(mergeQualityPath)
	if err != nil {
		t.Fatalf("read merge quality report: %v", err)
	}
	var merge map[string]any
	if err := json.Unmarshal(mergeData, &merge); err != nil {
		t.Fatalf("decode merge quality report: %v", err)
	}
	bench, _ := merge["benchmark"].(map[string]any)
	if bench == nil {
		t.Fatalf("expected refreshed merge quality report to include benchmark block")
	}
}

func TestCalibrateBaselinesGeneratesRealSourcePolicy(t *testing.T) {
	tmp := t.TempDir()
	summaryPath := filepath.Join(tmp, "summary.json")
	outPath := filepath.Join(tmp, "source_baselines.json")
	mustWriteJSON(t, summaryPath, map[string]any{
		"runs": []map[string]any{
			{
				"source_id":   "repo-a",
				"source_type": "real",
				"gates":       map[string]any{"contract_gate_applicable": true},
				"scorecard": map[string]any{
					"accuracy": map[string]any{"pass_rate": 0.92, "precision": 0.81, "recall": 0.83, "f1": 0.82},
					"completeness": map[string]any{
						"section_coverage_ratio": 0.90,
					},
					"task_pass_rate": 1.0,
				},
			},
			{
				"source_id":   "repo-a",
				"source_type": "real",
				"gates":       map[string]any{"contract_gate_applicable": true},
				"scorecard": map[string]any{
					"accuracy": map[string]any{"pass_rate": 0.90, "precision": 0.80, "recall": 0.84, "f1": 0.81},
					"completeness": map[string]any{
						"section_coverage_ratio": 0.88,
					},
					"task_pass_rate": 1.0,
				},
			},
			{
				"source_id":   "fixture-x",
				"source_type": "fixture",
				"gates":       map[string]any{"contract_gate_applicable": false},
				"scorecard": map[string]any{
					"accuracy": map[string]any{"pass_rate": 0.50, "precision": 0.40, "recall": 0.50, "f1": 0.44},
					"completeness": map[string]any{
						"section_coverage_ratio": 0.70,
					},
					"task_pass_rate": 0.95,
				},
			},
		},
	})

	if err := Run(context.Background(), []string{
		"calibrate-baselines",
		"--summary", summaryPath,
		"--out", outPath,
		"--min-samples", "2",
	}); err != nil {
		t.Fatalf("calibrate-baselines failed: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read calibrated policy: %v", err)
	}
	var policy map[string]any
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatalf("decode calibrated policy: %v", err)
	}
	sources, _ := policy["sources"].(map[string]any)
	if _, ok := sources["repo-a"]; !ok {
		t.Fatalf("expected source-specific baseline for repo-a")
	}
	if _, ok := sources["fixture-x"]; ok {
		t.Fatalf("did not expect fixture source baseline by default")
	}
	repoA, _ := sources["repo-a"].(map[string]any)
	if req, _ := repoA["require_contract_gate"].(bool); !req {
		t.Fatalf("expected require_contract_gate=true for repo-a")
	}
}

func TestCalibrateBaselinesIncludeFixturesWhenEnabled(t *testing.T) {
	tmp := t.TempDir()
	summaryPath := filepath.Join(tmp, "summary.json")
	outPath := filepath.Join(tmp, "source_baselines.json")
	mustWriteJSON(t, summaryPath, map[string]any{
		"runs": []map[string]any{
			{
				"source_id":   "fixture-x",
				"source_type": "fixture",
				"gates":       map[string]any{"contract_gate_applicable": false},
				"scorecard": map[string]any{
					"accuracy": map[string]any{"pass_rate": 0.90, "precision": 0.70, "recall": 0.80, "f1": 0.74},
					"completeness": map[string]any{
						"section_coverage_ratio": 0.86,
					},
					"task_pass_rate": 1.0,
				},
			},
		},
	})

	if err := Run(context.Background(), []string{
		"calibrate-baselines",
		"--summary", summaryPath,
		"--out", outPath,
		"--include-fixtures",
	}); err != nil {
		t.Fatalf("calibrate-baselines with fixtures failed: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read calibrated policy: %v", err)
	}
	var policy map[string]any
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatalf("decode calibrated policy: %v", err)
	}
	sources, _ := policy["sources"].(map[string]any)
	if _, ok := sources["fixture-x"]; !ok {
		t.Fatalf("expected fixture source baseline when include-fixtures is set")
	}
}

func TestCalibrateBaselinesSupportsMultipleSummaries(t *testing.T) {
	tmp := t.TempDir()
	summaryA := filepath.Join(tmp, "summary-a.json")
	summaryB := filepath.Join(tmp, "summary-b.json")
	outPath := filepath.Join(tmp, "source_baselines.json")
	mustWriteJSON(t, summaryA, map[string]any{
		"runs": []map[string]any{
			{
				"source_id":   "repo-a",
				"source_type": "real",
				"gates":       map[string]any{"contract_gate_applicable": true},
				"scorecard": map[string]any{
					"accuracy":       map[string]any{"pass_rate": 0.91, "precision": 0.82, "recall": 0.80, "f1": 0.81},
					"completeness":   map[string]any{"section_coverage_ratio": 0.88},
					"task_pass_rate": 1.0,
				},
			},
		},
	})
	mustWriteJSON(t, summaryB, map[string]any{
		"runs": []map[string]any{
			{
				"source_id":   "repo-b",
				"source_type": "real",
				"gates":       map[string]any{"contract_gate_applicable": false},
				"scorecard": map[string]any{
					"accuracy":       map[string]any{"pass_rate": 0.89, "precision": 0.78, "recall": 0.79, "f1": 0.78},
					"completeness":   map[string]any{"section_coverage_ratio": 0.84},
					"task_pass_rate": 1.0,
				},
			},
		},
	})

	if err := Run(context.Background(), []string{
		"calibrate-baselines",
		"--summary", summaryA,
		"--summaries", summaryB,
		"--out", outPath,
		"--min-samples", "1",
	}); err != nil {
		t.Fatalf("calibrate-baselines with multiple summaries failed: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read calibrated policy: %v", err)
	}
	var policy map[string]any
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatalf("decode calibrated policy: %v", err)
	}
	sources, _ := policy["sources"].(map[string]any)
	if _, ok := sources["repo-a"]; !ok {
		t.Fatalf("expected repo-a baseline from summary-a")
	}
	if _, ok := sources["repo-b"]; !ok {
		t.Fatalf("expected repo-b baseline from summary-b")
	}
	meta, _ := policy["meta"].(map[string]any)
	if rc, _ := meta["run_count"].(float64); int(rc) != 2 {
		t.Fatalf("expected merged run_count=2, got %v", meta["run_count"])
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
