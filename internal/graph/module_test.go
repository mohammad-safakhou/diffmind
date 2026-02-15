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
