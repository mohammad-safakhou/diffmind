package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAssessGeneratesMergeQualityReport(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")

	graphPayload := map[string]any{
		"graph_id": "g1",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "canon:host:1", "type": "canonical_api_host", "label": "billing.internal", "attributes": map[string]any{"canonical_host": "billing.internal"}, "confidence": 0.85, "inferred": true},
		},
		"edges": []map[string]any{
			{
				"id":        "e1",
				"type":      "service_calls_service",
				"source_id": "svc:a",
				"target_id": "svc:b",
				"attributes": map[string]any{
					"source_service_id": "a",
					"source_repo_path":  "/repos/a",
					"target_service_id": "b",
					"target_repo_path":  "/repos/b",
				},
				"confidence": 0.9,
				"inferred":   false,
			},
		},
		"stats": map[string]any{"node_count": 3, "edge_count": 1, "by_node_type": map[string]any{"service": 2}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	}
	writeJSONFile(t, graphPath, graphPayload)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--out", outPath, "--fail-on-gate"}); err != nil {
		t.Fatalf("graph assess failed: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep map[string]any
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if passed, _ := rep["passed"].(bool); !passed {
		t.Fatalf("expected merge quality report to pass")
	}
	metrics, _ := rep["metrics"].(map[string]any)
	if got, _ := metrics["service_calls_with_repo_provenance"].(float64); got != 1 {
		t.Fatalf("expected service_calls_with_repo_provenance=1, got %v", got)
	}
}

func TestRunAssessFailsOnGateWhenQualityGateFails(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")

	graphPayload := map[string]any{
		"graph_id": "g2",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "ua:1", "type": "unresolved_api_call", "label": "unresolved API call: GET", "attributes": map[string]any{"reason": "ambiguous_service_match"}, "confidence": 0.5, "inferred": true},
		},
		"edges": []map[string]any{
			{
				"id":         "e1",
				"type":       "service_calls_service",
				"source_id":  "svc:a",
				"target_id":  "svc:b",
				"attributes": map[string]any{},
				"confidence": 0.9,
				"inferred":   false,
			},
		},
		"stats": map[string]any{"node_count": 3, "edge_count": 1, "by_node_type": map[string]any{"service": 2, "unresolved_api_call": 1}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	}
	writeJSONFile(t, graphPath, graphPayload)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--out", outPath, "--fail-on-gate=true"}); err == nil {
		t.Fatalf("expected graph assess to fail when merge-quality gate fails")
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep map[string]any
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if passed, _ := rep["passed"].(bool); passed {
		t.Fatalf("expected merge quality report to fail")
	}
}

func TestRunAssessWritesHistoryIndexAndSnapshot(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")
	historyIndexPath := filepath.Join(tmp, "history", "index.json")

	graphPayload := map[string]any{
		"graph_id": "g-history",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{
			{
				"id":        "e1",
				"type":      "service_calls_service",
				"source_id": "svc:a",
				"target_id": "svc:b",
				"attributes": map[string]any{
					"source_service_id": "a",
					"source_repo_path":  "/repos/a",
					"target_service_id": "b",
					"target_repo_path":  "/repos/b",
				},
				"confidence": 0.95,
				"inferred":   false,
			},
		},
		"stats": map[string]any{"node_count": 2, "edge_count": 1, "by_node_type": map[string]any{"service": 2}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	}
	writeJSONFile(t, graphPath, graphPayload)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--out", outPath}); err != nil {
		t.Fatalf("graph assess failed: %v", err)
	}

	indexData, err := os.ReadFile(historyIndexPath)
	if err != nil {
		t.Fatalf("read history index: %v", err)
	}
	var idx struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal(indexData, &idx); err != nil {
		t.Fatalf("decode history index: %v", err)
	}
	if len(idx.Runs) == 0 {
		t.Fatalf("expected at least one history run")
	}
	snapshotPath, _ := idx.Runs[0]["snapshot_path"].(string)
	if strings.TrimSpace(snapshotPath) == "" {
		t.Fatalf("expected history run to include snapshot_path")
	}
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("expected snapshot file to exist: %v", err)
	}
}

func TestRunAssessBenchmarkPassesWithExpectedLinks(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	expectPath := filepath.Join(tmp, "expected_links.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")

	graphPayload := map[string]any{
		"graph_id": "g3",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{
			{
				"id":        "e1",
				"type":      "service_calls_service",
				"source_id": "svc:a",
				"target_id": "svc:b",
				"attributes": map[string]any{
					"source_service_id": "a",
					"source_repo_path":  "/repos/a",
					"target_service_id": "b",
					"target_repo_path":  "/repos/b",
				},
				"confidence": 0.95,
				"inferred":   false,
			},
		},
		"stats": map[string]any{"node_count": 2, "edge_count": 1, "by_node_type": map[string]any{"service": 2}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	}
	expectPayload := map[string]any{
		"service_calls_service": []map[string]any{
			{
				"source_service_id": "a",
				"source_repo_path":  "/repos/a",
				"target_service_id": "b",
				"target_repo_path":  "/repos/b",
			},
		},
	}
	writeJSONFile(t, graphPath, graphPayload)
	writeJSONFile(t, expectPath, expectPayload)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--expect-links", expectPath, "--out", outPath, "--fail-on-gate"}); err != nil {
		t.Fatalf("graph assess failed with benchmark: %v", err)
	}
	var rep map[string]any
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	bench, _ := rep["benchmark"].(map[string]any)
	if bench == nil {
		t.Fatalf("expected benchmark section in report")
	}
	if passed, _ := bench["passed"].(bool); !passed {
		t.Fatalf("expected benchmark to pass")
	}
}

func TestRunAssessBenchmarkFailsOnMismatch(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	expectPath := filepath.Join(tmp, "expected_links.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")

	graphPayload := map[string]any{
		"graph_id": "g4",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{
			{
				"id":        "e1",
				"type":      "service_calls_service",
				"source_id": "svc:a",
				"target_id": "svc:b",
				"attributes": map[string]any{
					"source_service_id": "a",
					"source_repo_path":  "/repos/a",
					"target_service_id": "b",
					"target_repo_path":  "/repos/b",
				},
				"confidence": 0.95,
				"inferred":   false,
			},
		},
		"stats": map[string]any{"node_count": 2, "edge_count": 1, "by_node_type": map[string]any{"service": 2}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	}
	expectPayload := map[string]any{
		"service_calls_service": []map[string]any{
			{
				"source_service_id": "x",
				"source_repo_path":  "/repos/x",
				"target_service_id": "y",
				"target_repo_path":  "/repos/y",
			},
		},
	}
	writeJSONFile(t, graphPath, graphPayload)
	writeJSONFile(t, expectPath, expectPayload)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--expect-links", expectPath, "--out", outPath, "--fail-on-gate=true"}); err == nil {
		t.Fatalf("expected graph assess to fail when benchmark gate fails")
	}
	var rep map[string]any
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	bench, _ := rep["benchmark"].(map[string]any)
	if bench == nil {
		t.Fatalf("expected benchmark section in report")
	}
	if passed, _ := bench["passed"].(bool); passed {
		t.Fatalf("expected benchmark to fail")
	}
	serviceCalls, _ := bench["service_calls_service"].(map[string]any)
	fpSamples, _ := serviceCalls["false_positive_samples"].([]any)
	fnSamples, _ := serviceCalls["false_negative_samples"].([]any)
	if len(fpSamples) == 0 || len(fnSamples) == 0 {
		t.Fatalf("expected service_calls_service mismatch samples in benchmark report")
	}
}

func TestRunAssessBenchmarkIdentityPassesWithExpectedCanonicalAliases(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	expectPath := filepath.Join(tmp, "expected_links.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")

	graphPayload := map[string]any{
		"graph_id": "g5",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "canon:svc:orders:prod", "type": "canonical_service", "label": "orders [prod]", "attributes": map[string]any{"canonical_key": "orders", "env_scope": "prod"}, "confidence": 0.85, "inferred": true},
		},
		"edges": []map[string]any{
			{
				"id":        "e1",
				"type":      "service_alias_of_canonical_service",
				"source_id": "svc:a",
				"target_id": "canon:svc:orders:prod",
				"attributes": map[string]any{
					"source_service_id": "a",
					"source_repo_path":  "/repos/a",
					"canonical_key":     "orders",
					"env_scope":         "prod",
				},
				"confidence": 0.9,
				"inferred":   true,
			},
		},
		"stats": map[string]any{"node_count": 2, "edge_count": 1, "by_node_type": map[string]any{"service": 1, "canonical_service": 1}, "by_edge_type": map[string]any{"service_alias_of_canonical_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	}
	expectPayload := map[string]any{
		"canonical_service_aliases": []map[string]any{
			{
				"source_service_id": "a",
				"source_repo_path":  "/repos/a",
				"canonical_key":     "orders",
				"env_scope":         "prod",
			},
		},
	}
	writeJSONFile(t, graphPath, graphPayload)
	writeJSONFile(t, expectPath, expectPayload)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--expect-links", expectPath, "--out", outPath, "--fail-on-gate"}); err != nil {
		t.Fatalf("graph assess failed with identity benchmark: %v", err)
	}
	var rep map[string]any
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	bench, _ := rep["benchmark"].(map[string]any)
	if bench == nil {
		t.Fatalf("expected benchmark section in report")
	}
	identity, _ := bench["canonical_service_aliases"].(map[string]any)
	if got, _ := identity["precision"].(float64); got < 0.99 {
		t.Fatalf("expected canonical_service_aliases precision ~1.0, got %v", got)
	}
}

func TestRunAssessBenchmarkIdentityFailsOnMismatch(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	expectPath := filepath.Join(tmp, "expected_links.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")

	graphPayload := map[string]any{
		"graph_id": "g6",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "canon:svc:orders:prod", "type": "canonical_service", "label": "orders [prod]", "attributes": map[string]any{"canonical_key": "orders", "env_scope": "prod"}, "confidence": 0.85, "inferred": true},
		},
		"edges": []map[string]any{
			{
				"id":        "e1",
				"type":      "service_alias_of_canonical_service",
				"source_id": "svc:a",
				"target_id": "canon:svc:orders:prod",
				"attributes": map[string]any{
					"source_service_id": "a",
					"source_repo_path":  "/repos/a",
					"canonical_key":     "orders",
					"env_scope":         "prod",
				},
				"confidence": 0.9,
				"inferred":   true,
			},
		},
		"stats": map[string]any{"node_count": 2, "edge_count": 1, "by_node_type": map[string]any{"service": 1, "canonical_service": 1}, "by_edge_type": map[string]any{"service_alias_of_canonical_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	}
	expectPayload := map[string]any{
		"canonical_service_aliases": []map[string]any{
			{
				"source_service_id": "a",
				"source_repo_path":  "/repos/a",
				"canonical_key":     "billing",
				"env_scope":         "prod",
			},
		},
	}
	writeJSONFile(t, graphPath, graphPayload)
	writeJSONFile(t, expectPath, expectPayload)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--expect-links", expectPath, "--out", outPath, "--fail-on-gate=true"}); err == nil {
		t.Fatalf("expected graph assess to fail when identity benchmark gate fails")
	}
	var rep map[string]any
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	bench, _ := rep["benchmark"].(map[string]any)
	identity, _ := bench["canonical_service_aliases"].(map[string]any)
	if got, _ := identity["recall"].(float64); got > 0.01 {
		t.Fatalf("expected canonical_service_aliases recall ~0.0 on mismatch, got %v", got)
	}
	fpSamples, _ := identity["false_positive_samples"].([]any)
	fnSamples, _ := identity["false_negative_samples"].([]any)
	if len(fpSamples) == 0 || len(fnSamples) == 0 {
		t.Fatalf("expected canonical_service_aliases mismatch samples in benchmark report")
	}
}

func TestRunAssessFailsOnInvalidExpectedLinksSchema(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	expectPath := filepath.Join(tmp, "expected_links.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")

	graphPayload := map[string]any{
		"graph_id": "g7",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{
			{
				"id":        "e1",
				"type":      "service_calls_service",
				"source_id": "svc:a",
				"target_id": "svc:b",
				"attributes": map[string]any{
					"source_service_id": "a",
					"source_repo_path":  "/repos/a",
					"target_service_id": "b",
					"target_repo_path":  "/repos/b",
				},
				"confidence": 0.95,
				"inferred":   false,
			},
		},
		"stats": map[string]any{"node_count": 2, "edge_count": 1, "by_node_type": map[string]any{"service": 2}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta":  map[string]any{"services": []any{}},
	}
	invalidExpected := map[string]any{
		"service_calls_service": []map[string]any{
			{
				"source_service_id": "",
				"target_service_id": "b",
			},
		},
	}
	writeJSONFile(t, graphPath, graphPayload)
	writeJSONFile(t, expectPath, invalidExpected)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--expect-links", expectPath, "--out", outPath}); err == nil {
		t.Fatalf("expected graph assess to fail on invalid expected-links input")
	}
}

func TestRunAssessFailsOnEmptyExpectedLinksFile(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	expectPath := filepath.Join(tmp, "expected_links.json")
	outPath := filepath.Join(tmp, "merge_quality_report.json")

	graphPayload := map[string]any{
		"graph_id": "g8",
		"mode":     "multi",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{},
		"stats": map[string]any{"node_count": 1, "edge_count": 0, "by_node_type": map[string]any{"service": 1}, "by_edge_type": map[string]any{}},
		"meta":  map[string]any{"services": []any{}},
	}
	emptyExpected := map[string]any{}
	writeJSONFile(t, graphPath, graphPayload)
	writeJSONFile(t, expectPath, emptyExpected)

	if err := Run(context.Background(), []string{"assess", "--graph", graphPath, "--expect-links", expectPath, "--out", outPath}); err == nil {
		t.Fatalf("expected graph assess to fail on empty expected-links input")
	}
}

func writeJSONFile(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
