package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..", "testdata")
}

func TestReadDiffMindArtifacts_OrderService(t *testing.T) {
	td := testdataDir(t)
	artifactsDir := filepath.Join(td, "sample_diffmind_output", "order-service", ".diffmind", "runs", "run_001")

	arch, err := ReadDiffMindArtifacts(artifactsDir)
	if err != nil {
		t.Fatalf("failed to read artifacts: %v", err)
	}

	if arch.Manifest == nil {
		t.Fatal("expected manifest to be loaded")
	}
	if arch.Manifest.RunID != "run_001" {
		t.Errorf("expected run_id run_001, got %s", arch.Manifest.RunID)
	}

	if len(arch.Exposures) != 2 {
		t.Errorf("expected 2 exposures, got %d", len(arch.Exposures))
	}
	if len(arch.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies (1 outbound_http + 1 db_operation), got %d", len(arch.Dependencies))
	}
	if len(arch.Connections) != 1 {
		t.Errorf("expected 1 connection, got %d", len(arch.Connections))
	}
}

func TestReadDiffMindArtifacts_BillingService(t *testing.T) {
	td := testdataDir(t)
	artifactsDir := filepath.Join(td, "sample_diffmind_output", "billing-service", ".diffmind", "runs", "run_001")

	arch, err := ReadDiffMindArtifacts(artifactsDir)
	if err != nil {
		t.Fatalf("failed to read artifacts: %v", err)
	}

	if len(arch.Exposures) != 1 {
		t.Errorf("expected 1 exposure, got %d", len(arch.Exposures))
	}
	if len(arch.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies, got %d", len(arch.Dependencies))
	}
	if len(arch.Connections) != 1 {
		t.Errorf("expected 1 connection, got %d", len(arch.Connections))
	}

	// Verify specific data.
	if arch.Exposures[0].Name != "POST /api/charge" {
		t.Errorf("expected exposure name 'POST /api/charge', got %s", arch.Exposures[0].Name)
	}
}

func TestReadDiffMindRun_LatestRun(t *testing.T) {
	td := testdataDir(t)
	repoPath := filepath.Join(td, "sample_diffmind_output", "order-service")

	arch, err := ReadDiffMindRun(repoPath)
	if err != nil {
		t.Fatalf("failed to read latest run: %v", err)
	}

	if arch.Manifest == nil {
		t.Fatal("expected manifest")
	}
	if len(arch.Exposures) != 2 {
		t.Errorf("expected 2 exposures, got %d", len(arch.Exposures))
	}
}

func TestReadDiffMindArtifacts_NonExistentDir(t *testing.T) {
	_, err := ReadDiffMindArtifacts("/nonexistent/path")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

func TestReadDiffMindArchfile(t *testing.T) {
	repo := t.TempDir()
	body := []byte(`schema: diffmind.discovery.v1
service: order-service
vars:
  queue: order-events
resources:
  - id: orders_db
    kind: datastore
    platform: postgres
    name: orders
    instance: orders-db
  - id: order_queue
    kind: message_bus
    platform: sqs
    name: ${queue}
    instance: ${queue}
exposures:
  - id: exp_create
    type: http_route
    name: POST /orders
    details: {method: POST, path: /orders}
dependencies:
  - id: dep_db
    type: db_operation
    name: write orders
    resource: orders_db
    details: {operation: write}
  - id: dep_pub
    type: queue_publish
    name: publish order event
    resource: order_queue
connections:
  - from: exp_create
    to: dep_db
  - from: exp_create
    to: dep_pub
`)
	path := filepath.Join(repo, "diffmind.yaml")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	arch, err := ReadDiffMindArtifacts(path)
	if err != nil {
		t.Fatalf("read archfile: %v", err)
	}
	if arch.Manifest == nil || arch.Manifest.RunID != RepoArchfileRunID {
		t.Fatalf("manifest = %+v", arch.Manifest)
	}
	if len(arch.Resources) != 2 || len(arch.Exposures) != 1 || len(arch.Dependencies) != 2 || len(arch.Connections) != 2 {
		t.Fatalf("counts resources=%d exp=%d dep=%d conn=%d", len(arch.Resources), len(arch.Exposures), len(arch.Dependencies), len(arch.Connections))
	}
	if arch.Dependencies[0].Details["resource"] != "orders_db" {
		t.Fatalf("resource detail not preserved: %+v", arch.Dependencies[0].Details)
	}

	exposures, deps, conns := ReadDiffMindFileMaps(path)
	if len(exposures["http_route"]) != 1 || len(deps["db_operation"]) != 1 || len(conns["connections"]) != 2 {
		t.Fatalf("map counts exp=%+v dep=%+v conns=%+v", exposures, deps, conns)
	}
	if deps["queue_publish"][0]["details"].(map[string]any)["queue"] != "order-events" {
		t.Fatalf("queue resource not expanded into details: %+v", deps["queue_publish"][0])
	}
}
