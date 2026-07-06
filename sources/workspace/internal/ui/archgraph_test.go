package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	ag "github.com/mohammad-safakhou/diffmind/internal/archgraph"
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
      },
      {
        "id": "httpcall.path_only_any",
        "kind": "http_call",
        "name": "ANY /contents/{id}",
        "method": "ANY",
        "url_template": "ANY /contents/{id}",
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
		if n.Name == "unresolved-service-target" || n.Name == "any-contents-id" {
			t.Fatalf("placeholder target became external service node: %+v", n)
		}
	}
	foundRedis := false
	for _, n := range graph.ResourceNodes {
		if n.Name == "redis-main" && n.Kind == "cache" && n.Platform == "redis" {
			foundRedis = true
		}
	}
	if !foundRedis {
		t.Fatalf("expected redis cache resource node, got %+v", graph.ResourceNodes)
	}
	for _, n := range graph.DatabaseNodes {
		if n.Kind == "redis" {
			t.Fatalf("redis cache leaked into database nodes: %+v", n)
		}
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

func TestArchitectureGraphBuildsLazyViewsAndTraceContinuation(t *testing.T) {
	graph := &ArchGraph{
		RunID: "run-views",
		Services: []*ServiceNode{
			{
				Name:            "entry-api",
				Team:            "mantra",
				EntrypointCount: 1,
				DownstreamCount: 2,
				TraceCount:      1,
				HTTPRoutes:      []EntitySummary{{ID: "http.entry", Kind: "http_endpoint", Name: "GET /entry"}},
				Dependencies:    []EntitySummary{{ID: "httpcall.worker", Kind: "http_call", Name: "Call worker"}},
				Connections: []ConnectionSummary{{
					FromID:       "http.entry",
					FromName:     "GET /entry",
					FromType:     "http_endpoint",
					ToID:         "httpcall.worker",
					ToName:       "Call worker",
					ToType:       "http_call",
					FlowID:       "flow.entry",
					EntrypointID: "http.entry",
					Nodes:        []any{map[string]any{"id": "n1", "ref": "http.entry"}, map[string]any{"id": "n2", "ref": "httpcall.worker"}},
					Edges:        []any{map[string]any{"from": "n1", "to": "n2"}},
				}},
			},
			{
				Name:            "worker-api",
				Team:            "dynamite",
				EntrypointCount: 1,
				HTTPRoutes:      []EntitySummary{{ID: "http.worker", Kind: "http_endpoint", Name: "GET /worker"}},
			},
		},
		ResourceNodes: []*ResourceNode{{
			ID:             "redis_entry",
			GraphID:        "resource:redis_entry",
			Name:           "entry-cache",
			Kind:           "cache",
			Platform:       "redis",
			OperationCount: 1,
		}},
		Edges: []*GraphEdge{
			{From: "entry-api", To: "worker-api", Type: "http", Label: "HTTP", Details: []EntitySummary{{ID: "httpcall.worker", Kind: "http_call", Name: "Call worker"}}},
			{From: "entry-api", To: "resource:redis_entry", Type: "cache", Label: "read", Details: []EntitySummary{{ID: "cache.entry", Kind: "cache_operation", Name: "Read entry cache"}}},
		},
	}

	teamView, ok := ag.BuildTeamView(graph, "mantra", "connected")
	if !ok {
		t.Fatal("expected mantra team view")
	}
	if teamView.Summary.ServiceCount != 2 || teamView.Summary.ResourceCount != 1 || teamView.Summary.EdgeCount != 2 {
		t.Fatalf("unexpected team view summary: %+v", teamView.Summary)
	}

	serviceView, ok := ag.BuildServiceView(graph, "entry-api")
	if !ok {
		t.Fatal("expected service view")
	}
	if len(serviceView.OutboundEdges) != 2 || len(serviceView.NeighborServices) != 1 || len(serviceView.ResourceNodes) != 1 {
		t.Fatalf("unexpected service view: %+v", serviceView)
	}
	if len(serviceView.AvailableTraceIDs) != 1 || serviceView.AvailableTraceIDs[0] != "flow.entry" {
		t.Fatalf("expected trace ids, got %+v", serviceView.AvailableTraceIDs)
	}

	resourceView, ok := ag.BuildResourceView(graph, "resource:redis_entry")
	if !ok {
		t.Fatal("expected resource view")
	}
	if resourceView.Resource.Kind != "cache" || len(resourceView.Services) != 1 || resourceView.Services[0].Name != "entry-api" {
		t.Fatalf("unexpected resource view: %+v", resourceView)
	}

	traceView, ok := ag.BuildTraceView(graph, "entry-api", "http.entry")
	if !ok {
		t.Fatal("expected trace view")
	}
	if traceView.Status != "complete" || len(traceView.Segments) != 1 || len(traceView.Continuations) != 1 {
		t.Fatalf("unexpected trace view: %+v", traceView)
	}
	if traceView.Continuations[0].ToService != "worker-api" || traceView.Continuations[0].Status != "matched_known_service" {
		t.Fatalf("unexpected continuation: %+v", traceView.Continuations)
	}
	if !containsString(traceView.Quality, "no field-level data dependencies extracted yet") {
		t.Fatalf("expected data dependency quality warning, got %+v", traceView.Quality)
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

func TestBuildArchitectureGraphUsesExampleServiceNameConventions(t *testing.T) {
	root := t.TempDir()
	contentStoreRun := filepath.Join(root, "content-store")
	mediaStoreRun := filepath.Join(root, "media-store")
	catalogueRun := filepath.Join(root, "catalogue")
	callerRun := filepath.Join(root, "caller")
	writeDiffMind protocolRun(t, contentStoreRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "cdp.content-store", "name": "cdp.content-store"},
  "objects": {},
  "flows": [],
  "observations": [],
  "evidence": []
}`)
	writeDiffMind protocolRun(t, mediaStoreRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "cdp.media-store", "name": "cdp.media-store"},
  "objects": {},
  "flows": [],
  "observations": [],
  "evidence": []
}`)
	writeDiffMind protocolRun(t, catalogueRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "checkout-service", "name": "checkout-service"},
  "objects": {},
  "flows": [],
  "observations": [],
  "evidence": []
}`)
	writeDiffMind protocolRun(t, callerRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "caller", "name": "caller"},
  "objects": {
    "http_calls": [
      {
        "id": "httpcall.content",
        "kind": "http_call",
        "name": "Call content store",
        "method": "GET",
        "target": {"type": "service", "ref": "service.content-store", "unresolved": false},
        "status": "confirmed",
        "confidence": "high",
        "origin": "deterministic"
      },
      {
        "id": "httpcall.media",
        "kind": "http_call",
        "name": "Call media store",
        "method": "GET",
        "target": {"type": "service", "ref": "service.cdp-media-store", "unresolved": false},
        "status": "confirmed",
        "confidence": "high",
        "origin": "deterministic"
      },
      {
        "id": "httpcall.content_store_api",
        "kind": "http_call",
        "name": "Call content store API",
        "method": "GET",
        "target": {"type": "service", "ref": "service.cdp-content-store-api", "unresolved": false},
        "status": "confirmed",
        "confidence": "high",
        "origin": "deterministic"
      },
      {
        "id": "httpcall.catalog",
        "kind": "http_call",
        "name": "Call catalog",
        "method": "GET",
        "target": {"type": "service", "ref": "service.catalog-management-api", "unresolved": false},
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

	graph := buildArchitectureGraph("run-1", map[string]string{
		"cdp.content-store":         contentStoreRun,
		"cdp.media-store":           mediaStoreRun,
		"checkout-service": catalogueRun,
		"caller":                    callerRun,
	})
	expected := map[string]bool{
		"cdp.content-store":         false,
		"cdp.media-store":           false,
		"checkout-service": false,
	}
	for _, edge := range graph.Edges {
		if edge.Type == "http" && edge.From == "caller" {
			if _, ok := expected[edge.To]; ok {
				expected[edge.To] = true
			}
		}
	}
	for target, found := range expected {
		if !found {
			t.Fatalf("expected caller to resolve %s, edges=%+v external=%+v", target, graph.Edges, graph.ExternalNodes)
		}
	}
}

func TestBuildArchitectureGraphRendersDiffMind protocolRPCCalls(t *testing.T) {
	root := t.TempDir()
	callerRun := filepath.Join(root, "game-service")
	metadataRun := filepath.Join(root, "metadata")
	writeDiffMind protocolRun(t, metadataRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "metadata", "name": "metadata"},
  "objects": {
    "rpc_endpoints": [
      {
        "id": "rpc.indicator_calculate",
        "kind": "rpc_endpoint",
        "name": "IndicatorService/Calculate",
        "protocol": "grpc",
        "service": "IndicatorService",
        "method": "Calculate",
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
	writeDiffMind protocolRun(t, callerRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "game-service", "name": "game-service"},
  "objects": {
    "rpc_calls": [
      {
        "id": "rpccall.metadata_get",
        "kind": "rpc_call",
        "name": "metadata.Get",
        "protocol": "grpc",
        "service": "metadata",
        "method": "Get",
        "target": {"type": "service", "ref": "service.metadata", "unresolved": false},
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

	graph := buildArchitectureGraph("run-1", map[string]string{
		"game-service": callerRun,
		"metadata":         metadataRun,
	})
	foundEdge := false
	foundDependency := false
	foundEndpoint := false
	for _, edge := range graph.Edges {
		if edge.Type == "rpc" && edge.From == "game-service" && edge.To == "metadata" && edge.Label == "gRPC" {
			foundEdge = true
			if len(edge.Details) != 1 || edge.Details[0].Name != "metadata.Get" {
				t.Fatalf("expected RPC edge details to carry rpc call, got %+v", edge)
			}
		}
	}
	for _, svc := range graph.Services {
		switch svc.Name {
		case "game-service":
			for _, dep := range svc.Dependencies {
				if dep.Name == "metadata.Get" {
					foundDependency = true
				}
			}
		case "metadata":
			for _, endpoint := range svc.RPCEndpoints {
				if endpoint.Name == "IndicatorService/Calculate" {
					foundEndpoint = true
				}
			}
		}
	}
	if !foundEdge {
		t.Fatalf("expected gRPC edge to known service, edges=%+v external=%+v", graph.Edges, graph.ExternalNodes)
	}
	if !foundDependency {
		t.Fatalf("expected RPC call in service dependency details, services=%+v", graph.Services)
	}
	if !foundEndpoint {
		t.Fatalf("expected RPC endpoint in service entrypoint details, services=%+v", graph.Services)
	}
}

func TestBuildArchitectureGraphRendersWorkflowOrchestration(t *testing.T) {
	root := t.TempDir()
	payloadBuilderRun := filepath.Join(root, "payload-builder")
	camundaRun := filepath.Join(root, "camunda")
	writeDiffMind protocolRun(t, camundaRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "cdp.stories.camunda", "name": "cdp.stories.camunda"},
  "objects": {},
  "flows": [],
  "observations": [],
  "evidence": []
}`)
	writeDiffMind protocolRun(t, payloadBuilderRun, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "cdp.stories.payload-builder", "name": "cdp.stories.payload-builder"},
  "objects": {
    "config_reads": [{
      "id": "workflow.camunda.stories_import_build_initial_payload",
      "kind": "workflow_orchestration",
      "name": "Camunda external task stories::import::build_initial_payload",
      "key": "stories::import::build_initial_payload",
      "value": "https://cdp-stories-camunda/rest",
      "source": "application.yml:external-task.url",
      "metadata": {
        "legacy_type": "workflow_orchestration",
        "details": {
          "orchestrator": "camunda",
          "target_service": "cdp-stories-camunda",
          "url_template": "https://cdp-stories-camunda/rest",
          "topic": "stories::import::build_initial_payload",
          "invocation_mode": "external_task_worker"
        }
      },
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
		"cdp.stories.payload-builder": payloadBuilderRun,
		"cdp.stories.camunda":         camundaRun,
	})
	foundEdge := false
	foundDependency := false
	for _, edge := range graph.Edges {
		if edge.Type == "workflow" && edge.From == "cdp.stories.payload-builder" && edge.To == "cdp.stories.camunda" {
			foundEdge = true
			if edge.Label != "CAMUNDA" || len(edge.Details) != 1 {
				t.Fatalf("expected Camunda workflow edge details, got %+v", edge)
			}
		}
	}
	for _, svc := range graph.Services {
		if svc.Name != "cdp.stories.payload-builder" {
			continue
		}
		for _, dep := range svc.Dependencies {
			if dep.Kind == "workflow_orchestration" && dep.Details["topic"] == "stories::import::build_initial_payload" {
				foundDependency = true
			}
		}
	}
	if !foundEdge {
		t.Fatalf("expected workflow edge to known Camunda service, edges=%+v external=%+v", graph.Edges, graph.ExternalNodes)
	}
	if !foundDependency {
		t.Fatalf("expected workflow dependency in service details, services=%+v", graph.Services)
	}
}

func TestArchitectureGraphCarriesDiffMind protocolFlowDetails(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "orders")
	writeDiffMind protocolRun(t, runDir, `{
  "schema": "diffmind.service.v1",
  "service": {"id": "orders-api", "name": "orders-api"},
  "objects": {
    "http_endpoints": [{
      "id": "http.create_order",
      "kind": "http_endpoint",
      "name": "POST /orders",
      "method": "POST",
      "path": "/orders",
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }],
    "db_queries": [{
      "id": "dbq.insert_order",
      "kind": "db_query",
      "name": "Insert order",
      "engine": "postgresql",
      "operation": "create",
      "access": "write",
      "target": {"database": "orders", "tables": ["orders"]},
      "status": "confirmed",
      "confidence": "high",
      "origin": "deterministic"
    }]
  },
  "flows": [{
    "id": "flow.create_order",
    "kind": "request_flow",
    "entrypoint": "http.create_order",
    "nodes": [
      {"id": "n1", "ref": "http.create_order", "role": "entrypoint"},
      {"id": "n2", "ref": "dbq.insert_order", "role": "action"}
    ],
    "edges": [{
      "from": "n1",
      "to": "n2",
      "reachability": "conditional",
      "condition": {"summary": "validation succeeds", "confidence": "high"}
    }],
    "data_dependencies": [{
      "id": "data.order_id",
      "from": {"object_ref": "http.create_order", "expression": "request.body.id"},
      "to": {"object_ref": "dbq.insert_order", "expression": "orders.id"},
      "kind": "value_flow",
      "confidence": "high"
    }],
    "status": "confirmed",
    "confidence": "high",
    "origin": "deterministic"
  }],
  "observations": [],
  "evidence": []
}`)

	graph := buildArchitectureGraph("run-1", map[string]string{"orders-api": runDir})
	if len(graph.Services) != 1 {
		t.Fatalf("expected one service, got %+v", graph.Services)
	}
	conns := graph.Services[0].Connections
	if len(conns) != 1 {
		t.Fatalf("expected one connection, got %+v", conns)
	}
	if conns[0].Kind != "request_flow" || conns[0].DataDependencies == nil || conns[0].Edges == nil {
		t.Fatalf("expected DiffMind protocol flow details in connection summary, got %+v", conns[0])
	}
	if conns[0].FromID != "http.create_order" || conns[0].ToID != "dbq.insert_order" || conns[0].FlowID != "flow.create_order" || conns[0].EntrypointID != "http.create_order" {
		t.Fatalf("expected stable flow/object ids in connection summary, got %+v", conns[0])
	}
	if got := graph.Services[0].HTTPRoutes[0]; got.ID != "http.create_order" || got.Kind != "http_endpoint" {
		t.Fatalf("expected entity summary to expose DiffMind protocol id/kind, got %+v", got)
	}
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
	assertBefore("sync-service", "resource:redis_redis")
	assertBefore("resource:redis_redis", "gateway-service")
}

func writeDiffMind protocolRun(t *testing.T, runDir, body string) {
	writeDiffMind protocolRunWithRepoPath(t, runDir, "", body)
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
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
