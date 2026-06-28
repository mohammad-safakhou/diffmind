package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBuildArchitectureGraphUsesDiffMind protocolTargetsAndResourceKinds(t *testing.T) {
	root := t.TempDir()
	atsRun := filepath.Join(root, "ats")
	boostRun := filepath.Join(root, "boost")
	writeDiffMind protocolRun(t, atsRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "routing-service", "name": "routing-service"},
  "objects": {},
  "flows": [],
  "observations": [],
  "evidence": []
}`)
	writeDiffMind protocolRun(t, boostRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "pricing-service", "name": "pricing-service"},
  "objects": {
    "http_calls": [
      {
        "id": "httpcall.score",
        "kind": "http_call",
        "name": "Get ATS score",
        "method": "GET",
        "url_template": "http://routing-service/score/{storeCode}",
        "target": {"type": "unresolved", "ref": "service.routing_service", "unresolved": true},
        "status": "confirmed",
        "confidence": "high",
        "origin": "deterministic"
      },
      {
        "id": "httpcall.operation_name",
        "kind": "http_call",
        "name": "GET /campaigns/{id}",
        "method": "GET",
        "target": {"type": "unresolved", "ref": "service.get_campaigns_id", "unresolved": true},
        "status": "confirmed",
        "confidence": "medium",
        "origin": "deterministic"
      }
    ],
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
    }],
    "db_queries": [{
      "id": "dbq.read_traffic_info",
      "kind": "db_query",
      "name": "Read traffic-info",
      "engine": "dynamodb",
      "operation": "read",
      "access": "read",
      "target": {"database": "dynamodb", "tables": ["traffic-info"]},
      "metadata": {"details": {"database_type": "dynamodb", "table": "traffic-info"}},
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
    }]
  },
  "flows": [],
  "observations": [],
  "evidence": []
}`)

	graph := buildArchitectureGraph("run-1", map[string]string{
		"routing-service": atsRun,
		"pricing-service":   boostRun,
	})

	foundATS := false
	for _, edge := range graph.Edges {
		if edge.Type == "http" && edge.From == "pricing-service" && edge.To == "routing-service" {
			foundATS = true
		}
	}
	if !foundATS {
		t.Fatalf("expected canonical service HTTP edge, edges=%+v", graph.Edges)
	}
	for _, n := range graph.ExternalNodes {
		if strings.HasPrefix(n.Name, "GET /") || looksLikeHTTPMethodSlug(n.Name) {
			t.Fatalf("operation label became external service node: %+v", n)
		}
	}
	foundRedis := false
	for _, n := range graph.DatabaseNodes {
		if n.Name == "redis-main" && n.Kind == "redis" {
			foundRedis = true
		}
	}
	if !foundRedis {
		t.Fatalf("expected redis cache resource node, got %+v", graph.DatabaseNodes)
	}
	foundDynamoTable := false
	for _, n := range graph.DatabaseNodes {
		if n.Name == "traffic-info" && n.Kind == "dynamodb" {
			foundDynamoTable = true
		}
		if n.Name == "dynamodb" && n.Kind == "dynamodb" {
			t.Fatalf("generic DynamoDB platform became the resource name: %+v", n)
		}
	}
	if !foundDynamoTable {
		t.Fatalf("expected DynamoDB table resource node, got %+v", graph.DatabaseNodes)
	}
	foundStream := false
	for _, n := range graph.QueueNodes {
		if n.Kind == "dynamodb_stream" {
			foundStream = true
		}
	}
	if !foundStream {
		t.Fatalf("expected DynamoDB stream queue node, got %+v", graph.QueueNodes)
	}
}

func TestArchitectureGraphGroupsRelationalTablesUnderDatasource(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "game-service")
	writeDiffMind protocolRun(t, runDir, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "game-service", "name": "game-service"},
  "objects": {
    "db_queries": [
      {
        "id": "dbq.read_payments",
        "kind": "db_query",
        "name": "Read payments",
        "engine": "postgresql",
        "operation": "read",
        "access": "read",
        "target": {"tables": ["payments"]},
        "status": "confirmed",
        "confidence": "high",
        "origin": "deterministic"
      },
      {
        "id": "dbq.write_kycs",
        "kind": "db_query",
        "name": "Write kycs",
        "engine": "postgresql",
        "operation": "write",
        "access": "write",
        "target": {"tables": ["kycs"]},
        "status": "confirmed",
        "confidence": "high",
        "origin": "deterministic"
      }
    ]
  },
  "flows": [],
  "observations": [],
  "evidence": []
}`)

	graph := buildArchitectureGraph("run-1", map[string]string{"game-service": runDir})
	if len(graph.DatabaseNodes) != 1 {
		t.Fatalf("expected one datasource node, got %+v", graph.DatabaseNodes)
	}
	db := graph.DatabaseNodes[0]
	if db.Name != "game-service-db" || db.Kind != "postgresql" {
		t.Fatalf("expected service datasource node, got %+v", db)
	}
	if db.OperationCount != 2 || len(db.Tables) != 2 {
		t.Fatalf("expected two table operations inside datasource, got %+v", db)
	}
	for _, table := range db.Tables {
		if table.Name == "game-service-db" {
			t.Fatalf("table identity collapsed to datasource name: %+v", db.Tables)
		}
	}
	var dbEdges []*GraphEdge
	for _, edge := range graph.Edges {
		if edge.Type == "database" {
			dbEdges = append(dbEdges, edge)
		}
	}
	if len(dbEdges) != 1 || len(dbEdges[0].Details) != 2 {
		t.Fatalf("expected one aggregated database edge with both operations, got %+v", dbEdges)
	}
}

func TestBuildArchitectureGraphUsesConfiguredServiceAliases(t *testing.T) {
	root := t.TempDir()
	atsRepo := filepath.Join(root, "routing-service-repo")
	if err := os.MkdirAll(atsRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(atsRepo, "diffmind-configuration.yaml"), []byte(`
service:
  id: routing-service
  name: routing-service
aliases:
  services:
    routing-service:
      - ats-api
      - routing_service
`), 0o644); err != nil {
		t.Fatal(err)
	}
	atsRun := filepath.Join(root, "ats-run")
	callerRun := filepath.Join(root, "caller-run")
	writeDiffMind protocolRunWithRepoPath(t, atsRun, atsRepo, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "routing-service", "name": "routing-service"},
  "objects": {},
  "flows": [],
  "observations": [],
  "evidence": []
}`)
	writeDiffMind protocolRun(t, callerRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "caller", "name": "caller"},
  "objects": {
    "http_calls": [{
      "id": "httpcall.ats",
      "kind": "http_call",
      "name": "Call ATS",
      "method": "GET",
      "target": {"type": "service", "ref": "service.ats-api", "unresolved": false},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }]
  },
  "flows": [],
  "observations": [],
  "evidence": []
}`)

	graph := buildArchitectureGraph("run-1", map[string]string{
		"routing-service": atsRun,
		"caller":                    callerRun,
	})
	for _, edge := range graph.Edges {
		if edge.Type == "http" && edge.From == "caller" && edge.To == "routing-service" {
			return
		}
	}
	t.Fatalf("expected alias target to join known service, edges=%+v external=%+v", graph.Edges, graph.ExternalNodes)
}

func TestArchitectureGraphLayoutIsStableAndRanksSharedResourceFlow(t *testing.T) {
	root := t.TempDir()
	boostRun := filepath.Join(root, "boost")
	syncRun := filepath.Join(root, "sync")
	shapingRun := filepath.Join(root, "shaping")
	writeDiffMind protocolRun(t, boostRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "pricing-service", "name": "pricing-service"},
  "objects": {
    "db_queries": [{
      "id": "dbq.write_traffic_info",
      "kind": "db_query",
      "name": "Write traffic-info",
      "engine": "dynamodb",
      "operation": "write",
      "access": "write",
      "target": {"database": "dynamodb", "tables": ["traffic-info"]},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }]
  },
  "flows": [],
  "observations": [],
  "evidence": []
}`)
	writeDiffMind protocolRun(t, syncRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "sync-service", "name": "sync-service"},
  "objects": {
    "db_queries": [{
      "id": "dbq.read_traffic_info",
      "kind": "db_query",
      "name": "Read traffic-info",
      "engine": "dynamodb",
      "operation": "read",
      "access": "read",
      "target": {"database": "dynamodb", "tables": ["traffic-info"]},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }],
    "cache_operations": [{
      "id": "cache.write_redis",
      "kind": "cache_operation",
      "name": "Write Redis",
      "platform": "redis",
      "operation": "write",
      "target": {"cache": "redis"},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }]
  },
  "flows": [],
  "observations": [],
  "evidence": []
}`)
	writeDiffMind protocolRun(t, shapingRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "gateway-service", "name": "gateway-service"},
  "objects": {
    "cache_operations": [{
      "id": "cache.read_redis",
      "kind": "cache_operation",
      "name": "Read Redis",
      "platform": "redis",
      "operation": "read",
      "target": {"cache": "redis"},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }]
  },
  "flows": [],
  "observations": [],
  "evidence": []
}`)

	runs := map[string]string{
		"pricing-service":      boostRun,
		"sync-service": syncRun,
		"gateway-service":          shapingRun,
	}
	first := buildArchitectureGraph("run-1", runs)
	second := buildArchitectureGraph("run-1", runs)
	if first.Layout == nil || second.Layout == nil {
		t.Fatal("expected persisted layout")
	}
	if len(first.Layout.Nodes) != len(second.Layout.Nodes) {
		t.Fatalf("layout node count changed: %d/%d", len(first.Layout.Nodes), len(second.Layout.Nodes))
	}
	for i := range first.Layout.Nodes {
		if first.Layout.Nodes[i] != second.Layout.Nodes[i] {
			t.Fatalf("layout is not stable at %d: %+v != %+v", i, first.Layout.Nodes[i], second.Layout.Nodes[i])
		}
	}

	ranks := map[string]int{}
	for _, n := range first.Layout.Nodes {
		ranks[n.ID] = n.Rank
	}
	assertBefore := func(a, b string) {
		t.Helper()
		if ranks[a] >= ranks[b] {
			t.Fatalf("expected %s before %s, ranks=%v", a, b, ranks)
		}
	}
	assertBefore("pricing-service", "db:dynamodb_traffic-info")
	assertBefore("db:dynamodb_traffic-info", "sync-service")
	assertBefore("sync-service", "db:redis_redis")
	assertBefore("db:redis_redis", "gateway-service")
}

func writeDiffMind protocolRun(t *testing.T, runDir, body string) {
	writeDiffMind protocolRunWithRepoPath(t, runDir, "", body)
}

func writeDiffMind protocolRunWithRepoPath(t *testing.T, runDir, repoPath, body string) {
	t.Helper()
	contextDir := filepath.Join(runDir, ".diffmind", "context")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"run_id":"run-1","schema_version":"diffmind.service.v1"}`
	if repoPath != "" {
		manifest = `{"run_id":"run-1","schema_version":"diffmind.service.v1","repo_path":` + strconv.Quote(repoPath) + `}`
	}
	if err := os.WriteFile(filepath.Join(runDir, "run_manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "service.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
