package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"diffmind/internal/audit"
	"diffmind/internal/graphschema"
)

func TestHealthEndpoint(t *testing.T) {
	mux := newMux("", "")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, withAuth(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestDefaultsEndpoint(t *testing.T) {
	mux := newMux("/tmp/diffmind/repo-a/bundle/intelligence_bundle.json", "/tmp/diffmind/graph-out/graph")
	req := httptest.NewRequest(http.MethodGet, "/defaults", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, withAuth(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload struct {
		DefaultBundlePath string `json:"default_bundle_path"`
		GraphRoot         string `json:"graph_root"`
		BuildDefaults     struct {
			OutDir             string `json:"out_dir"`
			ManifestPath       string `json:"manifest_path"`
			BundlePath         string `json:"bundle_path"`
			AnalyzerBundlePath string `json:"analyzer_bundle_path"`
		} `json:"build_defaults"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	if payload.DefaultBundlePath != "/tmp/diffmind/repo-a/bundle/intelligence_bundle.json" {
		t.Fatalf("unexpected default bundle path: %q", payload.DefaultBundlePath)
	}
	if payload.GraphRoot != "/tmp/diffmind/graph-out/graph" {
		t.Fatalf("unexpected graph root: %q", payload.GraphRoot)
	}
	if payload.BuildDefaults.OutDir != "/tmp/diffmind/graph-out" {
		t.Fatalf("unexpected out_dir: %q", payload.BuildDefaults.OutDir)
	}
	if payload.BuildDefaults.BundlePath != "/tmp/diffmind/graph-out/bundle/intelligence_bundle.json" {
		t.Fatalf("unexpected bundle path: %q", payload.BuildDefaults.BundlePath)
	}
	if payload.BuildDefaults.AnalyzerBundlePath != "/tmp/diffmind/graph-out/analyzers/bundle.json" {
		t.Fatalf("unexpected analyzer path: %q", payload.BuildDefaults.AnalyzerBundlePath)
	}
	if payload.BuildDefaults.ManifestPath != "/tmp/diffmind/graph-out/graph/services.yaml" {
		t.Fatalf("unexpected manifest path: %q", payload.BuildDefaults.ManifestPath)
	}
}

func TestDiscoverSourcesFromOutDir(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "graph-out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir out dir: %v", err)
	}
	got := discoverSourcesFromOutDir(outDir)
	if len(got) < 2 {
		t.Fatalf("expected at least two fallback sources, got %d (%#v)", len(got), got)
	}
	if got[0] != outDir {
		t.Fatalf("expected outDir first, got %q", got[0])
	}
	if got[1] != filepath.Dir(outDir) {
		t.Fatalf("expected parent second, got %q", got[1])
	}
}

func TestEntitiesEndpoint(t *testing.T) {
	tmp := t.TempDir()
	bundlePath := filepath.Join(tmp, "bundle.json")
	writeBundle(t, bundlePath, map[string]any{
		"snapshot_id": "s1",
		"entities": []map[string]any{
			{"id": "a", "type": "Endpoint", "natural_key": "GET|/a", "attributes": map[string]any{"method": "GET", "path": "/a"}, "evidence_ids": []string{"e1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
			{"id": "b", "type": "RuntimeUnit", "natural_key": "go|main|cmd/main.go", "attributes": map[string]any{"language": "go"}, "evidence_ids": []string{"e2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
		},
	})

	mux := newMux(bundlePath, "")
	req := httptest.NewRequest(http.MethodGet, "/entities?view=endpoints", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, withAuth(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Count != 1 {
		t.Fatalf("expected 1 endpoint, got %d", payload.Count)
	}
}

func TestDiffEndpoint(t *testing.T) {
	tmp := t.TempDir()
	fromPath := filepath.Join(tmp, "from.json")
	toPath := filepath.Join(tmp, "to.json")

	writeBundle(t, fromPath, map[string]any{
		"snapshot_id": "s1",
		"entities": []map[string]any{
			{"id": "a", "type": "Endpoint", "natural_key": "GET|/a", "attributes": map[string]any{"path": "/a"}, "evidence_ids": []string{"e1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
		},
	})
	writeBundle(t, toPath, map[string]any{
		"snapshot_id": "s2",
		"entities": []map[string]any{
			{"id": "a2", "type": "Endpoint", "natural_key": "GET|/a", "attributes": map[string]any{"path": "/a"}, "evidence_ids": []string{"e1"}, "fact_ids": []string{"f1"}, "confidence": 0.8},
		},
	})

	mux := newMux("", "")
	req := httptest.NewRequest(http.MethodGet, "/diff?from="+fromPath+"&to="+toPath, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, withAuth(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload struct {
		Changed int `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Changed != 1 {
		t.Fatalf("expected changed=1, got %d", payload.Changed)
	}
}

func TestGraphsEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	index := map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single", "node_count": 1, "edge_count": 0, "path": filepath.Join(graphRoot, "g1", "graph.json")},
			{"graph_id": "g2", "generated_at": "2026-01-02T00:00:00Z", "mode": "single", "node_count": 1, "edge_count": 0, "path": filepath.Join(graphRoot, "g2", "graph.json")},
		},
	}
	graph := map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "attributes": map[string]any{"verification_status": "verified", "environment": "prod"}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "service_id": "b", "attributes": map[string]any{"verification_status": "needs_review", "environment": "staging"}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e1", "type": "service_calls_service", "source_id": "svc:a", "target_id": "svc:b", "attributes": map[string]any{}, "confidence": 0.95, "inferred": false, "evidence_refs": []map[string]any{{"snapshot_id": "snap-a", "file_path": "main.go", "start_line": 12, "end_line": 12}}},
			{"id": "e2", "type": "service_calls_service", "source_id": "svc:b", "target_id": "svc:a", "attributes": map[string]any{}, "confidence": 0.4, "inferred": false, "evidence_refs": []any{}},
		},
		"stats": map[string]any{"node_count": 1, "edge_count": 1, "by_node_type": map[string]any{"service": 1}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta": map[string]any{
			"provenance": map[string]any{
				"tool":         "diffmind",
				"generated_by": "graph.build",
			},
			"services": []map[string]any{
				{
					"id":          "a",
					"name":        "A",
					"repo_path":   "/repos/a",
					"bundle_path": "a.json",
					"provenance": map[string]any{
						"output_root":     "/out/a",
						"run_report_path": "/out/a/run/report.json",
						"snapshot_id":     "snap-a",
						"snapshot_path":   "/out/a/snapshots/snap-a/snapshot.json",
					},
				},
				{
					"id":          "b",
					"name":        "B",
					"repo_path":   "/repos/b",
					"bundle_path": "b.json",
					"provenance": map[string]any{
						"output_root":     "/out/b",
						"run_report_path": "/out/b/run/report.json",
						"snapshot_id":     "snap-b",
						"snapshot_path":   "/out/b/snapshots/snap-b/snapshot.json",
					},
				},
			},
		},
	}
	graph2 := map[string]any{
		"graph_id": "g2", "generated_at": "2026-01-02T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B2", "service_id": "b", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:c", "type": "service", "label": "C", "service_id": "c", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e2", "type": "service_calls_service", "source_id": "svc:b", "target_id": "svc:a", "attributes": map[string]any{}, "confidence": 0.55, "inferred": false, "evidence_refs": []any{}},
			{"id": "e3", "type": "service_calls_service", "source_id": "svc:c", "target_id": "svc:a", "attributes": map[string]any{}, "confidence": 0.97, "inferred": false, "evidence_refs": []any{}},
		},
		"stats": map[string]any{"node_count": 3, "edge_count": 2, "by_node_type": map[string]any{"service": 3}, "by_edge_type": map[string]any{"service_calls_service": 2}},
		"meta": map[string]any{"services": []map[string]any{
			{"id": "a", "name": "A", "repo_path": "/repos/a", "bundle_path": "a.json"},
			{"id": "b", "name": "B", "repo_path": "/repos/b", "bundle_path": "b.json"},
			{"id": "c", "name": "C", "repo_path": "/repos/c", "bundle_path": "c.json"},
		}},
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), index)
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), graph)
	writeJSONFile(t, filepath.Join(graphRoot, "g2", "graph.json"), graph2)

	mux := newMux("", graphRoot)

	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/graphs", nil)
	mux.ServeHTTP(recList, withAuth(reqList))
	if recList.Code != http.StatusOK {
		t.Fatalf("expected /graphs 200, got %d", recList.Code)
	}

	recAtLatest := httptest.NewRecorder()
	reqAtLatest := httptest.NewRequest(http.MethodGet, "/graphs/at", nil)
	mux.ServeHTTP(recAtLatest, withAuth(reqAtLatest))
	if recAtLatest.Code != http.StatusOK {
		t.Fatalf("expected /graphs/at latest 200, got %d body=%s", recAtLatest.Code, recAtLatest.Body.String())
	}
	var atLatestPayload struct {
		GraphID string `json:"graph_id"`
		Meta    struct {
			Freshness struct {
				MaxAgeHours int   `json:"max_age_hours"`
				AgeSeconds  int64 `json:"age_seconds"`
			} `json:"freshness"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(recAtLatest.Body.Bytes(), &atLatestPayload); err != nil {
		t.Fatalf("decode /graphs/at latest response: %v", err)
	}
	if atLatestPayload.GraphID != "g2" {
		t.Fatalf("expected latest graph g2, got %q", atLatestPayload.GraphID)
	}
	if atLatestPayload.Meta.Freshness.MaxAgeHours != 24 {
		t.Fatalf("expected default max_age_hours=24, got %d", atLatestPayload.Meta.Freshness.MaxAgeHours)
	}
	if atLatestPayload.Meta.Freshness.AgeSeconds < 0 {
		t.Fatalf("expected non-negative age_seconds, got %d", atLatestPayload.Meta.Freshness.AgeSeconds)
	}

	recAtTime := httptest.NewRecorder()
	reqAtTime := httptest.NewRequest(http.MethodGet, "/graphs/at?at=2026-01-01T12:00:00Z", nil)
	mux.ServeHTTP(recAtTime, withAuth(reqAtTime))
	if recAtTime.Code != http.StatusOK {
		t.Fatalf("expected /graphs/at at=... 200, got %d body=%s", recAtTime.Code, recAtTime.Body.String())
	}
	var atTimePayload struct {
		GraphID string `json:"graph_id"`
	}
	if err := json.Unmarshal(recAtTime.Body.Bytes(), &atTimePayload); err != nil {
		t.Fatalf("decode /graphs/at at=... response: %v", err)
	}
	if atTimePayload.GraphID != "g1" {
		t.Fatalf("expected graph g1 for time-travel request, got %q", atTimePayload.GraphID)
	}

	recAtExplain := httptest.NewRecorder()
	reqAtExplain := httptest.NewRequest(http.MethodGet, "/graphs/at?at=2026-01-01T12:00:00Z&verification_status=needs_review&explain=true", nil)
	mux.ServeHTTP(recAtExplain, withAuth(reqAtExplain))
	if recAtExplain.Code != http.StatusOK {
		t.Fatalf("expected /graphs/at explain 200, got %d body=%s", recAtExplain.Code, recAtExplain.Body.String())
	}
	var atExplainPayload struct {
		Graph struct {
			Nodes []map[string]any `json:"nodes"`
		} `json:"graph"`
		Explain struct {
			Nodes []map[string]any `json:"nodes"`
		} `json:"explain"`
	}
	if err := json.Unmarshal(recAtExplain.Body.Bytes(), &atExplainPayload); err != nil {
		t.Fatalf("decode /graphs/at explain response: %v", err)
	}
	if len(atExplainPayload.Graph.Nodes) != 1 || len(atExplainPayload.Explain.Nodes) != 1 {
		t.Fatalf("expected filtered explain payload with one node, got graph=%d explain=%d", len(atExplainPayload.Graph.Nodes), len(atExplainPayload.Explain.Nodes))
	}

	recAtBad := httptest.NewRecorder()
	reqAtBad := httptest.NewRequest(http.MethodGet, "/graphs/at?at=not-a-time", nil)
	mux.ServeHTTP(recAtBad, withAuth(reqAtBad))
	if recAtBad.Code != http.StatusBadRequest {
		t.Fatalf("expected /graphs/at invalid time 400, got %d", recAtBad.Code)
	}

	recAtMiss := httptest.NewRecorder()
	reqAtMiss := httptest.NewRequest(http.MethodGet, "/graphs/at?at=2025-01-01T00:00:00Z", nil)
	mux.ServeHTTP(recAtMiss, withAuth(reqAtMiss))
	if recAtMiss.Code != http.StatusNotFound {
		t.Fatalf("expected /graphs/at miss 404, got %d", recAtMiss.Code)
	}

	recGraph := httptest.NewRecorder()
	reqGraph := httptest.NewRequest(http.MethodGet, "/graphs/g1", nil)
	mux.ServeHTTP(recGraph, withAuth(reqGraph))
	if recGraph.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1 200, got %d", recGraph.Code)
	}

	recEvidence := httptest.NewRecorder()
	reqEvidence := httptest.NewRequest(http.MethodGet, "/graphs/g1/evidence/e1", nil)
	mux.ServeHTTP(recEvidence, withAuth(reqEvidence))
	if recEvidence.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1/evidence/e1 200, got %d", recEvidence.Code)
	}
	var evidencePayload struct {
		GraphID    string  `json:"graph_id"`
		EdgeID     string  `json:"edge_id"`
		EdgeType   string  `json:"edge_type"`
		Confidence float64 `json:"confidence"`
		Source     struct {
			ID        string `json:"id"`
			ServiceID string `json:"service_id"`
		} `json:"source"`
		Target struct {
			ID        string `json:"id"`
			ServiceID string `json:"service_id"`
		} `json:"target"`
		SourceService map[string]any   `json:"source_service"`
		TargetService map[string]any   `json:"target_service"`
		EvidenceRefs  []map[string]any `json:"evidence_refs"`
		Provenance    struct {
			Graph map[string]any `json:"graph"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(recEvidence.Body.Bytes(), &evidencePayload); err != nil {
		t.Fatalf("decode evidence response: %v", err)
	}
	if evidencePayload.GraphID != "g1" || evidencePayload.EdgeID != "e1" || evidencePayload.EdgeType != "service_calls_service" {
		t.Fatalf("unexpected evidence identity: %+v", evidencePayload)
	}
	if evidencePayload.Source.ID != "svc:a" || evidencePayload.Target.ID != "svc:b" {
		t.Fatalf("unexpected evidence nodes: source=%+v target=%+v", evidencePayload.Source, evidencePayload.Target)
	}
	if evidencePayload.Confidence != 0.95 {
		t.Fatalf("unexpected evidence confidence: %v", evidencePayload.Confidence)
	}
	if len(evidencePayload.EvidenceRefs) != 1 {
		t.Fatalf("expected one evidence ref, got %d", len(evidencePayload.EvidenceRefs))
	}
	if evidencePayload.SourceService["id"] != "a" || evidencePayload.TargetService["id"] != "b" {
		t.Fatalf("unexpected evidence service metadata: source=%v target=%v", evidencePayload.SourceService, evidencePayload.TargetService)
	}
	if evidencePayload.Provenance.Graph["tool"] != "diffmind" {
		t.Fatalf("unexpected evidence provenance graph payload: %v", evidencePayload.Provenance.Graph)
	}

	recMetrics := httptest.NewRecorder()
	reqMetrics := httptest.NewRequest(http.MethodGet, "/graphs/g1/metrics", nil)
	mux.ServeHTTP(recMetrics, withAuth(reqMetrics))
	if recMetrics.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1/metrics 200, got %d", recMetrics.Code)
	}
	var metricsPayload struct {
		GraphID string `json:"graph_id"`
		Edges   int    `json:"edge_count"`
		Nodes   int    `json:"node_count"`
	}
	if err := json.Unmarshal(recMetrics.Body.Bytes(), &metricsPayload); err != nil {
		t.Fatalf("decode metrics response: %v", err)
	}
	if metricsPayload.GraphID != "g1" || metricsPayload.Edges != 2 || metricsPayload.Nodes != 2 {
		t.Fatalf("unexpected metrics payload: %+v", metricsPayload)
	}

	recSummary := httptest.NewRecorder()
	reqSummary := httptest.NewRequest(http.MethodGet, "/graphs/g1/summary", nil)
	mux.ServeHTTP(recSummary, withAuth(reqSummary))
	if recSummary.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1/summary 200, got %d", recSummary.Code)
	}
	var summaryPayload struct {
		GraphID   string `json:"graph_id"`
		NodeCount int    `json:"node_count"`
		EdgeCount int    `json:"edge_count"`
	}
	if err := json.Unmarshal(recSummary.Body.Bytes(), &summaryPayload); err != nil {
		t.Fatalf("decode summary response: %v", err)
	}
	if summaryPayload.GraphID != "g1" || summaryPayload.NodeCount != 2 || summaryPayload.EdgeCount != 2 {
		t.Fatalf("unexpected summary payload: %+v", summaryPayload)
	}

	recQuery := httptest.NewRecorder()
	reqQuery := httptest.NewRequest(http.MethodGet, "/graphs/g1/query?verification_status=needs_review&explain=true", nil)
	mux.ServeHTTP(recQuery, withAuth(reqQuery))
	if recQuery.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1/query 200, got %d body=%s", recQuery.Code, recQuery.Body.String())
	}
	var queryPayload struct {
		Graph struct {
			Nodes []map[string]any `json:"nodes"`
		} `json:"graph"`
		Explain struct {
			Nodes []map[string]any `json:"nodes"`
			Edges []map[string]any `json:"edges"`
		} `json:"explain"`
	}
	if err := json.Unmarshal(recQuery.Body.Bytes(), &queryPayload); err != nil {
		t.Fatalf("decode query response: %v", err)
	}
	if len(queryPayload.Graph.Nodes) != 1 {
		t.Fatalf("expected one node after verification_status filter, got %d", len(queryPayload.Graph.Nodes))
	}
	if len(queryPayload.Explain.Nodes) != 1 {
		t.Fatalf("expected one explain node after filter, got %d", len(queryPayload.Explain.Nodes))
	}
	if queryPayload.Explain.Nodes[0]["verification_status"] != "needs_review" {
		t.Fatalf("expected explain verification_status=needs_review, got %v", queryPayload.Explain.Nodes[0]["verification_status"])
	}

	recConfidence := httptest.NewRecorder()
	reqConfidence := httptest.NewRequest(http.MethodGet, "/graphs/g1?confidence_min=0.9", nil)
	mux.ServeHTTP(recConfidence, withAuth(reqConfidence))
	if recConfidence.Code != http.StatusOK {
		t.Fatalf("expected confidence filter 200, got %d", recConfidence.Code)
	}
	var confidencePayload struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal(recConfidence.Body.Bytes(), &confidencePayload); err != nil {
		t.Fatalf("decode confidence response: %v", err)
	}
	if len(confidencePayload.Edges) != 1 {
		t.Fatalf("expected one edge after confidence filter, got %d", len(confidencePayload.Edges))
	}
	if len(confidencePayload.Nodes) != 2 {
		t.Fatalf("expected two nodes after confidence filter, got %d", len(confidencePayload.Nodes))
	}

	recMetricsConfidence := httptest.NewRecorder()
	reqMetricsConfidence := httptest.NewRequest(http.MethodGet, "/graphs/g1/metrics?confidence_min=0.9", nil)
	mux.ServeHTTP(recMetricsConfidence, withAuth(reqMetricsConfidence))
	if recMetricsConfidence.Code != http.StatusOK {
		t.Fatalf("expected metrics confidence filter 200, got %d", recMetricsConfidence.Code)
	}
	var metricsConfidencePayload struct {
		EdgeCount int `json:"edge_count"`
	}
	if err := json.Unmarshal(recMetricsConfidence.Body.Bytes(), &metricsConfidencePayload); err != nil {
		t.Fatalf("decode confidence metrics response: %v", err)
	}
	if metricsConfidencePayload.EdgeCount != 1 {
		t.Fatalf("expected one edge in metrics after confidence filter, got %d", metricsConfidencePayload.EdgeCount)
	}

	recRepo := httptest.NewRecorder()
	reqRepo := httptest.NewRequest(http.MethodGet, "/graphs/g1?repo=/repos/a", nil)
	mux.ServeHTTP(recRepo, withAuth(reqRepo))
	if recRepo.Code != http.StatusOK {
		t.Fatalf("expected repo filter 200, got %d", recRepo.Code)
	}
	var repoPayload struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal(recRepo.Body.Bytes(), &repoPayload); err != nil {
		t.Fatalf("decode repo response: %v", err)
	}
	if len(repoPayload.Edges) != 2 {
		t.Fatalf("expected two edges for repo /repos/a, got %d", len(repoPayload.Edges))
	}
	if len(repoPayload.Nodes) != 2 {
		t.Fatalf("expected two nodes for repo /repos/a, got %d", len(repoPayload.Nodes))
	}

	recNode := httptest.NewRecorder()
	reqNode := httptest.NewRequest(http.MethodGet, "/graphs/g1?node=svc:b", nil)
	mux.ServeHTTP(recNode, withAuth(reqNode))
	if recNode.Code != http.StatusOK {
		t.Fatalf("expected node filter 200, got %d", recNode.Code)
	}
	var nodePayload struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal(recNode.Body.Bytes(), &nodePayload); err != nil {
		t.Fatalf("decode node filter response: %v", err)
	}
	if len(nodePayload.Edges) != 2 {
		t.Fatalf("expected two edges touching svc:b, got %d", len(nodePayload.Edges))
	}
	if len(nodePayload.Nodes) != 2 {
		t.Fatalf("expected two nodes in node-filtered subgraph, got %d", len(nodePayload.Nodes))
	}

	recBad := httptest.NewRecorder()
	reqBad := httptest.NewRequest(http.MethodGet, "/graphs/g1?confidence_min=abc", nil)
	mux.ServeHTTP(recBad, withAuth(reqBad))
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid confidence_min 400, got %d", recBad.Code)
	}

	compareBody := map[string]any{
		"from_graph_id": "g1",
		"to_graph_id":   "g2",
	}
	compareData, err := json.Marshal(compareBody)
	if err != nil {
		t.Fatalf("marshal compare request: %v", err)
	}
	recCompare := httptest.NewRecorder()
	reqCompare := httptest.NewRequest(http.MethodPost, "/graphs/compare", bytes.NewReader(compareData))
	reqCompare.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recCompare, withAuth(reqCompare))
	if recCompare.Code != http.StatusOK {
		t.Fatalf("expected compare 200, got %d body=%s", recCompare.Code, recCompare.Body.String())
	}
	var comparePayload struct {
		CompareID    string           `json:"compare_id"`
		AddedNodes   []map[string]any `json:"added_nodes"`
		RemovedNodes []map[string]any `json:"removed_nodes"`
		ChangedNodes []map[string]any `json:"changed_nodes"`
		AddedEdges   []map[string]any `json:"added_edges"`
		RemovedEdges []map[string]any `json:"removed_edges"`
		ChangedEdges []map[string]any `json:"changed_edges"`
	}
	if err := json.Unmarshal(recCompare.Body.Bytes(), &comparePayload); err != nil {
		t.Fatalf("decode compare response: %v", err)
	}
	if len(comparePayload.AddedNodes) != 1 || len(comparePayload.RemovedNodes) != 0 {
		t.Fatalf("unexpected node compare payload: %+v", comparePayload)
	}
	if len(comparePayload.AddedEdges) != 1 || len(comparePayload.RemovedEdges) != 1 {
		t.Fatalf("unexpected edge compare payload: %+v", comparePayload)
	}
	if len(comparePayload.ChangedNodes) < 1 || len(comparePayload.ChangedEdges) != 1 {
		t.Fatalf("unexpected changed compare payload: %+v", comparePayload)
	}
	if _, ok := comparePayload.ChangedNodes[0]["before"]; !ok {
		t.Fatalf("expected changed node to include before")
	}
	if _, ok := comparePayload.ChangedNodes[0]["after"]; !ok {
		t.Fatalf("expected changed node to include after")
	}
	if _, ok := comparePayload.ChangedEdges[0]["before"]; !ok {
		t.Fatalf("expected changed edge to include before")
	}
	if _, ok := comparePayload.ChangedEdges[0]["after"]; !ok {
		t.Fatalf("expected changed edge to include after")
	}
	if comparePayload.CompareID == "" {
		t.Fatalf("expected compare_id to be set")
	}

	recCompareFiltered := httptest.NewRecorder()
	reqCompareFiltered := httptest.NewRequest(http.MethodPost, "/graphs/compare?confidence_min=0.95", bytes.NewReader(compareData))
	reqCompareFiltered.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recCompareFiltered, withAuth(reqCompareFiltered))
	if recCompareFiltered.Code != http.StatusOK {
		t.Fatalf("expected filtered compare 200, got %d body=%s", recCompareFiltered.Code, recCompareFiltered.Body.String())
	}
	var compareFilteredPayload struct {
		AddedEdges []map[string]any `json:"added_edges"`
	}
	if err := json.Unmarshal(recCompareFiltered.Body.Bytes(), &compareFilteredPayload); err != nil {
		t.Fatalf("decode filtered compare response: %v", err)
	}
	if len(compareFilteredPayload.AddedEdges) != 1 {
		t.Fatalf("expected one added edge after confidence filter, got %d", len(compareFilteredPayload.AddedEdges))
	}

	recCompareList := httptest.NewRecorder()
	reqCompareList := httptest.NewRequest(http.MethodGet, "/graphs/compare", nil)
	mux.ServeHTTP(recCompareList, withAuth(reqCompareList))
	if recCompareList.Code != http.StatusOK {
		t.Fatalf("expected compare list 200, got %d", recCompareList.Code)
	}
	var compareListPayload struct {
		Compares []map[string]any `json:"compares"`
	}
	if err := json.Unmarshal(recCompareList.Body.Bytes(), &compareListPayload); err != nil {
		t.Fatalf("decode compare list response: %v", err)
	}
	if len(compareListPayload.Compares) == 0 {
		t.Fatalf("expected compare history entries")
	}

	recCompareListLimited := httptest.NewRecorder()
	reqCompareListLimited := httptest.NewRequest(http.MethodGet, "/graphs/compare?limit=1", nil)
	mux.ServeHTTP(recCompareListLimited, withAuth(reqCompareListLimited))
	if recCompareListLimited.Code != http.StatusOK {
		t.Fatalf("expected compare list limit 200, got %d", recCompareListLimited.Code)
	}
	var compareListLimitedPayload struct {
		Compares   []map[string]any `json:"compares"`
		NextBefore string           `json:"next_before"`
	}
	if err := json.Unmarshal(recCompareListLimited.Body.Bytes(), &compareListLimitedPayload); err != nil {
		t.Fatalf("decode compare list limit response: %v", err)
	}
	if len(compareListLimitedPayload.Compares) != 1 {
		t.Fatalf("expected one compare with limit=1, got %d", len(compareListLimitedPayload.Compares))
	}
	if compareListLimitedPayload.NextBefore == "" {
		t.Fatalf("expected next_before cursor for limit=1")
	}

	recCompareListCursor := httptest.NewRecorder()
	reqCompareListCursor := httptest.NewRequest(http.MethodGet, "/graphs/compare?limit=1&before="+compareListLimitedPayload.NextBefore, nil)
	mux.ServeHTTP(recCompareListCursor, withAuth(reqCompareListCursor))
	if recCompareListCursor.Code != http.StatusOK {
		t.Fatalf("expected compare list cursor 200, got %d", recCompareListCursor.Code)
	}
	var compareListCursorPayload struct {
		Compares []map[string]any `json:"compares"`
	}
	if err := json.Unmarshal(recCompareListCursor.Body.Bytes(), &compareListCursorPayload); err != nil {
		t.Fatalf("decode compare list cursor response: %v", err)
	}
	if len(compareListCursorPayload.Compares) == 0 {
		t.Fatalf("expected cursor page to contain remaining compares")
	}

	recCompareListBadLimit := httptest.NewRecorder()
	reqCompareListBadLimit := httptest.NewRequest(http.MethodGet, "/graphs/compare?limit=bad", nil)
	mux.ServeHTTP(recCompareListBadLimit, withAuth(reqCompareListBadLimit))
	if recCompareListBadLimit.Code != http.StatusBadRequest {
		t.Fatalf("expected compare list bad limit 400, got %d", recCompareListBadLimit.Code)
	}

	recCompareListBadCursor := httptest.NewRecorder()
	reqCompareListBadCursor := httptest.NewRequest(http.MethodGet, "/graphs/compare?limit=1&before=unknown", nil)
	mux.ServeHTTP(recCompareListBadCursor, withAuth(reqCompareListBadCursor))
	if recCompareListBadCursor.Code != http.StatusBadRequest {
		t.Fatalf("expected compare list bad cursor 400, got %d", recCompareListBadCursor.Code)
	}

	recComparePrune := httptest.NewRecorder()
	reqComparePrune := httptest.NewRequest(http.MethodDelete, "/graphs/compare?keep_latest=100", nil)
	mux.ServeHTTP(recComparePrune, withAuth(reqComparePrune))
	if recComparePrune.Code != http.StatusOK {
		t.Fatalf("expected compare prune 200, got %d", recComparePrune.Code)
	}
	var comparePrunePayload struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(recComparePrune.Body.Bytes(), &comparePrunePayload); err != nil {
		t.Fatalf("decode compare prune response: %v", err)
	}
	if comparePrunePayload.Deleted < 0 {
		t.Fatalf("invalid deleted count after prune: %d", comparePrunePayload.Deleted)
	}

	recComparePruneBad := httptest.NewRecorder()
	reqComparePruneBad := httptest.NewRequest(http.MethodDelete, "/graphs/compare?keep_latest=bad", nil)
	mux.ServeHTTP(recComparePruneBad, withAuth(reqComparePruneBad))
	if recComparePruneBad.Code != http.StatusBadRequest {
		t.Fatalf("expected compare prune bad keep_latest 400, got %d", recComparePruneBad.Code)
	}

	recCompareByID := httptest.NewRecorder()
	reqCompareByID := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID, nil)
	mux.ServeHTTP(recCompareByID, withAuth(reqCompareByID))
	if recCompareByID.Code != http.StatusOK {
		t.Fatalf("expected compare by id 200, got %d", recCompareByID.Code)
	}

	recCompareDelete := httptest.NewRecorder()
	reqCompareDelete := httptest.NewRequest(http.MethodDelete, "/graphs/compare/"+comparePayload.CompareID, nil)
	mux.ServeHTTP(recCompareDelete, withAuth(reqCompareDelete))
	if recCompareDelete.Code != http.StatusOK {
		t.Fatalf("expected compare delete 200, got %d", recCompareDelete.Code)
	}

	recCompareByIDAfterDelete := httptest.NewRecorder()
	reqCompareByIDAfterDelete := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID, nil)
	mux.ServeHTTP(recCompareByIDAfterDelete, withAuth(reqCompareByIDAfterDelete))
	if recCompareByIDAfterDelete.Code != http.StatusNotFound {
		t.Fatalf("expected compare by id after delete 404, got %d", recCompareByIDAfterDelete.Code)
	}
}

func TestAnnotateGraphFreshness(t *testing.T) {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	graph := annotateGraphFreshness(
		mapGraphWithTime("g1", base),
		base.Add(2*time.Hour),
		1,
	)
	if !graph.Meta.Freshness.IsStale {
		t.Fatalf("expected graph to be stale")
	}
	if graph.Meta.Freshness.MaxAgeHours != 1 {
		t.Fatalf("expected max_age_hours=1, got %d", graph.Meta.Freshness.MaxAgeHours)
	}
	if graph.Meta.Freshness.AgeSeconds != int64(2*time.Hour/time.Second) {
		t.Fatalf("unexpected age_seconds: %d", graph.Meta.Freshness.AgeSeconds)
	}

	fresh := annotateGraphFreshness(
		mapGraphWithTime("g2", base),
		base.Add(30*time.Minute),
		1,
	)
	if fresh.Meta.Freshness.IsStale {
		t.Fatalf("expected graph to be fresh")
	}
}

func TestGraphFilterKeepsNodesWhenNoEdgesRemain(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	index := map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single", "node_count": 2, "edge_count": 1, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	}
	graph := map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "service_id": "b", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e1", "type": "service_calls_service", "source_id": "svc:a", "target_id": "svc:b", "attributes": map[string]any{}, "confidence": 0.95, "inferred": true, "evidence_refs": []any{}},
		},
		"stats": map[string]any{"node_count": 2, "edge_count": 1, "by_node_type": map[string]any{"service": 2}, "by_edge_type": map[string]any{"service_calls_service": 1}},
		"meta": map[string]any{"services": []map[string]any{
			{"id": "a", "name": "A", "repo_path": "/repos/a", "bundle_path": "a.json"},
			{"id": "b", "name": "B", "repo_path": "/repos/b", "bundle_path": "b.json"},
		}},
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), index)
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), graph)

	mux := newMux("", graphRoot)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphs/g1", nil)
	mux.ServeHTTP(rec, withAuth(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Edges) != 0 {
		t.Fatalf("expected inferred edge filtered out by default")
	}
	if len(payload.Nodes) != 2 {
		t.Fatalf("expected nodes to remain visible when edges are filtered out, got %d", len(payload.Nodes))
	}
}

func TestStrictPublishPolicyDefaultsVerifiedAndAllowsDisputedOverride(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	index := map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single", "node_count": 4, "edge_count": 3, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	}
	graph := map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "section": "logic", "verification_state": "verified", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "ep:verified", "type": "endpoint", "label": "GET /ok", "service_id": "a", "section": "exposure", "verification_state": "verified", "attributes": map[string]any{}, "confidence": 0.95, "inferred": false},
			{"id": "ep:needs-review", "type": "endpoint", "label": "GET /review", "service_id": "a", "section": "exposure", "verification_state": "needs_review", "attributes": map[string]any{}, "confidence": 0.75, "inferred": false},
			{"id": "q:disputed", "type": "queue", "label": "orders.events", "service_id": "a", "section": "dependencies", "verification_state": "disputed", "attributes": map[string]any{}, "confidence": 0.6, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e-verified", "type": "service_exposes_endpoint", "source_id": "svc:a", "target_id": "ep:verified", "section": "exposure", "verification_state": "verified", "attributes": map[string]any{}, "confidence": 0.95, "inferred": false, "evidence_refs": []any{}},
			{"id": "e-needs-review", "type": "service_exposes_endpoint", "source_id": "svc:a", "target_id": "ep:needs-review", "section": "exposure", "verification_state": "needs_review", "attributes": map[string]any{}, "confidence": 0.75, "inferred": false, "evidence_refs": []any{}},
			{"id": "e-disputed", "type": "service_publishes_queue", "source_id": "svc:a", "target_id": "q:disputed", "section": "dependencies", "verification_state": "disputed", "attributes": map[string]any{}, "confidence": 0.6, "inferred": false, "evidence_refs": []any{}},
		},
		"stats": map[string]any{
			"node_count":   4,
			"edge_count":   3,
			"by_node_type": map[string]any{"service": 1, "endpoint": 2, "queue": 1},
			"by_edge_type": map[string]any{"service_exposes_endpoint": 2, "service_publishes_queue": 1},
		},
		"meta": map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), index)
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), graph)

	mux := newMux("", graphRoot)

	recDefault := httptest.NewRecorder()
	reqDefault := httptest.NewRequest(http.MethodGet, "/graphs/g1", nil)
	mux.ServeHTTP(recDefault, withAuth(reqDefault))
	if recDefault.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1 200, got %d body=%s", recDefault.Code, recDefault.Body.String())
	}
	var payloadDefault struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Edges []struct {
			ID string `json:"id"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(recDefault.Body.Bytes(), &payloadDefault); err != nil {
		t.Fatalf("decode default graph response: %v", err)
	}
	if len(payloadDefault.Nodes) != 2 {
		t.Fatalf("expected default policy to keep only service + verified critical node, got %d", len(payloadDefault.Nodes))
	}
	if len(payloadDefault.Edges) != 1 {
		t.Fatalf("expected default policy to keep only verified critical edge, got %d", len(payloadDefault.Edges))
	}

	recIncludeDisputed := httptest.NewRecorder()
	reqIncludeDisputed := httptest.NewRequest(http.MethodGet, "/graphs/g1?include_disputed=true", nil)
	mux.ServeHTTP(recIncludeDisputed, withAuth(reqIncludeDisputed))
	if recIncludeDisputed.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1?include_disputed=true 200, got %d body=%s", recIncludeDisputed.Code, recIncludeDisputed.Body.String())
	}
	var payloadIncludeDisputed struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Edges []struct {
			ID string `json:"id"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(recIncludeDisputed.Body.Bytes(), &payloadIncludeDisputed); err != nil {
		t.Fatalf("decode include_disputed graph response: %v", err)
	}
	if len(payloadIncludeDisputed.Nodes) != 3 {
		t.Fatalf("expected disputed override to include disputed critical node, got %d", len(payloadIncludeDisputed.Nodes))
	}
	if len(payloadIncludeDisputed.Edges) != 2 {
		t.Fatalf("expected disputed override to include disputed critical edge, got %d", len(payloadIncludeDisputed.Edges))
	}
}

func TestGraphQueryFiltersSectionClassVerificationAndProvenance(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	index := map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single", "node_count": 3, "edge_count": 2, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	}
	graph := map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "section": "logic", "class": "logic_service_core", "verification_state": "verified", "attributes": map[string]any{"adapter_id": "go-types", "provenance_version": "2026.01"}, "confidence": 1.0, "inferred": false},
			{"id": "ep:orders", "type": "endpoint", "label": "GET /orders", "service_id": "a", "section": "exposure", "class": "exposure_http_endpoint", "verification_state": "verified", "attributes": map[string]any{"adapter_id": "pyright", "provenance_version": "2026.02"}, "confidence": 0.95, "inferred": false},
			{"id": "dep:billing", "type": "dependency", "label": "billing", "service_id": "a", "section": "dependencies", "class": "dependency_http_call", "verification_state": "disputed", "attributes": map[string]any{"adapter_id": "semgrep", "provenance_version": "2026.02"}, "confidence": 0.6, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e-exposure", "type": "service_exposes_endpoint", "source_id": "svc:a", "target_id": "ep:orders", "section": "exposure", "class": "exposure_http_endpoint", "verification_state": "verified", "attributes": map[string]any{"adapter_id": "pyright", "provenance_version": "2026.02"}, "confidence": 0.95, "inferred": false, "evidence_refs": []any{}},
			{"id": "e-dependency", "type": "service_calls_dependency", "source_id": "svc:a", "target_id": "dep:billing", "section": "dependencies", "class": "dependency_http_call", "verification_state": "disputed", "attributes": map[string]any{"adapter_id": "semgrep", "provenance_version": "2026.02"}, "confidence": 0.6, "inferred": false, "evidence_refs": []any{}},
		},
		"stats": map[string]any{"node_count": 3, "edge_count": 2, "by_node_type": map[string]any{"service": 1, "endpoint": 1, "dependency": 1}, "by_edge_type": map[string]any{"service_exposes_endpoint": 1, "service_calls_dependency": 1}},
		"meta":  map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), index)
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), graph)

	mux := newMux("", graphRoot)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphs/g1/query?section=exposure&class=exposure_http_endpoint&verification_state=verified&adapter_id=pyright&provenance_version=2026.02", nil)
	mux.ServeHTTP(rec, withAuth(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1/query with section filters 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode filtered graph response: %v", err)
	}
	if len(payload.Nodes) != 1 || payload.Nodes[0].ID != "ep:orders" {
		t.Fatalf("expected only endpoint node after canonical filters, got %+v", payload.Nodes)
	}

	recCompat := httptest.NewRecorder()
	reqCompat := httptest.NewRequest(http.MethodGet, "/graphs/g1/query?section=exposure&class=exposure_http_endpoint&verification_status=verified&adapter_id=pyright&provenance_version=2026.02", nil)
	mux.ServeHTTP(recCompat, withAuth(reqCompat))
	if recCompat.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1/query with verification_status 200, got %d body=%s", recCompat.Code, recCompat.Body.String())
	}
	var payloadCompat struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(recCompat.Body.Bytes(), &payloadCompat); err != nil {
		t.Fatalf("decode compatibility graph response: %v", err)
	}
	if len(payloadCompat.Nodes) != 1 || payloadCompat.Nodes[0].ID != "ep:orders" {
		t.Fatalf("expected compatibility filter to keep endpoint node, got %+v", payloadCompat.Nodes)
	}
}

func TestGraphsBuildEndpoint(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	graphRoot := filepath.Join(outDir, "graph")

	bundlePath := filepath.Join(outDir, "bundle", "intelligence_bundle.json")
	analyzerPath := filepath.Join(outDir, "analyzers", "bundle.json")
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o755); err != nil {
		t.Fatalf("mkdir bundle dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(analyzerPath), 0o755); err != nil {
		t.Fatalf("mkdir analyzer dir: %v", err)
	}

	writeBundle(t, bundlePath, map[string]any{
		"snapshot_id": "s1",
		"entities": []map[string]any{
			{"id": "ru1", "type": "RuntimeUnit", "natural_key": "go|main|main.go", "attributes": map[string]any{"language": "go"}, "evidence_ids": []string{"e1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
		},
	})
	writeJSONFile(t, analyzerPath, map[string]any{
		"facts":     []any{},
		"evidence":  []any{},
		"generated": "2026-01-01T00:00:00Z",
	})

	mux := newMux(bundlePath, graphRoot)
	reqBody := map[string]any{
		"mode":                 "single",
		"out_dir":              outDir,
		"service_id":           "svc.local",
		"service_name":         "Local",
		"bundle_path":          bundlePath,
		"analyzer_bundle_path": analyzerPath,
		"base_urls":            []string{"http://localhost:8080"},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphs", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, withAuth(req))

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected POST /graphs 201, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		GraphID   string `json:"graph_id"`
		GraphPath string `json:"graph_path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.GraphID == "" || payload.GraphPath == "" {
		t.Fatalf("expected graph id and graph path in response")
	}
	if _, err := os.Stat(payload.GraphPath); err != nil {
		t.Fatalf("expected graph artifact to exist: %v", err)
	}

	recAlias := httptest.NewRecorder()
	reqAlias := httptest.NewRequest(http.MethodPost, "/graphs/build", bytes.NewReader(data))
	reqAlias.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recAlias, withAuth(reqAlias))
	if recAlias.Code != http.StatusCreated {
		t.Fatalf("expected POST /graphs/build 201, got %d body=%s", recAlias.Code, recAlias.Body.String())
	}
}

func TestRuntimeEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	index := map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single", "node_count": 3, "edge_count": 2, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	}
	graph := map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "section": "logic", "class": "logic_service_core", "verification_state": "verified", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "ep:orders", "type": "endpoint", "label": "GET /orders", "service_id": "a", "section": "exposure", "class": "exposure_http_endpoint", "verification_state": "verified", "attributes": map[string]any{}, "confidence": 0.95, "inferred": false},
			{"id": "dep:billing", "type": "dependency", "label": "billing", "service_id": "a", "section": "dependencies", "class": "dependency_http_call", "verification_state": "disputed", "attributes": map[string]any{}, "confidence": 0.6, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e-exposure", "type": "service_exposes_endpoint", "source_id": "svc:a", "target_id": "ep:orders", "section": "exposure", "class": "exposure_http_endpoint", "verification_state": "verified", "attributes": map[string]any{}, "confidence": 0.95, "inferred": false, "evidence_refs": []map[string]any{{"file_path": "main.go", "start_line": 10}}},
			{"id": "e-dependency", "type": "service_calls_dependency", "source_id": "svc:a", "target_id": "dep:billing", "section": "dependencies", "class": "dependency_http_call", "verification_state": "disputed", "attributes": map[string]any{}, "confidence": 0.6, "inferred": false, "evidence_refs": []any{}},
		},
		"stats": map[string]any{"node_count": 3, "edge_count": 2, "by_node_type": map[string]any{"service": 1, "endpoint": 1, "dependency": 1}, "by_edge_type": map[string]any{"service_exposes_endpoint": 1, "service_calls_dependency": 1}},
		"meta":  map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), index)
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), graph)

	mux := newMux("", graphRoot)

	recPlan := httptest.NewRecorder()
	reqPlan := httptest.NewRequest(http.MethodGet, "/runtime/plan", nil)
	mux.ServeHTTP(recPlan, withAuth(reqPlan))
	if recPlan.Code != http.StatusOK {
		t.Fatalf("expected /runtime/plan 200, got %d body=%s", recPlan.Code, recPlan.Body.String())
	}
	var planPayload struct {
		Enabled         bool   `json:"enabled"`
		PublishBlocking bool   `json:"publish_blocking"`
		Phase           string `json:"phase"`
	}
	if err := json.Unmarshal(recPlan.Body.Bytes(), &planPayload); err != nil {
		t.Fatalf("decode runtime plan response: %v", err)
	}
	if planPayload.Enabled {
		t.Fatalf("expected runtime plan enabled=false")
	}
	if planPayload.PublishBlocking {
		t.Fatalf("expected runtime plan publish_blocking=false")
	}
	if planPayload.Phase == "" {
		t.Fatalf("expected runtime plan phase")
	}

	body := []byte(`{
		"tenant_id":"default",
		"graph_id":"g1",
		"claims":[{"graph_id":"g1","edge_id":"e1"},{"graph_id":"g1","node_id":"n1"}],
		"observations":[
			{"source_system":"gateway","signal_type":"http","attributes":{"edge_id":"e1"}},
			{"source_system":"broker","signal_type":"queue","attributes":{"node_id":"n1","contradicts":"true"}}
		]
	}`)
	recReconcile := httptest.NewRecorder()
	reqReconcile := httptest.NewRequest(http.MethodPost, "/runtime/reconcile", bytes.NewReader(body))
	reqReconcile.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recReconcile, withAuth(reqReconcile))
	if recReconcile.Code != http.StatusOK {
		t.Fatalf("expected /runtime/reconcile 200, got %d body=%s", recReconcile.Code, recReconcile.Body.String())
	}
	var reconcilePayload struct {
		ReconcileID  string   `json:"reconcile_id"`
		GraphID      string   `json:"graph_id"`
		Confirmed    []string `json:"confirmed"`
		Contradicted []string `json:"contradicted"`
	}
	if err := json.Unmarshal(recReconcile.Body.Bytes(), &reconcilePayload); err != nil {
		t.Fatalf("decode runtime reconcile response: %v", err)
	}
	if reconcilePayload.GraphID != "g1" {
		t.Fatalf("expected graph_id g1, got %q", reconcilePayload.GraphID)
	}
	if len(reconcilePayload.Confirmed) != 1 || reconcilePayload.Confirmed[0] != "edge:e1" {
		t.Fatalf("expected confirmed edge:e1, got %+v", reconcilePayload.Confirmed)
	}
	if len(reconcilePayload.Contradicted) != 1 || reconcilePayload.Contradicted[0] != "node:n1" {
		t.Fatalf("expected contradicted node:n1, got %+v", reconcilePayload.Contradicted)
	}
	if reconcilePayload.ReconcileID == "" {
		t.Fatalf("expected reconcile_id in runtime reconcile response")
	}

	recReconcileHistory := httptest.NewRecorder()
	reqReconcileHistory := httptest.NewRequest(http.MethodGet, "/runtime/reconcile?limit=10", nil)
	mux.ServeHTTP(recReconcileHistory, withAuth(reqReconcileHistory))
	if recReconcileHistory.Code != http.StatusOK {
		t.Fatalf("expected /runtime/reconcile history 200, got %d body=%s", recReconcileHistory.Code, recReconcileHistory.Body.String())
	}
	var historyPayload struct {
		Runs []struct {
			ReconcileID string `json:"reconcile_id"`
			GeneratedAt string `json:"generated_at"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(recReconcileHistory.Body.Bytes(), &historyPayload); err != nil {
		t.Fatalf("decode runtime reconcile history response: %v", err)
	}
	if len(historyPayload.Runs) == 0 || historyPayload.Runs[0].ReconcileID == "" {
		t.Fatalf("expected runtime reconcile history entries")
	}

	recReconcileByID := httptest.NewRecorder()
	reqReconcileByID := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/"+reconcilePayload.ReconcileID, nil)
	mux.ServeHTTP(recReconcileByID, withAuth(reqReconcileByID))
	if recReconcileByID.Code != http.StatusOK {
		t.Fatalf("expected /runtime/reconcile/{id} 200, got %d body=%s", recReconcileByID.Code, recReconcileByID.Body.String())
	}
	var byIDPayload struct {
		ReconcileID string `json:"reconcile_id"`
		Result      struct {
			GraphID string `json:"graph_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recReconcileByID.Body.Bytes(), &byIDPayload); err != nil {
		t.Fatalf("decode runtime reconcile by id response: %v", err)
	}
	if byIDPayload.ReconcileID != reconcilePayload.ReconcileID || byIDPayload.Result.GraphID != "g1" {
		t.Fatalf("unexpected runtime reconcile by id payload: %+v", byIDPayload)
	}

	recDeleteWrongTenant := httptest.NewRecorder()
	reqDeleteWrongTenant := httptest.NewRequest(http.MethodDelete, "/runtime/reconcile/"+reconcilePayload.ReconcileID, nil)
	reqDeleteWrongTenant = withAuthTenantRoleScope(reqDeleteWrongTenant, "other", "analyst", "graph:read")
	mux.ServeHTTP(recDeleteWrongTenant, reqDeleteWrongTenant)
	if recDeleteWrongTenant.Code != http.StatusForbidden {
		t.Fatalf("expected DELETE /runtime/reconcile/{id} tenant mismatch 403, got %d body=%s", recDeleteWrongTenant.Code, recDeleteWrongTenant.Body.String())
	}

	recDeleteByID := httptest.NewRecorder()
	reqDeleteByID := httptest.NewRequest(http.MethodDelete, "/runtime/reconcile/"+reconcilePayload.ReconcileID, nil)
	mux.ServeHTTP(recDeleteByID, withAuth(reqDeleteByID))
	if recDeleteByID.Code != http.StatusOK {
		t.Fatalf("expected DELETE /runtime/reconcile/{id} 200, got %d body=%s", recDeleteByID.Code, recDeleteByID.Body.String())
	}

	recByIDAfterDelete := httptest.NewRecorder()
	reqByIDAfterDelete := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/"+reconcilePayload.ReconcileID, nil)
	mux.ServeHTTP(recByIDAfterDelete, withAuth(reqByIDAfterDelete))
	if recByIDAfterDelete.Code != http.StatusNotFound {
		t.Fatalf("expected GET /runtime/reconcile/{id} after delete to return 404, got %d", recByIDAfterDelete.Code)
	}

	seedIDs := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		recSeed := httptest.NewRecorder()
		reqSeed := httptest.NewRequest(http.MethodPost, "/runtime/reconcile", bytes.NewReader(body))
		reqSeed.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(recSeed, withAuth(reqSeed))
		if recSeed.Code != http.StatusOK {
			t.Fatalf("expected seeded runtime reconcile post 200, got %d body=%s", recSeed.Code, recSeed.Body.String())
		}
		var seeded struct {
			ReconcileID string `json:"reconcile_id"`
		}
		if err := json.Unmarshal(recSeed.Body.Bytes(), &seeded); err != nil {
			t.Fatalf("decode seeded runtime reconcile response: %v", err)
		}
		if seeded.ReconcileID == "" {
			t.Fatalf("expected reconcile_id for seeded runtime run")
		}
		seedIDs = append(seedIDs, seeded.ReconcileID)
	}
	recCompareRuns := httptest.NewRecorder()
	reqCompareRuns := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/compare?from="+seedIDs[0]+"&to="+seedIDs[1], nil)
	mux.ServeHTTP(recCompareRuns, withAuth(reqCompareRuns))
	if recCompareRuns.Code != http.StatusOK {
		t.Fatalf("expected GET /runtime/reconcile/compare 200, got %d body=%s", recCompareRuns.Code, recCompareRuns.Body.String())
	}
	var compareRunsPayload struct {
		FromReconcileID string `json:"from_reconcile_id"`
		ToReconcileID   string `json:"to_reconcile_id"`
	}
	if err := json.Unmarshal(recCompareRuns.Body.Bytes(), &compareRunsPayload); err != nil {
		t.Fatalf("decode runtime reconcile compare response: %v", err)
	}
	if compareRunsPayload.FromReconcileID != seedIDs[0] || compareRunsPayload.ToReconcileID != seedIDs[1] {
		t.Fatalf("unexpected runtime compare payload ids: %+v", compareRunsPayload)
	}
	recSeedByID := httptest.NewRecorder()
	reqSeedByID := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/"+seedIDs[0], nil)
	mux.ServeHTTP(recSeedByID, withAuth(reqSeedByID))
	if recSeedByID.Code != http.StatusOK {
		t.Fatalf("expected GET /runtime/reconcile/{id} for seeded run 200, got %d body=%s", recSeedByID.Code, recSeedByID.Body.String())
	}
	var seedByIDPayload struct {
		GeneratedAt string `json:"generated_at"`
	}
	if err := json.Unmarshal(recSeedByID.Body.Bytes(), &seedByIDPayload); err != nil {
		t.Fatalf("decode seeded runtime reconcile by id response: %v", err)
	}
	if seedByIDPayload.GeneratedAt == "" {
		t.Fatalf("expected generated_at in seeded runtime reconcile by id response")
	}
	recCompareWrongTenant := httptest.NewRecorder()
	reqCompareWrongTenant := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/compare?from="+seedIDs[0]+"&to="+seedIDs[1], nil)
	reqCompareWrongTenant = withAuthTenantRoleScope(reqCompareWrongTenant, "other", "analyst", "graph:read")
	mux.ServeHTTP(recCompareWrongTenant, reqCompareWrongTenant)
	if recCompareWrongTenant.Code != http.StatusForbidden {
		t.Fatalf("expected runtime compare tenant mismatch 403, got %d body=%s", recCompareWrongTenant.Code, recCompareWrongTenant.Body.String())
	}

	recReport := httptest.NewRecorder()
	reqReport := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/report?graph_id=g1", nil)
	mux.ServeHTTP(recReport, withAuth(reqReport))
	if recReport.Code != http.StatusOK {
		t.Fatalf("expected GET /runtime/reconcile/report 200, got %d body=%s", recReport.Code, recReport.Body.String())
	}
	var reportPayload struct {
		TotalRuns         int `json:"total_runs"`
		TotalConfirmed    int `json:"total_confirmed"`
		TotalContradicted int `json:"total_contradicted"`
		TotalUnmapped     int `json:"total_runtime_only_unmapped"`
		TopGraphs         []struct {
			GraphID string `json:"graph_id"`
			Runs    int    `json:"runs"`
		} `json:"top_graphs"`
	}
	if err := json.Unmarshal(recReport.Body.Bytes(), &reportPayload); err != nil {
		t.Fatalf("decode runtime reconcile report response: %v", err)
	}
	if reportPayload.TotalRuns < 2 {
		t.Fatalf("expected report total_runs >= 2, got %d", reportPayload.TotalRuns)
	}
	if reportPayload.TotalConfirmed < 2 {
		t.Fatalf("expected report total_confirmed >= 2, got %d", reportPayload.TotalConfirmed)
	}
	if reportPayload.TotalContradicted < 2 {
		t.Fatalf("expected report total_contradicted >= 2, got %d", reportPayload.TotalContradicted)
	}
	if reportPayload.TotalUnmapped != 0 {
		t.Fatalf("expected report runtime_only_unmapped total 0, got %d", reportPayload.TotalUnmapped)
	}
	if len(reportPayload.TopGraphs) == 0 || reportPayload.TopGraphs[0].GraphID != "g1" {
		t.Fatalf("expected runtime report top_graphs to include g1, got %+v", reportPayload.TopGraphs)
	}

	recReportFiltered := httptest.NewRecorder()
	reqReportFiltered := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/report?from="+seedByIDPayload.GeneratedAt+"&to="+seedByIDPayload.GeneratedAt, nil)
	mux.ServeHTTP(recReportFiltered, withAuth(reqReportFiltered))
	if recReportFiltered.Code != http.StatusOK {
		t.Fatalf("expected GET /runtime/reconcile/report with from/to 200, got %d body=%s", recReportFiltered.Code, recReportFiltered.Body.String())
	}
	var reportFilteredPayload struct {
		TotalRuns int `json:"total_runs"`
	}
	if err := json.Unmarshal(recReportFiltered.Body.Bytes(), &reportFilteredPayload); err != nil {
		t.Fatalf("decode filtered runtime reconcile report response: %v", err)
	}
	if reportFilteredPayload.TotalRuns < 1 {
		t.Fatalf("expected filtered runtime report total_runs >= 1, got %d", reportFilteredPayload.TotalRuns)
	}

	recReportBadFrom := httptest.NewRecorder()
	reqReportBadFrom := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/report?from=not-a-time", nil)
	mux.ServeHTTP(recReportBadFrom, withAuth(reqReportBadFrom))
	if recReportBadFrom.Code != http.StatusBadRequest {
		t.Fatalf("expected runtime report bad from 400, got %d", recReportBadFrom.Code)
	}

	recReportTenantMismatch := httptest.NewRecorder()
	reqReportTenantMismatch := httptest.NewRequest(http.MethodGet, "/runtime/reconcile/report", nil)
	reqReportTenantMismatch = withAuthTenantRoleScope(reqReportTenantMismatch, "other", "analyst", "graph:read")
	mux.ServeHTTP(recReportTenantMismatch, reqReportTenantMismatch)
	if recReportTenantMismatch.Code != http.StatusOK {
		t.Fatalf("expected runtime report with other tenant to return 200, got %d body=%s", recReportTenantMismatch.Code, recReportTenantMismatch.Body.String())
	}
	var reportTenantMismatchPayload struct {
		TotalRuns int `json:"total_runs"`
	}
	if err := json.Unmarshal(recReportTenantMismatch.Body.Bytes(), &reportTenantMismatchPayload); err != nil {
		t.Fatalf("decode runtime report tenant mismatch payload: %v", err)
	}
	if reportTenantMismatchPayload.TotalRuns != 0 {
		t.Fatalf("expected runtime report for other tenant to return 0 runs, got %d", reportTenantMismatchPayload.TotalRuns)
	}

	recPrune := httptest.NewRecorder()
	reqPrune := httptest.NewRequest(http.MethodDelete, "/runtime/reconcile?keep_latest=1", nil)
	mux.ServeHTTP(recPrune, withAuth(reqPrune))
	if recPrune.Code != http.StatusOK {
		t.Fatalf("expected DELETE /runtime/reconcile?keep_latest=1 200, got %d body=%s", recPrune.Code, recPrune.Body.String())
	}
	var prunePayload struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(recPrune.Body.Bytes(), &prunePayload); err != nil {
		t.Fatalf("decode runtime reconcile prune response: %v", err)
	}
	if prunePayload.Deleted < 1 {
		t.Fatalf("expected prune to delete at least one run, got %d", prunePayload.Deleted)
	}

	recClaimsDefault := httptest.NewRecorder()
	reqClaimsDefault := httptest.NewRequest(http.MethodGet, "/runtime/claims/g1", nil)
	mux.ServeHTTP(recClaimsDefault, withAuth(reqClaimsDefault))
	if recClaimsDefault.Code != http.StatusOK {
		t.Fatalf("expected /runtime/claims/g1 200, got %d body=%s", recClaimsDefault.Code, recClaimsDefault.Body.String())
	}
	var claimsDefaultPayload struct {
		Count  int `json:"count"`
		Claims []struct {
			NodeID string `json:"node_id"`
			EdgeID string `json:"edge_id"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(recClaimsDefault.Body.Bytes(), &claimsDefaultPayload); err != nil {
		t.Fatalf("decode runtime claims default response: %v", err)
	}
	// default strict policy should exclude disputed dependency claims
	if claimsDefaultPayload.Count != 2 {
		t.Fatalf("expected 2 default claims (verified exposure node+edge), got %d", claimsDefaultPayload.Count)
	}

	recClaimsIncludeDisputed := httptest.NewRecorder()
	reqClaimsIncludeDisputed := httptest.NewRequest(http.MethodGet, "/runtime/claims/g1?include_disputed=true", nil)
	mux.ServeHTTP(recClaimsIncludeDisputed, withAuth(reqClaimsIncludeDisputed))
	if recClaimsIncludeDisputed.Code != http.StatusOK {
		t.Fatalf("expected /runtime/claims/g1?include_disputed=true 200, got %d body=%s", recClaimsIncludeDisputed.Code, recClaimsIncludeDisputed.Body.String())
	}
	var claimsIncludeDisputedPayload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(recClaimsIncludeDisputed.Body.Bytes(), &claimsIncludeDisputedPayload); err != nil {
		t.Fatalf("decode runtime claims include_disputed response: %v", err)
	}
	if claimsIncludeDisputedPayload.Count != 4 {
		t.Fatalf("expected 4 claims with disputed override, got %d", claimsIncludeDisputedPayload.Count)
	}
}

func TestUIIndexIsServed(t *testing.T) {
	mux := newMux("", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	mux.ServeHTTP(rec, withAuth(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected / 200, got %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("DiffMind Service Graph")) {
		t.Fatalf("expected UI title in response")
	}
}

func TestAuthRequiredForGraphEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single", "node_count": 1, "edge_count": 0, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	})
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false}},
		"edges": []map[string]any{},
		"stats": map[string]any{"node_count": 1, "edge_count": 0, "by_node_type": map[string]any{"service": 1}, "by_edge_type": map[string]any{}},
		"meta":  map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	})
	mux := newMux("", graphRoot)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphs", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth headers, got %d", rec.Code)
	}

	recRuntime := httptest.NewRecorder()
	reqRuntime := httptest.NewRequest(http.MethodGet, "/runtime/plan", nil)
	mux.ServeHTTP(recRuntime, reqRuntime)
	if recRuntime.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on runtime endpoint without auth headers, got %d", recRuntime.Code)
	}

	recRuntimeHistory := httptest.NewRecorder()
	reqRuntimeHistory := httptest.NewRequest(http.MethodGet, "/runtime/reconcile", nil)
	mux.ServeHTTP(recRuntimeHistory, reqRuntimeHistory)
	if recRuntimeHistory.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on runtime reconcile history without auth headers, got %d", recRuntimeHistory.Code)
	}

	recRuntimeClaims := httptest.NewRecorder()
	reqRuntimeClaims := httptest.NewRequest(http.MethodGet, "/runtime/claims/g1", nil)
	mux.ServeHTTP(recRuntimeClaims, reqRuntimeClaims)
	if recRuntimeClaims.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on runtime claims endpoint without auth headers, got %d", recRuntimeClaims.Code)
	}

	recProductTemplates := httptest.NewRecorder()
	reqProductTemplates := httptest.NewRequest(http.MethodGet, "/products/templates", nil)
	mux.ServeHTTP(recProductTemplates, reqProductTemplates)
	if recProductTemplates.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products templates endpoint without auth headers, got %d", recProductTemplates.Code)
	}

	recProductQuestions := httptest.NewRecorder()
	reqProductQuestions := httptest.NewRequest(http.MethodGet, "/products/questions", nil)
	mux.ServeHTTP(recProductQuestions, reqProductQuestions)
	if recProductQuestions.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products questions endpoint without auth headers, got %d", recProductQuestions.Code)
	}

	recProductCoverage := httptest.NewRecorder()
	reqProductCoverage := httptest.NewRequest(http.MethodGet, "/products/questions/coverage", nil)
	mux.ServeHTTP(recProductCoverage, reqProductCoverage)
	if recProductCoverage.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products questions coverage endpoint without auth headers, got %d", recProductCoverage.Code)
	}

	recProductQuestionRun := httptest.NewRecorder()
	reqProductQuestionRun := httptest.NewRequest(http.MethodPost, "/products/questions/run", bytes.NewReader([]byte(`{"vars":{"graph_id":"g1"}}`)))
	reqProductQuestionRun.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recProductQuestionRun, reqProductQuestionRun)
	if recProductQuestionRun.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products questions run endpoint without auth headers, got %d", recProductQuestionRun.Code)
	}

	recFinalReadiness := httptest.NewRecorder()
	reqFinalReadiness := httptest.NewRequest(http.MethodGet, "/final/readiness", nil)
	mux.ServeHTTP(recFinalReadiness, reqFinalReadiness)
	if recFinalReadiness.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on final readiness endpoint without auth headers, got %d", recFinalReadiness.Code)
	}
}

func TestGraphRedactsSensitiveByDefault(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	index := map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "tenant_id": "default", "mode": "single", "node_count": 1, "edge_count": 0, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	}
	graph := map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "attributes": map[string]any{"api_token": "sk_live_123"}, "confidence": 1.0, "inferred": false},
		},
		"edges": []map[string]any{},
		"stats": map[string]any{"node_count": 1, "edge_count": 0, "by_node_type": map[string]any{"service": 1}, "by_edge_type": map[string]any{}},
		"meta":  map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), index)
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), graph)

	mux := newMux("", graphRoot)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphs/g1", nil)
	req = withAuthRoleScope(req, "analyst", "graph:read")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Nodes []struct {
			Attributes map[string]any `json:"attributes"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode graph response: %v", err)
	}
	if got := payload.Nodes[0].Attributes["api_token"]; got != "[REDACTED]" {
		t.Fatalf("expected redacted api_token, got %v", got)
	}
}

func TestComplianceAuditEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(graphRoot, 0o755); err != nil {
		t.Fatalf("mkdir graph root: %v", err)
	}
	if err := audit.AppendEvent(filepath.Dir(graphRoot), audit.Event{
		Timestamp: time.Now().UTC().Add(-48 * time.Hour),
		Action:    "query_graph",
		TenantID:  "default",
		Principal: "u1",
		Method:    "GET",
		Path:      "/graphs/g1",
		Decision:  "allow",
	}); err != nil {
		t.Fatalf("seed audit event: %v", err)
	}
	if err := audit.AppendEvent(filepath.Dir(graphRoot), audit.Event{
		Timestamp: time.Now().UTC(),
		Action:    "query_graph",
		TenantID:  "default",
		Principal: "u1",
		Method:    "GET",
		Path:      "/graphs/g1",
		Decision:  "allow",
	}); err != nil {
		t.Fatalf("seed audit event: %v", err)
	}
	mux := newMux("", graphRoot)

	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/compliance/audit?limit=10", nil)
	reqList = withAuthRoleScope(reqList, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("expected audit list 200, got %d body=%s", recList.Code, recList.Body.String())
	}

	recExport := httptest.NewRecorder()
	reqExport := httptest.NewRequest(http.MethodPost, "/compliance/audit/export", bytes.NewReader([]byte(`{"encrypt":false}`)))
	reqExport = withAuthRoleScope(reqExport, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recExport, reqExport)
	if recExport.Code != http.StatusOK {
		t.Fatalf("expected audit export 200, got %d body=%s", recExport.Code, recExport.Body.String())
	}
	var exportPayload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(recExport.Body.Bytes(), &exportPayload); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if exportPayload.Path == "" {
		t.Fatalf("expected export path")
	}
	if _, err := os.Stat(exportPayload.Path); err != nil {
		t.Fatalf("expected export artifact: %v", err)
	}

	recPrune := httptest.NewRecorder()
	reqPrune := httptest.NewRequest(http.MethodPost, "/compliance/audit/retention", bytes.NewReader([]byte(`{"retain_days":1}`)))
	reqPrune = withAuthRoleScope(reqPrune, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recPrune, reqPrune)
	if recPrune.Code != http.StatusOK {
		t.Fatalf("expected audit retention 200, got %d body=%s", recPrune.Code, recPrune.Body.String())
	}
}

func TestProductEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	index := map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "tenant_id": "default", "mode": "single", "node_count": 4, "edge_count": 3, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	}
	graph := map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "attributes": map[string]any{"verification_status": "needs_review"}, "confidence": 1.0, "inferred": false},
			{"id": "svc:b", "type": "service", "label": "B", "service_id": "b", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "risk:1", "type": "dependency_risk", "label": "Risk 1", "service_id": "a", "attributes": map[string]any{}, "confidence": 0.9, "inferred": false},
			{"id": "conflict:1", "type": "conflict", "label": "Conflict 1", "service_id": "a", "attributes": map[string]any{}, "confidence": 0.9, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e1", "type": "service_calls_service", "source_id": "svc:a", "target_id": "svc:b", "attributes": map[string]any{}, "confidence": 0.6, "inferred": false, "evidence_refs": []any{}},
			{"id": "e2", "type": "service_has_dependency_risk", "source_id": "svc:a", "target_id": "risk:1", "attributes": map[string]any{"verification_status": "rejected"}, "confidence": 0.9, "inferred": false, "evidence_refs": []any{}},
			{"id": "e3", "type": "service_has_conflict", "source_id": "svc:a", "target_id": "conflict:1", "attributes": map[string]any{}, "confidence": 0.9, "inferred": false, "evidence_refs": []any{}},
		},
		"stats": map[string]any{"node_count": 4, "edge_count": 3, "by_node_type": map[string]any{"service": 2, "dependency_risk": 1, "conflict": 1}, "by_edge_type": map[string]any{"service_calls_service": 1, "service_has_dependency_risk": 1, "service_has_conflict": 1}},
		"meta":  map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), index)
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), graph)
	mux := newMux("", graphRoot)

	prBody := []byte(`{"graph_id":"g1","changed_nodes":["svc:a"],"max_findings":20}`)
	recPR := httptest.NewRecorder()
	reqPR := httptest.NewRequest(http.MethodPost, "/products/pr-review?explain=true", bytes.NewReader(prBody))
	reqPR = withAuthRoleScope(reqPR, "analyst", "graph:read")
	mux.ServeHTTP(recPR, reqPR)
	if recPR.Code != http.StatusOK {
		t.Fatalf("expected pr-review 200, got %d body=%s", recPR.Code, recPR.Body.String())
	}

	recDocs := httptest.NewRecorder()
	reqDocs := httptest.NewRequest(http.MethodGet, "/products/docs/g1?service=a&explain=true", nil)
	reqDocs = withAuthRoleScope(reqDocs, "analyst", "graph:read")
	mux.ServeHTTP(recDocs, reqDocs)
	if recDocs.Code != http.StatusOK {
		t.Fatalf("expected docs 200, got %d body=%s", recDocs.Code, recDocs.Body.String())
	}

	recMapper := httptest.NewRecorder()
	reqMapper := httptest.NewRequest(http.MethodGet, "/products/mapper/g1?service=a&explain=true", nil)
	reqMapper = withAuthRoleScope(reqMapper, "analyst", "graph:read")
	mux.ServeHTTP(recMapper, reqMapper)
	if recMapper.Code != http.StatusOK {
		t.Fatalf("expected mapper 200, got %d body=%s", recMapper.Code, recMapper.Body.String())
	}

	recTemplates := httptest.NewRecorder()
	reqTemplates := httptest.NewRequest(http.MethodGet, "/products/templates", nil)
	reqTemplates = withAuthRoleScope(reqTemplates, "analyst", "graph:read")
	mux.ServeHTTP(recTemplates, reqTemplates)
	if recTemplates.Code != http.StatusOK {
		t.Fatalf("expected product templates 200, got %d body=%s", recTemplates.Code, recTemplates.Body.String())
	}
	var templatesPayload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(recTemplates.Body.Bytes(), &templatesPayload); err != nil {
		t.Fatalf("decode product templates: %v", err)
	}
	if templatesPayload.Count == 0 {
		t.Fatalf("expected non-empty product template catalog")
	}

	execBody := []byte(`{
		"template_id":"docs-service",
		"vars":{"graph_id":"g1","service_id":"a"}
	}`)
	recTemplateExec := httptest.NewRecorder()
	reqTemplateExec := httptest.NewRequest(http.MethodPost, "/products/templates/execute", bytes.NewReader(execBody))
	reqTemplateExec.Header.Set("Content-Type", "application/json")
	reqTemplateExec = withAuthRoleScope(reqTemplateExec, "analyst", "graph:read")
	mux.ServeHTTP(recTemplateExec, reqTemplateExec)
	if recTemplateExec.Code != http.StatusOK {
		t.Fatalf("expected product template execute 200, got %d body=%s", recTemplateExec.Code, recTemplateExec.Body.String())
	}
	var templateExecPayload struct {
		TemplateID string         `json:"template_id"`
		Status     int            `json:"status"`
		Result     map[string]any `json:"result"`
	}
	if err := json.Unmarshal(recTemplateExec.Body.Bytes(), &templateExecPayload); err != nil {
		t.Fatalf("decode product template execute payload: %v", err)
	}
	if templateExecPayload.TemplateID != "docs-service" || templateExecPayload.Status != http.StatusOK {
		t.Fatalf("unexpected product template execute metadata: %+v", templateExecPayload)
	}
	if len(templateExecPayload.Result) == 0 {
		t.Fatalf("expected non-empty template execute result payload")
	}
	if _, ok := templateExecPayload.Result["overview"]; !ok {
		resultPayload, _ := templateExecPayload.Result["result"].(map[string]any)
		if len(resultPayload) == 0 || resultPayload["overview"] == nil {
			t.Fatalf("expected template execute overview in result payload, got %+v", templateExecPayload.Result)
		}
	}

	recQuestions := httptest.NewRecorder()
	reqQuestions := httptest.NewRequest(http.MethodGet, "/products/questions", nil)
	reqQuestions = withAuthRoleScope(reqQuestions, "analyst", "graph:read")
	mux.ServeHTTP(recQuestions, reqQuestions)
	if recQuestions.Code != http.StatusOK {
		t.Fatalf("expected product questions 200, got %d body=%s", recQuestions.Code, recQuestions.Body.String())
	}
	var questionsPayload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(recQuestions.Body.Bytes(), &questionsPayload); err != nil {
		t.Fatalf("decode product questions payload: %v", err)
	}
	if questionsPayload.Count == 0 {
		t.Fatalf("expected non-empty product question catalog")
	}

	questionExecBody := []byte(`{
		"question_id":"q-service-external",
		"vars":{"graph_id":"g1","service_id":"a"}
	}`)
	recQuestionExec := httptest.NewRecorder()
	reqQuestionExec := httptest.NewRequest(http.MethodPost, "/products/questions/execute", bytes.NewReader(questionExecBody))
	reqQuestionExec.Header.Set("Content-Type", "application/json")
	reqQuestionExec = withAuthRoleScope(reqQuestionExec, "analyst", "graph:read")
	mux.ServeHTTP(recQuestionExec, reqQuestionExec)
	if recQuestionExec.Code != http.StatusOK {
		t.Fatalf("expected question execute 200, got %d body=%s", recQuestionExec.Code, recQuestionExec.Body.String())
	}
	var questionExecPayload struct {
		QuestionID string `json:"question_id"`
		Status     int    `json:"status"`
	}
	if err := json.Unmarshal(recQuestionExec.Body.Bytes(), &questionExecPayload); err != nil {
		t.Fatalf("decode question execute payload: %v", err)
	}
	if questionExecPayload.QuestionID != "q-service-external" || questionExecPayload.Status != http.StatusOK {
		t.Fatalf("unexpected question execute payload: %+v", questionExecPayload)
	}

	recQuestionCoverage := httptest.NewRecorder()
	reqQuestionCoverage := httptest.NewRequest(http.MethodGet, "/products/questions/coverage", nil)
	reqQuestionCoverage = withAuthRoleScope(reqQuestionCoverage, "analyst", "graph:read")
	mux.ServeHTTP(recQuestionCoverage, reqQuestionCoverage)
	if recQuestionCoverage.Code != http.StatusOK {
		t.Fatalf("expected question coverage 200, got %d body=%s", recQuestionCoverage.Code, recQuestionCoverage.Body.String())
	}
	var coveragePayload struct {
		Total         int     `json:"total"`
		Covered       int     `json:"covered"`
		CoverageRatio float64 `json:"coverage_ratio"`
	}
	if err := json.Unmarshal(recQuestionCoverage.Body.Bytes(), &coveragePayload); err != nil {
		t.Fatalf("decode question coverage payload: %v", err)
	}
	if coveragePayload.Total == 0 || coveragePayload.Covered == 0 {
		t.Fatalf("expected non-zero question coverage totals, got %+v", coveragePayload)
	}
	if coveragePayload.CoverageRatio <= 0 {
		t.Fatalf("expected positive question coverage ratio, got %+v", coveragePayload)
	}

	questionRunBody := []byte(`{
		"vars":{"graph_id":"g1","service_id":"a","node_id":"svc:a"}
	}`)
	recQuestionRun := httptest.NewRecorder()
	reqQuestionRun := httptest.NewRequest(http.MethodPost, "/products/questions/run", bytes.NewReader(questionRunBody))
	reqQuestionRun.Header.Set("Content-Type", "application/json")
	reqQuestionRun = withAuthRoleScope(reqQuestionRun, "analyst", "graph:read")
	mux.ServeHTTP(recQuestionRun, reqQuestionRun)
	if recQuestionRun.Code != http.StatusOK {
		t.Fatalf("expected question run 200, got %d body=%s", recQuestionRun.Code, recQuestionRun.Body.String())
	}
	var questionRunPayload struct {
		Total         int  `json:"total"`
		Succeeded     int  `json:"succeeded"`
		Failed        int  `json:"failed"`
		OverallPassed bool `json:"overall_passed"`
	}
	if err := json.Unmarshal(recQuestionRun.Body.Bytes(), &questionRunPayload); err != nil {
		t.Fatalf("decode question run payload: %v", err)
	}
	if questionRunPayload.Total == 0 {
		t.Fatalf("expected question run total > 0")
	}
	if questionRunPayload.Succeeded == 0 || questionRunPayload.Failed != 0 || !questionRunPayload.OverallPassed {
		t.Fatalf("unexpected question run summary: %+v", questionRunPayload)
	}

	recGov := httptest.NewRecorder()
	reqGov := httptest.NewRequest(http.MethodGet, "/products/governance/g1?explain=true", nil)
	reqGov = withAuthRoleScope(reqGov, "analyst", "graph:read")
	mux.ServeHTTP(recGov, reqGov)
	if recGov.Code != http.StatusOK {
		t.Fatalf("expected governance 200, got %d body=%s", recGov.Code, recGov.Body.String())
	}

	var govPayload struct {
		Result struct {
			RiskPosture string `json:"risk_posture"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recGov.Body.Bytes(), &govPayload); err != nil {
		t.Fatalf("decode governance: %v", err)
	}
	if govPayload.Result.RiskPosture == "" {
		t.Fatalf("expected governance risk posture")
	}

	runtimeBody := []byte(`{
		"tenant_id":"default",
		"graph_id":"g1",
		"claims":[{"graph_id":"g1","edge_id":"e1"}],
		"observations":[{"source_system":"gateway","signal_type":"http","attributes":{"edge_id":"e1"}}]
	}`)
	recRuntime := httptest.NewRecorder()
	reqRuntime := httptest.NewRequest(http.MethodPost, "/runtime/reconcile", bytes.NewReader(runtimeBody))
	reqRuntime.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recRuntime, withAuth(reqRuntime))
	if recRuntime.Code != http.StatusOK {
		t.Fatalf("expected runtime reconcile seed 200, got %d body=%s", recRuntime.Code, recRuntime.Body.String())
	}

	recOpsMetrics := httptest.NewRecorder()
	reqOpsMetrics := httptest.NewRequest(http.MethodGet, "/ops/metrics", nil)
	reqOpsMetrics = withAuthRoleScope(reqOpsMetrics, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recOpsMetrics, reqOpsMetrics)
	if recOpsMetrics.Code != http.StatusOK {
		t.Fatalf("expected ops metrics 200, got %d body=%s", recOpsMetrics.Code, recOpsMetrics.Body.String())
	}
	var opsMetricsPayload struct {
		Routes []map[string]any `json:"routes"`
	}
	if err := json.Unmarshal(recOpsMetrics.Body.Bytes(), &opsMetricsPayload); err != nil {
		t.Fatalf("decode ops metrics: %v", err)
	}
	if len(opsMetricsPayload.Routes) == 0 {
		t.Fatalf("expected non-empty ops route metrics")
	}

	recOpsSLO := httptest.NewRecorder()
	reqOpsSLO := httptest.NewRequest(http.MethodGet, "/ops/slo", nil)
	reqOpsSLO = withAuthRoleScope(reqOpsSLO, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recOpsSLO, reqOpsSLO)
	if recOpsSLO.Code != http.StatusOK {
		t.Fatalf("expected ops slo 200, got %d body=%s", recOpsSLO.Code, recOpsSLO.Body.String())
	}
	var sloPayload struct {
		SLOAdherence float64 `json:"slo_adherence"`
		SLOPassed    bool    `json:"slo_passed"`
		SLOChecks    struct {
			APIAvailabilityPassed bool `json:"api_availability_passed"`
			RuntimeQualityPassed  bool `json:"runtime_quality_passed"`
		} `json:"slo_checks"`
		Runtime struct {
			TotalRuns            int     `json:"total_runs"`
			ConfirmedRate        float64 `json:"confirmed_rate"`
			RuntimeQualityPassed bool    `json:"runtime_quality_passed"`
		} `json:"runtime_reconciliation"`
	}
	if err := json.Unmarshal(recOpsSLO.Body.Bytes(), &sloPayload); err != nil {
		t.Fatalf("decode ops slo: %v", err)
	}
	if sloPayload.SLOAdherence <= 0 {
		t.Fatalf("expected slo_adherence in ops slo payload")
	}
	if sloPayload.Runtime.TotalRuns < 1 {
		t.Fatalf("expected runtime_reconciliation.total_runs >= 1, got %d", sloPayload.Runtime.TotalRuns)
	}
	if sloPayload.Runtime.ConfirmedRate <= 0 {
		t.Fatalf("expected runtime_reconciliation.confirmed_rate > 0")
	}
	if !sloPayload.Runtime.RuntimeQualityPassed {
		t.Fatalf("expected runtime quality gate to pass")
	}
	if !sloPayload.SLOChecks.RuntimeQualityPassed {
		t.Fatalf("expected slo_checks.runtime_quality_passed=true")
	}
	_ = sloPayload.SLOPassed
	_ = sloPayload.SLOChecks.APIAvailabilityPassed

	finalQualityPath := filepath.Join(tmp, "final-inputs", "quality_gate.json")
	finalSLOPath := filepath.Join(tmp, "final-inputs", "slo.json")
	finalTemplatesPath := filepath.Join(tmp, "final-inputs", "templates.json")
	finalCatalogPath := filepath.Join(tmp, "final-inputs", "catalog.json")
	finalGraphIndexPath := filepath.Join(tmp, "final-inputs", "graph_index.json")
	finalOutReportPath := filepath.Join(tmp, "final-output", "readiness_report.json")
	finalOutDecisionPath := filepath.Join(tmp, "final-output", "gate_decision.md")
	writeJSONFile(t, finalQualityPath, map[string]any{"passed": true})
	writeJSONFile(t, finalSLOPath, map[string]any{"passed": true, "slo_checks": map[string]any{"runtime_quality_passed": true}})
	writeJSONFile(t, finalTemplatesPath, map[string]any{
		"templates": []map[string]any{
			{"id": "docs-service", "path": "/products/docs/${graph_id}", "method": "GET", "query": map[string]any{"explain": true}},
		},
	})
	writeJSONFile(t, finalCatalogPath, map[string]any{
		"questions": []map[string]any{
			{"id": "q-service-external", "question": "What external services does each service depend on?", "endpoint": "/products/docs/{graph_id}"},
		},
	})
	writeJSONFile(t, finalGraphIndexPath, map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "tenant_id": "default", "fingerprint": "abc123"},
		},
	})

	finalReqBody := map[string]any{
		"quality_gate_path": finalQualityPath,
		"slo_path":          finalSLOPath,
		"templates_path":    finalTemplatesPath,
		"catalog_path":      finalCatalogPath,
		"graph_index_path":  finalGraphIndexPath,
		"out_report_path":   finalOutReportPath,
		"out_decision_path": finalOutDecisionPath,
		"signers":           []string{"engineering", "platform", "security"},
	}
	finalReqBodyBytes, err := json.Marshal(finalReqBody)
	if err != nil {
		t.Fatalf("marshal final gate request body: %v", err)
	}
	recFinalAttest := httptest.NewRecorder()
	reqFinalAttest := httptest.NewRequest(http.MethodPost, "/final/attest", bytes.NewReader(finalReqBodyBytes))
	reqFinalAttest.Header.Set("Content-Type", "application/json")
	reqFinalAttest = withAuthRoleScope(reqFinalAttest, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recFinalAttest, reqFinalAttest)
	if recFinalAttest.Code != http.StatusOK {
		t.Fatalf("expected final attest 200, got %d body=%s", recFinalAttest.Code, recFinalAttest.Body.String())
	}
	var finalAttestPayload struct {
		OverallPassed bool `json:"overall_passed"`
		Readiness     struct {
			OverallPassed bool `json:"overall_passed"`
		} `json:"readiness_report"`
	}
	if err := json.Unmarshal(recFinalAttest.Body.Bytes(), &finalAttestPayload); err != nil {
		t.Fatalf("decode final attest payload: %v", err)
	}
	if !finalAttestPayload.OverallPassed || !finalAttestPayload.Readiness.OverallPassed {
		t.Fatalf("expected final attest to pass, got %+v", finalAttestPayload)
	}

	recFinalReadiness := httptest.NewRecorder()
	reqFinalReadiness := httptest.NewRequest(http.MethodGet, "/final/readiness?path="+url.QueryEscape(finalOutReportPath), nil)
	reqFinalReadiness = withAuthRoleScope(reqFinalReadiness, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recFinalReadiness, reqFinalReadiness)
	if recFinalReadiness.Code != http.StatusOK {
		t.Fatalf("expected final readiness 200, got %d body=%s", recFinalReadiness.Code, recFinalReadiness.Body.String())
	}

	recFinalDecision := httptest.NewRecorder()
	reqFinalDecision := httptest.NewRequest(http.MethodGet, "/final/decision?path="+url.QueryEscape(finalOutDecisionPath), nil)
	reqFinalDecision = withAuthRoleScope(reqFinalDecision, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recFinalDecision, reqFinalDecision)
	if recFinalDecision.Code != http.StatusOK {
		t.Fatalf("expected final decision 200, got %d body=%s", recFinalDecision.Code, recFinalDecision.Body.String())
	}
	var finalDecisionPayload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(recFinalDecision.Body.Bytes(), &finalDecisionPayload); err != nil {
		t.Fatalf("decode final decision payload: %v", err)
	}
	if !strings.Contains(finalDecisionPayload.Content, "APPROVE") {
		t.Fatalf("expected final decision markdown to contain APPROVE, got %q", finalDecisionPayload.Content)
	}
}

func writeBundle(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
}

func writeJSONFile(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir json dir: %v", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write json file: %v", err)
	}
}

func mapGraphWithTime(id string, generatedAt time.Time) graphschema.Graph {
	return graphschema.Graph{
		GraphID:     id,
		GeneratedAt: generatedAt,
		Mode:        "single",
		Nodes:       []graphschema.Node{},
		Edges:       []graphschema.Edge{},
		Stats: graphschema.GraphStats{
			ByNode: map[string]int{},
			ByEdge: map[string]int{},
		},
		Meta: graphschema.GraphMeta{
			TenantID: "default",
			Services: []graphschema.ServiceMeta{},
		},
	}
}

func withAuth(req *http.Request) *http.Request {
	return withAuthRoleScope(req, "platform_admin,tenant_admin,analyst,compliance_auditor", "graph:read,graph:write,evidence:read,evidence:raw,sensitive:read,audit:read,audit:export")
}

func withAuthRoleScope(req *http.Request, roles string, scopes string) *http.Request {
	return withAuthTenantRoleScope(req, "default", roles, scopes)
}

func withAuthTenantRoleScope(req *http.Request, tenant string, roles string, scopes string) *http.Request {
	req.Header.Set("X-DiffMind-Tenant", "default")
	if strings.TrimSpace(tenant) != "" {
		req.Header.Set("X-DiffMind-Tenant", strings.TrimSpace(tenant))
	}
	req.Header.Set("X-DiffMind-Principal", "test-user")
	req.Header.Set("X-DiffMind-Roles", roles)
	req.Header.Set("X-DiffMind-Scopes", scopes)
	return req
}
