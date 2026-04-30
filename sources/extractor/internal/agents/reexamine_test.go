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
	reason, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
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
	reason, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if !needs || reason != "no_source_location" {
		t.Fatalf("expected no_source_location trigger, got reason=%q needs=%v", reason, needs)
	}
}

// With the back-fill logic, a name like "GET /x" should NOT trigger
// missing_required_details: we parse method/path out of the name and
// keep the seed clean for downstream stages.
func TestShouldReexamineDerivesHTTPMethodAndPathFromName(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /accounts/{id}", Confidence: 0.95,
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if needs {
		t.Fatalf("derived details from name should keep seed clean; reason=%q", reason)
	}
	if e.Details["method"] != "GET" || e.Details["path"] != "/accounts/{id}" {
		t.Fatalf("expected method/path to be back-filled, got %+v", e.Details)
	}
}

// Names without a clear method prefix should still flag re-examination.
func TestShouldReexamineFlagsHTTPRouteWithProseName(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "user search endpoint", Confidence: 0.95,
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if !needs || reason != "missing_required_details" {
		t.Fatalf("prose name should still trigger; reason=%q needs=%v", reason, needs)
	}
}

func TestShouldReexamineClean(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /x", Confidence: 0.9,
		Details:   map[string]any{"method": "GET", "path": "/x"},
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	_, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if needs {
		t.Fatalf("expected clean seed to pass")
	}
}

// Targeted tests for the splitMethodPath / splitServiceMethod helpers.
func TestSplitMethodPath(t *testing.T) {
	cases := []struct {
		in           string
		method, path string
		ok           bool
	}{
		{"GET /users/{id}", "GET", "/users/{id}", true},
		{"POST: /charge", "POST", "/charge", true},
		{"DELETE /v2/users/{uid}/sessions", "DELETE", "/v2/users/{uid}/sessions", true},
		{"GET /v1/foo (FooHandler)", "GET", "/v1/foo", true},
		{"POST relative-path", "", "", false},
		{"hello world", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		m, p, ok := splitMethodPath(c.in)
		if ok != c.ok || m != c.method || p != c.path {
			t.Errorf("splitMethodPath(%q) = (%q,%q,%v) want (%q,%q,%v)",
				c.in, m, p, ok, c.method, c.path, c.ok)
		}
	}
}

func TestSplitServiceMethod(t *testing.T) {
	cases := []struct {
		in     string
		svc, m string
		ok     bool
	}{
		{"FooService.bar", "FooService", "bar", true},
		{"foo/bar", "foo", "bar", true},
		{"AccountService#list", "AccountService", "list", true},
		{"singletoken", "", "", false},
	}
	for _, c := range cases {
		s, m, ok := splitServiceMethod(c.in)
		if ok != c.ok || s != c.svc || m != c.m {
			t.Errorf("splitServiceMethod(%q) = (%q,%q,%v) want (%q,%q,%v)",
				c.in, s, m, ok, c.svc, c.m, c.ok)
		}
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
