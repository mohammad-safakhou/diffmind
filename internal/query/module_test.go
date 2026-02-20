package query

import "diffmind/internal/bundleio"
import "testing"

func TestFilterEntitiesByView(t *testing.T) {
	in := []bundleio.Entity{
		{ID: "1", Type: "Endpoint"},
		{ID: "2", Type: "RuntimeUnit"},
		{ID: "3", Type: "Endpoint"},
	}
	got := filterEntitiesWithOptions(in, options{View: "endpoints"})
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

func TestFilterEntitiesWithVerificationQueryAndConfidence(t *testing.T) {
	in := []bundleio.Entity{
		{ID: "1", Type: "Endpoint", NaturalKey: "GET|/health", Attributes: map[string]any{"verification_status": "verified", "library": "gin"}, Confidence: 0.95},
		{ID: "2", Type: "Endpoint", NaturalKey: "GET|/orders", Attributes: map[string]any{"verification_status": "needs_review", "library": "echo"}, Confidence: 0.75},
		{ID: "3", Type: "ExternalCall", NaturalKey: "GET|billing", Attributes: map[string]any{"verification_status": "verified", "library": "axios"}, Confidence: 0.91},
	}
	got := filterEntitiesWithOptions(in, options{
		View:               "all",
		VerificationFilter: "verified",
		QueryText:          "gin",
		ConfidenceMin:      0.9,
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 entity after filters, got %d", len(got))
	}
	if got[0].ID != "1" {
		t.Fatalf("expected entity 1, got %s", got[0].ID)
	}
}

func TestPaginateEntities(t *testing.T) {
	in := []bundleio.Entity{
		{ID: "1", Type: "Endpoint"},
		{ID: "2", Type: "Endpoint"},
		{ID: "3", Type: "Endpoint"},
	}
	got := paginateEntities(in, 1, 1)
	if len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("unexpected pagination result: %+v", got)
	}
	gotEmpty := paginateEntities(in, 10, 10)
	if len(gotEmpty) != 0 {
		t.Fatalf("expected empty page for high offset, got %d", len(gotEmpty))
	}
}
