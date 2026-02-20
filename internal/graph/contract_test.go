package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"diffmind/internal/graphschema"
)

func TestGraphContractPassesOnMatchingSurface(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	contractPath := filepath.Join(tmp, "contract.json")
	outPath := filepath.Join(tmp, "contract_report.json")

	graph := graphschema.Graph{
		GraphID: "g1",
		Nodes: []graphschema.Node{
			{ID: "svc:a", Type: "service", ServiceID: "service-a", Label: "service-a", Confidence: 1},
			{ID: "svc:b", Type: "service", ServiceID: "service-b", Label: "service-b", Confidence: 1},
			{ID: "ep:a:get:/health", Type: "endpoint", Label: "GET /health", Attributes: map[string]any{"method": "GET", "path": "/health"}, Confidence: 1},
			{ID: "ep:a:sched", Type: "endpoint", Label: "cron:*/5 * * * *", Attributes: map[string]any{"method": "SCHEDULE", "path": "cron:*/5 * * * *"}, Confidence: 1},
			{ID: "queue:orders", Type: "queue", Label: "orders.events", Confidence: 1},
			{ID: "db:payments", Type: "database", Label: "payments", Confidence: 1},
		},
		Edges: []graphschema.Edge{
			{ID: "e1", Type: "service_exposes_endpoint", SourceID: "svc:a", TargetID: "ep:a:get:/health", Confidence: 1},
			{ID: "e2", Type: "service_exposes_endpoint", SourceID: "svc:a", TargetID: "ep:a:sched", Confidence: 1},
			{ID: "e3", Type: "service_publishes_queue", SourceID: "svc:a", TargetID: "queue:orders", Confidence: 1},
			{ID: "e4", Type: "queue_delivers_to_service", SourceID: "queue:orders", TargetID: "svc:a", Confidence: 1},
			{ID: "e5", Type: "service_reads_db", SourceID: "svc:a", TargetID: "db:payments", Confidence: 1},
			{ID: "e6", Type: "service_calls_service", SourceID: "svc:a", TargetID: "svc:b", Attributes: map[string]any{"target_service_id": "service-b"}, Confidence: 1},
			{ID: "e7", Type: "service_calls_endpoint", SourceID: "svc:a", TargetID: "ep:a:get:/health", Confidence: 1},
		},
	}
	writeAnyJSON(t, graphPath, graph)

	contract := map[string]any{
		"service_id": "service-a",
		"expected": map[string]any{
			"endpoints":       []string{"GET /health"},
			"queue_publishes": []string{"orders.events"},
			"queue_consumes":  []string{"orders.events"},
			"schedulers":      []string{"cron:*/5 * * * *"},
			"dependencies":    []string{"queue:orders.events", "db:payments", "service:service-b", "http:GET /health"},
		},
	}
	writeAnyJSON(t, contractPath, contract)

	res, err := Contract(context.Background(), ContractRequest{
		GraphPath:    graphPath,
		ContractPath: contractPath,
		OutPath:      outPath,
		FailOnGate:   true,
	})
	if err != nil {
		t.Fatalf("contract failed: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected contract to pass")
	}
}

func TestGraphContractFailsOnRecallGate(t *testing.T) {
	tmp := t.TempDir()
	graphPath := filepath.Join(tmp, "graph.json")
	contractPath := filepath.Join(tmp, "contract.json")
	outPath := filepath.Join(tmp, "contract_report.json")

	graph := graphschema.Graph{
		GraphID: "g1",
		Nodes: []graphschema.Node{
			{ID: "svc:a", Type: "service", ServiceID: "service-a", Label: "service-a", Confidence: 1},
			{ID: "ep:a:get:/health", Type: "endpoint", Label: "GET /health", Attributes: map[string]any{"method": "GET", "path": "/health"}, Confidence: 1},
		},
		Edges: []graphschema.Edge{
			{ID: "e1", Type: "service_exposes_endpoint", SourceID: "svc:a", TargetID: "ep:a:get:/health", Confidence: 1},
		},
	}
	writeAnyJSON(t, graphPath, graph)

	contract := map[string]any{
		"expected": map[string]any{
			"endpoints":       []string{"GET /health", "POST /orders"},
			"dependencies":    []string{},
			"queue_publishes": []string{},
			"queue_consumes":  []string{},
			"schedulers":      []string{},
		},
		"thresholds": map[string]any{
			"endpoints_recall_min": 0.95,
		},
	}
	writeAnyJSON(t, contractPath, contract)

	_, err := Contract(context.Background(), ContractRequest{
		GraphPath:    graphPath,
		ContractPath: contractPath,
		OutPath:      outPath,
		FailOnGate:   true,
	})
	if err == nil {
		t.Fatalf("expected contract gate failure")
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if passed, _ := report["passed"].(bool); passed {
		t.Fatalf("expected report passed=false")
	}
}

func writeAnyJSON(t *testing.T, path string, payload any) {
	t.Helper()
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
