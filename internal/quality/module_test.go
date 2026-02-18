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
