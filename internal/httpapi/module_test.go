package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

	recAtLimited := httptest.NewRecorder()
	reqAtLimited := httptest.NewRequest(http.MethodGet, "/graphs/at?node_limit=1", nil)
	mux.ServeHTTP(recAtLimited, withAuth(reqAtLimited))
	if recAtLimited.Code != http.StatusOK {
		t.Fatalf("expected /graphs/at node_limit 200, got %d body=%s", recAtLimited.Code, recAtLimited.Body.String())
	}
	var atLimitedPayload struct {
		Nodes []map[string]any `json:"nodes"`
		Meta  struct {
			QueryPagination struct {
				NodeLimit      int  `json:"node_limit"`
				ReturnedNodes  int  `json:"returned_nodes"`
				NodesTruncated bool `json:"nodes_truncated"`
			} `json:"query_pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(recAtLimited.Body.Bytes(), &atLimitedPayload); err != nil {
		t.Fatalf("decode /graphs/at node_limit response: %v", err)
	}
	if len(atLimitedPayload.Nodes) != 1 {
		t.Fatalf("expected one node with /graphs/at?node_limit=1, got %d", len(atLimitedPayload.Nodes))
	}
	if atLimitedPayload.Meta.QueryPagination.NodeLimit != 1 || !atLimitedPayload.Meta.QueryPagination.NodesTruncated {
		t.Fatalf("unexpected /graphs/at pagination metadata: %+v", atLimitedPayload.Meta.QueryPagination)
	}

	recAtBad := httptest.NewRecorder()
	reqAtBad := httptest.NewRequest(http.MethodGet, "/graphs/at?at=not-a-time", nil)
	mux.ServeHTTP(recAtBad, withAuth(reqAtBad))
	if recAtBad.Code != http.StatusBadRequest {
		t.Fatalf("expected /graphs/at invalid time 400, got %d", recAtBad.Code)
	}

	recAtBadLimit := httptest.NewRecorder()
	reqAtBadLimit := httptest.NewRequest(http.MethodGet, "/graphs/at?node_limit=bad", nil)
	mux.ServeHTTP(recAtBadLimit, withAuth(reqAtBadLimit))
	if recAtBadLimit.Code != http.StatusBadRequest {
		t.Fatalf("expected /graphs/at invalid node_limit 400, got %d", recAtBadLimit.Code)
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
			Meta  struct {
				QueryPagination struct {
					NodeLimit      int  `json:"node_limit"`
					TotalNodes     int  `json:"total_nodes"`
					ReturnedNodes  int  `json:"returned_nodes"`
					NodesTruncated bool `json:"nodes_truncated"`
				} `json:"query_pagination"`
			} `json:"meta"`
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

	recQueryLimited := httptest.NewRecorder()
	reqQueryLimited := httptest.NewRequest(http.MethodGet, "/graphs/g1/query?node_limit=1&explain=true", nil)
	mux.ServeHTTP(recQueryLimited, withAuth(reqQueryLimited))
	if recQueryLimited.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1/query node_limit 200, got %d body=%s", recQueryLimited.Code, recQueryLimited.Body.String())
	}
	var queryLimitedPayload struct {
		Graph struct {
			Nodes []map[string]any `json:"nodes"`
			Meta  struct {
				QueryPagination struct {
					NodeLimit      int  `json:"node_limit"`
					TotalNodes     int  `json:"total_nodes"`
					ReturnedNodes  int  `json:"returned_nodes"`
					NodesTruncated bool `json:"nodes_truncated"`
				} `json:"query_pagination"`
			} `json:"meta"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(recQueryLimited.Body.Bytes(), &queryLimitedPayload); err != nil {
		t.Fatalf("decode query limited response: %v", err)
	}
	if len(queryLimitedPayload.Graph.Nodes) != 1 {
		t.Fatalf("expected one node with node_limit=1, got %d", len(queryLimitedPayload.Graph.Nodes))
	}
	if queryLimitedPayload.Graph.Meta.QueryPagination.NodeLimit != 1 {
		t.Fatalf("expected query pagination node_limit=1, got %+v", queryLimitedPayload.Graph.Meta.QueryPagination)
	}
	if !queryLimitedPayload.Graph.Meta.QueryPagination.NodesTruncated {
		t.Fatalf("expected nodes_truncated=true for node_limit=1")
	}

	recQueryBadLimit := httptest.NewRecorder()
	reqQueryBadLimit := httptest.NewRequest(http.MethodGet, "/graphs/g1/query?node_limit=bad", nil)
	mux.ServeHTTP(recQueryBadLimit, withAuth(reqQueryBadLimit))
	if recQueryBadLimit.Code != http.StatusBadRequest {
		t.Fatalf("expected /graphs/g1/query invalid node_limit 400, got %d", recQueryBadLimit.Code)
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

	compareByTimeBody := map[string]any{
		"from_at": "2026-01-01T00:00:00Z",
		"to_at":   "2026-01-02T00:00:00Z",
		"mode":    "single",
	}
	compareByTimeData, err := json.Marshal(compareByTimeBody)
	if err != nil {
		t.Fatalf("marshal compare-by-time request: %v", err)
	}
	recCompareByTime := httptest.NewRecorder()
	reqCompareByTime := httptest.NewRequest(http.MethodPost, "/graphs/compare", bytes.NewReader(compareByTimeData))
	reqCompareByTime.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recCompareByTime, withAuth(reqCompareByTime))
	if recCompareByTime.Code != http.StatusOK {
		t.Fatalf("expected temporal compare 200, got %d body=%s", recCompareByTime.Code, recCompareByTime.Body.String())
	}
	var compareByTimePayload struct {
		FromGraphID string `json:"from_graph_id"`
		ToGraphID   string `json:"to_graph_id"`
	}
	if err := json.Unmarshal(recCompareByTime.Body.Bytes(), &compareByTimePayload); err != nil {
		t.Fatalf("decode temporal compare response: %v", err)
	}
	if compareByTimePayload.FromGraphID != "g1" || compareByTimePayload.ToGraphID != "g2" {
		t.Fatalf("unexpected temporal compare graph ids: %+v", compareByTimePayload)
	}

	recCompareByTimeBad := httptest.NewRecorder()
	reqCompareByTimeBad := httptest.NewRequest(http.MethodPost, "/graphs/compare", strings.NewReader(`{"from_at":"bad","to_at":"2026-01-02T00:00:00Z"}`))
	reqCompareByTimeBad.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recCompareByTimeBad, withAuth(reqCompareByTimeBad))
	if recCompareByTimeBad.Code != http.StatusBadRequest {
		t.Fatalf("expected temporal compare bad timestamp 400, got %d", recCompareByTimeBad.Code)
	}

	recCompareByTimeMiss := httptest.NewRecorder()
	reqCompareByTimeMiss := httptest.NewRequest(http.MethodPost, "/graphs/compare", strings.NewReader(`{"from_at":"2025-01-01T00:00:00Z","to_at":"2026-01-02T00:00:00Z"}`))
	reqCompareByTimeMiss.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recCompareByTimeMiss, withAuth(reqCompareByTimeMiss))
	if recCompareByTimeMiss.Code != http.StatusNotFound {
		t.Fatalf("expected temporal compare missing graph 404, got %d", recCompareByTimeMiss.Code)
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

	recCompareListFiltered := httptest.NewRecorder()
	reqCompareListFiltered := httptest.NewRequest(http.MethodGet, "/graphs/compare?from_graph_id=g1&to_graph_id=g2", nil)
	mux.ServeHTTP(recCompareListFiltered, withAuth(reqCompareListFiltered))
	if recCompareListFiltered.Code != http.StatusOK {
		t.Fatalf("expected compare list filtered 200, got %d", recCompareListFiltered.Code)
	}
	var compareListFilteredPayload struct {
		Compares []struct {
			FromGraphID string `json:"from_graph_id"`
			ToGraphID   string `json:"to_graph_id"`
		} `json:"compares"`
	}
	if err := json.Unmarshal(recCompareListFiltered.Body.Bytes(), &compareListFilteredPayload); err != nil {
		t.Fatalf("decode compare list filtered response: %v", err)
	}
	if len(compareListFilteredPayload.Compares) == 0 {
		t.Fatalf("expected filtered compare history entries")
	}
	for _, c := range compareListFilteredPayload.Compares {
		if c.FromGraphID != "g1" || c.ToGraphID != "g2" {
			t.Fatalf("unexpected filtered compare row: %+v", c)
		}
	}

	recCompareListBadFrom := httptest.NewRecorder()
	reqCompareListBadFrom := httptest.NewRequest(http.MethodGet, "/graphs/compare?from=bad-time", nil)
	mux.ServeHTTP(recCompareListBadFrom, withAuth(reqCompareListBadFrom))
	if recCompareListBadFrom.Code != http.StatusBadRequest {
		t.Fatalf("expected compare list bad from timestamp 400, got %d", recCompareListBadFrom.Code)
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

	recCompareImpact := httptest.NewRecorder()
	reqCompareImpact := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID+"/impact", nil)
	mux.ServeHTTP(recCompareImpact, withAuth(reqCompareImpact))
	if recCompareImpact.Code != http.StatusOK {
		t.Fatalf("expected compare impact 200, got %d body=%s", recCompareImpact.Code, recCompareImpact.Body.String())
	}
	var compareImpactPayload struct {
		CompareID       string              `json:"compare_id"`
		ImpactedNodeIDs []string            `json:"impacted_node_ids"`
		ImpactedEdgeIDs []string            `json:"impacted_edge_ids"`
		Counts          map[string]int      `json:"counts"`
		Reasons         map[string][]string `json:"reasons"`
	}
	if err := json.Unmarshal(recCompareImpact.Body.Bytes(), &compareImpactPayload); err != nil {
		t.Fatalf("decode compare impact response: %v", err)
	}
	if compareImpactPayload.CompareID != comparePayload.CompareID {
		t.Fatalf("unexpected compare impact compare_id: %q", compareImpactPayload.CompareID)
	}
	if compareImpactPayload.Counts["nodes"] == 0 || compareImpactPayload.Counts["edges"] == 0 {
		t.Fatalf("expected non-empty impact counts, got %+v", compareImpactPayload.Counts)
	}
	if len(compareImpactPayload.Reasons["changed_nodes"]) == 0 {
		t.Fatalf("expected changed_nodes reasons in compare impact payload")
	}
	recCompareImpactExplain := httptest.NewRecorder()
	reqCompareImpactExplain := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID+"/impact?explain=true", nil)
	mux.ServeHTTP(recCompareImpactExplain, withAuth(reqCompareImpactExplain))
	if recCompareImpactExplain.Code != http.StatusOK {
		t.Fatalf("expected compare impact explain 200, got %d body=%s", recCompareImpactExplain.Code, recCompareImpactExplain.Body.String())
	}
	var compareImpactExplainPayload struct {
		Impact  map[string]any `json:"impact"`
		Explain map[string]any `json:"explain"`
	}
	if err := json.Unmarshal(recCompareImpactExplain.Body.Bytes(), &compareImpactExplainPayload); err != nil {
		t.Fatalf("decode compare impact explain response: %v", err)
	}
	if len(compareImpactExplainPayload.Impact) == 0 || len(compareImpactExplainPayload.Explain) == 0 {
		t.Fatalf("expected compare impact explain payload fields, got %+v", compareImpactExplainPayload)
	}

	recCompareImpactSubgraph := httptest.NewRecorder()
	reqCompareImpactSubgraph := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID+"/impact/subgraph?hops=1", nil)
	mux.ServeHTTP(recCompareImpactSubgraph, withAuth(reqCompareImpactSubgraph))
	if recCompareImpactSubgraph.Code != http.StatusOK {
		t.Fatalf("expected compare impact subgraph 200, got %d body=%s", recCompareImpactSubgraph.Code, recCompareImpactSubgraph.Body.String())
	}
	var compareImpactSubgraphPayload struct {
		CompareID string `json:"compare_id"`
		GraphID   string `json:"graph_id"`
		Hops      int    `json:"hops"`
		Impact    struct {
			Nodes []map[string]any `json:"nodes"`
			Edges []map[string]any `json:"edges"`
		} `json:"impact_graph"`
	}
	if err := json.Unmarshal(recCompareImpactSubgraph.Body.Bytes(), &compareImpactSubgraphPayload); err != nil {
		t.Fatalf("decode compare impact subgraph response: %v", err)
	}
	if compareImpactSubgraphPayload.CompareID != comparePayload.CompareID {
		t.Fatalf("unexpected compare impact subgraph compare_id: %s", compareImpactSubgraphPayload.CompareID)
	}
	if compareImpactSubgraphPayload.GraphID != "g2" {
		t.Fatalf("expected default impact subgraph graph_id=g2, got %s", compareImpactSubgraphPayload.GraphID)
	}
	if compareImpactSubgraphPayload.Hops != 1 {
		t.Fatalf("expected hops=1 in response, got %d", compareImpactSubgraphPayload.Hops)
	}
	if len(compareImpactSubgraphPayload.Impact.Nodes) == 0 {
		t.Fatalf("expected non-empty impact subgraph nodes")
	}

	recCompareImpactSubgraphBadHops := httptest.NewRecorder()
	reqCompareImpactSubgraphBadHops := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID+"/impact/subgraph?hops=bad", nil)
	mux.ServeHTTP(recCompareImpactSubgraphBadHops, withAuth(reqCompareImpactSubgraphBadHops))
	if recCompareImpactSubgraphBadHops.Code != http.StatusBadRequest {
		t.Fatalf("expected compare impact subgraph invalid hops 400, got %d", recCompareImpactSubgraphBadHops.Code)
	}
	recCompareImpactSubgraphBadHopsHigh := httptest.NewRecorder()
	reqCompareImpactSubgraphBadHopsHigh := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID+"/impact/subgraph?hops=99", nil)
	mux.ServeHTTP(recCompareImpactSubgraphBadHopsHigh, withAuth(reqCompareImpactSubgraphBadHopsHigh))
	if recCompareImpactSubgraphBadHopsHigh.Code != http.StatusBadRequest {
		t.Fatalf("expected compare impact subgraph high hops 400, got %d", recCompareImpactSubgraphBadHopsHigh.Code)
	}
	recCompareImpactSubgraphExplain := httptest.NewRecorder()
	reqCompareImpactSubgraphExplain := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID+"/impact/subgraph?hops=1&explain=true", nil)
	mux.ServeHTTP(recCompareImpactSubgraphExplain, withAuth(reqCompareImpactSubgraphExplain))
	if recCompareImpactSubgraphExplain.Code != http.StatusOK {
		t.Fatalf("expected compare impact subgraph explain 200, got %d body=%s", recCompareImpactSubgraphExplain.Code, recCompareImpactSubgraphExplain.Body.String())
	}
	var compareImpactSubgraphExplainPayload struct {
		Explain map[string]any `json:"explain"`
	}
	if err := json.Unmarshal(recCompareImpactSubgraphExplain.Body.Bytes(), &compareImpactSubgraphExplainPayload); err != nil {
		t.Fatalf("decode compare impact subgraph explain response: %v", err)
	}
	if len(compareImpactSubgraphExplainPayload.Explain) == 0 {
		t.Fatalf("expected compare impact subgraph explain payload")
	}

	recCompareImpactSubgraphBadGraph := httptest.NewRecorder()
	reqCompareImpactSubgraphBadGraph := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID+"/impact/subgraph?graph_id=other-graph", nil)
	mux.ServeHTTP(recCompareImpactSubgraphBadGraph, withAuth(reqCompareImpactSubgraphBadGraph))
	if recCompareImpactSubgraphBadGraph.Code != http.StatusBadRequest {
		t.Fatalf("expected compare impact subgraph invalid graph_id 400, got %d", recCompareImpactSubgraphBadGraph.Code)
	}

	recCompareSubresourceMiss := httptest.NewRecorder()
	reqCompareSubresourceMiss := httptest.NewRequest(http.MethodGet, "/graphs/compare/"+comparePayload.CompareID+"/unknown", nil)
	mux.ServeHTTP(recCompareSubresourceMiss, withAuth(reqCompareSubresourceMiss))
	if recCompareSubresourceMiss.Code != http.StatusNotFound {
		t.Fatalf("expected compare unknown subresource 404, got %d", recCompareSubresourceMiss.Code)
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
	if !bytes.Contains(rec.Body.Bytes(), []byte("Validate Operator SLA")) {
		t.Fatalf("expected operator SLA control in UI")
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
	recAdjudications := httptest.NewRecorder()
	reqAdjudications := httptest.NewRequest(http.MethodGet, "/graphs/g1/adjudications", nil)
	mux.ServeHTTP(recAdjudications, reqAdjudications)
	if recAdjudications.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on graph adjudications endpoint without auth headers, got %d", recAdjudications.Code)
	}
	recVerifyRuns := httptest.NewRecorder()
	reqVerifyRuns := httptest.NewRequest(http.MethodGet, "/verify/runs", nil)
	mux.ServeHTTP(recVerifyRuns, reqVerifyRuns)
	if recVerifyRuns.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on verify runs endpoint without auth headers, got %d", recVerifyRuns.Code)
	}
	recVerifyRun := httptest.NewRecorder()
	reqVerifyRun := httptest.NewRequest(http.MethodPost, "/verify/run", bytes.NewReader([]byte(`{}`)))
	reqVerifyRun.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recVerifyRun, reqVerifyRun)
	if recVerifyRun.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on verify run endpoint without auth headers, got %d", recVerifyRun.Code)
	}
	recIncrementalList := httptest.NewRecorder()
	reqIncrementalList := httptest.NewRequest(http.MethodGet, "/graphs/incremental", nil)
	mux.ServeHTTP(recIncrementalList, reqIncrementalList)
	if recIncrementalList.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on incremental list endpoint without auth headers, got %d", recIncrementalList.Code)
	}
	recIncrementalPlan := httptest.NewRecorder()
	reqIncrementalPlan := httptest.NewRequest(http.MethodPost, "/graphs/incremental", bytes.NewReader([]byte(`{}`)))
	reqIncrementalPlan.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recIncrementalPlan, reqIncrementalPlan)
	if recIncrementalPlan.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on incremental plan endpoint without auth headers, got %d", recIncrementalPlan.Code)
	}

	recOpsPolicy := httptest.NewRecorder()
	reqOpsPolicy := httptest.NewRequest(http.MethodGet, "/ops/rollout-policy", nil)
	mux.ServeHTTP(recOpsPolicy, reqOpsPolicy)
	if recOpsPolicy.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on ops rollout policy endpoint without auth headers, got %d", recOpsPolicy.Code)
	}
	recOpsIncidents := httptest.NewRecorder()
	reqOpsIncidents := httptest.NewRequest(http.MethodGet, "/ops/incidents", nil)
	mux.ServeHTTP(recOpsIncidents, reqOpsIncidents)
	if recOpsIncidents.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on ops incidents endpoint without auth headers, got %d", recOpsIncidents.Code)
	}
	recOpsSLOEval := httptest.NewRecorder()
	reqOpsSLOEval := httptest.NewRequest(http.MethodPost, "/ops/slo/evaluate", bytes.NewReader([]byte(`{}`)))
	reqOpsSLOEval.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recOpsSLOEval, reqOpsSLOEval)
	if recOpsSLOEval.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on ops slo evaluate endpoint without auth headers, got %d", recOpsSLOEval.Code)
	}
	recOpsBackup := httptest.NewRecorder()
	reqOpsBackup := httptest.NewRequest(http.MethodPost, "/ops/backup", bytes.NewReader([]byte(`{}`)))
	reqOpsBackup.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recOpsBackup, reqOpsBackup)
	if recOpsBackup.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on ops backup endpoint without auth headers, got %d", recOpsBackup.Code)
	}

	recProductTemplates := httptest.NewRecorder()
	reqProductTemplates := httptest.NewRequest(http.MethodGet, "/products/templates", nil)
	mux.ServeHTTP(recProductTemplates, reqProductTemplates)
	if recProductTemplates.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products templates endpoint without auth headers, got %d", recProductTemplates.Code)
	}
	recQueryTemplates := httptest.NewRecorder()
	reqQueryTemplates := httptest.NewRequest(http.MethodGet, "/query/templates", nil)
	mux.ServeHTTP(recQueryTemplates, reqQueryTemplates)
	if recQueryTemplates.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on query templates endpoint without auth headers, got %d", recQueryTemplates.Code)
	}
	recQueryExecute := httptest.NewRecorder()
	reqQueryExecute := httptest.NewRequest(http.MethodPost, "/query/execute", bytes.NewReader([]byte(`{}`)))
	reqQueryExecute.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recQueryExecute, reqQueryExecute)
	if recQueryExecute.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on query execute endpoint without auth headers, got %d", recQueryExecute.Code)
	}
	recProductTemplatesValidate := httptest.NewRecorder()
	reqProductTemplatesValidate := httptest.NewRequest(http.MethodGet, "/products/templates/validate", nil)
	mux.ServeHTTP(recProductTemplatesValidate, reqProductTemplatesValidate)
	if recProductTemplatesValidate.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products templates validate endpoint without auth headers, got %d", recProductTemplatesValidate.Code)
	}

	recProductQuestions := httptest.NewRecorder()
	reqProductQuestions := httptest.NewRequest(http.MethodGet, "/products/questions", nil)
	mux.ServeHTTP(recProductQuestions, reqProductQuestions)
	if recProductQuestions.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products questions endpoint without auth headers, got %d", recProductQuestions.Code)
	}
	recProductRuntime := httptest.NewRecorder()
	reqProductRuntime := httptest.NewRequest(http.MethodGet, "/products/runtime/g1", nil)
	mux.ServeHTTP(recProductRuntime, reqProductRuntime)
	if recProductRuntime.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products runtime endpoint without auth headers, got %d", recProductRuntime.Code)
	}
	recProductTopology := httptest.NewRecorder()
	reqProductTopology := httptest.NewRequest(http.MethodGet, "/products/topology/g1", nil)
	mux.ServeHTTP(recProductTopology, reqProductTopology)
	if recProductTopology.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products topology endpoint without auth headers, got %d", recProductTopology.Code)
	}
	recProductCompany := httptest.NewRecorder()
	reqProductCompany := httptest.NewRequest(http.MethodGet, "/products/company/g1", nil)
	mux.ServeHTTP(recProductCompany, reqProductCompany)
	if recProductCompany.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products company endpoint without auth headers, got %d", recProductCompany.Code)
	}
	recProductTrust := httptest.NewRecorder()
	reqProductTrust := httptest.NewRequest(http.MethodGet, "/products/trust/g1", nil)
	mux.ServeHTTP(recProductTrust, reqProductTrust)
	if recProductTrust.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products trust endpoint without auth headers, got %d", recProductTrust.Code)
	}
	recProductArchitecture := httptest.NewRecorder()
	reqProductArchitecture := httptest.NewRequest(http.MethodGet, "/products/architecture/g1", nil)
	mux.ServeHTTP(recProductArchitecture, reqProductArchitecture)
	if recProductArchitecture.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on products architecture endpoint without auth headers, got %d", recProductArchitecture.Code)
	}
	recGraphArchitectureTasks := httptest.NewRecorder()
	reqGraphArchitectureTasks := httptest.NewRequest(http.MethodGet, "/graphs/g1/architecture-tasks", nil)
	mux.ServeHTTP(recGraphArchitectureTasks, reqGraphArchitectureTasks)
	if recGraphArchitectureTasks.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on graph architecture tasks endpoint without auth headers, got %d", recGraphArchitectureTasks.Code)
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
	recFinalCloseout := httptest.NewRecorder()
	reqFinalCloseout := httptest.NewRequest(http.MethodPost, "/final/closeout", bytes.NewReader([]byte(`{}`)))
	reqFinalCloseout.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recFinalCloseout, reqFinalCloseout)
	if recFinalCloseout.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on final closeout endpoint without auth headers, got %d", recFinalCloseout.Code)
	}
	recFinalMilestones := httptest.NewRecorder()
	reqFinalMilestones := httptest.NewRequest(http.MethodGet, "/final/milestones", nil)
	mux.ServeHTTP(recFinalMilestones, reqFinalMilestones)
	if recFinalMilestones.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on final milestones endpoint without auth headers, got %d", recFinalMilestones.Code)
	}
	recFinalClosureRules := httptest.NewRecorder()
	reqFinalClosureRules := httptest.NewRequest(http.MethodGet, "/final/closure-rules", nil)
	mux.ServeHTTP(recFinalClosureRules, reqFinalClosureRules)
	if recFinalClosureRules.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on final closure-rules endpoint without auth headers, got %d", recFinalClosureRules.Code)
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

func TestJWTAuthModeForProtectedEndpoints(t *testing.T) {
	t.Setenv("DIFFMIND_AUTH_MODE", "jwt")
	t.Setenv("DIFFMIND_AUTH_JWT_HS256_SECRET", "test-secret")
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single", "tenant_id": "default", "node_count": 1, "edge_count": 0, "path": filepath.Join(graphRoot, "g1", "graph.json")},
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

	recHeaderOnly := httptest.NewRecorder()
	reqHeaderOnly := httptest.NewRequest(http.MethodGet, "/graphs", nil)
	reqHeaderOnly = withAuth(reqHeaderOnly)
	mux.ServeHTTP(recHeaderOnly, reqHeaderOnly)
	if recHeaderOnly.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with headers-only in jwt mode, got %d", recHeaderOnly.Code)
	}

	recInvalid := httptest.NewRecorder()
	reqInvalid := httptest.NewRequest(http.MethodGet, "/graphs", nil)
	reqInvalid.Header.Set("Authorization", "Bearer invalid.token.value")
	mux.ServeHTTP(recInvalid, reqInvalid)
	if recInvalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid token in jwt mode, got %d", recInvalid.Code)
	}

	token := signTestHS256JWT(t, map[string]any{
		"sub":    "test-user",
		"tenant": "default",
		"roles":  []string{"analyst"},
		"scopes": []string{"graph:read"},
		"exp":    time.Now().UTC().Add(10 * time.Minute).Unix(),
	}, "test-secret")
	recValid := httptest.NewRecorder()
	reqValid := httptest.NewRequest(http.MethodGet, "/graphs", nil)
	reqValid.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(recValid, reqValid)
	if recValid.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid token in jwt mode, got %d body=%s", recValid.Code, recValid.Body.String())
	}
}

func TestComplianceAuditEndpoints(t *testing.T) {
	tmp := t.TempDir()
	manifestKey := make([]byte, 32)
	for i := range manifestKey {
		manifestKey[i] = byte(i + 41)
	}
	t.Setenv("DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64", base64.StdEncoding.EncodeToString(manifestKey))
	t.Setenv("DIFFMIND_AUDIT_MANIFEST_KEY_ID", "manifest-key-http")
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
	if err := audit.AppendEvent(filepath.Dir(graphRoot), audit.Event{
		Timestamp: time.Now().UTC().Add(-48 * time.Hour),
		Action:    "query_graph",
		TenantID:  "other",
		Principal: "u2",
		Method:    "GET",
		Path:      "/graphs/g2",
		Decision:  "allow",
	}); err != nil {
		t.Fatalf("seed other-tenant audit event: %v", err)
	}
	mux := newMux("", graphRoot)

	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/compliance/audit?limit=10", nil)
	reqList = withAuthRoleScope(reqList, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("expected audit list 200, got %d body=%s", recList.Code, recList.Body.String())
	}

	recIntegrity := httptest.NewRecorder()
	reqIntegrity := httptest.NewRequest(http.MethodGet, "/compliance/audit/integrity", nil)
	reqIntegrity = withAuthRoleScope(reqIntegrity, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recIntegrity, reqIntegrity)
	if recIntegrity.Code != http.StatusOK {
		t.Fatalf("expected audit integrity 200, got %d body=%s", recIntegrity.Code, recIntegrity.Body.String())
	}
	var integrityPayload struct {
		Valid         bool `json:"valid"`
		Checked       int  `json:"checked"`
		ChainEnforced bool `json:"chain_enforced"`
	}
	if err := json.Unmarshal(recIntegrity.Body.Bytes(), &integrityPayload); err != nil {
		t.Fatalf("decode audit integrity response: %v", err)
	}
	if !integrityPayload.Valid || integrityPayload.Checked == 0 {
		t.Fatalf("expected valid integrity response with checked events, got %+v", integrityPayload)
	}
	if integrityPayload.ChainEnforced {
		t.Fatalf("expected tenant-scoped integrity check to be non-chain mode")
	}

	recIntegrityTenantMismatch := httptest.NewRecorder()
	reqIntegrityTenantMismatch := httptest.NewRequest(http.MethodGet, "/compliance/audit/integrity?tenant_id=other", nil)
	reqIntegrityTenantMismatch = withAuthRoleScope(reqIntegrityTenantMismatch, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recIntegrityTenantMismatch, reqIntegrityTenantMismatch)
	if recIntegrityTenantMismatch.Code != http.StatusForbidden {
		t.Fatalf("expected audit integrity tenant mismatch 403, got %d", recIntegrityTenantMismatch.Code)
	}

	recIntegrityAllTenantsForbidden := httptest.NewRecorder()
	reqIntegrityAllTenantsForbidden := httptest.NewRequest(http.MethodGet, "/compliance/audit/integrity?all_tenants=true", nil)
	reqIntegrityAllTenantsForbidden = withAuthRoleScope(reqIntegrityAllTenantsForbidden, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recIntegrityAllTenantsForbidden, reqIntegrityAllTenantsForbidden)
	if recIntegrityAllTenantsForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected audit integrity all_tenants forbidden 403, got %d", recIntegrityAllTenantsForbidden.Code)
	}

	recIntegrityPlatform := httptest.NewRecorder()
	reqIntegrityPlatform := httptest.NewRequest(http.MethodGet, "/compliance/audit/integrity?all_tenants=true", nil)
	reqIntegrityPlatform = withAuthTenantRoleScope(reqIntegrityPlatform, "default", "platform_admin", "")
	mux.ServeHTTP(recIntegrityPlatform, reqIntegrityPlatform)
	if recIntegrityPlatform.Code != http.StatusOK {
		t.Fatalf("expected platform audit integrity 200, got %d body=%s", recIntegrityPlatform.Code, recIntegrityPlatform.Body.String())
	}
	var integrityPlatformPayload struct {
		ChainEnforced bool `json:"chain_enforced"`
	}
	if err := json.Unmarshal(recIntegrityPlatform.Body.Bytes(), &integrityPlatformPayload); err != nil {
		t.Fatalf("decode platform integrity response: %v", err)
	}
	if !integrityPlatformPayload.ChainEnforced {
		t.Fatalf("expected platform all-tenant integrity check to enforce chain")
	}

	recExport := httptest.NewRecorder()
	reqExport := httptest.NewRequest(http.MethodPost, "/compliance/audit/export", bytes.NewReader([]byte(`{"encrypt":false}`)))
	reqExport = withAuthRoleScope(reqExport, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recExport, reqExport)
	if recExport.Code != http.StatusOK {
		t.Fatalf("expected audit export 200, got %d body=%s", recExport.Code, recExport.Body.String())
	}
	var exportPayload struct {
		Path     string `json:"path"`
		Manifest string `json:"manifest_path"`
		Signed   bool   `json:"signed"`
	}
	if err := json.Unmarshal(recExport.Body.Bytes(), &exportPayload); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if exportPayload.Path == "" {
		t.Fatalf("expected export path")
	}
	if exportPayload.Manifest == "" {
		t.Fatalf("expected export manifest path")
	}
	if _, err := os.Stat(exportPayload.Path); err != nil {
		t.Fatalf("expected export artifact: %v", err)
	}
	if _, err := os.Stat(exportPayload.Manifest); err != nil {
		t.Fatalf("expected export manifest artifact: %v", err)
	}
	if !exportPayload.Signed {
		t.Fatalf("expected signed export manifest")
	}

	verifyBody := []byte(`{"manifest_path":"` + strings.ReplaceAll(exportPayload.Manifest, `\`, `\\`) + `"}`)
	recExportVerify := httptest.NewRecorder()
	reqExportVerify := httptest.NewRequest(http.MethodPost, "/compliance/audit/export/verify", bytes.NewReader(verifyBody))
	reqExportVerify.Header.Set("Content-Type", "application/json")
	reqExportVerify = withAuthRoleScope(reqExportVerify, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recExportVerify, reqExportVerify)
	if recExportVerify.Code != http.StatusOK {
		t.Fatalf("expected audit export verify 200, got %d body=%s", recExportVerify.Code, recExportVerify.Body.String())
	}
	var verifyPayload struct {
		Valid          bool `json:"valid"`
		Signed         bool `json:"signed"`
		SignatureValid bool `json:"signature_valid"`
	}
	if err := json.Unmarshal(recExportVerify.Body.Bytes(), &verifyPayload); err != nil {
		t.Fatalf("decode export verify response: %v", err)
	}
	if !verifyPayload.Valid || !verifyPayload.Signed || !verifyPayload.SignatureValid {
		t.Fatalf("unexpected export verify payload: %+v", verifyPayload)
	}

	recEvidenceBundle := httptest.NewRecorder()
	reqEvidenceBundle := httptest.NewRequest(http.MethodPost, "/compliance/audit/evidence-bundle", bytes.NewReader([]byte(`{"retain_days":30}`)))
	reqEvidenceBundle.Header.Set("Content-Type", "application/json")
	reqEvidenceBundle = withAuthRoleScope(reqEvidenceBundle, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recEvidenceBundle, reqEvidenceBundle)
	if recEvidenceBundle.Code != http.StatusOK {
		t.Fatalf("expected audit evidence bundle 200, got %d body=%s", recEvidenceBundle.Code, recEvidenceBundle.Body.String())
	}
	var evidenceBundlePayload struct {
		Path           string `json:"path"`
		ChecksumSHA256 string `json:"checksum_sha256"`
		Bundle         struct {
			Integrity struct {
				Valid bool `json:"valid"`
			} `json:"integrity"`
			Events struct {
				Count int `json:"count"`
			} `json:"events"`
		} `json:"bundle"`
	}
	if err := json.Unmarshal(recEvidenceBundle.Body.Bytes(), &evidenceBundlePayload); err != nil {
		t.Fatalf("decode evidence bundle response: %v", err)
	}
	if evidenceBundlePayload.Path == "" {
		t.Fatalf("expected evidence bundle path")
	}
	if evidenceBundlePayload.ChecksumSHA256 == "" {
		t.Fatalf("expected evidence bundle checksum")
	}
	if _, err := os.Stat(evidenceBundlePayload.Path); err != nil {
		t.Fatalf("expected evidence bundle artifact: %v", err)
	}
	if !evidenceBundlePayload.Bundle.Integrity.Valid {
		t.Fatalf("expected integrity.valid=true in evidence bundle")
	}
	if evidenceBundlePayload.Bundle.Events.Count == 0 {
		t.Fatalf("expected evidence bundle to include events summary")
	}

	recEvidenceBundleList := httptest.NewRecorder()
	reqEvidenceBundleList := httptest.NewRequest(http.MethodGet, "/compliance/audit/evidence-bundle?limit=10", nil)
	reqEvidenceBundleList = withAuthRoleScope(reqEvidenceBundleList, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recEvidenceBundleList, reqEvidenceBundleList)
	if recEvidenceBundleList.Code != http.StatusOK {
		t.Fatalf("expected evidence bundle list 200, got %d body=%s", recEvidenceBundleList.Code, recEvidenceBundleList.Body.String())
	}
	var evidenceBundleListPayload struct {
		Count   int `json:"count"`
		Bundles []struct {
			Path          string `json:"path"`
			ChecksumValid bool   `json:"checksum_valid"`
		} `json:"bundles"`
	}
	if err := json.Unmarshal(recEvidenceBundleList.Body.Bytes(), &evidenceBundleListPayload); err != nil {
		t.Fatalf("decode evidence bundle list response: %v", err)
	}
	if evidenceBundleListPayload.Count == 0 || len(evidenceBundleListPayload.Bundles) == 0 {
		t.Fatalf("expected non-empty evidence bundle list")
	}
	if !evidenceBundleListPayload.Bundles[0].ChecksumValid {
		t.Fatalf("expected listed evidence bundle checksum to be valid")
	}

	recEvidenceBundleRead := httptest.NewRecorder()
	reqEvidenceBundleRead := httptest.NewRequest(http.MethodGet, "/compliance/audit/evidence-bundle?path="+url.QueryEscape(evidenceBundlePayload.Path), nil)
	reqEvidenceBundleRead = withAuthRoleScope(reqEvidenceBundleRead, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recEvidenceBundleRead, reqEvidenceBundleRead)
	if recEvidenceBundleRead.Code != http.StatusOK {
		t.Fatalf("expected evidence bundle read 200, got %d body=%s", recEvidenceBundleRead.Code, recEvidenceBundleRead.Body.String())
	}
	var evidenceBundleReadPayload struct {
		ChecksumValid bool `json:"checksum_valid"`
	}
	if err := json.Unmarshal(recEvidenceBundleRead.Body.Bytes(), &evidenceBundleReadPayload); err != nil {
		t.Fatalf("decode evidence bundle read response: %v", err)
	}
	if !evidenceBundleReadPayload.ChecksumValid {
		t.Fatalf("expected read evidence bundle checksum valid")
	}

	recEvidenceBundleAllForbidden := httptest.NewRecorder()
	reqEvidenceBundleAllForbidden := httptest.NewRequest(http.MethodPost, "/compliance/audit/evidence-bundle", bytes.NewReader([]byte(`{"all_tenants":true}`)))
	reqEvidenceBundleAllForbidden.Header.Set("Content-Type", "application/json")
	reqEvidenceBundleAllForbidden = withAuthRoleScope(reqEvidenceBundleAllForbidden, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recEvidenceBundleAllForbidden, reqEvidenceBundleAllForbidden)
	if recEvidenceBundleAllForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected non-platform evidence bundle all_tenants 403, got %d", recEvidenceBundleAllForbidden.Code)
	}

	recEvidenceBundleListAllForbidden := httptest.NewRecorder()
	reqEvidenceBundleListAllForbidden := httptest.NewRequest(http.MethodGet, "/compliance/audit/evidence-bundle?all_tenants=true", nil)
	reqEvidenceBundleListAllForbidden = withAuthRoleScope(reqEvidenceBundleListAllForbidden, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recEvidenceBundleListAllForbidden, reqEvidenceBundleListAllForbidden)
	if recEvidenceBundleListAllForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected non-platform evidence bundle list all_tenants 403, got %d", recEvidenceBundleListAllForbidden.Code)
	}

	recExportBadRange := httptest.NewRecorder()
	reqExportBadRange := httptest.NewRequest(http.MethodPost, "/compliance/audit/export", bytes.NewReader([]byte(`{"from":"2026-01-02T00:00:00Z","to":"2026-01-01T00:00:00Z","encrypt":false}`)))
	reqExportBadRange = withAuthRoleScope(reqExportBadRange, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recExportBadRange, reqExportBadRange)
	if recExportBadRange.Code != http.StatusBadRequest {
		t.Fatalf("expected audit export invalid range 400, got %d", recExportBadRange.Code)
	}

	recExportTenantMismatch := httptest.NewRecorder()
	reqExportTenantMismatch := httptest.NewRequest(http.MethodPost, "/compliance/audit/export", bytes.NewReader([]byte(`{"tenant_id":"other","encrypt":false}`)))
	reqExportTenantMismatch = withAuthRoleScope(reqExportTenantMismatch, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recExportTenantMismatch, reqExportTenantMismatch)
	if recExportTenantMismatch.Code != http.StatusForbidden {
		t.Fatalf("expected audit export tenant mismatch 403, got %d", recExportTenantMismatch.Code)
	}

	recExportPlatform := httptest.NewRecorder()
	reqExportPlatform := httptest.NewRequest(http.MethodPost, "/compliance/audit/export", bytes.NewReader([]byte(`{"tenant_id":"other","encrypt":false}`)))
	reqExportPlatform = withAuthTenantRoleScope(reqExportPlatform, "default", "platform_admin", "")
	mux.ServeHTTP(recExportPlatform, reqExportPlatform)
	if recExportPlatform.Code != http.StatusOK {
		t.Fatalf("expected platform audit export 200, got %d body=%s", recExportPlatform.Code, recExportPlatform.Body.String())
	}
	var platformExportPayload struct {
		Path  string `json:"path"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(recExportPlatform.Body.Bytes(), &platformExportPayload); err != nil {
		t.Fatalf("decode platform export response: %v", err)
	}
	if platformExportPayload.Count != 1 {
		t.Fatalf("expected platform export to include only tenant=other events (count=1), got %d", platformExportPayload.Count)
	}
	if _, err := os.Stat(platformExportPayload.Path); err != nil {
		t.Fatalf("expected platform export artifact: %v", err)
	}

	recExportEncryptNoKey := httptest.NewRecorder()
	reqExportEncryptNoKey := httptest.NewRequest(http.MethodPost, "/compliance/audit/export", bytes.NewReader([]byte(`{"encrypt":true}`)))
	reqExportEncryptNoKey = withAuthRoleScope(reqExportEncryptNoKey, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recExportEncryptNoKey, reqExportEncryptNoKey)
	if recExportEncryptNoKey.Code != http.StatusBadRequest {
		t.Fatalf("expected audit export encrypt without key 400, got %d", recExportEncryptNoKey.Code)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv("DIFFMIND_AUDIT_EXPORT_KEY_B64", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DIFFMIND_KMS_KEY_ID", "kms-test")
	recExportEncrypt := httptest.NewRecorder()
	reqExportEncrypt := httptest.NewRequest(http.MethodPost, "/compliance/audit/export", bytes.NewReader([]byte(`{"encrypt":true}`)))
	reqExportEncrypt = withAuthRoleScope(reqExportEncrypt, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recExportEncrypt, reqExportEncrypt)
	if recExportEncrypt.Code != http.StatusOK {
		t.Fatalf("expected audit export encrypted 200, got %d body=%s", recExportEncrypt.Code, recExportEncrypt.Body.String())
	}
	var exportEncryptPayload struct {
		Path      string `json:"path"`
		Encrypted bool   `json:"encrypted"`
		KeyID     string `json:"key_id"`
	}
	if err := json.Unmarshal(recExportEncrypt.Body.Bytes(), &exportEncryptPayload); err != nil {
		t.Fatalf("decode encrypted export response: %v", err)
	}
	if !exportEncryptPayload.Encrypted {
		t.Fatalf("expected encrypted export payload")
	}
	if exportEncryptPayload.KeyID != "kms-test" {
		t.Fatalf("expected encrypted export key_id=kms-test, got %q", exportEncryptPayload.KeyID)
	}
	if !strings.HasSuffix(exportEncryptPayload.Path, ".enc") {
		t.Fatalf("expected encrypted export path to end with .enc, got %q", exportEncryptPayload.Path)
	}

	recPrune := httptest.NewRecorder()
	reqPrune := httptest.NewRequest(http.MethodPost, "/compliance/audit/retention", bytes.NewReader([]byte(`{"retain_days":1}`)))
	reqPrune = withAuthRoleScope(reqPrune, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recPrune, reqPrune)
	if recPrune.Code != http.StatusOK {
		t.Fatalf("expected audit retention 200, got %d body=%s", recPrune.Code, recPrune.Body.String())
	}

	recPruneAllTenantsForbidden := httptest.NewRecorder()
	reqPruneAllTenantsForbidden := httptest.NewRequest(http.MethodPost, "/compliance/audit/retention", bytes.NewReader([]byte(`{"retain_days":1,"all_tenants":true}`)))
	reqPruneAllTenantsForbidden = withAuthRoleScope(reqPruneAllTenantsForbidden, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recPruneAllTenantsForbidden, reqPruneAllTenantsForbidden)
	if recPruneAllTenantsForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected non-platform all_tenants retention 403, got %d", recPruneAllTenantsForbidden.Code)
	}

	recPruneTenantMismatch := httptest.NewRecorder()
	reqPruneTenantMismatch := httptest.NewRequest(http.MethodPost, "/compliance/audit/retention", bytes.NewReader([]byte(`{"retain_days":1,"tenant_id":"other"}`)))
	reqPruneTenantMismatch = withAuthRoleScope(reqPruneTenantMismatch, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recPruneTenantMismatch, reqPruneTenantMismatch)
	if recPruneTenantMismatch.Code != http.StatusForbidden {
		t.Fatalf("expected non-platform tenant override retention 403, got %d", recPruneTenantMismatch.Code)
	}

	recPrunePlatform := httptest.NewRecorder()
	reqPrunePlatform := httptest.NewRequest(http.MethodPost, "/compliance/audit/retention", bytes.NewReader([]byte(`{"retain_days":1,"tenant_id":"other"}`)))
	reqPrunePlatform = withAuthTenantRoleScope(reqPrunePlatform, "default", "platform_admin", "")
	mux.ServeHTTP(recPrunePlatform, reqPrunePlatform)
	if recPrunePlatform.Code != http.StatusOK {
		t.Fatalf("expected platform tenant-scoped retention 200, got %d body=%s", recPrunePlatform.Code, recPrunePlatform.Body.String())
	}
	otherEvents, err := audit.ListEvents(filepath.Dir(graphRoot), "other", 20)
	if err != nil {
		t.Fatalf("list other tenant events: %v", err)
	}
	if len(otherEvents) != 0 {
		t.Fatalf("expected other tenant events pruned by platform request, got %d", len(otherEvents))
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
	recRuntimeProduct := httptest.NewRecorder()
	reqRuntimeProduct := httptest.NewRequest(http.MethodGet, "/products/runtime/g1?service=a&explain=true", nil)
	reqRuntimeProduct = withAuthRoleScope(reqRuntimeProduct, "analyst", "graph:read")
	mux.ServeHTTP(recRuntimeProduct, reqRuntimeProduct)
	if recRuntimeProduct.Code != http.StatusOK {
		t.Fatalf("expected runtime product 200, got %d body=%s", recRuntimeProduct.Code, recRuntimeProduct.Body.String())
	}
	recTopologyProduct := httptest.NewRecorder()
	reqTopologyProduct := httptest.NewRequest(http.MethodGet, "/products/topology/g1?service=a&explain=true", nil)
	reqTopologyProduct = withAuthRoleScope(reqTopologyProduct, "analyst", "graph:read")
	mux.ServeHTTP(recTopologyProduct, reqTopologyProduct)
	if recTopologyProduct.Code != http.StatusOK {
		t.Fatalf("expected topology product 200, got %d body=%s", recTopologyProduct.Code, recTopologyProduct.Body.String())
	}
	recCompanyProduct := httptest.NewRecorder()
	reqCompanyProduct := httptest.NewRequest(http.MethodGet, "/products/company/g1?explain=true", nil)
	reqCompanyProduct = withAuthRoleScope(reqCompanyProduct, "analyst", "graph:read")
	mux.ServeHTTP(recCompanyProduct, reqCompanyProduct)
	if recCompanyProduct.Code != http.StatusOK {
		t.Fatalf("expected company product 200, got %d body=%s", recCompanyProduct.Code, recCompanyProduct.Body.String())
	}
	recTrustProduct := httptest.NewRecorder()
	reqTrustProduct := httptest.NewRequest(http.MethodGet, "/products/trust/g1?explain=true", nil)
	reqTrustProduct = withAuthRoleScope(reqTrustProduct, "analyst", "graph:read")
	mux.ServeHTTP(recTrustProduct, reqTrustProduct)
	if recTrustProduct.Code != http.StatusOK {
		t.Fatalf("expected trust product 200, got %d body=%s", recTrustProduct.Code, recTrustProduct.Body.String())
	}
	var trustPayload map[string]any
	if err := json.Unmarshal(recTrustProduct.Body.Bytes(), &trustPayload); err != nil {
		t.Fatalf("decode trust product payload: %v", err)
	}
	resultPayload, _ := trustPayload["result"].(map[string]any)
	if len(resultPayload) == 0 || resultPayload["trust"] == nil {
		t.Fatalf("expected trust product response to include trust report, got %+v", trustPayload)
	}
	recArchitectureProduct := httptest.NewRecorder()
	reqArchitectureProduct := httptest.NewRequest(http.MethodGet, "/products/architecture/g1?service=a&explain=true", nil)
	reqArchitectureProduct = withAuthRoleScope(reqArchitectureProduct, "analyst", "graph:read")
	mux.ServeHTTP(recArchitectureProduct, reqArchitectureProduct)
	if recArchitectureProduct.Code != http.StatusOK {
		t.Fatalf("expected architecture product 200, got %d body=%s", recArchitectureProduct.Code, recArchitectureProduct.Body.String())
	}
	var architecturePayload map[string]any
	if err := json.Unmarshal(recArchitectureProduct.Body.Bytes(), &architecturePayload); err != nil {
		t.Fatalf("decode architecture product payload: %v", err)
	}
	archResult, _ := architecturePayload["result"].(map[string]any)
	if len(archResult) == 0 || archResult["architecture"] == nil {
		t.Fatalf("expected architecture product response to include architecture report, got %+v", architecturePayload)
	}
	archReport, _ := archResult["architecture"].(map[string]any)
	archSummary, _ := archReport["summary"].(map[string]any)
	if len(archSummary) == 0 {
		t.Fatalf("expected architecture report summary in product response, got %+v", architecturePayload)
	}
	recArchitectureAssess := httptest.NewRecorder()
	reqArchitectureAssess := httptest.NewRequest(http.MethodPost, "/graphs/g1/architecture-tasks", bytes.NewReader([]byte(`{"export_subgraph":true,"include_graph_data":true}`)))
	reqArchitectureAssess.Header.Set("Content-Type", "application/json")
	reqArchitectureAssess = withAuthRoleScope(reqArchitectureAssess, "tenant_admin", "graph:write")
	mux.ServeHTTP(recArchitectureAssess, reqArchitectureAssess)
	if recArchitectureAssess.Code != http.StatusOK {
		t.Fatalf("expected graph architecture tasks assess 200, got %d body=%s", recArchitectureAssess.Code, recArchitectureAssess.Body.String())
	}
	var architectureAssessPayload struct {
		Path                string         `json:"path"`
		FocusedSubgraph     map[string]any `json:"focused_subgraph"`
		FocusedSubgraphPath string         `json:"focused_subgraph_path"`
		Report              map[string]any `json:"report"`
	}
	if err := json.Unmarshal(recArchitectureAssess.Body.Bytes(), &architectureAssessPayload); err != nil {
		t.Fatalf("decode architecture assess payload: %v", err)
	}
	if strings.TrimSpace(architectureAssessPayload.Path) == "" || architectureAssessPayload.Report == nil {
		t.Fatalf("expected architecture assess payload to include path/report, got %+v", architectureAssessPayload)
	}
	if strings.TrimSpace(architectureAssessPayload.FocusedSubgraphPath) == "" || architectureAssessPayload.FocusedSubgraph == nil {
		t.Fatalf("expected architecture assess payload to include focused subgraph artifact, got %+v", architectureAssessPayload)
	}
	recArchitectureGet := httptest.NewRecorder()
	reqArchitectureGet := httptest.NewRequest(http.MethodGet, "/graphs/g1/architecture-tasks", nil)
	reqArchitectureGet = withAuthRoleScope(reqArchitectureGet, "analyst", "graph:read")
	mux.ServeHTTP(recArchitectureGet, reqArchitectureGet)
	if recArchitectureGet.Code != http.StatusOK {
		t.Fatalf("expected graph architecture tasks get 200, got %d body=%s", recArchitectureGet.Code, recArchitectureGet.Body.String())
	}
	var architectureGetPayload struct {
		Path   string         `json:"path"`
		Report map[string]any `json:"report"`
	}
	if err := json.Unmarshal(recArchitectureGet.Body.Bytes(), &architectureGetPayload); err != nil {
		t.Fatalf("decode architecture get payload: %v", err)
	}
	if strings.TrimSpace(architectureGetPayload.Path) == "" || architectureGetPayload.Report == nil {
		t.Fatalf("expected architecture get payload to include persisted path/report, got %+v", architectureGetPayload)
	}

	recTemplates := httptest.NewRecorder()
	reqTemplates := httptest.NewRequest(http.MethodGet, "/products/templates", nil)
	reqTemplates = withAuthRoleScope(reqTemplates, "analyst", "graph:read")
	mux.ServeHTTP(recTemplates, reqTemplates)
	if recTemplates.Code != http.StatusOK {
		t.Fatalf("expected product templates 200, got %d body=%s", recTemplates.Code, recTemplates.Body.String())
	}
	var templatesPayload struct {
		Count     int `json:"count"`
		Templates []struct {
			ID string `json:"id"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(recTemplates.Body.Bytes(), &templatesPayload); err != nil {
		t.Fatalf("decode product templates: %v", err)
	}
	if templatesPayload.Count == 0 {
		t.Fatalf("expected non-empty product template catalog")
	}
	foundArchitectureTemplate := false
	for _, item := range templatesPayload.Templates {
		if strings.TrimSpace(item.ID) == "architecture-task-traces" {
			foundArchitectureTemplate = true
			break
		}
	}
	if !foundArchitectureTemplate {
		t.Fatalf("expected architecture-task-traces template to be present in product catalog")
	}
	recTemplateValidate := httptest.NewRecorder()
	reqTemplateValidate := httptest.NewRequest(http.MethodGet, "/products/templates/validate", nil)
	reqTemplateValidate = withAuthRoleScope(reqTemplateValidate, "analyst", "graph:read")
	mux.ServeHTTP(recTemplateValidate, reqTemplateValidate)
	if recTemplateValidate.Code != http.StatusOK {
		t.Fatalf("expected product template validate 200, got %d body=%s", recTemplateValidate.Code, recTemplateValidate.Body.String())
	}
	var templateValidatePayload struct {
		Valid         bool    `json:"valid"`
		Questions     int     `json:"questions_total"`
		Covered       int     `json:"questions_covered"`
		CoverageRatio float64 `json:"coverage_ratio"`
		ErrorCount    int     `json:"error_count"`
	}
	if err := json.Unmarshal(recTemplateValidate.Body.Bytes(), &templateValidatePayload); err != nil {
		t.Fatalf("decode template validate payload: %v", err)
	}
	if !templateValidatePayload.Valid || templateValidatePayload.ErrorCount != 0 {
		t.Fatalf("expected template validation to pass, got %+v", templateValidatePayload)
	}
	if templateValidatePayload.Questions == 0 || templateValidatePayload.Covered == 0 || templateValidatePayload.CoverageRatio <= 0 {
		t.Fatalf("expected non-zero question coverage in template validation, got %+v", templateValidatePayload)
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
	execDryRunBody := []byte(`{
		"template_id":"docs-service",
		"dry_run": true,
		"vars":{"graph_id":"g1","service_id":"a"}
	}`)
	recTemplateExecDryRun := httptest.NewRecorder()
	reqTemplateExecDryRun := httptest.NewRequest(http.MethodPost, "/products/templates/execute", bytes.NewReader(execDryRunBody))
	reqTemplateExecDryRun.Header.Set("Content-Type", "application/json")
	reqTemplateExecDryRun = withAuthRoleScope(reqTemplateExecDryRun, "analyst", "graph:read")
	mux.ServeHTTP(recTemplateExecDryRun, reqTemplateExecDryRun)
	if recTemplateExecDryRun.Code != http.StatusOK {
		t.Fatalf("expected product template dry-run execute 200, got %d body=%s", recTemplateExecDryRun.Code, recTemplateExecDryRun.Body.String())
	}
	var templateExecDryRunPayload struct {
		DryRun bool `json:"dry_run"`
		Status int  `json:"status"`
	}
	if err := json.Unmarshal(recTemplateExecDryRun.Body.Bytes(), &templateExecDryRunPayload); err != nil {
		t.Fatalf("decode product template dry-run payload: %v", err)
	}
	if !templateExecDryRunPayload.DryRun || templateExecDryRunPayload.Status != http.StatusOK {
		t.Fatalf("unexpected template dry-run payload: %+v", templateExecDryRunPayload)
	}
	invalidProductTemplatePath := filepath.Join(tmp, "m15-invalid-product-templates.json")
	invalidTemplateCatalog := map[string]any{
		"templates": []map[string]any{
			{
				"id":      "invalid-arch-method",
				"product": "architecture",
				"method":  "POST",
				"path":    "/products/architecture/${graph_id}",
			},
			{
				"id":      "invalid-arch-product",
				"product": "docs",
				"method":  "GET",
				"path":    "/products/architecture/${graph_id}",
			},
		},
	}
	invalidTemplateCatalogRaw, err := json.Marshal(invalidTemplateCatalog)
	if err != nil {
		t.Fatalf("marshal invalid template catalog: %v", err)
	}
	if err := os.WriteFile(invalidProductTemplatePath, invalidTemplateCatalogRaw, 0o644); err != nil {
		t.Fatalf("write invalid template catalog: %v", err)
	}
	invalidQuestionCatalogPath := filepath.Join(tmp, "m15-invalid-question-catalog.json")
	invalidQuestionCatalog := map[string]any{
		"questions": []map[string]any{
			{
				"id":       "q-arch",
				"question": "Architecture traces",
				"endpoint": "/products/architecture/{graph_id}?service={service_id}",
			},
		},
	}
	invalidQuestionCatalogRaw, err := json.Marshal(invalidQuestionCatalog)
	if err != nil {
		t.Fatalf("marshal invalid question catalog: %v", err)
	}
	if err := os.WriteFile(invalidQuestionCatalogPath, invalidQuestionCatalogRaw, 0o644); err != nil {
		t.Fatalf("write invalid question catalog: %v", err)
	}
	recTemplateValidateInvalid := httptest.NewRecorder()
	reqTemplateValidateInvalid := httptest.NewRequest(http.MethodGet, "/products/templates/validate?path="+url.QueryEscape(invalidProductTemplatePath)+"&catalog_path="+url.QueryEscape(invalidQuestionCatalogPath), nil)
	reqTemplateValidateInvalid = withAuthRoleScope(reqTemplateValidateInvalid, "analyst", "graph:read")
	mux.ServeHTTP(recTemplateValidateInvalid, reqTemplateValidateInvalid)
	if recTemplateValidateInvalid.Code != http.StatusOK {
		t.Fatalf("expected invalid product template validation 200, got %d body=%s", recTemplateValidateInvalid.Code, recTemplateValidateInvalid.Body.String())
	}
	var templateValidateInvalidPayload struct {
		Valid      bool `json:"valid"`
		ErrorCount int  `json:"error_count"`
	}
	if err := json.Unmarshal(recTemplateValidateInvalid.Body.Bytes(), &templateValidateInvalidPayload); err != nil {
		t.Fatalf("decode invalid template validate payload: %v", err)
	}
	if templateValidateInvalidPayload.Valid || templateValidateInvalidPayload.ErrorCount < 3 {
		t.Fatalf("expected invalid template validation failures, got %+v", templateValidateInvalidPayload)
	}
	if !strings.Contains(recTemplateValidateInvalid.Body.String(), "method must be GET for path") {
		t.Fatalf("expected method-path contract validation error, got %s", recTemplateValidateInvalid.Body.String())
	}
	if !strings.Contains(recTemplateValidateInvalid.Body.String(), "product must be") {
		t.Fatalf("expected product-path contract validation error, got %s", recTemplateValidateInvalid.Body.String())
	}
	if !strings.Contains(recTemplateValidateInvalid.Body.String(), "missing vars required by question endpoint") {
		t.Fatalf("expected question/template variable contract validation error, got %s", recTemplateValidateInvalid.Body.String())
	}
	recTemplateExecInvalid := httptest.NewRecorder()
	execInvalidBody := []byte(fmt.Sprintf(`{"template_id":"invalid-arch-method","template_path":%q,"vars":{"graph_id":"g1"}}`, invalidProductTemplatePath))
	reqTemplateExecInvalid := httptest.NewRequest(http.MethodPost, "/products/templates/execute", bytes.NewReader(execInvalidBody))
	reqTemplateExecInvalid.Header.Set("Content-Type", "application/json")
	reqTemplateExecInvalid = withAuthRoleScope(reqTemplateExecInvalid, "analyst", "graph:read")
	mux.ServeHTTP(recTemplateExecInvalid, reqTemplateExecInvalid)
	if recTemplateExecInvalid.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid product template execute 400, got %d body=%s", recTemplateExecInvalid.Code, recTemplateExecInvalid.Body.String())
	}
	if !strings.Contains(recTemplateExecInvalid.Body.String(), "template method must be GET for path") {
		t.Fatalf("expected template execute method-path contract enforcement error, got %s", recTemplateExecInvalid.Body.String())
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
	questionExecMissingVarsBody := []byte(`{
		"question_id":"q-runtime-build-deploy",
		"vars":{"graph_id":"g1"}
	}`)
	recQuestionExecMissingVars := httptest.NewRecorder()
	reqQuestionExecMissingVars := httptest.NewRequest(http.MethodPost, "/products/questions/execute", bytes.NewReader(questionExecMissingVarsBody))
	reqQuestionExecMissingVars.Header.Set("Content-Type", "application/json")
	reqQuestionExecMissingVars = withAuthRoleScope(reqQuestionExecMissingVars, "analyst", "graph:read")
	mux.ServeHTTP(recQuestionExecMissingVars, reqQuestionExecMissingVars)
	if recQuestionExecMissingVars.Code != http.StatusBadRequest {
		t.Fatalf("expected question execute missing-vars 400, got %d body=%s", recQuestionExecMissingVars.Code, recQuestionExecMissingVars.Body.String())
	}
	if !strings.Contains(recQuestionExecMissingVars.Body.String(), "missing template vars") || !strings.Contains(recQuestionExecMissingVars.Body.String(), "service_id") {
		t.Fatalf("expected missing template vars error for question execute, got %s", recQuestionExecMissingVars.Body.String())
	}

	recQuestionCoverage := httptest.NewRecorder()
	reqQuestionCoverage := httptest.NewRequest(http.MethodGet, "/products/questions/coverage", nil)
	reqQuestionCoverage = withAuthRoleScope(reqQuestionCoverage, "analyst", "graph:read")
	mux.ServeHTTP(recQuestionCoverage, reqQuestionCoverage)
	if recQuestionCoverage.Code != http.StatusOK {
		t.Fatalf("expected question coverage 200, got %d body=%s", recQuestionCoverage.Code, recQuestionCoverage.Body.String())
	}
	var coveragePayload struct {
		Total                int     `json:"total"`
		Covered              int     `json:"covered"`
		ContractValidCovered int     `json:"contract_valid_covered"`
		CoverageRatio        float64 `json:"coverage_ratio"`
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
	if coveragePayload.ContractValidCovered == 0 {
		t.Fatalf("expected non-zero contract-valid covered questions, got %+v", coveragePayload)
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

	recOpsSLOEval := httptest.NewRecorder()
	reqOpsSLOEval := httptest.NewRequest(http.MethodPost, "/ops/slo/evaluate", bytes.NewReader([]byte(`{"force_incident":true,"reason":"test_drill"}`)))
	reqOpsSLOEval.Header.Set("Content-Type", "application/json")
	reqOpsSLOEval = withAuthRoleScope(reqOpsSLOEval, "tenant_admin", "ops:write")
	mux.ServeHTTP(recOpsSLOEval, reqOpsSLOEval)
	if recOpsSLOEval.Code != http.StatusOK {
		t.Fatalf("expected ops slo evaluate 200, got %d body=%s", recOpsSLOEval.Code, recOpsSLOEval.Body.String())
	}
	var sloEvalPayload struct {
		IncidentCreated bool   `json:"incident_created"`
		IncidentID      string `json:"incident_id"`
	}
	if err := json.Unmarshal(recOpsSLOEval.Body.Bytes(), &sloEvalPayload); err != nil {
		t.Fatalf("decode ops slo evaluate payload: %v", err)
	}
	if !sloEvalPayload.IncidentCreated || sloEvalPayload.IncidentID == "" {
		t.Fatalf("expected incident creation on forced eval, got %+v", sloEvalPayload)
	}

	recOpsIncidents := httptest.NewRecorder()
	reqOpsIncidents := httptest.NewRequest(http.MethodGet, "/ops/incidents?status=open", nil)
	reqOpsIncidents = withAuthRoleScope(reqOpsIncidents, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recOpsIncidents, reqOpsIncidents)
	if recOpsIncidents.Code != http.StatusOK {
		t.Fatalf("expected ops incidents 200, got %d body=%s", recOpsIncidents.Code, recOpsIncidents.Body.String())
	}
	var incidentsPayload struct {
		Incidents []map[string]any `json:"incidents"`
	}
	if err := json.Unmarshal(recOpsIncidents.Body.Bytes(), &incidentsPayload); err != nil {
		t.Fatalf("decode ops incidents payload: %v", err)
	}
	if len(incidentsPayload.Incidents) == 0 {
		t.Fatalf("expected non-empty ops incidents list")
	}

	recOpsIncident := httptest.NewRecorder()
	reqOpsIncident := httptest.NewRequest(http.MethodGet, "/ops/incidents/"+sloEvalPayload.IncidentID, nil)
	reqOpsIncident = withAuthRoleScope(reqOpsIncident, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recOpsIncident, reqOpsIncident)
	if recOpsIncident.Code != http.StatusOK {
		t.Fatalf("expected ops incident by id 200, got %d body=%s", recOpsIncident.Code, recOpsIncident.Body.String())
	}

	recOpsPolicy := httptest.NewRecorder()
	reqOpsPolicy := httptest.NewRequest(http.MethodGet, "/ops/rollout-policy", nil)
	reqOpsPolicy = withAuthRoleScope(reqOpsPolicy, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recOpsPolicy, reqOpsPolicy)
	if recOpsPolicy.Code != http.StatusOK {
		t.Fatalf("expected ops rollout policy 200, got %d body=%s", recOpsPolicy.Code, recOpsPolicy.Body.String())
	}

	opsBackupPath := filepath.Join(tmp, "ops-output", "ops-backup.tar.gz")
	recOpsBackup := httptest.NewRecorder()
	reqOpsBackupBody := []byte(fmt.Sprintf(`{"source_root":%q,"out_path":%q}`, graphRoot, opsBackupPath))
	reqOpsBackup := httptest.NewRequest(http.MethodPost, "/ops/backup", bytes.NewReader(reqOpsBackupBody))
	reqOpsBackup.Header.Set("Content-Type", "application/json")
	reqOpsBackup = withAuthRoleScope(reqOpsBackup, "tenant_admin", "ops:write")
	mux.ServeHTTP(recOpsBackup, reqOpsBackup)
	if recOpsBackup.Code != http.StatusOK {
		t.Fatalf("expected ops backup 200, got %d body=%s", recOpsBackup.Code, recOpsBackup.Body.String())
	}

	opsRestoreTarget := filepath.Join(tmp, "ops-output", "restore")
	recOpsRestore := httptest.NewRecorder()
	reqOpsRestoreBody := []byte(fmt.Sprintf(`{"archive_path":%q,"target_root":%q}`, opsBackupPath, opsRestoreTarget))
	reqOpsRestore := httptest.NewRequest(http.MethodPost, "/ops/restore", bytes.NewReader(reqOpsRestoreBody))
	reqOpsRestore.Header.Set("Content-Type", "application/json")
	reqOpsRestore = withAuthRoleScope(reqOpsRestore, "tenant_admin", "ops:write")
	mux.ServeHTTP(recOpsRestore, reqOpsRestore)
	if recOpsRestore.Code != http.StatusOK {
		t.Fatalf("expected ops restore 200, got %d body=%s", recOpsRestore.Code, recOpsRestore.Body.String())
	}

	opsRolloutPath := filepath.Join(tmp, "ops-output", "rollout_plan.json")
	recOpsRollout := httptest.NewRecorder()
	reqOpsRolloutBody := []byte(fmt.Sprintf(`{"component":"extractor","candidate":"v2.0.0","current":"v1.0.0","out_path":%q}`, opsRolloutPath))
	reqOpsRollout := httptest.NewRequest(http.MethodPost, "/ops/rollout", bytes.NewReader(reqOpsRolloutBody))
	reqOpsRollout.Header.Set("Content-Type", "application/json")
	reqOpsRollout = withAuthRoleScope(reqOpsRollout, "tenant_admin", "ops:write")
	mux.ServeHTTP(recOpsRollout, reqOpsRollout)
	if recOpsRollout.Code != http.StatusOK {
		t.Fatalf("expected ops rollout 200, got %d body=%s", recOpsRollout.Code, recOpsRollout.Body.String())
	}

	opsQualityPath := filepath.Join(tmp, "quality", "report.json")
	writeJSONFile(t, opsQualityPath, map[string]any{
		"metrics": map[string]any{"pass_rate": 1.0},
	})
	recOpsDrill := httptest.NewRecorder()
	reqOpsDrillBody := []byte(fmt.Sprintf(`{"source_root":%q,"quality_path":%q,"drill_out_dir":%q}`, graphRoot, opsQualityPath, filepath.Join(tmp, "ops-output", "drill")))
	reqOpsDrill := httptest.NewRequest(http.MethodPost, "/ops/drill", bytes.NewReader(reqOpsDrillBody))
	reqOpsDrill.Header.Set("Content-Type", "application/json")
	reqOpsDrill = withAuthRoleScope(reqOpsDrill, "tenant_admin", "ops:write")
	mux.ServeHTTP(recOpsDrill, reqOpsDrill)
	if recOpsDrill.Code != http.StatusOK {
		t.Fatalf("expected ops drill 200, got %d body=%s", recOpsDrill.Code, recOpsDrill.Body.String())
	}
	var opsDrillPayload struct {
		Passed bool `json:"passed"`
	}
	if err := json.Unmarshal(recOpsDrill.Body.Bytes(), &opsDrillPayload); err != nil {
		t.Fatalf("decode ops drill payload: %v", err)
	}
	if !opsDrillPayload.Passed {
		t.Fatalf("expected ops drill passed=true, body=%s", recOpsDrill.Body.String())
	}

	finalQualityPath := filepath.Join(tmp, "final-inputs", "quality_gate.json")
	finalMergeQualityPath := filepath.Join(tmp, "final-inputs", "merge_quality_report.json")
	finalMergeExpectLinksPath := filepath.Join(tmp, "final-inputs", "expected_links.json")
	finalSLOPath := filepath.Join(tmp, "final-inputs", "slo.json")
	finalTemplatesPath := filepath.Join(tmp, "final-inputs", "templates.json")
	finalCatalogPath := filepath.Join(tmp, "final-inputs", "catalog.json")
	finalGraphIndexPath := filepath.Join(tmp, "final-inputs", "graph_index.json")
	finalOutReportPath := filepath.Join(tmp, "final-output", "readiness_report.json")
	finalOutDecisionPath := filepath.Join(tmp, "final-output", "gate_decision.md")
	writeJSONFile(t, finalQualityPath, map[string]any{"passed": true})
	writeJSONFile(t, finalMergeQualityPath, map[string]any{
		"passed": true,
		"benchmark": map[string]any{
			"passed": true,
		},
	})
	writeJSONFile(t, finalMergeExpectLinksPath, map[string]any{
		"service_calls_service": []map[string]any{
			{"source_service_id": "a", "source_repo_path": "/repos/a", "target_service_id": "b", "target_repo_path": "/repos/b"},
		},
	})
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
		"quality_gate_path":               finalQualityPath,
		"merge_quality_path":              finalMergeQualityPath,
		"merge_quality_expect_links_path": finalMergeExpectLinksPath,
		"slo_path":                        finalSLOPath,
		"templates_path":                  finalTemplatesPath,
		"catalog_path":                    finalCatalogPath,
		"graph_index_path":                finalGraphIndexPath,
		"out_report_path":                 finalOutReportPath,
		"out_decision_path":               finalOutDecisionPath,
		"signers":                         []string{"engineering", "platform", "security"},
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

	finalQualityReportPath := filepath.Join(tmp, "final-inputs", "quality_report.json")
	finalCorpusReportPath := filepath.Join(tmp, "final-inputs", "corpus_report.json")
	finalPerfPolicyPath := filepath.Join(tmp, "final-inputs", "perf_policy.md")
	finalAuditRoot := filepath.Join(tmp, "final-inputs", "audit-root")
	finalDrillSource := filepath.Join(tmp, "final-inputs", "drill-source")
	finalDrillOutDir := filepath.Join(tmp, "final-output", "drills")
	finalOutMilestonesPath := filepath.Join(tmp, "final-output", "milestone_closure_report.json")
	finalOutBenchmarkPath := filepath.Join(tmp, "final-output", "benchmark_evidence_report.json")
	finalOutSecurityPath := filepath.Join(tmp, "final-output", "security_validation_report.json")
	finalOutOpsPath := filepath.Join(tmp, "final-output", "operations_drill_report.json")
	finalContractReportPath := filepath.Join(tmp, "final-inputs", "contract_report.json")
	finalOutClosureRulesPath := filepath.Join(tmp, "final-output", "closure_rules_report.json")
	writeJSONFile(t, finalQualityReportPath, map[string]any{"metrics": map[string]any{"pass_rate": 0.99}})
	writeJSONFile(t, finalCorpusReportPath, map[string]any{"cases": []map[string]any{{"name": "fixture", "status": "passed"}}})
	writeJSONFile(t, finalContractReportPath, map[string]any{
		"passed": true,
		"surfaces": map[string]any{
			"endpoints": map[string]any{
				"evidence_samples": []map[string]any{
					{"value": "GET /health", "links": []string{"graph://node/endpoint:health"}},
				},
			},
		},
	})
	if err := os.MkdirAll(filepath.Dir(finalPerfPolicyPath), 0o755); err != nil {
		t.Fatalf("mkdir perf policy dir: %v", err)
	}
	if err := os.WriteFile(finalPerfPolicyPath, []byte("p95<=250ms\n"), 0o644); err != nil {
		t.Fatalf("write perf policy: %v", err)
	}
	if err := os.MkdirAll(finalAuditRoot, 0o755); err != nil {
		t.Fatalf("mkdir audit root: %v", err)
	}
	if err := audit.AppendEvent(finalAuditRoot, audit.Event{
		Timestamp: time.Now().UTC(),
		Action:    "query_graph",
		TenantID:  "default",
		Principal: "test-user",
		Method:    "GET",
		Path:      "/graphs/g1",
		Decision:  "allow",
	}); err != nil {
		t.Fatalf("append final closeout audit event: %v", err)
	}
	if err := os.MkdirAll(finalDrillSource, 0o755); err != nil {
		t.Fatalf("mkdir drill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(finalDrillSource, "seed.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write drill source seed: %v", err)
	}

	finalCloseoutReqBody := map[string]any{
		"quality_gate_path":               finalQualityPath,
		"merge_quality_path":              finalMergeQualityPath,
		"merge_quality_expect_links_path": finalMergeExpectLinksPath,
		"slo_path":                        finalSLOPath,
		"templates_path":                  finalTemplatesPath,
		"catalog_path":                    finalCatalogPath,
		"graph_index_path":                finalGraphIndexPath,
		"contract_report_path":            finalContractReportPath,
		"quality_report_path":             finalQualityReportPath,
		"corpus_report_path":              finalCorpusReportPath,
		"performance_policy_path":         finalPerfPolicyPath,
		"audit_root":                      finalAuditRoot,
		"drill_source":                    finalDrillSource,
		"drill_out_dir":                   finalDrillOutDir,
		"out_report_path":                 finalOutReportPath,
		"out_decision_path":               finalOutDecisionPath,
		"out_milestones_path":             finalOutMilestonesPath,
		"out_benchmark_path":              finalOutBenchmarkPath,
		"out_security_path":               finalOutSecurityPath,
		"out_ops_path":                    finalOutOpsPath,
		"out_closure_rules_path":          finalOutClosureRulesPath,
		"signers":                         []string{"engineering", "platform", "security"},
	}
	finalCloseoutReqBodyBytes, err := json.Marshal(finalCloseoutReqBody)
	if err != nil {
		t.Fatalf("marshal final closeout request body: %v", err)
	}
	recFinalCloseout := httptest.NewRecorder()
	reqFinalCloseout := httptest.NewRequest(http.MethodPost, "/final/closeout", bytes.NewReader(finalCloseoutReqBodyBytes))
	reqFinalCloseout.Header.Set("Content-Type", "application/json")
	reqFinalCloseout = withAuthRoleScope(reqFinalCloseout, "compliance_auditor", "audit:export")
	mux.ServeHTTP(recFinalCloseout, reqFinalCloseout)
	if recFinalCloseout.Code != http.StatusOK {
		t.Fatalf("expected final closeout 200, got %d body=%s", recFinalCloseout.Code, recFinalCloseout.Body.String())
	}
	var finalCloseoutPayload struct {
		OverallPassed bool `json:"overall_passed"`
	}
	if err := json.Unmarshal(recFinalCloseout.Body.Bytes(), &finalCloseoutPayload); err != nil {
		t.Fatalf("decode final closeout payload: %v", err)
	}
	if !finalCloseoutPayload.OverallPassed {
		t.Fatalf("expected final closeout overall_passed=true, got body=%s", recFinalCloseout.Body.String())
	}

	recFinalMilestones := httptest.NewRecorder()
	reqFinalMilestones := httptest.NewRequest(http.MethodGet, "/final/milestones?path="+url.QueryEscape(finalOutMilestonesPath), nil)
	reqFinalMilestones = withAuthRoleScope(reqFinalMilestones, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recFinalMilestones, reqFinalMilestones)
	if recFinalMilestones.Code != http.StatusOK {
		t.Fatalf("expected final milestones 200, got %d body=%s", recFinalMilestones.Code, recFinalMilestones.Body.String())
	}
	recFinalBenchmark := httptest.NewRecorder()
	reqFinalBenchmark := httptest.NewRequest(http.MethodGet, "/final/benchmark?path="+url.QueryEscape(finalOutBenchmarkPath), nil)
	reqFinalBenchmark = withAuthRoleScope(reqFinalBenchmark, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recFinalBenchmark, reqFinalBenchmark)
	if recFinalBenchmark.Code != http.StatusOK {
		t.Fatalf("expected final benchmark 200, got %d body=%s", recFinalBenchmark.Code, recFinalBenchmark.Body.String())
	}
	recFinalSecurity := httptest.NewRecorder()
	reqFinalSecurity := httptest.NewRequest(http.MethodGet, "/final/security?path="+url.QueryEscape(finalOutSecurityPath), nil)
	reqFinalSecurity = withAuthRoleScope(reqFinalSecurity, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recFinalSecurity, reqFinalSecurity)
	if recFinalSecurity.Code != http.StatusOK {
		t.Fatalf("expected final security 200, got %d body=%s", recFinalSecurity.Code, recFinalSecurity.Body.String())
	}
	recFinalOps := httptest.NewRecorder()
	reqFinalOps := httptest.NewRequest(http.MethodGet, "/final/ops?path="+url.QueryEscape(finalOutOpsPath), nil)
	reqFinalOps = withAuthRoleScope(reqFinalOps, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recFinalOps, reqFinalOps)
	if recFinalOps.Code != http.StatusOK {
		t.Fatalf("expected final ops 200, got %d body=%s", recFinalOps.Code, recFinalOps.Body.String())
	}
	recFinalClosureRules := httptest.NewRecorder()
	reqFinalClosureRules := httptest.NewRequest(http.MethodGet, "/final/closure-rules?path="+url.QueryEscape(finalOutClosureRulesPath), nil)
	reqFinalClosureRules = withAuthRoleScope(reqFinalClosureRules, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recFinalClosureRules, reqFinalClosureRules)
	if recFinalClosureRules.Code != http.StatusOK {
		t.Fatalf("expected final closure-rules 200, got %d body=%s", recFinalClosureRules.Code, recFinalClosureRules.Body.String())
	}
}

func TestMergeQualityEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}

	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), map[string]any{
		"graph_id": "g1",
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
					"source_service_id": "svc-a",
					"source_repo_path":  "/repos/a",
					"target_service_id": "svc-b",
					"target_repo_path":  "/repos/b",
				},
				"confidence": 0.95,
				"inferred":   false,
			},
		},
		"stats": map[string]any{
			"node_count":   2,
			"edge_count":   1,
			"by_node_type": map[string]any{"service": 2},
			"by_edge_type": map[string]any{"service_calls_service": 1},
		},
		"meta": map[string]any{
			"tenant_id": "default",
			"services":  []any{},
		},
	})
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "tenant_id": "default", "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	})
	writeJSONFile(t, filepath.Join(graphRoot, "merge_quality_report.json"), map[string]any{
		"graph_id": "g1",
		"passed":   true,
		"metrics":  map[string]any{"service_calls_total": 1, "repo_provenance_coverage": 1.0},
	})

	mux := newMux(filepath.Join(tmp, "bundle.json"), graphRoot)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphs/merge-quality", nil)
	req = withAuth(req)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /graphs/merge-quality 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Report map[string]any `json:"report"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode /graphs/merge-quality response: %v", err)
	}
	if got, _ := payload.Report["graph_id"].(string); got != "g1" {
		t.Fatalf("expected graph_id g1 in merge quality report, got %q", got)
	}

	recByID := httptest.NewRecorder()
	reqByID := httptest.NewRequest(http.MethodGet, "/graphs/g1/merge-quality", nil)
	reqByID = withAuth(reqByID)
	mux.ServeHTTP(recByID, reqByID)
	if recByID.Code != http.StatusOK {
		t.Fatalf("expected /graphs/g1/merge-quality 200, got %d body=%s", recByID.Code, recByID.Body.String())
	}

	expectLinksPath := filepath.Join(graphRoot, "expected_links.json")
	writeJSONFile(t, expectLinksPath, map[string]any{
		"service_calls_service": []map[string]any{
			{
				"source_service_id": "svc-a",
				"source_repo_path":  "/repos/a",
				"target_service_id": "svc-b",
				"target_repo_path":  "/repos/b",
			},
		},
	})
	assessReqBody := map[string]any{
		"graph_path":        filepath.Join(graphRoot, "g1", "graph.json"),
		"expect_links_path": expectLinksPath,
		"out_path":          filepath.Join(graphRoot, "merge_quality_report.json"),
		"fail_on_gate":      false,
	}
	assessBody, err := json.Marshal(assessReqBody)
	if err != nil {
		t.Fatalf("marshal merge quality assess request: %v", err)
	}
	recPost := httptest.NewRecorder()
	reqPost := httptest.NewRequest(http.MethodPost, "/graphs/merge-quality", bytes.NewReader(assessBody))
	reqPost.Header.Set("Content-Type", "application/json")
	reqPost = withAuth(reqPost)
	mux.ServeHTTP(recPost, reqPost)
	if recPost.Code != http.StatusOK {
		t.Fatalf("expected POST /graphs/merge-quality 200, got %d body=%s", recPost.Code, recPost.Body.String())
	}
	var postPayload struct {
		Passed bool           `json:"passed"`
		Report map[string]any `json:"report"`
	}
	if err := json.Unmarshal(recPost.Body.Bytes(), &postPayload); err != nil {
		t.Fatalf("decode POST /graphs/merge-quality response: %v", err)
	}
	if !postPayload.Passed {
		t.Fatalf("expected merge-quality assess result to pass")
	}
	bench, _ := postPayload.Report["benchmark"].(map[string]any)
	if bench == nil {
		t.Fatalf("expected benchmark section in assessed merge quality report")
	}

	scopedBody, err := json.Marshal(map[string]any{
		"expect_links_path": expectLinksPath,
	})
	if err != nil {
		t.Fatalf("marshal scoped merge quality request: %v", err)
	}
	recScoped := httptest.NewRecorder()
	reqScoped := httptest.NewRequest(http.MethodPost, "/graphs/g1/merge-quality", bytes.NewReader(scopedBody))
	reqScoped.Header.Set("Content-Type", "application/json")
	reqScoped = withAuth(reqScoped)
	mux.ServeHTTP(recScoped, reqScoped)
	if recScoped.Code != http.StatusOK {
		t.Fatalf("expected POST /graphs/g1/merge-quality 200, got %d body=%s", recScoped.Code, recScoped.Body.String())
	}
	var scopedPayload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(recScoped.Body.Bytes(), &scopedPayload); err != nil {
		t.Fatalf("decode scoped merge-quality response: %v", err)
	}
	expectedScopedPath := filepath.Join(graphRoot, "g1", "merge_quality_report.json")
	if scopedPayload.Path != expectedScopedPath {
		t.Fatalf("expected scoped merge-quality path %q, got %q", expectedScopedPath, scopedPayload.Path)
	}

	badExpectLinksPath := filepath.Join(graphRoot, "bad_expected_links.json")
	writeJSONFile(t, badExpectLinksPath, map[string]any{
		"service_calls_service": []map[string]any{
			{
				"source_service_id": "svc-x",
				"target_service_id": "svc-y",
			},
		},
	})
	scopedFailBody, err := json.Marshal(map[string]any{
		"expect_links_path": badExpectLinksPath,
		"out_path":          filepath.Join(graphRoot, "merge_quality_report.json"),
		"fail_on_gate":      true,
	})
	if err != nil {
		t.Fatalf("marshal scoped merge quality fail request: %v", err)
	}
	recScopedFail := httptest.NewRecorder()
	reqScopedFail := httptest.NewRequest(http.MethodPost, "/graphs/g1/merge-quality", bytes.NewReader(scopedFailBody))
	reqScopedFail.Header.Set("Content-Type", "application/json")
	reqScopedFail = withAuth(reqScopedFail)
	mux.ServeHTTP(recScopedFail, reqScopedFail)
	if recScopedFail.Code != http.StatusBadRequest {
		t.Fatalf("expected POST /graphs/g1/merge-quality fail_on_gate to return 400, got %d body=%s", recScopedFail.Code, recScopedFail.Body.String())
	}
}

func TestQualityEndpoints(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "artifacts")
	graphRoot := filepath.Join(root, "graph")
	if err := os.MkdirAll(graphRoot, 0o755); err != nil {
		t.Fatalf("mkdir graph root: %v", err)
	}
	corpusPath := filepath.Join(root, "corpus", "report.json")
	goldenPath := filepath.Join(root, "corpus", "golden.summary.json")
	mergeQualityPath := filepath.Join(root, "graph", "merge_quality_report.json")
	policyPath := filepath.Join(root, "quality", "policy.pass.json")
	policyStrictPath := filepath.Join(root, "quality", "policy.fail.json")
	gateOutPath := filepath.Join(root, "quality", "gate_result.json")
	gateFailOutPath := filepath.Join(root, "quality", "gate_result.fail.json")

	writeJSONFile(t, corpusPath, map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1}, "domain": "api", "language": "go", "framework": "gin", "framework_version": "v1", "duration_ms": 120, "confidence": 0.95},
			{"name": "case-b", "status": "failed", "counts_by_type": map[string]any{"Endpoint": 1}, "domain": "api", "language": "go", "framework": "gin", "framework_version": "v1", "duration_ms": 180, "tags": []string{"sev1"}, "failures": []string{"required entity type missing: RuntimeUnit"}, "confidence": 0.9},
		},
	})
	writeJSONFile(t, goldenPath, map[string]any{
		"cases": []map[string]any{
			{"name": "case-a", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1}},
			{"name": "case-b", "status": "passed", "counts_by_type": map[string]any{"Endpoint": 1, "RuntimeUnit": 1}},
		},
	})
	writeJSONFile(t, mergeQualityPath, map[string]any{
		"graph_id": "g1",
		"passed":   true,
		"metrics": map[string]any{
			"repo_provenance_coverage": 1.0,
			"unresolved_rate":          0.0,
			"ambiguous_rate":           0.0,
		},
	})
	writeJSONFile(t, policyPath, map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                  0.5,
			"precision":                  0.5,
			"recall":                     0.5,
			"f1":                         0.5,
			"calibration_error_max":      0.8,
			"adversarial_pass_rate":      0.0,
			"framework_matrix_pass_rate": 0.0,
			"drift_precision":            0.0,
			"drift_recall":               0.0,
			"drift_f1":                   0.0,
			"benchmark_p95_ms_max":       3000,
		},
		"severity1": map[string]any{"regressions_max": 1},
	})
	writeJSONFile(t, policyStrictPath, map[string]any{
		"thresholds": map[string]any{
			"pass_rate":                  0.5,
			"precision":                  0.5,
			"recall":                     0.5,
			"f1":                         0.5,
			"calibration_error_max":      0.8,
			"adversarial_pass_rate":      0.0,
			"framework_matrix_pass_rate": 0.0,
			"drift_precision":            0.0,
			"drift_recall":               0.0,
			"drift_f1":                   0.0,
			"benchmark_p95_ms_max":       3000,
		},
		"severity1": map[string]any{"regressions_max": 0},
	})

	mux := newMux(filepath.Join(tmp, "bundle.json"), graphRoot)
	evaluateBody, err := json.Marshal(map[string]any{
		"corpus_path":         corpusPath,
		"golden_path":         goldenPath,
		"merge_quality_path":  mergeQualityPath,
		"merge_quality_auto":  false,
		"report_path":         filepath.Join(root, "quality", "report.json"),
		"dashboard_path":      filepath.Join(root, "quality", "dashboard.md"),
		"triage_path":         filepath.Join(root, "quality", "triage.md"),
		"graph_index_path":    filepath.Join(root, "graph", "index.json"),
		"out_path":            filepath.Join(root, "quality", "report.json"),
		"merge_quality_check": false,
	})
	if err != nil {
		t.Fatalf("marshal quality evaluate request: %v", err)
	}
	recEval := httptest.NewRecorder()
	reqEval := httptest.NewRequest(http.MethodPost, "/quality/evaluate", bytes.NewReader(evaluateBody))
	reqEval.Header.Set("Content-Type", "application/json")
	reqEval = withAuth(reqEval)
	mux.ServeHTTP(recEval, reqEval)
	if recEval.Code != http.StatusOK {
		t.Fatalf("expected POST /quality/evaluate 200, got %d body=%s", recEval.Code, recEval.Body.String())
	}
	var evalPayload struct {
		ReportPath    string         `json:"report_path"`
		DashboardPath string         `json:"dashboard_path"`
		TriagePath    string         `json:"triage_path"`
		Report        map[string]any `json:"report"`
		Dashboard     string         `json:"dashboard"`
		Triage        string         `json:"triage"`
	}
	if err := json.Unmarshal(recEval.Body.Bytes(), &evalPayload); err != nil {
		t.Fatalf("decode quality evaluate response: %v", err)
	}
	if evalPayload.ReportPath == "" || evalPayload.DashboardPath == "" || evalPayload.TriagePath == "" {
		t.Fatalf("expected quality evaluate output paths to be populated")
	}
	if len(evalPayload.Dashboard) == 0 || len(evalPayload.Triage) == 0 {
		t.Fatalf("expected quality dashboard and triage content to be returned")
	}

	recGate := httptest.NewRecorder()
	reqGateBody, err := json.Marshal(map[string]any{
		"report_path": evalPayload.ReportPath,
		"policy_path": policyPath,
		"out_path":    gateOutPath,
	})
	if err != nil {
		t.Fatalf("marshal quality gate request: %v", err)
	}
	reqGate := httptest.NewRequest(http.MethodPost, "/quality/gate", bytes.NewReader(reqGateBody))
	reqGate.Header.Set("Content-Type", "application/json")
	reqGate = withAuth(reqGate)
	mux.ServeHTTP(recGate, reqGate)
	if recGate.Code != http.StatusOK {
		t.Fatalf("expected POST /quality/gate pass 200, got %d body=%s", recGate.Code, recGate.Body.String())
	}
	var gatePayload struct {
		OverallPassed bool           `json:"overall_passed"`
		Result        map[string]any `json:"result"`
	}
	if err := json.Unmarshal(recGate.Body.Bytes(), &gatePayload); err != nil {
		t.Fatalf("decode quality gate response: %v", err)
	}
	if !gatePayload.OverallPassed {
		t.Fatalf("expected quality gate pass response to set overall_passed=true")
	}

	recGateGet := httptest.NewRecorder()
	reqGateGet := httptest.NewRequest(http.MethodGet, "/quality/gate?path="+url.QueryEscape(gateOutPath), nil)
	reqGateGet = withAuth(reqGateGet)
	mux.ServeHTTP(recGateGet, reqGateGet)
	if recGateGet.Code != http.StatusOK {
		t.Fatalf("expected GET /quality/gate 200, got %d body=%s", recGateGet.Code, recGateGet.Body.String())
	}

	recGateFail := httptest.NewRecorder()
	reqGateFailBody, err := json.Marshal(map[string]any{
		"report_path": evalPayload.ReportPath,
		"policy_path": policyStrictPath,
		"out_path":    gateFailOutPath,
	})
	if err != nil {
		t.Fatalf("marshal quality gate fail request: %v", err)
	}
	reqGateFail := httptest.NewRequest(http.MethodPost, "/quality/gate", bytes.NewReader(reqGateFailBody))
	reqGateFail.Header.Set("Content-Type", "application/json")
	reqGateFail = withAuth(reqGateFail)
	mux.ServeHTTP(recGateFail, reqGateFail)
	if recGateFail.Code != http.StatusOK {
		t.Fatalf("expected POST /quality/gate fail response 200, got %d body=%s", recGateFail.Code, recGateFail.Body.String())
	}
	var gateFailPayload struct {
		OverallPassed bool   `json:"overall_passed"`
		GateError     string `json:"gate_error"`
	}
	if err := json.Unmarshal(recGateFail.Body.Bytes(), &gateFailPayload); err != nil {
		t.Fatalf("decode failing quality gate response: %v", err)
	}
	if gateFailPayload.OverallPassed {
		t.Fatalf("expected failing quality gate response to set overall_passed=false")
	}
	if gateFailPayload.GateError == "" {
		t.Fatalf("expected failing quality gate response to include gate_error")
	}

	recReport := httptest.NewRecorder()
	reqReport := httptest.NewRequest(http.MethodGet, "/quality/report?path="+url.QueryEscape(evalPayload.ReportPath), nil)
	reqReport = withAuth(reqReport)
	mux.ServeHTTP(recReport, reqReport)
	if recReport.Code != http.StatusOK {
		t.Fatalf("expected GET /quality/report 200, got %d body=%s", recReport.Code, recReport.Body.String())
	}

	recDashboard := httptest.NewRecorder()
	reqDashboard := httptest.NewRequest(http.MethodGet, "/quality/dashboard?path="+url.QueryEscape(evalPayload.DashboardPath), nil)
	reqDashboard = withAuth(reqDashboard)
	mux.ServeHTTP(recDashboard, reqDashboard)
	if recDashboard.Code != http.StatusOK {
		t.Fatalf("expected GET /quality/dashboard 200, got %d body=%s", recDashboard.Code, recDashboard.Body.String())
	}

	recTriage := httptest.NewRecorder()
	reqTriage := httptest.NewRequest(http.MethodGet, "/quality/triage?path="+url.QueryEscape(evalPayload.TriagePath), nil)
	reqTriage = withAuth(reqTriage)
	mux.ServeHTTP(recTriage, reqTriage)
	if recTriage.Code != http.StatusOK {
		t.Fatalf("expected GET /quality/triage 200, got %d body=%s", recTriage.Code, recTriage.Body.String())
	}
}

func TestGraphConflictStoreAndAdjudications(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "tenant_id": "default", "mode": "single", "node_count": 3, "edge_count": 1, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	})
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "ep:orders", "type": "endpoint", "label": "GET /orders", "service_id": "a", "verification_state": "needs_review", "attributes": map[string]any{}, "confidence": 0.62, "inferred": false},
			{"id": "conflict:a:1", "type": "conflict", "label": "timeout mismatch", "service_id": "a", "attributes": map[string]any{"status": "open", "conflict_type": "config_mismatch"}, "confidence": 0.52, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e-conflict", "type": "service_has_conflict", "source_id": "svc:a", "target_id": "conflict:a:1", "attributes": map[string]any{}, "confidence": 0.52, "inferred": false, "evidence_refs": []any{}},
		},
		"stats": map[string]any{"node_count": 3, "edge_count": 1, "by_node_type": map[string]any{"service": 1, "endpoint": 1, "conflict": 1}, "by_edge_type": map[string]any{"service_has_conflict": 1}},
		"meta":  map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	})
	mux := newMux("", graphRoot)

	recConflicts := httptest.NewRecorder()
	reqConflicts := httptest.NewRequest(http.MethodGet, "/graphs/g1/conflicts", nil)
	reqConflicts = withAuthRoleScope(reqConflicts, "analyst", "graph:read")
	mux.ServeHTTP(recConflicts, reqConflicts)
	if recConflicts.Code != http.StatusOK {
		t.Fatalf("expected conflicts endpoint 200, got %d body=%s", recConflicts.Code, recConflicts.Body.String())
	}
	var conflictsPayload struct {
		Total int `json:"total"`
		Open  int `json:"open"`
	}
	if err := json.Unmarshal(recConflicts.Body.Bytes(), &conflictsPayload); err != nil {
		t.Fatalf("decode conflicts payload: %v", err)
	}
	if conflictsPayload.Total != 1 || conflictsPayload.Open != 1 {
		t.Fatalf("unexpected conflicts payload: %+v", conflictsPayload)
	}

	recAdjudicate := httptest.NewRecorder()
	reqAdjudicate := httptest.NewRequest(http.MethodPost, "/graphs/g1/adjudications", bytes.NewReader([]byte(`{
		"target_id":"ep:orders",
		"target_kind":"node",
		"decision":"verified",
		"reason":"validated in production traces",
		"source":"human_review",
		"confidence":0.94
	}`)))
	reqAdjudicate.Header.Set("Content-Type", "application/json")
	reqAdjudicate = withAuthRoleScope(reqAdjudicate, "tenant_admin", "graph:write")
	mux.ServeHTTP(recAdjudicate, reqAdjudicate)
	if recAdjudicate.Code != http.StatusOK {
		t.Fatalf("expected adjudication post 200, got %d body=%s", recAdjudicate.Code, recAdjudicate.Body.String())
	}

	recAdjList := httptest.NewRecorder()
	reqAdjList := httptest.NewRequest(http.MethodGet, "/graphs/g1/adjudications?decision=verified", nil)
	reqAdjList = withAuthRoleScope(reqAdjList, "analyst", "graph:read")
	mux.ServeHTTP(recAdjList, reqAdjList)
	if recAdjList.Code != http.StatusOK {
		t.Fatalf("expected adjudication list 200, got %d body=%s", recAdjList.Code, recAdjList.Body.String())
	}
	var adjListPayload struct {
		Count int `json:"count"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(recAdjList.Body.Bytes(), &adjListPayload); err != nil {
		t.Fatalf("decode adjudication list payload: %v", err)
	}
	if adjListPayload.Count != 1 || adjListPayload.Total != 1 {
		t.Fatalf("unexpected adjudication list payload: %+v", adjListPayload)
	}

	recAdjSummary := httptest.NewRecorder()
	reqAdjSummary := httptest.NewRequest(http.MethodGet, "/graphs/g1/adjudications/summary", nil)
	reqAdjSummary = withAuthRoleScope(reqAdjSummary, "analyst", "graph:read")
	mux.ServeHTTP(recAdjSummary, reqAdjSummary)
	if recAdjSummary.Code != http.StatusOK {
		t.Fatalf("expected adjudication summary 200, got %d body=%s", recAdjSummary.Code, recAdjSummary.Body.String())
	}
	var adjSummaryPayload struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(recAdjSummary.Body.Bytes(), &adjSummaryPayload); err != nil {
		t.Fatalf("decode adjudication summary payload: %v", err)
	}
	if adjSummaryPayload.Total != 1 {
		t.Fatalf("unexpected adjudication summary payload: %+v", adjSummaryPayload)
	}
}

func TestVerifyRunEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(graphRoot, 0o755); err != nil {
		t.Fatalf("mkdir graph root: %v", err)
	}
	inBundlePath := filepath.Join(tmp, "input", "bundle.json")
	writeJSONFile(t, inBundlePath, map[string]any{
		"snapshot_id":  "snap-1",
		"generated_at": "2026-01-01T00:00:00Z",
		"entities": []map[string]any{
			{
				"id":           "ent-endpoint-1",
				"type":         "Endpoint",
				"natural_key":  "GET|/orders",
				"attributes":   map[string]any{"section": "exposure", "class": "exposure_http_endpoint"},
				"evidence_ids": []string{},
				"fact_ids":     []string{"fact-1"},
				"confidence":   0.82,
			},
		},
	})
	mux := newMux("", graphRoot)

	runBody := []byte(fmt.Sprintf(`{"in_bundle":%q,"graph_id":"g1","strict_evidence":true,"two_pass":true}`, inBundlePath))
	recRun := httptest.NewRecorder()
	reqRun := httptest.NewRequest(http.MethodPost, "/verify/run", bytes.NewReader(runBody))
	reqRun.Header.Set("Content-Type", "application/json")
	reqRun = withAuthRoleScope(reqRun, "tenant_admin", "graph:write")
	mux.ServeHTTP(recRun, reqRun)
	if recRun.Code != http.StatusOK {
		t.Fatalf("expected verify run 200, got %d body=%s", recRun.Code, recRun.Body.String())
	}
	var runPayload struct {
		Run struct {
			RunID      string `json:"run_id"`
			QueueItems int    `json:"queue_items"`
			Disputed   int    `json:"disputed"`
		} `json:"run"`
		Report struct {
			ReviewQueueItems        int `json:"review_queue_items"`
			MissingEvidenceCritical int `json:"missing_evidence_critical"`
		} `json:"report"`
	}
	if err := json.Unmarshal(recRun.Body.Bytes(), &runPayload); err != nil {
		t.Fatalf("decode verify run payload: %v", err)
	}
	if runPayload.Run.RunID == "" {
		t.Fatalf("expected verify run id")
	}
	if runPayload.Run.QueueItems == 0 || runPayload.Run.Disputed == 0 {
		t.Fatalf("expected disputed review queue output, got %+v", runPayload.Run)
	}
	if runPayload.Report.ReviewQueueItems == 0 || runPayload.Report.MissingEvidenceCritical == 0 {
		t.Fatalf("expected strict evidence queue signal in report, got %+v", runPayload.Report)
	}

	recRuns := httptest.NewRecorder()
	reqRuns := httptest.NewRequest(http.MethodGet, "/verify/runs?graph_id=g1", nil)
	reqRuns = withAuthRoleScope(reqRuns, "analyst", "graph:read")
	mux.ServeHTTP(recRuns, reqRuns)
	if recRuns.Code != http.StatusOK {
		t.Fatalf("expected verify runs list 200, got %d body=%s", recRuns.Code, recRuns.Body.String())
	}
	var runsPayload struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal(recRuns.Body.Bytes(), &runsPayload); err != nil {
		t.Fatalf("decode verify runs payload: %v", err)
	}
	if len(runsPayload.Runs) == 0 {
		t.Fatalf("expected at least one verify run in list")
	}

	recRunReport := httptest.NewRecorder()
	reqRunReport := httptest.NewRequest(http.MethodGet, "/verify/runs/"+runPayload.Run.RunID+"/report", nil)
	reqRunReport = withAuthRoleScope(reqRunReport, "analyst", "graph:read")
	mux.ServeHTTP(recRunReport, reqRunReport)
	if recRunReport.Code != http.StatusOK {
		t.Fatalf("expected verify run report 200, got %d body=%s", recRunReport.Code, recRunReport.Body.String())
	}

	recRunQueue := httptest.NewRecorder()
	reqRunQueue := httptest.NewRequest(http.MethodGet, "/verify/runs/"+runPayload.Run.RunID+"/queue", nil)
	reqRunQueue = withAuthRoleScope(reqRunQueue, "analyst", "graph:read")
	mux.ServeHTTP(recRunQueue, reqRunQueue)
	if recRunQueue.Code != http.StatusOK {
		t.Fatalf("expected verify run queue 200, got %d body=%s", recRunQueue.Code, recRunQueue.Body.String())
	}
	var queuePayload struct {
		ReviewQueue struct {
			Items []map[string]any `json:"items"`
		} `json:"review_queue"`
	}
	if err := json.Unmarshal(recRunQueue.Body.Bytes(), &queuePayload); err != nil {
		t.Fatalf("decode verify run queue payload: %v", err)
	}
	if len(queuePayload.ReviewQueue.Items) == 0 {
		t.Fatalf("expected non-empty review queue payload")
	}
}

func TestQueryTemplatesAndExecuteEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "tenant_id": "default", "mode": "single", "node_count": 3, "edge_count": 1, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	})
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "section": "logic", "attributes": map[string]any{}, "confidence": 1.0, "inferred": false},
			{"id": "ep:orders", "type": "endpoint", "label": "GET /orders", "service_id": "a", "section": "exposure", "attributes": map[string]any{}, "confidence": 0.95, "inferred": false},
			{"id": "dep:billing", "type": "dependency", "label": "billing", "service_id": "a", "section": "dependencies", "attributes": map[string]any{}, "confidence": 0.70, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e1", "type": "service_calls_dependency", "source_id": "svc:a", "target_id": "dep:billing", "section": "dependencies", "attributes": map[string]any{}, "confidence": 0.70, "inferred": false, "evidence_refs": []any{}},
		},
		"stats": map[string]any{"node_count": 3, "edge_count": 1, "by_node_type": map[string]any{"service": 1, "endpoint": 1, "dependency": 1}, "by_edge_type": map[string]any{"service_calls_dependency": 1}},
		"meta":  map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	})
	queryTemplatePath := filepath.Join(tmp, "query-templates.json")
	writeJSONFile(t, queryTemplatePath, map[string]any{
		"templates": []map[string]any{
			{
				"id":     "dependencies-by-service",
				"method": "POST",
				"path":   "/query/execute",
				"payload": map[string]any{
					"graph_id":         "${graph_id}",
					"service_id":       "${service_id}",
					"section":          "dependencies",
					"include_inferred": true,
					"explain":          true,
				},
			},
		},
	})
	mux := newMux("", graphRoot)

	recTemplates := httptest.NewRecorder()
	reqTemplates := httptest.NewRequest(http.MethodGet, "/query/templates?path="+url.QueryEscape(queryTemplatePath), nil)
	reqTemplates = withAuthRoleScope(reqTemplates, "analyst", "graph:read")
	mux.ServeHTTP(recTemplates, reqTemplates)
	if recTemplates.Code != http.StatusOK {
		t.Fatalf("expected query templates 200, got %d body=%s", recTemplates.Code, recTemplates.Body.String())
	}

	recValidate := httptest.NewRecorder()
	reqValidate := httptest.NewRequest(http.MethodGet, "/query/templates/validate?path="+url.QueryEscape(queryTemplatePath), nil)
	reqValidate = withAuthRoleScope(reqValidate, "analyst", "graph:read")
	mux.ServeHTTP(recValidate, reqValidate)
	if recValidate.Code != http.StatusOK {
		t.Fatalf("expected query template validate 200, got %d body=%s", recValidate.Code, recValidate.Body.String())
	}
	var validatePayload struct {
		Valid      bool `json:"valid"`
		ErrorCount int  `json:"error_count"`
	}
	if err := json.Unmarshal(recValidate.Body.Bytes(), &validatePayload); err != nil {
		t.Fatalf("decode query template validate payload: %v", err)
	}
	if !validatePayload.Valid || validatePayload.ErrorCount != 0 {
		t.Fatalf("expected valid query template catalog, got %+v", validatePayload)
	}
	invalidQueryTemplatePath := filepath.Join(tmp, "query-templates-invalid.json")
	writeJSONFile(t, invalidQueryTemplatePath, map[string]any{
		"templates": []map[string]any{
			{
				"id":     "missing-graph-id",
				"method": "POST",
				"path":   "/query/execute",
				"payload": map[string]any{
					"service_id": "${service_id}",
				},
			},
		},
	})
	recValidateInvalid := httptest.NewRecorder()
	reqValidateInvalid := httptest.NewRequest(http.MethodGet, "/query/templates/validate?path="+url.QueryEscape(invalidQueryTemplatePath), nil)
	reqValidateInvalid = withAuthRoleScope(reqValidateInvalid, "analyst", "graph:read")
	mux.ServeHTTP(recValidateInvalid, reqValidateInvalid)
	if recValidateInvalid.Code != http.StatusOK {
		t.Fatalf("expected invalid query template validate 200, got %d body=%s", recValidateInvalid.Code, recValidateInvalid.Body.String())
	}
	var validateInvalidPayload struct {
		Valid      bool `json:"valid"`
		ErrorCount int  `json:"error_count"`
	}
	if err := json.Unmarshal(recValidateInvalid.Body.Bytes(), &validateInvalidPayload); err != nil {
		t.Fatalf("decode invalid query template validate payload: %v", err)
	}
	if validateInvalidPayload.Valid || validateInvalidPayload.ErrorCount == 0 {
		t.Fatalf("expected invalid query template catalog diagnostics, got %+v", validateInvalidPayload)
	}
	if !strings.Contains(recValidateInvalid.Body.String(), "payload must resolve non-empty graph_id") {
		t.Fatalf("expected graph_id contract error in query template validation, got %s", recValidateInvalid.Body.String())
	}

	recExec := httptest.NewRecorder()
	reqExec := httptest.NewRequest(http.MethodPost, "/query/execute", bytes.NewReader([]byte(`{
		"graph_id":"g1",
		"section":"dependencies",
		"include_inferred":true,
		"explain":true
	}`)))
	reqExec.Header.Set("Content-Type", "application/json")
	reqExec = withAuthRoleScope(reqExec, "analyst", "graph:read")
	mux.ServeHTTP(recExec, reqExec)
	if recExec.Code != http.StatusOK {
		t.Fatalf("expected query execute 200, got %d body=%s", recExec.Code, recExec.Body.String())
	}
	var execPayload map[string]any
	if err := json.Unmarshal(recExec.Body.Bytes(), &execPayload); err != nil {
		t.Fatalf("decode query execute payload: %v", err)
	}
	if execPayload["graph"] == nil || execPayload["explain"] == nil {
		t.Fatalf("expected query execute explain response, got %+v", execPayload)
	}

	recTemplateExec := httptest.NewRecorder()
	reqTemplateExec := httptest.NewRequest(http.MethodPost, "/query/templates/execute", bytes.NewReader([]byte(fmt.Sprintf(`{
		"template_id":"dependencies-by-service",
		"template_path":%q,
		"vars":{"graph_id":"g1","service_id":"a"}
	}`, queryTemplatePath))))
	reqTemplateExec.Header.Set("Content-Type", "application/json")
	reqTemplateExec = withAuthRoleScope(reqTemplateExec, "analyst", "graph:read")
	mux.ServeHTTP(recTemplateExec, reqTemplateExec)
	if recTemplateExec.Code != http.StatusOK {
		t.Fatalf("expected query template execute 200, got %d body=%s", recTemplateExec.Code, recTemplateExec.Body.String())
	}
	var templateExecPayload struct {
		TemplateID string `json:"template_id"`
		Status     int    `json:"status"`
		Result     any    `json:"result"`
	}
	if err := json.Unmarshal(recTemplateExec.Body.Bytes(), &templateExecPayload); err != nil {
		t.Fatalf("decode query template execute payload: %v", err)
	}
	if templateExecPayload.TemplateID != "dependencies-by-service" || templateExecPayload.Status != http.StatusOK || templateExecPayload.Result == nil {
		t.Fatalf("unexpected query template execute payload: %+v", templateExecPayload)
	}
	recTemplateExecMissingVars := httptest.NewRecorder()
	reqTemplateExecMissingVars := httptest.NewRequest(http.MethodPost, "/query/templates/execute", bytes.NewReader([]byte(fmt.Sprintf(`{
		"template_id":"dependencies-by-service",
		"template_path":%q,
		"vars":{"graph_id":"g1"}
	}`, queryTemplatePath))))
	reqTemplateExecMissingVars.Header.Set("Content-Type", "application/json")
	reqTemplateExecMissingVars = withAuthRoleScope(reqTemplateExecMissingVars, "analyst", "graph:read")
	mux.ServeHTTP(recTemplateExecMissingVars, reqTemplateExecMissingVars)
	if recTemplateExecMissingVars.Code != http.StatusBadRequest {
		t.Fatalf("expected query template execute missing-vars 400, got %d body=%s", recTemplateExecMissingVars.Code, recTemplateExecMissingVars.Body.String())
	}
	if !strings.Contains(recTemplateExecMissingVars.Body.String(), "missing template vars") || !strings.Contains(recTemplateExecMissingVars.Body.String(), "service_id") {
		t.Fatalf("expected missing-vars error for query template execute, got %s", recTemplateExecMissingVars.Body.String())
	}
}

func TestGraphIncrementalPlanEndpoints(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(filepath.Join(graphRoot, "g1"), 0o755); err != nil {
		t.Fatalf("mkdir graph dir: %v", err)
	}
	writeJSONFile(t, filepath.Join(graphRoot, "index.json"), map[string]any{
		"graphs": []map[string]any{
			{"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "tenant_id": "default", "mode": "single", "node_count": 3, "edge_count": 2, "path": filepath.Join(graphRoot, "g1", "graph.json")},
		},
	})
	writeJSONFile(t, filepath.Join(graphRoot, "g1", "graph.json"), map[string]any{
		"graph_id": "g1", "generated_at": "2026-01-01T00:00:00Z", "mode": "single",
		"nodes": []map[string]any{
			{"id": "svc:a", "type": "service", "label": "A", "service_id": "a", "attributes": map[string]any{"source_file": "cmd/service/main.go"}, "confidence": 1.0, "inferred": false},
			{"id": "ep:orders", "type": "endpoint", "label": "GET /orders", "service_id": "a", "attributes": map[string]any{"file_path": "internal/http/routes.go"}, "confidence": 0.95, "inferred": false},
			{"id": "dep:billing", "type": "dependency", "label": "billing", "service_id": "a", "attributes": map[string]any{}, "confidence": 0.8, "inferred": false},
		},
		"edges": []map[string]any{
			{"id": "e1", "type": "service_exposes_endpoint", "source_id": "svc:a", "target_id": "ep:orders", "attributes": map[string]any{}, "confidence": 0.95, "inferred": false, "evidence_refs": []map[string]any{{"file_path": "internal/http/routes.go", "start_line": 12}}},
			{"id": "e2", "type": "service_calls_dependency", "source_id": "svc:a", "target_id": "dep:billing", "attributes": map[string]any{}, "confidence": 0.8, "inferred": false, "evidence_refs": []map[string]any{{"file_path": "internal/clients/billing.go", "start_line": 42}}},
		},
		"stats": map[string]any{"node_count": 3, "edge_count": 2, "by_node_type": map[string]any{"service": 1, "endpoint": 1, "dependency": 1}, "by_edge_type": map[string]any{"service_exposes_endpoint": 1, "service_calls_dependency": 1}},
		"meta":  map[string]any{"tenant_id": "default", "services": []map[string]any{}},
	})
	mux := newMux("", graphRoot)

	recPlan := httptest.NewRecorder()
	reqPlan := httptest.NewRequest(http.MethodPost, "/graphs/incremental", bytes.NewReader([]byte(`{
		"graph_id":"g1",
		"changed_files":["internal/http/routes.go"],
		"hops": 1
	}`)))
	reqPlan.Header.Set("Content-Type", "application/json")
	reqPlan = withAuthRoleScope(reqPlan, "tenant_admin", "graph:write")
	mux.ServeHTTP(recPlan, reqPlan)
	if recPlan.Code != http.StatusOK {
		t.Fatalf("expected incremental plan 200, got %d body=%s", recPlan.Code, recPlan.Body.String())
	}
	var planPayload struct {
		PlanID          string   `json:"plan_id"`
		ImpactedNodeIDs []string `json:"impacted_node_ids"`
		ImpactedEdgeIDs []string `json:"impacted_edge_ids"`
	}
	if err := json.Unmarshal(recPlan.Body.Bytes(), &planPayload); err != nil {
		t.Fatalf("decode incremental plan payload: %v", err)
	}
	if planPayload.PlanID == "" {
		t.Fatalf("expected non-empty plan_id")
	}
	if len(planPayload.ImpactedNodeIDs) == 0 || len(planPayload.ImpactedEdgeIDs) == 0 {
		t.Fatalf("expected impacted nodes/edges in incremental plan, got %+v", planPayload)
	}
	recPlanInvalidChangedFiles := httptest.NewRecorder()
	reqPlanInvalidChangedFiles := httptest.NewRequest(http.MethodPost, "/graphs/incremental", bytes.NewReader([]byte(`{
		"graph_id":"g1",
		"changed_files":[" ","./",""],
		"hops": 1
	}`)))
	reqPlanInvalidChangedFiles.Header.Set("Content-Type", "application/json")
	reqPlanInvalidChangedFiles = withAuthRoleScope(reqPlanInvalidChangedFiles, "tenant_admin", "graph:write")
	mux.ServeHTTP(recPlanInvalidChangedFiles, reqPlanInvalidChangedFiles)
	if recPlanInvalidChangedFiles.Code != http.StatusBadRequest {
		t.Fatalf("expected incremental plan invalid changed_files 400, got %d", recPlanInvalidChangedFiles.Code)
	}
	recPlanInvalidHops := httptest.NewRecorder()
	reqPlanInvalidHops := httptest.NewRequest(http.MethodPost, "/graphs/incremental", bytes.NewReader([]byte(`{
		"graph_id":"g1",
		"changed_files":["internal/http/routes.go"],
		"hops": 99
	}`)))
	reqPlanInvalidHops.Header.Set("Content-Type", "application/json")
	reqPlanInvalidHops = withAuthRoleScope(reqPlanInvalidHops, "tenant_admin", "graph:write")
	mux.ServeHTTP(recPlanInvalidHops, reqPlanInvalidHops)
	if recPlanInvalidHops.Code != http.StatusBadRequest {
		t.Fatalf("expected incremental plan invalid hops 400, got %d", recPlanInvalidHops.Code)
	}

	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/graphs/incremental?graph_id=g1", nil)
	reqList = withAuthRoleScope(reqList, "analyst", "graph:read")
	mux.ServeHTTP(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("expected incremental list 200, got %d body=%s", recList.Code, recList.Body.String())
	}
	var listPayload struct {
		Plans []map[string]any `json:"plans"`
	}
	if err := json.Unmarshal(recList.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode incremental list payload: %v", err)
	}
	if len(listPayload.Plans) == 0 {
		t.Fatalf("expected non-empty incremental plan history")
	}
	recListBadFrom := httptest.NewRecorder()
	reqListBadFrom := httptest.NewRequest(http.MethodGet, "/graphs/incremental?graph_id=g1&from=bad-time", nil)
	reqListBadFrom = withAuthRoleScope(reqListBadFrom, "analyst", "graph:read")
	mux.ServeHTTP(recListBadFrom, reqListBadFrom)
	if recListBadFrom.Code != http.StatusBadRequest {
		t.Fatalf("expected incremental list bad from 400, got %d", recListBadFrom.Code)
	}
	recListFuture := httptest.NewRecorder()
	reqListFuture := httptest.NewRequest(http.MethodGet, "/graphs/incremental?graph_id=g1&from=2100-01-01T00:00:00Z", nil)
	reqListFuture = withAuthRoleScope(reqListFuture, "analyst", "graph:read")
	mux.ServeHTTP(recListFuture, reqListFuture)
	if recListFuture.Code != http.StatusOK {
		t.Fatalf("expected incremental list future filter 200, got %d", recListFuture.Code)
	}
	var listFuturePayload struct {
		Plans []map[string]any `json:"plans"`
	}
	if err := json.Unmarshal(recListFuture.Body.Bytes(), &listFuturePayload); err != nil {
		t.Fatalf("decode incremental future list payload: %v", err)
	}
	if len(listFuturePayload.Plans) != 0 {
		t.Fatalf("expected no incremental plans for far-future filter, got %d", len(listFuturePayload.Plans))
	}

	recSubgraph := httptest.NewRecorder()
	reqSubgraph := httptest.NewRequest(http.MethodGet, "/graphs/incremental/"+planPayload.PlanID+"/subgraph", nil)
	reqSubgraph = withAuthRoleScope(reqSubgraph, "analyst", "graph:read")
	mux.ServeHTTP(recSubgraph, reqSubgraph)
	if recSubgraph.Code != http.StatusOK {
		t.Fatalf("expected incremental subgraph 200, got %d body=%s", recSubgraph.Code, recSubgraph.Body.String())
	}
	var subgraphPayload map[string]any
	if err := json.Unmarshal(recSubgraph.Body.Bytes(), &subgraphPayload); err != nil {
		t.Fatalf("decode incremental subgraph payload: %v", err)
	}
	if subgraphPayload["impact_graph"] == nil {
		t.Fatalf("expected impact_graph in incremental subgraph payload")
	}
	recSubgraphExplain := httptest.NewRecorder()
	reqSubgraphExplain := httptest.NewRequest(http.MethodGet, "/graphs/incremental/"+planPayload.PlanID+"/subgraph?explain=true", nil)
	reqSubgraphExplain = withAuthRoleScope(reqSubgraphExplain, "analyst", "graph:read")
	mux.ServeHTTP(recSubgraphExplain, reqSubgraphExplain)
	if recSubgraphExplain.Code != http.StatusOK {
		t.Fatalf("expected incremental subgraph explain 200, got %d body=%s", recSubgraphExplain.Code, recSubgraphExplain.Body.String())
	}
	var subgraphExplainPayload struct {
		Explain map[string]any `json:"explain"`
	}
	if err := json.Unmarshal(recSubgraphExplain.Body.Bytes(), &subgraphExplainPayload); err != nil {
		t.Fatalf("decode incremental subgraph explain payload: %v", err)
	}
	if len(subgraphExplainPayload.Explain) == 0 {
		t.Fatalf("expected explain details in incremental subgraph payload")
	}
}

func TestOpsUITelemetryAPI(t *testing.T) {
	tmp := t.TempDir()
	graphRoot := filepath.Join(tmp, "graph")
	if err := os.MkdirAll(graphRoot, 0o755); err != nil {
		t.Fatalf("mkdir graph root: %v", err)
	}
	mux := newMux("", graphRoot)

	eventA := []byte(`{
		"session_id":"sess-1",
		"event_type":"task_end",
		"task_id":"exposure_scan",
		"status":"ok",
		"duration_ms":100,
		"timestamp_utc":"2026-02-20T12:00:00Z"
	}`)
	recPostA := httptest.NewRecorder()
	reqPostA := httptest.NewRequest(http.MethodPost, "/ops/ui-telemetry", bytes.NewReader(eventA))
	reqPostA.Header.Set("Content-Type", "application/json")
	reqPostA = withAuthRoleScope(reqPostA, "tenant_admin", "ops:write")
	mux.ServeHTTP(recPostA, reqPostA)
	if recPostA.Code != http.StatusOK {
		t.Fatalf("expected telemetry post A 200, got %d body=%s", recPostA.Code, recPostA.Body.String())
	}

	eventB := []byte(`{
		"session_id":"sess-2",
		"event_type":"task_end",
		"task_id":"exposure_scan",
		"status":"dead_end",
		"duration_ms":300,
		"dead_end":true,
		"timestamp_utc":"2026-02-20T12:00:01Z"
	}`)
	recPostB := httptest.NewRecorder()
	reqPostB := httptest.NewRequest(http.MethodPost, "/ops/ui-telemetry", bytes.NewReader(eventB))
	reqPostB.Header.Set("Content-Type", "application/json")
	reqPostB = withAuthRoleScope(reqPostB, "tenant_admin", "ops:write")
	mux.ServeHTTP(recPostB, reqPostB)
	if recPostB.Code != http.StatusOK {
		t.Fatalf("expected telemetry post B 200, got %d body=%s", recPostB.Code, recPostB.Body.String())
	}

	telemetryPath := filepath.Join(tmp, "ops", "ui_telemetry_events.jsonl")
	data, err := os.ReadFile(telemetryPath)
	if err != nil {
		t.Fatalf("read telemetry file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 telemetry events on disk, got %d", len(lines))
	}

	recGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/ops/ui-telemetry", nil)
	reqGet = withAuthRoleScope(reqGet, "compliance_auditor", "audit:read")
	mux.ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("expected telemetry get 200, got %d body=%s", recGet.Code, recGet.Body.String())
	}
	var payload struct {
		Path    string     `json:"path"`
		Events  []struct{} `json:"events"`
		Summary struct {
			TotalEvents       int                `json:"total_events"`
			TotalSessions     int                `json:"total_sessions"`
			DeadEndEvents     int                `json:"dead_end_events"`
			ByEventType       map[string]int     `json:"by_event_type"`
			ByTask            map[string]int     `json:"by_task"`
			AvgDurationByTask map[string]float64 `json:"avg_duration_ms_by_task"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(recGet.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode telemetry get payload: %v", err)
	}
	if payload.Path != telemetryPath {
		t.Fatalf("unexpected telemetry path: %q", payload.Path)
	}
	if len(payload.Events) != 2 {
		t.Fatalf("expected 2 telemetry events in response, got %d", len(payload.Events))
	}
	if payload.Summary.TotalEvents != 2 {
		t.Fatalf("expected total_events=2, got %d", payload.Summary.TotalEvents)
	}
	if payload.Summary.TotalSessions != 2 {
		t.Fatalf("expected total_sessions=2, got %d", payload.Summary.TotalSessions)
	}
	if payload.Summary.DeadEndEvents != 1 {
		t.Fatalf("expected dead_end_events=1, got %d", payload.Summary.DeadEndEvents)
	}
	if payload.Summary.ByEventType["task_end"] != 2 {
		t.Fatalf("expected by_event_type.task_end=2, got %+v", payload.Summary.ByEventType)
	}
	if payload.Summary.ByTask["exposure_scan"] != 2 {
		t.Fatalf("expected by_task.exposure_scan=2, got %+v", payload.Summary.ByTask)
	}
	if payload.Summary.AvgDurationByTask["exposure_scan"] != 200 {
		t.Fatalf("expected avg_duration_ms_by_task.exposure_scan=200, got %+v", payload.Summary.AvgDurationByTask)
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

func signTestHS256JWT(t *testing.T, claims map[string]any, secret string) string {
	t.Helper()
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal jwt header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt claims: %v", err)
	}
	h := base64.RawURLEncoding.EncodeToString(headerJSON)
	c := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := h + "." + c
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return h + "." + c + "." + sig
}
