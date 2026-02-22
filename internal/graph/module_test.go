package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"diffmind/internal/graphschema"
)

func TestRunBuildMultiCreatesGraphAndIndex(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")

	serviceABundle := filepath.Join(tmp, "a.bundle.json")
	serviceAAnalyzer := filepath.Join(tmp, "a.analyzer.json")
	serviceBBundle := filepath.Join(tmp, "b.bundle.json")
	serviceBAnalyzer := filepath.Join(tmp, "b.analyzer.json")
	manifestPath := filepath.Join(tmp, "services.yaml")

	writeJSON(t, serviceABundle, map[string]any{
		"snapshot_id": "sa",
		"entities": []map[string]any{
			{"id": "ru-a", "type": "RuntimeUnit", "natural_key": "go|main|main.go", "attributes": map[string]any{"file": "main.go", "language": "go", "kind": "main"}, "evidence_ids": []string{"ev-ru"}, "fact_ids": []string{"f-ru"}, "confidence": 0.9},
			{"id": "ec-a", "type": "ExternalCall", "natural_key": "http|GET|http://b.local/users|go-net-http", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "http://b.local/users", "library": "go-net-http", "runtime_unit_id": "ru-a"}, "evidence_ids": []string{"ev-call"}, "fact_ids": []string{"f-call"}, "confidence": 0.9},
		},
	})
	writeJSON(t, serviceAAnalyzer, map[string]any{
		"facts":     []map[string]any{},
		"evidence":  []map[string]any{{"id": "ev-call", "snapshot_id": "sa", "file_path": "main.go", "start_line": 10, "start_col": 1, "end_line": 10, "end_col": 30, "snippet_hash": "x", "created_at_utc": "2026-01-01T00:00:00Z"}},
		"generated": "2026-01-01T00:00:00Z",
	})

	writeJSON(t, serviceBBundle, map[string]any{
		"snapshot_id": "sb",
		"entities": []map[string]any{
			{"id": "ep-b", "type": "Endpoint", "natural_key": "inbound|GET|/users|go-router", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/users", "framework": "go-router"}, "evidence_ids": []string{"ev-ep"}, "fact_ids": []string{"f-ep"}, "confidence": 0.9},
		},
	})
	writeJSON(t, serviceBAnalyzer, map[string]any{
		"facts":     []map[string]any{},
		"evidence":  []map[string]any{},
		"generated": "2026-01-01T00:00:00Z",
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + serviceABundle + `
    analyzer_bundle_path: ` + serviceAAnalyzer + `
    base_urls: ["http://a.local"]
    queue_publishes: ["orders.events"]
    db_writes: ["payments"]
  - id: service-b
    name: Service B
    bundle_path: ` + serviceBBundle + `
    analyzer_bundle_path: ` + serviceBAnalyzer + `
    base_urls: ["http://b.local"]
    queue_consumes: ["orders.events"]
    db_reads: ["payments"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexPath := filepath.Join(outDir, "graph", "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			GraphID string `json:"graph_id"`
			Path    string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if len(index.Graphs) != 1 {
		t.Fatalf("expected one graph in index, got %d", len(index.Graphs))
	}

	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph json: %v", err)
	}
	var graph struct {
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph json: %v", err)
	}
	types := map[string]int{}
	for _, e := range graph.Edges {
		types[e.Type]++
	}
	if types["service_calls_service"] == 0 {
		t.Fatalf("expected service_calls_service edge")
	}
	if types["service_calls_endpoint"] == 0 {
		t.Fatalf("expected service_calls_endpoint edge")
	}
	if types["service_publishes_queue"] == 0 || types["queue_delivers_to_service"] == 0 {
		t.Fatalf("expected queue edges from manifest")
	}
	if types["service_reads_db"] == 0 || types["service_writes_db"] == 0 {
		t.Fatalf("expected db edges from manifest")
	}
}

func TestRunBuildMultiABCDAcceptanceFixture(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")

	svcABundle := filepath.Join(tmp, "a.bundle.json")
	svcBBundle := filepath.Join(tmp, "b.bundle.json")
	svcCBundle := filepath.Join(tmp, "c.bundle.json")
	svcDBundle := filepath.Join(tmp, "d.bundle.json")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	writeJSON(t, svcABundle, map[string]any{
		"snapshot_id": "sa",
		"entities": []map[string]any{
			{"id": "ru-a", "type": "RuntimeUnit", "natural_key": "go|main|a.go", "attributes": map[string]any{"file": "a.go", "language": "go", "kind": "main"}, "evidence_ids": []string{"ev-a-ru"}, "fact_ids": []string{"f-a-ru"}, "confidence": 0.9},
			{"id": "ec-a1", "type": "ExternalCall", "natural_key": "http|GET|http://service-b.internal/users", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "http://service-b.internal/users", "library": "go-net-http"}, "evidence_ids": []string{"ev-a1"}, "fact_ids": []string{"f-a1"}, "confidence": 0.9},
			{"id": "ec-a2", "type": "ExternalCall", "natural_key": "http|POST|http://service-b.internal/orders", "attributes": map[string]any{"protocol": "http", "method": "POST", "target": "http://service-b.internal/orders", "library": "go-net-http"}, "evidence_ids": []string{"ev-a2"}, "fact_ids": []string{"f-a2"}, "confidence": 0.9},
			{"id": "ec-aq", "type": "ExternalCall", "natural_key": "queue|PUBLISH|orders.events", "attributes": map[string]any{"protocol": "queue", "method": "PUBLISH", "target": "orders.events", "library": "kafka-go"}, "evidence_ids": []string{"ev-aq"}, "fact_ids": []string{"f-aq"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcBBundle, map[string]any{
		"snapshot_id": "sb",
		"entities": []map[string]any{
			{"id": "ep-b1", "type": "Endpoint", "natural_key": "inbound|GET|/users", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/users", "framework": "go-router"}, "evidence_ids": []string{"ev-b1"}, "fact_ids": []string{"f-b1"}, "confidence": 0.9},
			{"id": "ep-b2", "type": "Endpoint", "natural_key": "inbound|POST|/orders", "attributes": map[string]any{"direction": "inbound", "method": "POST", "path": "/orders", "framework": "go-router"}, "evidence_ids": []string{"ev-b2"}, "fact_ids": []string{"f-b2"}, "confidence": 0.9},
			{"id": "ec-bq", "type": "ExternalCall", "natural_key": "queue|CONSUME|orders.events", "attributes": map[string]any{"protocol": "queue", "method": "CONSUME", "target": "orders.events", "library": "kafka-go"}, "evidence_ids": []string{"ev-bq"}, "fact_ids": []string{"f-bq"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcCBundle, map[string]any{
		"snapshot_id": "sc",
		"entities": []map[string]any{
			{"id": "ec-cdb", "type": "ExternalCall", "natural_key": "db|READ|analytics", "attributes": map[string]any{"protocol": "db", "method": "READ", "target": "analytics", "library": "database/sql"}, "evidence_ids": []string{"ev-cdb"}, "fact_ids": []string{"f-cdb"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcDBundle, map[string]any{
		"snapshot_id": "sd",
		"entities": []map[string]any{
			{"id": "ec-ddb", "type": "ExternalCall", "natural_key": "db|WRITE|analytics", "attributes": map[string]any{"protocol": "db", "method": "WRITE", "target": "analytics", "library": "database/sql"}, "evidence_ids": []string{"ev-ddb"}, "fact_ids": []string{"f-ddb"}, "confidence": 0.9},
		},
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + svcABundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://a.local"]
  - id: service-b
    name: Service B
    bundle_path: ` + svcBBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: []
  - id: service-c
    name: Service C
    bundle_path: ` + svcCBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://c.local"]
  - id: service-d
    name: Service D
    bundle_path: ` + svcDBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://d.local"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexPath := filepath.Join(outDir, "graph", "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	typeCount := map[string]int{}
	for _, e := range graph.Edges {
		typeCount[e.Type]++
	}
	if typeCount["service_calls_service"] < 2 {
		t.Fatalf("expected at least 2 service_calls_service edges, got %d", typeCount["service_calls_service"])
	}
	if typeCount["service_calls_endpoint"] < 2 {
		t.Fatalf("expected at least 2 service_calls_endpoint edges, got %d", typeCount["service_calls_endpoint"])
	}
	if typeCount["service_publishes_queue"] < 1 || typeCount["queue_delivers_to_service"] < 1 {
		t.Fatalf("expected queue publish/consume edges from code-derived external calls")
	}
	if typeCount["service_reads_db"] < 1 || typeCount["service_writes_db"] < 1 {
		t.Fatalf("expected db read/write edges from code-derived external calls")
	}
}

func TestRunBuildMultiFromSourcesAutoDiscover(t *testing.T) {
	tmp := t.TempDir()
	outputsRoot := filepath.Join(tmp, "outputs")
	outDir := filepath.Join(tmp, "graph-out")
	if err := os.MkdirAll(outputsRoot, 0o755); err != nil {
		t.Fatalf("mkdir outputs root: %v", err)
	}

	serviceAOut := filepath.Join(outputsRoot, "service-a")
	serviceBOut := filepath.Join(outputsRoot, "service-b")
	writeOutputBundle(t, serviceAOut, map[string]any{
		"snapshot_id": "sa",
		"entities": []map[string]any{
			{"id": "ec-a-http", "type": "ExternalCall", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "https://service-b.internal/users"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
			{"id": "ec-a-q", "type": "ExternalCall", "attributes": map[string]any{"protocol": "queue", "method": "PUBLISH", "target": "orders.events"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
		},
	})
	writeOutputBundle(t, serviceBOut, map[string]any{
		"snapshot_id": "sb",
		"entities": []map[string]any{
			{"id": "ep-b", "type": "Endpoint", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/users", "framework": "go-router"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
			{"id": "ec-b-q", "type": "ExternalCall", "attributes": map[string]any{"protocol": "queue", "method": "CONSUME", "target": "orders.events"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
		},
	})
	writeRunReportSource(t, serviceAOut, "/repos/service-a")
	writeRunReportSource(t, serviceBOut, "/repos/service-b")

	if err := Run(context.Background(), []string{"build", "--mode", "multi", "--sources", outputsRoot, "--out", outDir}); err != nil {
		t.Fatalf("graph build with --sources failed: %v", err)
	}

	indexPath := filepath.Join(outDir, "graph", "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if len(index.Graphs) != 1 {
		t.Fatalf("expected one graph, got %d", len(index.Graphs))
	}

	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	typeCount := map[string]int{}
	for _, e := range graph.Edges {
		typeCount[e.Type]++
	}
	if typeCount["service_calls_service"] < 1 {
		t.Fatalf("expected service_calls_service edge, got %#v", typeCount)
	}
	if typeCount["queue_delivers_to_service"] < 1 || typeCount["service_publishes_queue"] < 1 {
		t.Fatalf("expected queue edges, got %#v", typeCount)
	}
}

func TestTrimUnconnectedNodesKeepsServicesAndConnectedNodes(t *testing.T) {
	nodes := []graphschema.Node{
		{ID: "svc:a", Type: "service"},
		{ID: "svc:b", Type: "service"},
		{ID: "ep:a:1", Type: "endpoint"},
		{ID: "ep:b:1", Type: "endpoint"},
	}
	edges := []graphschema.Edge{
		{ID: "e1", SourceID: "svc:a", TargetID: "ep:b:1"},
	}
	got := trimUnconnectedNodes(nodes, edges)
	ids := map[string]struct{}{}
	for _, n := range got {
		ids[n.ID] = struct{}{}
	}
	if _, ok := ids["svc:a"]; !ok {
		t.Fatalf("expected svc:a to remain")
	}
	if _, ok := ids["svc:b"]; !ok {
		t.Fatalf("expected svc:b to remain")
	}
	if _, ok := ids["ep:b:1"]; !ok {
		t.Fatalf("expected connected endpoint to remain")
	}
	if _, ok := ids["ep:a:1"]; ok {
		t.Fatalf("expected unconnected endpoint to be removed")
	}
}

func TestBuildGraphServiceMetaIncludesProvenance(t *testing.T) {
	tmp := t.TempDir()
	outRoot := filepath.Join(tmp, "out-a")
	if err := os.MkdirAll(filepath.Join(outRoot, "bundle"), 0o755); err != nil {
		t.Fatalf("mkdir bundle dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(outRoot, "analyzers"), 0o755); err != nil {
		t.Fatalf("mkdir analyzers dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(outRoot, "run"), 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(outRoot, "snapshots", "snap-1"), 0o755); err != nil {
		t.Fatalf("mkdir snapshots dir: %v", err)
	}

	bundlePath := filepath.Join(outRoot, "bundle", "intelligence_bundle.json")
	analyzerPath := filepath.Join(outRoot, "analyzers", "bundle.json")
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "snap-1",
		"entities": []map[string]any{
			{"id": "ep-1", "type": "Endpoint", "natural_key": "inbound|GET|/health", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/health"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
		},
	})
	writeJSON(t, analyzerPath, map[string]any{
		"facts":    []any{},
		"evidence": []any{},
	})
	writeJSON(t, filepath.Join(outRoot, "run", "report.json"), map[string]any{
		"source": "/repos/service-a",
		"ref":    "main",
	})
	writeJSON(t, filepath.Join(outRoot, "snapshots", "snap-1", "snapshot.json"), map[string]any{
		"snapshot_id":  "snap-1",
		"repo_locator": "git@github.com:org/service-a.git",
		"ref":          "main",
		"commit_sha":   "abc123",
		"source_type":  "remote",
		"tool_version": "dev",
		"generated_at": "2026-01-01T00:00:00Z",
		"file_count":   1,
	})

	graph, err := buildGraph([]serviceSpec{
		{
			ID:             "service-a",
			Name:           "Service A",
			RepoPath:       "/repos/service-a",
			BundlePath:     bundlePath,
			AnalyzerBundle: analyzerPath,
		},
	}, "single")
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	if graph.Meta.Provenance.Tool != "diffmind" || graph.Meta.Provenance.GeneratedBy != "graph.build" {
		t.Fatalf("unexpected graph provenance: %+v", graph.Meta.Provenance)
	}
	if len(graph.Meta.Services) != 1 {
		t.Fatalf("expected one service meta, got %d", len(graph.Meta.Services))
	}
	meta := graph.Meta.Services[0]
	prov := meta.Provenance
	if prov.OutputRoot != outRoot {
		t.Fatalf("unexpected output_root: %q", prov.OutputRoot)
	}
	if prov.RunReportPath != filepath.Join(outRoot, "run", "report.json") {
		t.Fatalf("unexpected run_report_path: %q", prov.RunReportPath)
	}
	if prov.RunSource != "/repos/service-a" || prov.RunRef != "main" {
		t.Fatalf("unexpected run metadata: source=%q ref=%q", prov.RunSource, prov.RunRef)
	}
	if prov.SnapshotID != "snap-1" {
		t.Fatalf("unexpected snapshot id: %q", prov.SnapshotID)
	}
	if prov.SnapshotPath != filepath.Join(outRoot, "snapshots", "snap-1", "snapshot.json") {
		t.Fatalf("unexpected snapshot path: %q", prov.SnapshotPath)
	}
	if prov.SnapshotRepoLocator != "git@github.com:org/service-a.git" || prov.SnapshotRef != "main" || prov.SnapshotCommitSHA != "abc123" || prov.SnapshotSourceType != "remote" {
		t.Fatalf("unexpected snapshot metadata: %+v", prov)
	}
	bundleSHA, err := fileSHA256(bundlePath)
	if err != nil {
		t.Fatalf("hash bundle: %v", err)
	}
	if prov.BundleSHA256 != bundleSHA {
		t.Fatalf("unexpected bundle sha: %q", prov.BundleSHA256)
	}
	analyzerSHA, err := fileSHA256(analyzerPath)
	if err != nil {
		t.Fatalf("hash analyzer: %v", err)
	}
	if prov.AnalyzerBundleSHA256 != analyzerSHA {
		t.Fatalf("unexpected analyzer sha: %q", prov.AnalyzerBundleSHA256)
	}
	hasEndpointNode := false
	hasExposeEdge := false
	for _, n := range graph.Nodes {
		if n.Type == "endpoint" {
			hasEndpointNode = true
			break
		}
	}
	for _, e := range graph.Edges {
		if e.Type == "service_exposes_endpoint" {
			hasExposeEdge = true
			break
		}
	}
	if !hasEndpointNode || !hasExposeEdge {
		t.Fatalf("expected endpoint node and service_exposes_endpoint edge, got node=%v edge=%v", hasEndpointNode, hasExposeEdge)
	}
}

func TestBuildGraphAddsRuntimeBuildDeployEdges(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")

	bundlePath := filepath.Join(tmp, "svc.bundle.json")
	analyzerPath := filepath.Join(tmp, "svc.analyzer.json")
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "s1",
		"entities": []map[string]any{
			{"id": "ru1", "type": "RuntimeUnit", "natural_key": "go|main|main.go", "attributes": map[string]any{"language": "go", "kind": "main", "file": "main.go"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.95},
			{"id": "ps1", "type": "PipelineStep", "natural_key": "github-actions|run|docker build", "attributes": map[string]any{"provider": "github-actions", "kind": "run", "value": "docker build -t api ."}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
			{"id": "ba1", "type": "BuildArtifact", "natural_key": "container-image|ghcr.io/acme/api:1.0", "attributes": map[string]any{"artifact_type": "container-image", "name": "ghcr.io/acme/api:1.0"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.9},
			{"id": "dep1", "type": "Deployment", "natural_key": "kubernetes|Deployment|api", "attributes": map[string]any{"platform": "kubernetes", "resource_kind": "Deployment", "name": "api"}, "evidence_ids": []string{"ev4"}, "fact_ids": []string{"f4"}, "confidence": 0.9},
			{"id": "ir1", "type": "InfraResource", "natural_key": "kubernetes|Deployment|api", "attributes": map[string]any{"provider": "kubernetes", "kind": "Deployment", "name": "api"}, "evidence_ids": []string{"ev5"}, "fact_ids": []string{"f5"}, "confidence": 0.9},
		},
	})
	writeJSON(t, analyzerPath, map[string]any{
		"facts": []map[string]any{},
		"evidence": []map[string]any{
			{"id": "ev1", "snapshot_id": "s1", "file_path": "main.go", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 10, "snippet_hash": "a", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev2", "snapshot_id": "s1", "file_path": ".github/workflows/ci.yml", "start_line": 4, "start_col": 1, "end_line": 4, "end_col": 40, "snippet_hash": "b", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev3", "snapshot_id": "s1", "file_path": "Dockerfile", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 30, "snippet_hash": "c", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev4", "snapshot_id": "s1", "file_path": "k8s/deployment.yaml", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 30, "snippet_hash": "d", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev5", "snapshot_id": "s1", "file_path": "k8s/deployment.yaml", "start_line": 2, "start_col": 1, "end_line": 2, "end_col": 30, "snippet_hash": "e", "created_at_utc": "2026-01-01T00:00:00Z"},
		},
		"generated": "2026-01-01T00:00:00Z",
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + bundlePath + `
    analyzer_bundle_path: ` + analyzerPath + `
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type string `json:"type"`
		} `json:"nodes"`
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	nodeTypes := map[string]int{}
	for _, n := range graph.Nodes {
		nodeTypes[n.Type]++
	}
	if nodeTypes["runtime_unit"] == 0 || nodeTypes["pipeline_step"] == 0 || nodeTypes["build_artifact"] == 0 || nodeTypes["deployment"] == 0 || nodeTypes["infra_resource"] == 0 {
		t.Fatalf("missing runtime/build/deploy node types: %#v", nodeTypes)
	}

	edgeTypes := map[string]int{}
	for _, e := range graph.Edges {
		edgeTypes[e.Type]++
	}
	required := []string{
		"service_has_runtime_unit",
		"service_built_by_pipeline_step",
		"pipeline_step_produces_artifact",
		"artifact_deployed_to_runtime",
		"service_deployed_to_runtime",
		"service_uses_infra_resource",
	}
	for _, typ := range required {
		if edgeTypes[typ] == 0 {
			t.Fatalf("expected edge type %s, got %#v", typ, edgeTypes)
		}
	}
}

func TestBuildGraphAddsAdvancedLogicPathEdges(t *testing.T) {
	tmp := t.TempDir()
	bundlePath := filepath.Join(tmp, "svc.bundle.json")
	analyzerPath := filepath.Join(tmp, "svc.analyzer.json")

	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "snap-logic-1",
		"entities": []map[string]any{
			{
				"id":          "ep1",
				"type":        "Endpoint",
				"natural_key": "GET|/accounts/{id}",
				"attributes": map[string]any{
					"method": "GET",
					"path":   "/accounts/{id}",
					"file":   "src/controller/AccountController.java",
					"line":   10,
					"col":    1,
				},
				"fact_ids":     []string{"f-ep1"},
				"evidence_ids": []string{"ev-ep1"},
				"confidence":   0.99,
			},
			{
				"id":          "sym-handler",
				"type":        "CodeSymbol",
				"natural_key": "AccountController#getAccount",
				"attributes": map[string]any{
					"name":        "AccountController.getAccount",
					"symbol_kind": "method",
					"file":        "src/controller/AccountController.java",
					"line":        12,
					"col":         3,
				},
				"fact_ids":     []string{"f-sym-handler"},
				"evidence_ids": []string{"ev-sym-handler"},
				"confidence":   0.95,
			},
			{
				"id":          "sym-service",
				"type":        "CodeSymbol",
				"natural_key": "AccountService#getAccountDtoById",
				"attributes": map[string]any{
					"name":        "AccountService.getAccountDtoById",
					"symbol_kind": "method",
					"file":        "src/service/AccountService.java",
					"line":        20,
					"col":         3,
				},
				"fact_ids":     []string{"f-sym-service"},
				"evidence_ids": []string{"ev-sym-service"},
				"confidence":   0.95,
			},
			{
				"id":          "call-1",
				"type":        "CodeCall",
				"natural_key": "handler->service",
				"attributes": map[string]any{
					"caller": "AccountController.getAccount",
					"callee": "getAccountDtoById",
					"file":   "src/controller/AccountController.java",
					"line":   13,
					"col":    8,
				},
				"fact_ids":     []string{"f-call-1"},
				"evidence_ids": []string{"ev-call-1"},
				"confidence":   0.93,
			},
			{
				"id":          "ext-1",
				"type":        "ExternalCall",
				"natural_key": "GET|publisher-api",
				"attributes": map[string]any{
					"protocol": "http",
					"method":   "GET",
					"target":   "https://publisher-api.internal/publishers/{id}",
					"file":     "src/service/AccountService.java",
					"line":     24,
					"col":      5,
				},
				"fact_ids":     []string{"f-ext-1"},
				"evidence_ids": []string{"ev-ext-1"},
				"confidence":   0.9,
			},
		},
	})
	writeJSON(t, analyzerPath, map[string]any{
		"facts": []map[string]any{},
		"evidence": []map[string]any{
			{"id": "ev-ep1", "snapshot_id": "snap-logic-1", "file_path": "src/controller/AccountController.java", "start_line": 10, "start_col": 1, "end_line": 10, "end_col": 40, "snippet_hash": "h1", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev-sym-handler", "snapshot_id": "snap-logic-1", "file_path": "src/controller/AccountController.java", "start_line": 12, "start_col": 3, "end_line": 12, "end_col": 35, "snippet_hash": "h2", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev-sym-service", "snapshot_id": "snap-logic-1", "file_path": "src/service/AccountService.java", "start_line": 20, "start_col": 3, "end_line": 20, "end_col": 45, "snippet_hash": "h3", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev-call-1", "snapshot_id": "snap-logic-1", "file_path": "src/controller/AccountController.java", "start_line": 13, "start_col": 8, "end_line": 13, "end_col": 60, "snippet_hash": "h4", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev-ext-1", "snapshot_id": "snap-logic-1", "file_path": "src/service/AccountService.java", "start_line": 24, "start_col": 5, "end_line": 24, "end_col": 80, "snippet_hash": "h5", "created_at_utc": "2026-01-01T00:00:00Z"},
		},
		"generated": "2026-01-01T00:00:00Z",
	})

	graph, err := buildGraph([]serviceSpec{
		{
			ID:             "svc-logic",
			Name:           "Service Logic",
			RepoPath:       tmp,
			BundlePath:     bundlePath,
			AnalyzerBundle: analyzerPath,
		},
	}, "single")
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	edgeTypes := map[string]int{}
	nodeTypes := map[string]int{}
	for _, n := range graph.Nodes {
		nodeTypes[n.Type]++
	}
	for _, e := range graph.Edges {
		edgeTypes[e.Type]++
	}

	if nodeTypes["function"] == 0 {
		t.Fatalf("expected function nodes in advanced logic layer, got %#v", nodeTypes)
	}
	if edgeTypes["exposure_invokes_function"] == 0 {
		t.Fatalf("expected exposure_invokes_function edge, got %#v", edgeTypes)
	}
	if edgeTypes["function_calls_function"] == 0 {
		t.Fatalf("expected function_calls_function edge, got %#v", edgeTypes)
	}
	if edgeTypes["function_calls_dependency"] == 0 {
		t.Fatalf("expected function_calls_dependency edge, got %#v", edgeTypes)
	}
}

func TestBuildGraphAddsConfigAndSensitiveEdges(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")

	bundlePath := filepath.Join(tmp, "svc.bundle.json")
	analyzerPath := filepath.Join(tmp, "svc.analyzer.json")
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "s2",
		"entities": []map[string]any{
			{"id": "ru1", "type": "RuntimeUnit", "natural_key": "go|main|main.go", "attributes": map[string]any{"language": "go", "kind": "main", "file": "main.go"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.95},
			{"id": "cfg1", "type": "ConfigKey", "natural_key": "DB_PASSWORD", "attributes": map[string]any{"key": "DB_PASSWORD", "pattern": "env_assignment", "environment": "prod", "source_kind": "config_manifest", "sensitive": true, "runtime_unit_id": "ru1"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.92},
			{"id": "ss1", "type": "SensitiveSurface", "natural_key": "config_key|DB_PASSWORD", "attributes": map[string]any{"kind": "config_key", "key": "DB_PASSWORD", "classification": "secret-like", "environment": "prod", "source_kind": "config_manifest"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.91},
		},
	})
	writeJSON(t, analyzerPath, map[string]any{
		"facts": []map[string]any{},
		"evidence": []map[string]any{
			{"id": "ev1", "snapshot_id": "s2", "file_path": "main.go", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 10, "snippet_hash": "a", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev2", "snapshot_id": "s2", "file_path": ".env.prod", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 30, "snippet_hash": "b", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev3", "snapshot_id": "s2", "file_path": ".env.prod", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 30, "snippet_hash": "c", "created_at_utc": "2026-01-01T00:00:00Z"},
		},
		"generated": "2026-01-01T00:00:00Z",
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + bundlePath + `
    analyzer_bundle_path: ` + analyzerPath + `
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type string `json:"type"`
		} `json:"nodes"`
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	nodeTypes := map[string]int{}
	for _, n := range graph.Nodes {
		nodeTypes[n.Type]++
	}
	if nodeTypes["config_key"] == 0 || nodeTypes["environment"] == 0 || nodeTypes["sensitive_surface"] == 0 {
		t.Fatalf("missing config/sensitive node types: %#v", nodeTypes)
	}
	edgeTypes := map[string]int{}
	for _, e := range graph.Edges {
		edgeTypes[e.Type]++
	}
	required := []string{
		"service_uses_config",
		"runtime_unit_reads_config",
		"config_scoped_to_environment",
		"service_exposes_sensitive_surface",
		"config_has_sensitive_surface",
	}
	for _, typ := range required {
		if edgeTypes[typ] == 0 {
			t.Fatalf("expected edge type %s, got %#v", typ, edgeTypes)
		}
	}
}

func TestBuildGraphResolvesConfigBackedExternalCallTargets(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")

	bundlePath := filepath.Join(tmp, "svc.bundle.json")
	analyzerPath := filepath.Join(tmp, "svc.analyzer.json")
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "s2b",
		"entities": []map[string]any{
			{"id": "ecq1", "type": "ExternalCall", "natural_key": "queue|PUBLISH|cfg:queue.name", "attributes": map[string]any{"protocol": "queue", "method": "PUBLISH", "target": "cfg:queue.name", "library": "aws-sdk-sqs-java-semantic"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.91},
			{"id": "cfgd", "type": "ConfigKey", "natural_key": "queue.name|default", "attributes": map[string]any{"key": "queue.name", "environment": "default", "profile": "default", "pattern": "spring_profile_resolved", "resolved_value": "orders.default"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
			{"id": "cfgp", "type": "ConfigKey", "natural_key": "queue.name|prod", "attributes": map[string]any{"key": "queue.name", "environment": "prod", "profile": "prod", "pattern": "spring_profile_resolved", "resolved_value": "orders.prod"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.9},
		},
	})
	writeJSON(t, analyzerPath, map[string]any{
		"facts": []map[string]any{},
		"evidence": []map[string]any{
			{"id": "ev1", "snapshot_id": "s2b", "file_path": "src/main/java/com/acme/Worker.java", "start_line": 10, "start_col": 1, "end_line": 10, "end_col": 40, "snippet_hash": "a", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev2", "snapshot_id": "s2b", "file_path": "src/main/resources/application.yml", "start_line": 2, "start_col": 1, "end_line": 2, "end_col": 20, "snippet_hash": "b", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev3", "snapshot_id": "s2b", "file_path": "src/main/resources/application-prod.yml", "start_line": 2, "start_col": 1, "end_line": 2, "end_col": 20, "snippet_hash": "c", "created_at_utc": "2026-01-01T00:00:00Z"},
		},
		"generated": "2026-01-01T00:00:00Z",
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + bundlePath + `
    analyzer_bundle_path: ` + analyzerPath + `
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type       string         `json:"type"`
			Label      string         `json:"label"`
			Attributes map[string]any `json:"attributes"`
		} `json:"nodes"`
		Edges []struct {
			Type       string         `json:"type"`
			Attributes map[string]any `json:"attributes"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	foundResolvedQueueNode := false
	for _, n := range graph.Nodes {
		if n.Type == "queue" && n.Label == "orders.prod" {
			foundResolvedQueueNode = true
			break
		}
	}
	if !foundResolvedQueueNode {
		t.Fatalf("expected queue node resolved from config key to orders.prod")
	}

	foundResolvedEdgeAttr := false
	for _, e := range graph.Edges {
		if e.Type != "service_publishes_queue" {
			continue
		}
		topic, _ := e.Attributes["topic"].(string)
		raw, _ := e.Attributes["topic_raw"].(string)
		resolved, _ := e.Attributes["resolved_from_config"].(bool)
		if topic == "orders.prod" && raw == "cfg:queue.name" && resolved {
			foundResolvedEdgeAttr = true
			break
		}
	}
	if !foundResolvedEdgeAttr {
		t.Fatalf("expected service_publishes_queue edge with raw cfg target and resolved topic")
	}
}

func TestBuildGraphResolvesAPICallsUsingSemanticHints(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	svcABundle := filepath.Join(tmp, "a.bundle.json")
	svcBBundle := filepath.Join(tmp, "b.bundle.json")
	writeJSON(t, svcABundle, map[string]any{
		"snapshot_id": "sha",
		"entities": []map[string]any{
			{"id": "ec-h1", "type": "ExternalCall", "natural_key": "http|GET|/users", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "/users", "target_service": "billing-service", "library": "feign-semantic"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.91},
			{"id": "ec-h2", "type": "ExternalCall", "natural_key": "http|POST|/orders", "attributes": map[string]any{"protocol": "http", "method": "POST", "target": "/orders", "base_url_ref": "${billing.base-url}", "library": "feign-semantic"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
			{"id": "cfg-h2", "type": "ConfigKey", "natural_key": "billing.base-url|prod", "attributes": map[string]any{"key": "billing.base-url", "environment": "prod", "resolved_value": "https://billing.internal"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.89},
		},
	})
	writeJSON(t, svcBBundle, map[string]any{
		"snapshot_id": "shb",
		"entities": []map[string]any{
			{"id": "ep-b1", "type": "Endpoint", "natural_key": "inbound|GET|/users", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/users"}, "evidence_ids": []string{"ev4"}, "fact_ids": []string{"f4"}, "confidence": 0.92},
			{"id": "ep-b2", "type": "Endpoint", "natural_key": "inbound|POST|/orders", "attributes": map[string]any{"direction": "inbound", "method": "POST", "path": "/orders"}, "evidence_ids": []string{"ev5"}, "fact_ids": []string{"f5"}, "confidence": 0.92},
		},
	})

	manifest := []byte(`
services:
  - id: orders-a
    name: Orders A
    repo_path: /repos/orders-a
    bundle_path: ` + svcABundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
  - id: billing-service
    name: Billing Service
    repo_path: /repos/billing-service
    bundle_path: ` + svcBBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://billing.internal"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Edges []struct {
			Type       string         `json:"type"`
			Attributes map[string]any `json:"attributes"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	byMatch := map[string]int{}
	serviceEndpointEdges := 0
	for _, e := range graph.Edges {
		if e.Type == "service_calls_service" || e.Type == "service_calls_endpoint" {
			match, _ := e.Attributes["service_match"].(string)
			if match != "" {
				byMatch[match]++
			}
		}
		if e.Type == "service_calls_endpoint" {
			serviceEndpointEdges++
		}
		if e.Type == "service_calls_service" {
			srcID, _ := e.Attributes["source_service_id"].(string)
			srcRepo, _ := e.Attributes["source_repo_path"].(string)
			tgtID, _ := e.Attributes["target_service_id"].(string)
			tgtRepo, _ := e.Attributes["target_repo_path"].(string)
			if srcID == "orders-a" && srcRepo == "/repos/orders-a" && tgtID == "billing-service" && tgtRepo == "/repos/billing-service" {
				byMatch["repo_provenance_ok"]++
			}
		}
	}
	if byMatch["target_service"] == 0 {
		t.Fatalf("expected service_calls_* edge resolved by target_service hint, got %#v", byMatch)
	}
	if byMatch["base_url_ref"] == 0 {
		t.Fatalf("expected service_calls_* edge resolved by base_url_ref hint, got %#v", byMatch)
	}
	if serviceEndpointEdges < 2 {
		t.Fatalf("expected endpoint-level edges for hint-resolved calls, got %d", serviceEndpointEdges)
	}
	if byMatch["repo_provenance_ok"] == 0 {
		t.Fatalf("expected service_calls_service edges to include source/target repo provenance")
	}
}

func TestBuildGraphPrefersMostSpecificBaseURLMatch(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	svcABundle := filepath.Join(tmp, "a.bundle.json")
	svcBBundle := filepath.Join(tmp, "b.bundle.json")
	svcCBundle := filepath.Join(tmp, "c.bundle.json")
	writeJSON(t, svcABundle, map[string]any{
		"snapshot_id": "sx-a",
		"entities": []map[string]any{
			{"id": "ec-http", "type": "ExternalCall", "natural_key": "http|GET|http://orders.internal/v1/users", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "http://orders.internal/v1/users", "library": "go-net-http"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.93},
		},
	})
	writeJSON(t, svcBBundle, map[string]any{
		"snapshot_id": "sx-b",
		"entities": []map[string]any{
			{"id": "ep-b1", "type": "Endpoint", "natural_key": "inbound|GET|/legacy", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/legacy"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcCBundle, map[string]any{
		"snapshot_id": "sx-c",
		"entities": []map[string]any{
			{"id": "ep-c1", "type": "Endpoint", "natural_key": "inbound|GET|/v1/users", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/v1/users"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.92},
		},
	})

	manifest := []byte(`
services:
  - id: caller-a
    name: Caller A
    bundle_path: ` + svcABundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
  - id: orders-root
    name: Orders Root
    bundle_path: ` + svcBBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://orders.internal"]
  - id: orders-v1
    name: Orders V1
    bundle_path: ` + svcCBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://orders.internal/v1"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Edges []struct {
			Type       string         `json:"type"`
			SourceID   string         `json:"source_id"`
			TargetID   string         `json:"target_id"`
			Attributes map[string]any `json:"attributes"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	foundToSpecificService := false
	foundToSpecificEndpoint := false
	foundToGenericService := false
	for _, e := range graph.Edges {
		if e.Type == "service_calls_service" && e.SourceID == "svc:caller-a" {
			if e.TargetID == "svc:orders-v1" {
				foundToSpecificService = true
			}
			if e.TargetID == "svc:orders-root" {
				foundToGenericService = true
			}
		}
		if e.Type == "service_calls_endpoint" && e.SourceID == "svc:caller-a" && e.TargetID == "ep:orders-v1:ep-c1" {
			foundToSpecificEndpoint = true
		}
	}
	if !foundToSpecificService {
		t.Fatalf("expected call to resolve to most specific base_url service orders-v1")
	}
	if foundToGenericService {
		t.Fatalf("did not expect call to resolve to generic base_url service orders-root")
	}
	if !foundToSpecificEndpoint {
		t.Fatalf("expected endpoint-level edge to orders-v1 /v1/users")
	}
}

func TestBuildGraphSkipsAmbiguousServiceMatch(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	callerBundle := filepath.Join(tmp, "caller.bundle.json")
	svcBBundle := filepath.Join(tmp, "b.bundle.json")
	svcCBundle := filepath.Join(tmp, "c.bundle.json")
	writeJSON(t, callerBundle, map[string]any{
		"snapshot_id": "sy-a",
		"entities": []map[string]any{
			{"id": "ec-http", "type": "ExternalCall", "natural_key": "http|GET|http://orders.internal/users", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "http://orders.internal/users", "library": "go-net-http"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcBBundle, map[string]any{
		"snapshot_id": "sy-b",
		"entities": []map[string]any{
			{"id": "ep-b1", "type": "Endpoint", "natural_key": "inbound|GET|/users", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/users"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcCBundle, map[string]any{
		"snapshot_id": "sy-c",
		"entities": []map[string]any{
			{"id": "ep-c1", "type": "Endpoint", "natural_key": "inbound|GET|/users", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/users"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.9},
		},
	})

	manifest := []byte(`
services:
  - id: caller-a
    name: Caller A
    bundle_path: ` + callerBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
  - id: orders-b
    name: Orders B
    bundle_path: ` + svcBBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://orders.internal"]
  - id: orders-c
    name: Orders C
    bundle_path: ` + svcCBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://orders.internal"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type       string         `json:"type"`
			SourceID   string         `json:"source_id"`
			TargetID   string         `json:"target_id"`
			Attributes map[string]any `json:"attributes"`
		} `json:"nodes"`
		Edges []struct {
			Type       string         `json:"type"`
			SourceID   string         `json:"source_id"`
			TargetID   string         `json:"target_id"`
			Attributes map[string]any `json:"attributes"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	foundUnresolvedNode := false
	foundUnresolvedEdge := false
	foundCandidates := false
	for _, e := range graph.Edges {
		if e.SourceID == "svc:caller-a" && (e.Type == "service_calls_service" || e.Type == "service_calls_endpoint") {
			t.Fatalf("expected no API service-link edges from caller-a for ambiguous target match, got type=%s", e.Type)
		}
		if e.Type == "service_has_unresolved_api_call" && e.SourceID == "svc:caller-a" {
			foundUnresolvedEdge = true
		}
	}
	for _, n := range graph.Nodes {
		if n.Type != "unresolved_api_call" {
			continue
		}
		foundUnresolvedNode = true
		raw, _ := n.Attributes["reason"].(string)
		if raw == "ambiguous_service_match" {
			if arr, ok := n.Attributes["service_match_candidates"].([]any); ok && len(arr) >= 2 {
				foundCandidates = true
			}
		}
	}
	if !foundUnresolvedNode {
		t.Fatalf("expected unresolved_api_call node for ambiguous match")
	}
	if !foundUnresolvedEdge {
		t.Fatalf("expected service_has_unresolved_api_call edge for ambiguous match")
	}
	if !foundCandidates {
		t.Fatalf("expected unresolved_api_call to include ambiguous service candidates")
	}
}

func TestBuildGraphUsesEnvironmentCompatibilityForAPITargetMatch(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	callerBundle := filepath.Join(tmp, "caller.bundle.json")
	prodBundle := filepath.Join(tmp, "target-prod.bundle.json")
	stageBundle := filepath.Join(tmp, "target-stage.bundle.json")
	writeJSON(t, callerBundle, map[string]any{
		"snapshot_id": "env-call",
		"entities": []map[string]any{
			{"id": "cfg-caller", "type": "ConfigKey", "natural_key": "spring.profiles.active|prod", "attributes": map[string]any{"key": "spring.profiles.active", "environment": "prod", "resolved_value": "prod"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
			{"id": "ec-http", "type": "ExternalCall", "natural_key": "http|GET|https://orders.internal/v1/users", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "https://orders.internal/v1/users"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.93},
		},
	})
	writeJSON(t, prodBundle, map[string]any{
		"snapshot_id": "env-target-prod",
		"entities": []map[string]any{
			{"id": "cfg-prod", "type": "ConfigKey", "natural_key": "spring.profiles.active|prod", "attributes": map[string]any{"key": "spring.profiles.active", "environment": "prod", "resolved_value": "prod"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.9},
			{"id": "ep-prod", "type": "Endpoint", "natural_key": "inbound|GET|/v1/users", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/v1/users"}, "evidence_ids": []string{"ev4"}, "fact_ids": []string{"f4"}, "confidence": 0.9},
		},
	})
	writeJSON(t, stageBundle, map[string]any{
		"snapshot_id": "env-target-stage",
		"entities": []map[string]any{
			{"id": "cfg-stage", "type": "ConfigKey", "natural_key": "spring.profiles.active|stage", "attributes": map[string]any{"key": "spring.profiles.active", "environment": "stage", "resolved_value": "stage"}, "evidence_ids": []string{"ev5"}, "fact_ids": []string{"f5"}, "confidence": 0.9},
			{"id": "ep-stage", "type": "Endpoint", "natural_key": "inbound|GET|/v1/users", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/v1/users"}, "evidence_ids": []string{"ev6"}, "fact_ids": []string{"f6"}, "confidence": 0.9},
		},
	})

	manifest := []byte(`
services:
  - id: caller-prod
    name: Caller Prod
    bundle_path: ` + callerBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
  - id: orders-prod
    name: Orders Prod
    bundle_path: ` + prodBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://orders.internal/v1"]
  - id: orders-stage
    name: Orders Stage
    bundle_path: ` + stageBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://orders.internal/v1"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Edges []struct {
			Type     string `json:"type"`
			SourceID string `json:"source_id"`
			TargetID string `json:"target_id"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	foundProdMatch := false
	foundStageMatch := false
	foundUnresolved := false
	for _, e := range graph.Edges {
		if e.SourceID != "svc:caller-prod" {
			continue
		}
		if e.Type == "service_calls_service" && e.TargetID == "svc:orders-prod" {
			foundProdMatch = true
		}
		if e.Type == "service_calls_service" && e.TargetID == "svc:orders-stage" {
			foundStageMatch = true
		}
		if e.Type == "service_has_unresolved_api_call" {
			foundUnresolved = true
		}
	}
	if !foundProdMatch {
		t.Fatalf("expected caller-prod to resolve API target to orders-prod with env-compatible matching")
	}
	if foundStageMatch {
		t.Fatalf("did not expect caller-prod to resolve API target to orders-stage")
	}
	if foundUnresolved {
		t.Fatalf("did not expect unresolved API call when env compatibility provides unique match")
	}
}

func TestBuildGraphAddsDependencyOwnershipRiskEdges(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")

	bundlePath := filepath.Join(tmp, "svc.bundle.json")
	analyzerPath := filepath.Join(tmp, "svc.analyzer.json")
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "s3",
		"entities": []map[string]any{
			{"id": "dep1", "type": "Dependency", "natural_key": "npm|axios|^1.7.0", "attributes": map[string]any{"ecosystem": "npm", "name": "axios", "version": "^1.7.0", "scope": "runtime", "source_file": "package.json"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
			{"id": "own1", "type": "OwnershipRule", "natural_key": "/package.json|@frontend-team", "attributes": map[string]any{"pattern": "/package.json", "owner": "@frontend-team", "source_file": "CODEOWNERS"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
			{"id": "risk1", "type": "DependencyRisk", "natural_key": "npm|axios|version_drift", "attributes": map[string]any{"ecosystem": "npm", "name": "axios", "version": "^1.7.0", "risk_type": "version_drift", "severity": "medium"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.85},
		},
	})
	writeJSON(t, analyzerPath, map[string]any{
		"facts": []map[string]any{},
		"evidence": []map[string]any{
			{"id": "ev1", "snapshot_id": "s3", "file_path": "package.json", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 20, "snippet_hash": "a", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev2", "snapshot_id": "s3", "file_path": "CODEOWNERS", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 20, "snippet_hash": "b", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev3", "snapshot_id": "s3", "file_path": "package.json", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 20, "snippet_hash": "c", "created_at_utc": "2026-01-01T00:00:00Z"},
		},
		"generated": "2026-01-01T00:00:00Z",
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + bundlePath + `
    analyzer_bundle_path: ` + analyzerPath + `
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type string `json:"type"`
		} `json:"nodes"`
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	nodeTypes := map[string]int{}
	for _, n := range graph.Nodes {
		nodeTypes[n.Type]++
	}
	if nodeTypes["dependency"] == 0 || nodeTypes["owner"] == 0 || nodeTypes["dependency_risk"] == 0 {
		t.Fatalf("missing dependency topology nodes: %#v", nodeTypes)
	}

	edgeTypes := map[string]int{}
	for _, e := range graph.Edges {
		edgeTypes[e.Type]++
	}
	required := []string{
		"service_depends_on_dependency",
		"dependency_owned_by",
		"service_has_dependency_risk",
		"dependency_has_risk",
	}
	for _, typ := range required {
		if edgeTypes[typ] == 0 {
			t.Fatalf("expected edge type %s, got %#v", typ, edgeTypes)
		}
	}
}

func TestBuildGraphPreservesDependencyInternalFlag(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")

	bundlePath := filepath.Join(tmp, "svc.bundle.json")
	analyzerPath := filepath.Join(tmp, "svc.analyzer.json")
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "s3b",
		"entities": []map[string]any{
			{"id": "dep1", "type": "Dependency", "natural_key": "go|example.com/acme/lib|v1.2.3", "attributes": map[string]any{"ecosystem": "go", "name": "example.com/acme/lib", "version": "v1.2.3", "scope": "runtime", "internal": true, "source_file": "go.mod"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
			{"id": "dep2", "type": "Dependency", "natural_key": "go|github.com/gin-gonic/gin|v1.10.0", "attributes": map[string]any{"ecosystem": "go", "name": "github.com/gin-gonic/gin", "version": "v1.10.0", "scope": "runtime", "internal": false, "source_file": "go.mod"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
		},
	})
	writeJSON(t, analyzerPath, map[string]any{
		"facts": []map[string]any{},
		"evidence": []map[string]any{
			{"id": "ev1", "snapshot_id": "s3b", "file_path": "go.mod", "start_line": 1, "start_col": 1, "end_line": 1, "end_col": 20, "snippet_hash": "a", "created_at_utc": "2026-01-01T00:00:00Z"},
			{"id": "ev2", "snapshot_id": "s3b", "file_path": "go.mod", "start_line": 2, "start_col": 1, "end_line": 2, "end_col": 20, "snippet_hash": "b", "created_at_utc": "2026-01-01T00:00:00Z"},
		},
		"generated": "2026-01-01T00:00:00Z",
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + bundlePath + `
    analyzer_bundle_path: ` + analyzerPath + `
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type       string         `json:"type"`
			Attributes map[string]any `json:"attributes"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	internalSeen := false
	externalSeen := false
	for _, n := range graph.Nodes {
		if n.Type != "dependency" {
			continue
		}
		name, _ := n.Attributes["name"].(string)
		internal, _ := n.Attributes["internal"].(bool)
		if name == "example.com/acme/lib" && internal {
			internalSeen = true
		}
		if name == "github.com/gin-gonic/gin" && !internal {
			externalSeen = true
		}
	}
	if !internalSeen || !externalSeen {
		t.Fatalf("expected both internal and external dependency flags on dependency nodes")
	}
}

func TestBuildGraphAddsCrossRepoCanonicalization(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	svcABundle := filepath.Join(tmp, "a.bundle.json")
	svcBBundle := filepath.Join(tmp, "b.bundle.json")
	writeJSON(t, svcABundle, map[string]any{
		"snapshot_id": "sa",
		"entities": []map[string]any{
			{"id": "ec-aq", "type": "ExternalCall", "natural_key": "queue|PUBLISH|orders.events", "attributes": map[string]any{"protocol": "queue", "method": "PUBLISH", "target": "orders.events"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
			{"id": "ec-adb", "type": "ExternalCall", "natural_key": "db|READ|payments", "attributes": map[string]any{"protocol": "db", "method": "READ", "target": "payments"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcBBundle, map[string]any{
		"snapshot_id": "sb",
		"entities": []map[string]any{
			{"id": "ec-bq", "type": "ExternalCall", "natural_key": "queue|PUBLISH|kafka:orders.events", "attributes": map[string]any{"protocol": "queue", "method": "PUBLISH", "target": "kafka:orders.events"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.9},
			{"id": "ec-bdb", "type": "ExternalCall", "natural_key": "db|WRITE|db:payments", "attributes": map[string]any{"protocol": "db", "method": "WRITE", "target": "db:payments"}, "evidence_ids": []string{"ev4"}, "fact_ids": []string{"f4"}, "confidence": 0.9},
		},
	})

	manifest := []byte(`
services:
  - id: orders-service-a
    name: Orders Service A
    repo_path: /repos/orders-a
    bundle_path: ` + svcABundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://orders.internal"]
  - id: orders-service-b
    name: Orders Service B
    repo_path: /repos/orders-b
    bundle_path: ` + svcBBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://orders.internal/v1"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type string `json:"type"`
		} `json:"nodes"`
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	nodeTypes := map[string]int{}
	for _, n := range graph.Nodes {
		nodeTypes[n.Type]++
	}
	if nodeTypes["canonical_service"] == 0 || nodeTypes["canonical_queue"] == 0 || nodeTypes["canonical_database"] == 0 {
		t.Fatalf("expected canonical nodes, got %#v", nodeTypes)
	}
	edgeTypes := map[string]int{}
	for _, e := range graph.Edges {
		edgeTypes[e.Type]++
	}
	required := []string{
		"service_alias_of_canonical_service",
		"queue_alias_of_canonical_queue",
		"database_alias_of_canonical_database",
	}
	for _, typ := range required {
		if edgeTypes[typ] == 0 {
			t.Fatalf("expected edge type %s, got %#v", typ, edgeTypes)
		}
	}
}

func TestBuildGraphAddsCanonicalAPIHostNodes(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	svcABundle := filepath.Join(tmp, "a.bundle.json")
	svcBBundle := filepath.Join(tmp, "b.bundle.json")
	writeJSON(t, svcABundle, map[string]any{
		"snapshot_id": "host-a",
		"entities": []map[string]any{
			{"id": "ec-a1", "type": "ExternalCall", "natural_key": "http|POST|https://payments.internal/v1/charge", "attributes": map[string]any{"protocol": "http", "method": "POST", "target": "https://payments.internal/v1/charge"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcBBundle, map[string]any{
		"snapshot_id": "host-b",
		"entities":    []map[string]any{},
	})

	manifest := []byte(`
services:
  - id: checkout-service
    name: Checkout Service
    repo_path: /repos/checkout
    bundle_path: ` + svcABundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://checkout.internal"]
  - id: billing-service
    name: Billing Service
    repo_path: /repos/billing
    bundle_path: ` + svcBBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["http://payments.internal"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type       string         `json:"type"`
			Label      string         `json:"label"`
			Attributes map[string]any `json:"attributes"`
		} `json:"nodes"`
		Edges []struct {
			Type       string         `json:"type"`
			SourceID   string         `json:"source_id"`
			TargetID   string         `json:"target_id"`
			Attributes map[string]any `json:"attributes"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	var hostNodeID string
	for _, n := range graph.Nodes {
		if n.Type != "canonical_api_host" {
			continue
		}
		if n.Label == "payments.internal" {
			hostNodeID = n.Attributes["canonical_host"].(string)
		}
	}
	if hostNodeID == "" {
		t.Fatalf("expected canonical_api_host node for payments.internal")
	}

	foundCheckoutEdge := false
	foundBillingEdge := false
	foundRepoProvenance := false
	for _, e := range graph.Edges {
		if e.Type != "service_alias_of_canonical_api_host" {
			continue
		}
		if host, _ := e.Attributes["canonical_host"].(string); host != "payments.internal" {
			continue
		}
		if e.SourceID == "svc:checkout-service" {
			foundCheckoutEdge = true
		}
		if e.SourceID == "svc:billing-service" {
			foundBillingEdge = true
			if srcID, _ := e.Attributes["source_service_id"].(string); srcID == "billing-service" {
				if srcRepo, _ := e.Attributes["source_repo_path"].(string); srcRepo == "/repos/billing" {
					foundRepoProvenance = true
				}
			}
		}
	}
	if !foundCheckoutEdge || !foundBillingEdge {
		t.Fatalf("expected canonical host alias edges from both services, checkout=%t billing=%t", foundCheckoutEdge, foundBillingEdge)
	}
	if !foundRepoProvenance {
		t.Fatalf("expected canonical host alias edge to preserve source repo provenance")
	}
}

func TestBuildGraphDoesNotCanonicalizeServicesAcrossDifferentEnvironments(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	svcProdBundle := filepath.Join(tmp, "orders-prod.bundle.json")
	svcStageBundle := filepath.Join(tmp, "orders-stage.bundle.json")
	writeJSON(t, svcProdBundle, map[string]any{
		"snapshot_id": "env-prod",
		"entities": []map[string]any{
			{"id": "cfg-prod", "type": "ConfigKey", "natural_key": "spring.profiles.active|prod", "attributes": map[string]any{"key": "spring.profiles.active", "environment": "prod", "resolved_value": "prod"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcStageBundle, map[string]any{
		"snapshot_id": "env-stage",
		"entities": []map[string]any{
			{"id": "cfg-stage", "type": "ConfigKey", "natural_key": "spring.profiles.active|stage", "attributes": map[string]any{"key": "spring.profiles.active", "environment": "stage", "resolved_value": "stage"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
		},
	})

	manifest := []byte(`
services:
  - id: orders-svc-1
    name: Orders Service
    bundle_path: ` + svcProdBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://orders.prod.internal"]
  - id: orders-svc-2
    name: Orders Service
    bundle_path: ` + svcStageBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://orders.staging.internal"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type string `json:"type"`
		} `json:"nodes"`
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	nodeTypes := map[string]int{}
	for _, n := range graph.Nodes {
		nodeTypes[n.Type]++
	}
	if nodeTypes["canonical_service"] != 0 {
		t.Fatalf("expected no canonical_service nodes across env-isolated services, got %#v", nodeTypes)
	}
	edgeTypes := map[string]int{}
	for _, e := range graph.Edges {
		edgeTypes[e.Type]++
	}
	if edgeTypes["service_alias_of_canonical_service"] != 0 {
		t.Fatalf("expected no service_alias_of_canonical_service edges across env-isolated services, got %#v", edgeTypes)
	}
}

func TestBuildGraphAddsNormalizedResolverAttributes(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	emptyAnalyzer := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, emptyAnalyzer, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	svcABundle := filepath.Join(tmp, "a.bundle.json")
	svcBBundle := filepath.Join(tmp, "b.bundle.json")
	writeJSON(t, svcABundle, map[string]any{
		"snapshot_id": "sa",
		"entities": []map[string]any{
			{"id": "ec-http", "type": "ExternalCall", "natural_key": "http|GET|https://orders-b.local/v1/users?active=true", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "https://orders-b.local/v1/users?active=true"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.95},
			{"id": "ec-q", "type": "ExternalCall", "natural_key": "queue|PUBLISH|kafka:orders.events", "attributes": map[string]any{"protocol": "queue", "method": "PUBLISH", "target": "kafka:orders.events"}, "evidence_ids": []string{"ev2"}, "fact_ids": []string{"f2"}, "confidence": 0.9},
			{"id": "ec-db", "type": "ExternalCall", "natural_key": "db|WRITE|db:payments", "attributes": map[string]any{"protocol": "db", "method": "WRITE", "target": "db:payments"}, "evidence_ids": []string{"ev3"}, "fact_ids": []string{"f3"}, "confidence": 0.9},
		},
	})
	writeJSON(t, svcBBundle, map[string]any{
		"snapshot_id": "sb",
		"entities": []map[string]any{
			{"id": "ep-b", "type": "Endpoint", "natural_key": "inbound|GET|/v1/users/", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/v1/users/"}, "evidence_ids": []string{"ev4"}, "fact_ids": []string{"f4"}, "confidence": 0.9},
			{"id": "ec-bq", "type": "ExternalCall", "natural_key": "queue|CONSUME|orders.events", "attributes": map[string]any{"protocol": "queue", "method": "CONSUME", "target": "orders.events"}, "evidence_ids": []string{"ev5"}, "fact_ids": []string{"f5"}, "confidence": 0.9},
			{"id": "ec-bdb", "type": "ExternalCall", "natural_key": "db|READ|payments", "attributes": map[string]any{"protocol": "db", "method": "READ", "target": "payments"}, "evidence_ids": []string{"ev6"}, "fact_ids": []string{"f6"}, "confidence": 0.9},
		},
	})

	manifest := []byte(`
services:
  - id: orders-a
    name: Orders A
    bundle_path: ` + svcABundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://orders-a.local"]
  - id: orders-b
    name: Orders B
    bundle_path: ` + svcBBundle + `
    analyzer_bundle_path: ` + emptyAnalyzer + `
    base_urls: ["https://orders-b.local"]
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type       string         `json:"type"`
			Attributes map[string]any `json:"attributes"`
		} `json:"nodes"`
		Edges []struct {
			Type       string         `json:"type"`
			Attributes map[string]any `json:"attributes"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}

	foundEndpointNormalized := false
	foundServiceCallPathNormalized := false
	foundQueueCanonical := false
	foundDBCanonical := false

	for _, n := range graph.Nodes {
		if n.Type == "endpoint" {
			if got, _ := n.Attributes["path_normalized"].(string); got == "/v1/users" {
				foundEndpointNormalized = true
			}
		}
		if n.Type == "queue" {
			if got, _ := n.Attributes["canonical_key"].(string); got == "orders.events" {
				foundQueueCanonical = true
			}
		}
		if n.Type == "database" {
			if got, _ := n.Attributes["canonical_key"].(string); got == "payments" {
				foundDBCanonical = true
			}
		}
	}
	for _, e := range graph.Edges {
		if e.Type == "service_calls_endpoint" {
			if got, _ := e.Attributes["target_path_normalized"].(string); got == "/v1/users" {
				foundServiceCallPathNormalized = true
			}
		}
	}
	if !foundEndpointNormalized {
		t.Fatalf("expected endpoint node path_normalized attribute")
	}
	if !foundServiceCallPathNormalized {
		t.Fatalf("expected service_calls_endpoint edge target_path_normalized attribute")
	}
	if !foundQueueCanonical {
		t.Fatalf("expected queue node canonical_key attribute")
	}
	if !foundDBCanonical {
		t.Fatalf("expected database node canonical_key attribute")
	}
}

func TestBuildGraphAddsConflictNodesAndEdges(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	analyzerPath := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, analyzerPath, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})
	bundlePath := filepath.Join(tmp, "svc.bundle.json")
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "sc",
		"entities": []map[string]any{
			{
				"id":          "cf1",
				"type":        "Conflict",
				"natural_key": "ExternalCall|http|get|svc",
				"attributes": map[string]any{
					"entity_type":        "ExternalCall",
					"entity_natural_key": "http|get|svc",
					"conflict_keys":      []string{"timeout_ms"},
					"observed_values":    map[string]any{"timeout_ms": []string{"1000", "2000"}},
					"status":             "unresolved",
					"severity":           "medium",
				},
				"evidence_ids": []string{"ev1"},
				"fact_ids":     []string{"f1"},
				"confidence":   0.55,
			},
		},
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + bundlePath + `
    analyzer_bundle_path: ` + analyzerPath + `
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type string `json:"type"`
		} `json:"nodes"`
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	nodeTypes := map[string]int{}
	for _, n := range graph.Nodes {
		nodeTypes[n.Type]++
	}
	if nodeTypes["conflict"] == 0 {
		t.Fatalf("expected conflict node type, got %#v", nodeTypes)
	}
	edgeTypes := map[string]int{}
	for _, e := range graph.Edges {
		edgeTypes[e.Type]++
	}
	if edgeTypes["service_has_conflict"] == 0 {
		t.Fatalf("expected service_has_conflict edge, got %#v", edgeTypes)
	}
}

func TestBuildGraphAddsVerificationDecisionEdges(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	manifestPath := filepath.Join(tmp, "services.yaml")
	analyzerPath := filepath.Join(tmp, "empty.analyzer.json")
	writeJSON(t, analyzerPath, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})
	bundlePath := filepath.Join(tmp, "svc.bundle.json")
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "sv",
		"entities": []map[string]any{
			{"id": "ep1", "type": "Endpoint", "natural_key": "inbound|GET|/health", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/health"}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.9},
			{"id": "vd1", "type": "VerificationDecision", "natural_key": "Endpoint|ep1|needs_review", "attributes": map[string]any{
				"subject_entity_id":   "ep1",
				"subject_entity_type": "Endpoint",
				"status":              "needs_review",
				"reason":              "confidence in review band",
				"verifier_id":         "verifier.rule.v1",
			}, "evidence_ids": []string{"ev1"}, "fact_ids": []string{"f1"}, "confidence": 0.8},
		},
	})

	manifest := []byte(`
services:
  - id: service-a
    name: Service A
    bundle_path: ` + bundlePath + `
    analyzer_bundle_path: ` + analyzerPath + `
`)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := Run(context.Background(), []string{"build", "--manifest", manifestPath, "--out", outDir, "--mode", "multi"}); err != nil {
		t.Fatalf("graph build failed: %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index struct {
		Graphs []struct {
			Path string `json:"path"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	graphData, err := os.ReadFile(index.Graphs[0].Path)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph struct {
		Nodes []struct {
			Type string `json:"type"`
		} `json:"nodes"`
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	nodeTypes := map[string]int{}
	for _, n := range graph.Nodes {
		nodeTypes[n.Type]++
	}
	if nodeTypes["verification_decision"] == 0 {
		t.Fatalf("expected verification_decision node, got %#v", nodeTypes)
	}
	edgeTypes := map[string]int{}
	for _, e := range graph.Edges {
		edgeTypes[e.Type]++
	}
	if edgeTypes["service_has_verification_decision"] == 0 || edgeTypes["verification_decision_targets_entity"] == 0 {
		t.Fatalf("expected verification decision edges, got %#v", edgeTypes)
	}
}

func TestBuildReusesLatestGraphWhenFingerprintMatches(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	bundlePath := filepath.Join(outDir, "bundle", "intelligence_bundle.json")
	analyzerPath := filepath.Join(outDir, "analyzers", "bundle.json")
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o755); err != nil {
		t.Fatalf("mkdir bundle dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(analyzerPath), 0o755); err != nil {
		t.Fatalf("mkdir analyzer dir: %v", err)
	}
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "s1",
		"entities": []map[string]any{
			{"id": "ep1", "type": "Endpoint", "natural_key": "inbound|GET|/health", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/health"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.95},
		},
	})
	writeJSON(t, analyzerPath, map[string]any{
		"facts":     []any{},
		"evidence":  []any{},
		"generated": "2026-01-01T00:00:00Z",
	})

	first, err := Build(context.Background(), BuildRequest{
		OutDir:             outDir,
		Mode:               "single",
		ServiceID:          "svc.local",
		ServiceName:        "Local",
		BundlePath:         bundlePath,
		AnalyzerBundlePath: analyzerPath,
		BaseURLs:           []string{"http://localhost:8080"},
	})
	if err != nil {
		t.Fatalf("first build failed: %v", err)
	}
	second, err := Build(context.Background(), BuildRequest{
		OutDir:             outDir,
		Mode:               "single",
		ServiceID:          "svc.local",
		ServiceName:        "Local",
		BundlePath:         bundlePath,
		AnalyzerBundlePath: analyzerPath,
		BaseURLs:           []string{"http://localhost:8080"},
	})
	if err != nil {
		t.Fatalf("second build failed: %v", err)
	}
	if first.GraphID != second.GraphID {
		t.Fatalf("expected reuse with same graph id, got first=%s second=%s", first.GraphID, second.GraphID)
	}
	if first.GraphPath != second.GraphPath {
		t.Fatalf("expected reuse with same graph path, got first=%s second=%s", first.GraphPath, second.GraphPath)
	}

	indexPath := filepath.Join(outDir, "graph", "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index graphschema.Index
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if len(index.Graphs) != 1 {
		t.Fatalf("expected one graph summary in index, got %d", len(index.Graphs))
	}
	if index.Graphs[0].GraphID != first.GraphID {
		t.Fatalf("expected summary graph id %q, got %q", first.GraphID, index.Graphs[0].GraphID)
	}
	if index.Graphs[0].Fingerprint == "" {
		t.Fatalf("expected fingerprint in summary")
	}

	graphData, err := os.ReadFile(first.GraphPath)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var graph graphschema.Graph
	if err := json.Unmarshal(graphData, &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if graph.Meta.Provenance.Fingerprint == "" {
		t.Fatalf("expected fingerprint in graph provenance")
	}
}

func TestBuildCreatesNewGraphWhenFingerprintChanges(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "out")
	bundlePath := filepath.Join(outDir, "bundle", "intelligence_bundle.json")
	analyzerPath := filepath.Join(outDir, "analyzers", "bundle.json")
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o755); err != nil {
		t.Fatalf("mkdir bundle dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(analyzerPath), 0o755); err != nil {
		t.Fatalf("mkdir analyzer dir: %v", err)
	}
	writeJSON(t, analyzerPath, map[string]any{
		"facts":     []any{},
		"evidence":  []any{},
		"generated": "2026-01-01T00:00:00Z",
	})
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "s1",
		"entities": []map[string]any{
			{"id": "ep1", "type": "Endpoint", "natural_key": "inbound|GET|/health", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/health"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.95},
		},
	})

	first, err := Build(context.Background(), BuildRequest{
		OutDir:             outDir,
		Mode:               "single",
		ServiceID:          "svc.local",
		ServiceName:        "Local",
		BundlePath:         bundlePath,
		AnalyzerBundlePath: analyzerPath,
		BaseURLs:           []string{"http://localhost:8080"},
	})
	if err != nil {
		t.Fatalf("first build failed: %v", err)
	}

	// Mutate bundle content to force a new fingerprint.
	writeJSON(t, bundlePath, map[string]any{
		"snapshot_id": "s2",
		"entities": []map[string]any{
			{"id": "ep1", "type": "Endpoint", "natural_key": "inbound|GET|/healthz", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/healthz"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.95},
		},
	})

	second, err := Build(context.Background(), BuildRequest{
		OutDir:             outDir,
		Mode:               "single",
		ServiceID:          "svc.local",
		ServiceName:        "Local",
		BundlePath:         bundlePath,
		AnalyzerBundlePath: analyzerPath,
		BaseURLs:           []string{"http://localhost:8080"},
	})
	if err != nil {
		t.Fatalf("second build failed: %v", err)
	}
	if first.GraphID == second.GraphID {
		t.Fatalf("expected new graph id after fingerprint change, got %s", first.GraphID)
	}

	indexData, err := os.ReadFile(filepath.Join(outDir, "graph", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index graphschema.Index
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if len(index.Graphs) != 2 {
		t.Fatalf("expected two graph summaries after changed build, got %d", len(index.Graphs))
	}
}

func writeJSON(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
