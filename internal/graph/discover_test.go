package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunDiscoverBuildsManifestFromOutputs(t *testing.T) {
	tmp := t.TempDir()
	outputsRoot := filepath.Join(tmp, "outputs")
	if err := os.MkdirAll(outputsRoot, 0o755); err != nil {
		t.Fatalf("mkdir outputs: %v", err)
	}

	serviceAOut := filepath.Join(outputsRoot, "service-a")
	serviceBOut := filepath.Join(outputsRoot, "service-b")
	writeOutputBundle(t, serviceAOut, map[string]any{
		"snapshot_id": "sa",
		"entities": []map[string]any{
			{"id": "ec-a-http", "type": "ExternalCall", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "https://service-b.internal/users"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
			{"id": "ec-a-q", "type": "ExternalCall", "attributes": map[string]any{"protocol": "queue", "method": "PUBLISH", "target": "orders.events"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
			{"id": "ec-a-db", "type": "ExternalCall", "attributes": map[string]any{"protocol": "db", "method": "WRITE", "target": "orders"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
		},
	})
	writeOutputBundle(t, serviceBOut, map[string]any{
		"snapshot_id": "sb",
		"entities": []map[string]any{
			{"id": "ec-b-q", "type": "ExternalCall", "attributes": map[string]any{"protocol": "queue", "method": "CONSUME", "target": "orders.events"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
			{"id": "ec-b-db", "type": "ExternalCall", "attributes": map[string]any{"protocol": "db", "method": "READ", "target": "orders"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
		},
	})
	writeRunReportSource(t, serviceAOut, "/repos/service-a")
	writeRunReportSource(t, serviceBOut, "/repos/service-b")

	manifestPath := filepath.Join(tmp, "services.yaml")
	if err := Run(context.Background(), []string{"discover", "--sources", outputsRoot, "--manifest-out", manifestPath}); err != nil {
		t.Fatalf("graph discover failed: %v", err)
	}

	services, err := loadManifestServices(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}

	byID := map[string]serviceSpec{}
	for _, svc := range services {
		byID[svc.ID] = svc
	}

	a, ok := byID["service-a"]
	if !ok {
		t.Fatalf("missing service-a")
	}
	if a.RepoPath != "/repos/service-a" {
		t.Fatalf("unexpected service-a repo_path: %q", a.RepoPath)
	}
	if len(a.QueuePublishes) != 1 || a.QueuePublishes[0] != "orders.events" {
		t.Fatalf("unexpected service-a queue_publishes: %#v", a.QueuePublishes)
	}
	if len(a.DBWrites) != 1 || a.DBWrites[0] != "orders" {
		t.Fatalf("unexpected service-a db_writes: %#v", a.DBWrites)
	}

	b, ok := byID["service-b"]
	if !ok {
		t.Fatalf("missing service-b")
	}
	if len(b.QueueConsumes) != 1 || b.QueueConsumes[0] != "orders.events" {
		t.Fatalf("unexpected service-b queue_consumes: %#v", b.QueueConsumes)
	}
	if len(b.DBReads) != 1 || b.DBReads[0] != "orders" {
		t.Fatalf("unexpected service-b db_reads: %#v", b.DBReads)
	}
	if len(b.BaseURLs) != 1 || b.BaseURLs[0] != "https://service-b.internal" {
		t.Fatalf("unexpected service-b base_urls: %#v", b.BaseURLs)
	}
}

func TestBuildWithSourcesInRequest(t *testing.T) {
	tmp := t.TempDir()
	outputsRoot := filepath.Join(tmp, "outputs")
	if err := os.MkdirAll(outputsRoot, 0o755); err != nil {
		t.Fatalf("mkdir outputs: %v", err)
	}
	serviceAOut := filepath.Join(outputsRoot, "service-a")
	serviceBOut := filepath.Join(outputsRoot, "service-b")
	writeOutputBundle(t, serviceAOut, map[string]any{
		"snapshot_id": "sa",
		"entities": []map[string]any{
			{"id": "ec-a-http", "type": "ExternalCall", "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "https://service-b.internal/users"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
		},
	})
	writeOutputBundle(t, serviceBOut, map[string]any{
		"snapshot_id": "sb",
		"entities": []map[string]any{
			{"id": "ep-b", "type": "Endpoint", "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/users", "framework": "go-router"}, "evidence_ids": []string{}, "fact_ids": []string{}, "confidence": 0.9},
		},
	})
	writeRunReportSource(t, serviceAOut, "/repos/service-a")
	writeRunReportSource(t, serviceBOut, "/repos/service-b")

	outDir := filepath.Join(tmp, "graph-out")
	res, err := Build(context.Background(), BuildRequest{
		Mode:    "multi",
		OutDir:  outDir,
		Sources: []string{outputsRoot},
	})
	if err != nil {
		t.Fatalf("build with sources failed: %v", err)
	}
	if res.NodeCount == 0 || res.EdgeCount == 0 {
		t.Fatalf("expected non-empty graph result, got nodes=%d edges=%d", res.NodeCount, res.EdgeCount)
	}
	if !fileExists(res.GraphPath) {
		t.Fatalf("expected graph path to exist: %s", res.GraphPath)
	}
}

func writeOutputBundle(t *testing.T, outRoot string, payload map[string]any) {
	t.Helper()
	bundleDir := filepath.Join(outRoot, "bundle")
	analyzerDir := filepath.Join(outRoot, "analyzers")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir bundle dir: %v", err)
	}
	if err := os.MkdirAll(analyzerDir, 0o755); err != nil {
		t.Fatalf("mkdir analyzer dir: %v", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "intelligence_bundle.json"), data, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(analyzerDir, "bundle.json"), []byte(`{"facts":[],"evidence":[]}`), 0o644); err != nil {
		t.Fatalf("write analyzer bundle: %v", err)
	}
}

func writeRunReportSource(t *testing.T, outRoot string, source string) {
	t.Helper()
	runDir := filepath.Join(outRoot, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	data, err := json.Marshal(map[string]any{"source": source})
	if err != nil {
		t.Fatalf("marshal run report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "report.json"), data, 0o644); err != nil {
		t.Fatalf("write run report: %v", err)
	}
}
