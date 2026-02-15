package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func BenchmarkBuildGraphMediumFixture(b *testing.B) {
	tmp := b.TempDir()
	bundlePath := filepath.Join(tmp, "bundle.json")
	analyzerPath := filepath.Join(tmp, "analyzer.json")

	entities := make([]map[string]any, 0, 1200)
	for i := 0; i < 300; i++ {
		entities = append(entities, map[string]any{"id": "ru-" + itoa(i), "type": "RuntimeUnit", "natural_key": "go|main|cmd/" + itoa(i), "attributes": map[string]any{"language": "go", "kind": "main", "file": "cmd/" + itoa(i) + "/main.go"}, "evidence_ids": []string{"ev-ru-" + itoa(i)}, "fact_ids": []string{"f-ru-" + itoa(i)}, "confidence": 0.9})
		entities = append(entities, map[string]any{"id": "ep-" + itoa(i), "type": "Endpoint", "natural_key": "inbound|GET|/v1/" + itoa(i), "attributes": map[string]any{"direction": "inbound", "method": "GET", "path": "/v1/" + itoa(i), "framework": "go-router"}, "evidence_ids": []string{"ev-ep-" + itoa(i)}, "fact_ids": []string{"f-ep-" + itoa(i)}, "confidence": 0.9})
		entities = append(entities, map[string]any{"id": "ec-http-" + itoa(i), "type": "ExternalCall", "natural_key": "http|GET|http://service-b.local/v1/" + itoa(i), "attributes": map[string]any{"protocol": "http", "method": "GET", "target": "http://service-b.local/v1/" + itoa(i), "library": "go-net-http"}, "evidence_ids": []string{"ev-http-" + itoa(i)}, "fact_ids": []string{"f-http-" + itoa(i)}, "confidence": 0.9})
		entities = append(entities, map[string]any{"id": "ec-queue-" + itoa(i), "type": "ExternalCall", "natural_key": "queue|PUBLISH|events." + itoa(i), "attributes": map[string]any{"protocol": "queue", "method": "PUBLISH", "target": "events." + itoa(i), "library": "kafka-go"}, "evidence_ids": []string{"ev-queue-" + itoa(i)}, "fact_ids": []string{"f-queue-" + itoa(i)}, "confidence": 0.9})
	}

	writeBenchJSON(b, bundlePath, map[string]any{"snapshot_id": "s-bench", "entities": entities})
	writeBenchJSON(b, analyzerPath, map[string]any{"facts": []any{}, "evidence": []any{}, "generated": "2026-01-01T00:00:00Z"})

	services := []serviceSpec{
		{ID: "service-a", Name: "Service A", BundlePath: bundlePath, AnalyzerBundle: analyzerPath, BaseURLs: []string{"http://service-a.local"}},
		{ID: "service-b", Name: "Service B", BundlePath: bundlePath, AnalyzerBundle: analyzerPath, BaseURLs: []string{"http://service-b.local"}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := buildGraph(services, "multi"); err != nil {
			b.Fatalf("build graph: %v", err)
		}
	}
}

func writeBenchJSON(b *testing.B, path string, payload map[string]any) {
	b.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		b.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		b.Fatalf("write json: %v", err)
	}
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
