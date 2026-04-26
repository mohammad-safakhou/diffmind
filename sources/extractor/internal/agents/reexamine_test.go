package agents

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func httpRouteObjective() objectives.Objective {
	return objectives.Objective{ID: "exposure.http_route", Kind: model.KindExposure, Type: "http_route"}
}

func TestShouldReexamineFlagsLowConfidence(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /x", Confidence: 0.5,
		Details:   map[string]any{"method": "GET", "path": "/x"},
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), e, 0.7)
	if !needs {
		t.Fatalf("expected low-confidence seed to be flagged")
	}
	if reason != "low_confidence" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestShouldReexamineFlagsMissingLocation(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /x", Confidence: 0.9,
		Details: map[string]any{"method": "GET", "path": "/x"},
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), e, 0.7)
	if !needs || reason != "no_source_location" {
		t.Fatalf("expected no_source_location trigger, got reason=%q needs=%v", reason, needs)
	}
}

func TestShouldReexamineFlagsMissingRequiredDetails(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /x", Confidence: 0.9,
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
		// no method/path
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), e, 0.7)
	if !needs || reason != "missing_required_details" {
		t.Fatalf("expected missing_required_details trigger, got reason=%q needs=%v", reason, needs)
	}
}

func TestShouldReexamineClean(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /x", Confidence: 0.9,
		Details:   map[string]any{"method": "GET", "path": "/x"},
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	_, _, needs := shouldReexamine(httpRouteObjective(), e, 0.7)
	if needs {
		t.Fatalf("expected clean seed to pass")
	}
}

func TestHasDetailKeyAcceptsCommonAliases(t *testing.T) {
	details := map[string]any{"queue_name": "my-q"}
	if !hasDetailKey(details, "queue") {
		t.Fatalf("expected queue_name alias to satisfy 'queue'")
	}
	details2 := map[string]any{"http_method": "GET"}
	if !hasDetailKey(details2, "method") {
		t.Fatalf("expected http_method alias to satisfy 'method'")
	}
}

func TestMissingRequiredDetailsUnknownTypeReturnsEmpty(t *testing.T) {
	if got := missingRequiredDetails("random_type", map[string]any{}); got != "" {
		t.Fatalf("unknown objective types should not require fields, got %q", got)
	}
}
