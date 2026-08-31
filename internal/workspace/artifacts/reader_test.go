package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..", "..", "testdata", "workspace")
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

func TestReadDiffMindArtifacts_ProtocolRun(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run_001")
	contextDir := filepath.Join(runDir, ".diffmind", "context")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "run_manifest.json"), []byte(`{"run_id":"run_001","repo_path":"/tmp/orders","schema_version":"diffmind.service.v1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
  "schema": "diffmind.service.v1",
  "service": {"id": "orders-api", "name": "orders-api"},
  "repository": {"path": "/tmp/orders"},
  "objects": {
    "http_endpoints": [{
      "id": "http.create_order",
      "kind": "http_endpoint",
      "name": "Create order",
      "method": "POST",
      "path": "/orders",
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }],
    "db_resources": [{
      "id": "db.orders",
      "kind": "db_resource",
      "name": "orders",
      "engine": "postgres",
      "database": "orders",
      "table": "orders",
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }],
    "http_calls": [{
      "id": "httpcall.score",
      "kind": "http_call",
      "name": "Get score",
      "method": "GET",
      "url_template": "http://routing-service/score/{storeCode}",
      "target": {"type": "unresolved", "ref": "service.routing_service", "unresolved": true},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }],
    "db_queries": [{
      "id": "dbq.insert_order",
      "kind": "db_query",
      "name": "Insert order",
      "engine": "postgres",
      "operation": "create",
      "access": "write",
      "target": {"resource_ref": "db.orders", "database": "orders", "tables": ["orders"]},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }],
    "queue_consumers": [{
      "id": "queue.consume_stream",
      "kind": "queue_consumer",
      "name": "sync.handler (DynamoDB Stream traffic-info)",
      "platform": "queue",
      "topic": "DynamoDB Stream traffic-info",
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }],
    "cache_operations": [{
      "id": "cache.redis_read",
      "kind": "cache_operation",
      "name": "Redis read",
      "platform": "database",
      "operation": "read",
      "target": {"cache": "redis-main"},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }]
  },
  "flows": [{
    "id": "flow.create_order.insert_order",
    "kind": "api_to_database",
    "from": "http.create_order",
    "to": "dbq.insert_order",
    "status": "confirmed",
    "confidence": "high",
    "origin": "deterministic"
  }]
}`)
	if err := os.WriteFile(filepath.Join(contextDir, "service.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	arch, err := ReadDiffMindArtifacts(runDir)
	if err != nil {
		t.Fatalf("read protocol run: %v", err)
	}
	if arch.Protocol == nil {
		t.Fatal("expected Protocol document")
	}
	if arch.ServiceName != "orders-api" || arch.RepoPath != "/tmp/orders" {
		t.Fatalf("identity not preserved: service=%q repo=%q", arch.ServiceName, arch.RepoPath)
	}
	if len(arch.Resources) != 1 || len(arch.Exposures) != 2 || len(arch.Dependencies) != 3 || len(arch.Connections) != 1 {
		t.Fatalf("counts resources=%d exp=%d dep=%d conn=%d", len(arch.Resources), len(arch.Exposures), len(arch.Dependencies), len(arch.Connections))
	}
	if arch.Connections[0].FromExposureID != "http.create_order" || arch.Connections[0].ToDependencyID != "dbq.insert_order" {
		t.Fatalf("flow not projected: %+v", arch.Connections[0])
	}
	foundHTTP := false
	foundCache := false
	for _, dep := range arch.Dependencies {
		switch dep.Type {
		case "outbound_http":
			foundHTTP = true
			if dep.Instance != "routing-service" || dep.Details["target_service"] != "routing-service" {
				t.Fatalf("http target not canonicalized: instance=%q details=%+v", dep.Instance, dep.Details)
			}
		case "cache_operation":
			foundCache = true
			if dep.Platform != "redis" || dep.Instance != "redis-main" {
				t.Fatalf("cache not normalized: platform=%q instance=%q", dep.Platform, dep.Instance)
			}
		}
	}
	if !foundHTTP || !foundCache {
		t.Fatalf("expected http/cache dependencies, got %+v", arch.Dependencies)
	}
	foundStream := false
	for _, exp := range arch.Exposures {
		if exp.Type == "queue_consumer" && exp.Platform == "dynamodb_stream" {
			foundStream = true
		}
	}
	if !foundStream {
		t.Fatalf("expected DynamoDB stream queue consumer, got %+v", arch.Exposures)
	}
}

func TestReadDiffMindArtifacts_NonExistentDir(t *testing.T) {
	_, err := ReadDiffMindArtifacts("/nonexistent/path")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}
