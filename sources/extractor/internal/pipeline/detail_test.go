package pipeline

import "testing"

func TestMergeEnrichmentPrefersEnrichedNonEmpty(t *testing.T) {
	seed := llmEntity{
		Type: "http_route", Name: "GET /x", Summary: "seed", Confidence: 0.8,
		Actions:   []string{"seed action"},
		Details:   map[string]any{"method": "GET", "seed_only": "yes"},
		Locations: []llmLocation{{File: "a.go", StartLine: 1, EndLine: 1}},
	}
	enriched := llmEntity{
		Type: "http_route", Name: "GET /x", Summary: "enriched summary", Confidence: 0.95,
		Actions: []string{"enriched action"},
		Details: map[string]any{"path": "/x"},
	}
	out := mergeEnrichment(seed, enriched)
	if out.Summary != "enriched summary" {
		t.Fatalf("expected enriched summary, got %q", out.Summary)
	}
	if out.Confidence != 0.95 {
		t.Fatalf("expected higher confidence kept, got %f", out.Confidence)
	}
	if out.Details["method"] != "GET" || out.Details["path"] != "/x" || out.Details["seed_only"] != "yes" {
		t.Fatalf("expected merged details, got %+v", out.Details)
	}
	if len(out.Locations) != 1 || out.Locations[0].File != "a.go" {
		t.Fatalf("expected locations inherited from seed when enriched has none, got %+v", out.Locations)
	}
	if len(out.Actions) != 1 || out.Actions[0] != "enriched action" {
		t.Fatalf("expected enriched actions preferred, got %+v", out.Actions)
	}
}

func TestMergeEnrichmentKeepsSeedWhenEnrichedEmpty(t *testing.T) {
	seed := llmEntity{Type: "t", Name: "n", Summary: "seed", Confidence: 0.9}
	enriched := llmEntity{}
	out := mergeEnrichment(seed, enriched)
	if out.Summary != "seed" {
		t.Fatalf("empty enriched should leave seed summary intact, got %q", out.Summary)
	}
	if out.Confidence != 0.9 {
		t.Fatalf("expected seed confidence kept, got %f", out.Confidence)
	}
}
