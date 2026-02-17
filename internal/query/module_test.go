package query

import "diffmind/internal/bundleio"
import "testing"

func TestFilterEntitiesByView(t *testing.T) {
	in := []bundleio.Entity{
		{ID: "1", Type: "Endpoint"},
		{ID: "2", Type: "RuntimeUnit"},
		{ID: "3", Type: "Endpoint"},
	}
	got := FilterEntities(in, "endpoints")
	if len(got) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(got))
	}
	if got[0].Type != "Endpoint" || got[1].Type != "Endpoint" {
		t.Fatalf("expected only endpoints")
	}
}

func TestViewTypeSet(t *testing.T) {
	cases := []string{
		"runtime",
		"endpoints",
		"config",
		"external",
		"pipeline",
		"infra",
		"dependency",
		"ownership",
		"risk",
		"conflict",
		"verify",
		"all",
	}
	for _, view := range cases {
		if !ValidateView(view) {
			t.Fatalf("view %s should be supported", view)
		}
	}
}

func TestSummarizeEndpoint(t *testing.T) {
	e := bundleio.Entity{Type: "Endpoint", Attributes: map[string]any{"method": "GET", "path": "/health", "framework": "express"}}
	if s := summarize(e); s == "" {
		t.Fatalf("summary should not be empty")
	}
}
