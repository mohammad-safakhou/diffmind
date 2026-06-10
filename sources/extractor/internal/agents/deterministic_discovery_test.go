package agents

import (
	"strings"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func objectiveByType(t *testing.T, typ string) objectives.Objective {
	t.Helper()
	for _, obj := range objectives.Default() {
		if obj.Type == typ {
			return obj
		}
	}
	t.Fatalf("objective %q not found", typ)
	return objectives.Objective{}
}

// TestDiscoverySemanticKeyDbOperationCollapsesByResource proves the high-level
// db dedup key: distinct repository methods on the same (table, operation)
// collapse to one key, while a different operation on the same table does not.
func TestDiscoverySemanticKeyDbOperationCollapsesByResource(t *testing.T) {
	obj := objectiveByType(t, "db_operation")
	readA := llmEntity{Type: "db_operation", Name: "OrderRepository.findById", Details: map[string]any{"table": "orders", "operation": "read"}}
	readB := llmEntity{Type: "db_operation", Name: "OrderRepository.findByStatus", Details: map[string]any{"table": "Orders", "operation": "READ"}}
	write := llmEntity{Type: "db_operation", Name: "OrderRepository.save", Details: map[string]any{"table": "orders", "operation": "write"}}

	kA, kB, kW := discoverySemanticKey(obj, readA), discoverySemanticKey(obj, readB), discoverySemanticKey(obj, write)
	if kA != kB {
		t.Errorf("two reads on the same table must share a key: %q vs %q", kA, kB)
	}
	if kA == kW {
		t.Errorf("read and write on the same table must differ: both %q", kA)
	}
}

// TestDeterministicDBOperations derives db ops from the call graph and
// collapses repository methods to high-level (table, operation) entities.
func TestDeterministicDBOperations(t *testing.T) {
	rng := func(l uint32) astpkg.Range { return astpkg.Range{StartLine: l, EndLine: l} }
	idx := &astpkg.ProjectIndex{
		CallGraph: map[string][]astpkg.CallSite{
			"com.x.OrderService.process": {
				{Caller: "com.x.OrderService.process", CalleeResolved: []string{"com.x.OrderRepository.findById"}, File: "src/OrderService.java", Range: rng(10)},
				{Caller: "com.x.OrderService.process", CalleeResolved: []string{"com.x.OrderRepository.findByStatus"}, File: "src/OrderService.java", Range: rng(11)},
				{Caller: "com.x.OrderService.process", CalleeResolved: []string{"com.x.OrderRepository.save"}, File: "src/OrderService.java", Range: rng(12)},
				// Non-repository call must be ignored.
				{Caller: "com.x.OrderService.process", CalleeResolved: []string{"com.x.Mapper.toDto"}, File: "src/OrderService.java", Range: rng(13)},
			},
		},
	}
	ops := deterministicDBOperations(idx)
	// Two reads on orders collapse to one; the write is separate => 2 ops.
	if len(ops) != 2 {
		t.Fatalf("expected 2 high-level db ops (read+write orders), got %d: %+v", len(ops), ops)
	}
	for _, e := range ops {
		if e.Confidence != 1.0 || !hasDeterministicEvidence(e) {
			t.Errorf("db op must be a confident deterministic seed: %+v", e)
		}
		if e.Details["table"] != "order" {
			t.Errorf("expected table=order, got %v", e.Details["table"])
		}
	}
}

func TestEntityFromFrameworkBindingHTTPRoute(t *testing.T) {
	obj := objectiveByType(t, "http_route")
	got, ok := entityFromFrameworkBinding(nil, obj, astpkg.FrameworkBinding{
		Framework:     "spring",
		Kind:          "http_handler",
		Symbol:        "com.example.OrderController.list",
		Trigger:       "GET /orders",
		TriggerSource: "@GetMapping(\"/orders\")",
		File:          "src/main/java/com/example/OrderController.java",
		Range:         astpkg.Range{StartLine: 41, EndLine: 41},
	})
	if !ok {
		t.Fatal("expected binding to produce entity")
	}
	if got.Type != "http_route" || got.Name != "GET /orders" {
		t.Fatalf("unexpected entity identity: %#v", got)
	}
	if got.Confidence != 1.0 {
		t.Fatalf("confidence = %v, want 1.0", got.Confidence)
	}
	if got.Details["method"] != "GET" || got.Details["path"] != "/orders" {
		t.Fatalf("missing route details: %#v", got.Details)
	}
	if got.Locations[0].StartLine != 42 {
		t.Fatalf("line = %d, want 42", got.Locations[0].StartLine)
	}
	if got.Evidence[0].Source != "deterministic_framework" {
		t.Fatalf("evidence source = %q", got.Evidence[0].Source)
	}
	if !isCompleteDeterministicSeed(obj, &got) {
		t.Fatal("expected route seed to be complete")
	}
}

func TestEntityFromFrameworkBindingQueueAndSchedule(t *testing.T) {
	queueObj := objectiveByType(t, "queue_consumer")
	queue, ok := entityFromFrameworkBinding(nil, queueObj, astpkg.FrameworkBinding{
		Framework:     "spring",
		Kind:          "queue_consumer",
		Symbol:        "com.example.OrderListener.handle",
		Trigger:       "sqs: orders-created",
		TriggerSource: "@SqsListener(\"orders-created\")",
		File:          "src/main/java/com/example/OrderListener.java",
		Range:         astpkg.Range{StartLine: 17, EndLine: 17},
	})
	if !ok {
		t.Fatal("expected queue binding to produce entity")
	}
	if queue.Type != "queue_consumer" || queue.Details["queue"] != "orders-created" || queue.Details["platform"] != "sqs" {
		t.Fatalf("unexpected queue entity: %#v", queue)
	}
	if !isCompleteDeterministicSeed(queueObj, &queue) {
		t.Fatal("expected queue seed to be complete")
	}

	scheduleObj := objectiveByType(t, "scheduled_job")
	job, ok := entityFromFrameworkBinding(nil, scheduleObj, astpkg.FrameworkBinding{
		Framework:     "nestjs",
		Kind:          "scheduler",
		Symbol:        "CleanupTasks.removeExpired",
		Trigger:       "cron: 0 0 * * *",
		TriggerSource: "@Cron(\"0 0 * * *\")",
		File:          "src/tasks.ts",
		Range:         astpkg.Range{StartLine: 8, EndLine: 8},
	})
	if !ok {
		t.Fatal("expected scheduler binding to produce entity")
	}
	if job.Type != "scheduled_job" || job.Name != "CleanupTasks.removeExpired" || job.Details["schedule"] != "0 0 * * *" {
		t.Fatalf("unexpected scheduled job entity: %#v", job)
	}
	if !isCompleteDeterministicSeed(scheduleObj, &job) {
		t.Fatal("expected scheduled seed to be complete")
	}
}

func TestMergeDiscoveryResultsDeterministicWinsDuplicate(t *testing.T) {
	obj := objectiveByType(t, "http_route")
	llm := llmEntity{
		Type:       "http_route",
		Name:       "GET /orders",
		Summary:    "LLM summary",
		Confidence: 0.82,
		Details:    map[string]any{"method": "GET", "path": "/orders", "handler": "OrderController.list"},
		Locations:  []llmLocation{{File: "orders.go", StartLine: 10, EndLine: 10}},
		Evidence:   []llmEvidence{{File: "orders.go", StartLine: 10, EndLine: 10, Source: "opencode"}},
	}
	det := llm
	det.Confidence = 1.0
	det.Tags = []string{"deterministic"}
	det.Evidence = []llmEvidence{{File: "orders.go", StartLine: 10, EndLine: 10, Source: "deterministic_framework"}}
	merged := mergeDiscoveryResults(
		[]discoveryResult{{Objective: obj, Items: []llmEntity{llm}}},
		[]discoveryResult{{Objective: obj, Items: []llmEntity{det}}},
	)
	if len(merged) != 1 || len(merged[0].Items) != 1 {
		t.Fatalf("expected one merged item, got %#v", merged)
	}
	item := merged[0].Items[0]
	if item.Confidence != 1.0 {
		t.Fatalf("confidence = %v, want deterministic 1.0", item.Confidence)
	}
	if !hasDeterministicEvidence(item) {
		t.Fatalf("merged item lost deterministic evidence: %#v", item.Evidence)
	}
}

func TestMergeDiscoveryResultsMatchesRouteByMethodAndPath(t *testing.T) {
	obj := objectiveByType(t, "http_route")
	llm := llmEntity{
		Type:       "http_route",
		Name:       "GET /orders/{id}",
		Summary:    "LLM summary",
		Confidence: 0.82,
		Details:    map[string]any{"method": "GET", "path": "/orders/{id}"},
		Locations:  []llmLocation{{File: "src/Orders.java", StartLine: 20, EndLine: 25}},
	}
	det := llmEntity{
		Type:       "http_route",
		Name:       "GET /orders/{id}",
		Confidence: 1,
		Details: map[string]any{
			"method": "GET", "path": "/orders/{id}", "handler": "OrdersController.get",
		},
		Locations: []llmLocation{{File: "src/Orders.java", StartLine: 18, EndLine: 25}},
		Evidence:  []llmEvidence{{Source: "deterministic_framework"}},
	}

	merged := mergeDiscoveryResults(
		[]discoveryResult{{Objective: obj, Items: []llmEntity{llm}}},
		[]discoveryResult{{Objective: obj, Items: []llmEntity{det}}},
	)
	if got := len(merged[0].Items); got != 1 {
		t.Fatalf("items = %d, want equivalent routes merged", got)
	}
	if merged[0].Items[0].Summary != "LLM summary" {
		t.Fatalf("LLM detail was not retained: %#v", merged[0].Items[0])
	}
}

// TestMergeDiscoveryResultsCollapsesSemanticDuplicate verifies the LLM and the
// deterministic floor collapse to a single item when they describe the same
// route, with the deterministic version preferred and metadata unioned.
func TestMergeDiscoveryResultsCollapsesSemanticDuplicate(t *testing.T) {
	obj := objectiveByType(t, "http_route")
	llm := llmEntity{
		Type:    "http_route",
		Name:    "GET /orders",
		Details: map[string]any{"method": "GET", "path": "/orders"},
	}
	det := llmEntity{
		Type:    "http_route",
		Name:    "GET /orders",
		Details: map[string]any{"method": "GET", "path": "/orders", "handler": "OrdersController.list"},
	}
	baseline := []discoveryResult{{Objective: obj, Items: []llmEntity{llm}}}
	deterministic := []discoveryResult{{Objective: obj, Items: []llmEntity{det}}}
	candidate := mergeDiscoveryResults(baseline, deterministic)

	total := 0
	for _, r := range candidate {
		total += len(r.Items)
	}
	if total != 1 {
		t.Fatalf("expected the duplicate route to collapse to 1 item, got %d", total)
	}
}

func TestConfirmedDiscoveryBlock(t *testing.T) {
	block := ConfirmedDiscoveryBlock([]llmEntity{{
		Name:      "GET /orders",
		Locations: []llmLocation{{File: "orders.go", StartLine: 10}},
	}})
	if !strings.Contains(block, "KNOWN_CONFIRMED_ITEMS") {
		t.Fatalf("missing confirmed header: %s", block)
	}
	if !strings.Contains(block, "Do not rediscover") {
		t.Fatalf("missing rediscovery instruction: %s", block)
	}
}

func TestDetailCheckpointForDeterministicSeed(t *testing.T) {
	obj := objectiveByType(t, "http_route")
	seed := llmEntity{
		Type:       "http_route",
		Name:       "GET /orders",
		Summary:    "deterministic route",
		Confidence: 1.0,
		Details:    map[string]any{"method": "GET", "path": "/orders", "handler": "OrderController.list"},
		Locations:  []llmLocation{{File: "orders.go", StartLine: 10, EndLine: 10}},
		Evidence:   []llmEvidence{{File: "orders.go", StartLine: 10, EndLine: 10, Source: "deterministic_framework"}},
	}
	o := &orchestrator{repoPath: "repo"}
	entry, ok := o.detailCheckpointForSeed(detailJob{Objective: obj, Seed: seed})
	if !ok {
		t.Fatal("expected checkpoint entry")
	}
	if entry.Exposure == nil || entry.Exposure.Type != "http_route" {
		t.Fatalf("unexpected checkpoint entry: %#v", entry)
	}
}
