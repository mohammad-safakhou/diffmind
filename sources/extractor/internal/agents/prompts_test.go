package agents

import (
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func testObj() objectives.Objective {
	return objectives.Objective{
		ID: "exposure.http_route", Kind: model.KindExposure, Type: "http_route",
		Description:       "HTTP routes",
		DiscoveryPrompt:   "find http routes",
		DetailPrompt:      "detail http routes",
		ConnectionContext: "http route connection context",
	}
}

func TestBuildDiscoveryPromptContainsRequiredMarkers(t *testing.T) {
	p := buildDiscoveryPrompt(testObj(), nil, "services/foo")
	mustContain(t, p, "AGENT ROLE: objective-extractor")
	mustContain(t, p, "OBJECTIVE_ID: exposure.http_route")
	mustContain(t, p, "OBJECTIVE_KIND: exposure")
	mustContain(t, p, "OBJECTIVE_TYPE: http_route")
	mustContain(t, p, "ONLY analyze files under 'services/foo/'")
	mustContain(t, p, "find http routes")
}

func TestBuildDetailPromptContainsSeed(t *testing.T) {
	seed := llmEntity{Type: "http_route", Name: "GET /", Confidence: 0.9}
	p := buildDetailPrompt(testObj(), seed, nil, "")
	mustContain(t, p, "AGENT ROLE: detail-extractor")
	mustContain(t, p, "OBJECTIVE_ID: exposure.http_route")
	mustContain(t, p, "detail http routes")
	mustContain(t, p, "\"name\": \"GET /\"")
}

func TestBuildReexaminePromptContainsTrigger(t *testing.T) {
	seed := llmEntity{Type: "http_route", Name: "GET /x"}
	p := buildReexaminePrompt(testObj(), seed, "low_confidence: below threshold", nil, "")
	mustContain(t, p, "AGENT ROLE: reexaminer")
	mustContain(t, p, "TRIGGER_REASON: low_confidence: below threshold")
	mustContain(t, p, "\"name\": \"GET /x\"")
}

func TestBuildConnectionPromptHasClosedSetSignal(t *testing.T) {
	exp := connectionCatalogItem{ID: "exp1", Type: "http_route", Name: "GET /"}
	cat := []connectionCatalogItem{{ID: "dep1", Type: "db_operation", Name: "users_select"}}
	p := buildConnectionPrompt(testObj(), exp, cat, 1, 1, nil, "")
	mustContain(t, p, "AGENT ROLE: connection-extractor")
	mustContain(t, p, "EXPOSURE_ID: exp1")
	mustContain(t, p, "DEPENDENCY_CATALOG (closed set")
	mustContain(t, p, "\"id\": \"dep1\"")
	mustContain(t, p, "http route connection context")
}

func TestBuildRepoFactsPromptReferencesMonorepo(t *testing.T) {
	p := buildRepoFactsPrompt("apps/x")
	mustContain(t, p, "AGENT ROLE: repo-facts")
	mustContain(t, p, "'apps/x/' subdirectory")
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("prompt missing required substring %q\n---prompt---\n%s", needle, haystack)
	}
}
