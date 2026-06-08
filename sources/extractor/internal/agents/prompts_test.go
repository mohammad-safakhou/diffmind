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
	p := buildDiscoveryPrompt(testObj(), nil, "services/foo", objectiveHints{}, nil, nil)
	mustContain(t, p, "AGENT ROLE: objective-extractor")
	mustContain(t, p, "OBJECTIVE_ID: exposure.http_route")
	mustContain(t, p, "OBJECTIVE_KIND: exposure")
	mustContain(t, p, "OBJECTIVE_TYPE: http_route")
	mustContain(t, p, "ONLY analyze files under 'services/foo/'")
	mustContain(t, p, "find http routes")
}

func TestBuildDetailPromptContainsSeed(t *testing.T) {
	seed := llmEntity{Type: "http_route", Name: "GET /", Confidence: 0.9}
	p := buildDetailPrompt(testObj(), seed, nil, "", objectiveHints{})
	mustContain(t, p, "AGENT ROLE: detail-extractor")
	mustContain(t, p, "OBJECTIVE_ID: exposure.http_route")
	mustContain(t, p, "detail http routes")
	mustContain(t, p, "\"name\": \"GET /\"")
}

func TestBuildReexaminePromptContainsTrigger(t *testing.T) {
	seed := llmEntity{Type: "http_route", Name: "GET /x"}
	p := buildReexaminePrompt(testObj(), seed, "low_confidence: below threshold", nil, "", objectiveHints{})
	mustContain(t, p, "AGENT ROLE: reexaminer")
	mustContain(t, p, "TRIGGER_REASON: low_confidence: below threshold")
	mustContain(t, p, "\"name\": \"GET /x\"")
}

// TestEmptyHintsProduceNoBlock guarantees that empty hints (the
// DiscoveryASTHints=false path) leave the prompt byte-identical to the
// pre-grounding behaviour — no AST_HINTS section at all.
func TestEmptyHintsProduceNoBlock(t *testing.T) {
	p := buildDiscoveryPrompt(testObj(), nil, "", objectiveHints{}, nil, nil)
	if strings.Contains(p, "AST_HINTS") {
		t.Fatalf("empty hints must not render an AST_HINTS block:\n%s", p)
	}
}

// TestDiscoveryPromptRendersHints checks the advisory block, candidate lines,
// the "not a whitelist" disclaimer, and the example/detail-keys.
func TestDiscoveryPromptRendersHints(t *testing.T) {
	obj := objByType(t, "http_route") // has Example + DetailKeys populated
	hints := objectiveHints{
		Symbols:  []symbolHint{{Qualified: "OrderController.create", File: "src/api/OrderController.java", Line: 34, Annotations: []string{"RestController", "PostMapping"}}},
		Bindings: []bindingHint{{Kind: "http_route", Symbol: "OrderController.create", Trigger: "@PostMapping", File: "src/api/OrderController.java", Line: 34}},
	}
	p := buildDiscoveryPrompt(obj, nil, "", hints, nil, nil)
	mustContain(t, p, "AST_HINTS")
	mustContain(t, p, "HINTS, NOT a whitelist")
	mustContain(t, p, "src/api/OrderController.java:34  OrderController.create")
	mustContain(t, p, "[RestController,PostMapping]")
	mustContain(t, p, "FRAMEWORK_BINDINGS")
	mustContain(t, p, "GOOD_EXAMPLE")
	mustContain(t, p, "REQUIRED_DETAIL_KEYS: populate details{} with at least these keys when determinable from code: method, path, auth")
}

// TestHintsTruncationNote renders the truncation note only when set.
func TestHintsTruncationNote(t *testing.T) {
	hints := objectiveHints{Symbols: []symbolHint{{Qualified: "X.y", File: "a.go", Line: 1}}, Truncated: true}
	p := buildDiscoveryPrompt(testObj(), nil, "", hints, nil, nil)
	mustContain(t, p, "candidate list truncated")
}

// TestBuildConnectionPromptHasClosedSetSignal was a regression guard
// for the old LLM-based connections prompt. The deterministic
// SCIP-driven stage does not build any prompt for connections, so the
// test (and the prompt builder it exercised) have been removed.

func TestBuildRepoFactsPromptReferencesMonorepo(t *testing.T) {
	p := buildRepoFactsPrompt("apps/x")
	mustContain(t, p, "AGENT ROLE: repo-facts")
	mustContain(t, p, "'apps/x/' subdirectory")
}

// TestScopeFrameworkPatternsDropsForeignLanguages verifies that, on a
// Java-only repo, language-labelled bullets for other languages are removed
// while framework-labelled and Java lines survive.
func TestScopeFrameworkPatternsDropsForeignLanguages(t *testing.T) {
	prompt := strings.Join([]string{
		"Find ALL routes.",
		"PATTERNS:",
		"- Spring Boot: @RestController, @GetMapping",
		"- JAX-RS: @Path, @GET",
		"- Node.js: Express router.get",
		"- Python: Flask @app.route",
		"- Go: gin.GET",
		"- Redis: RedisTemplate (cross-cutting)",
		"Do NOT include webhooks.",
	}, "\n")
	rf := &repoFacts{Languages: []string{"Java", "XML", "YAML"}}
	got := scopeFrameworkPatterns(prompt, detectedLanguageSet(rf))

	for _, keep := range []string{"Spring Boot", "JAX-RS", "Redis", "Do NOT include webhooks", "Find ALL routes"} {
		if !strings.Contains(got, keep) {
			t.Errorf("expected scoped prompt to keep %q\n%s", keep, got)
		}
	}
	for _, drop := range []string{"Node.js", "Python: Flask", "Go: gin"} {
		if strings.Contains(got, drop) {
			t.Errorf("expected scoped prompt to drop %q\n%s", drop, got)
		}
	}
}

// TestScopeFrameworkPatternsKeepsNodeForTS verifies a TypeScript repo keeps
// Node.js-labelled lines, and that an unknown language set filters nothing.
func TestScopeFrameworkPatternsLanguageGates(t *testing.T) {
	prompt := "- Node.js: Express\n- Python: Flask\n- Spring Boot: @GetMapping"

	ts := scopeFrameworkPatterns(prompt, detectedLanguageSet(&repoFacts{Languages: []string{"TypeScript"}}))
	if !strings.Contains(ts, "Node.js") {
		t.Errorf("TypeScript repo must keep Node.js line:\n%s", ts)
	}
	if strings.Contains(ts, "Python: Flask") {
		t.Errorf("TypeScript repo must drop Python line:\n%s", ts)
	}

	// nil/unknown languages → no filtering at all.
	if got := scopeFrameworkPatterns(prompt, detectedLanguageSet(nil)); got != prompt {
		t.Errorf("nil repo_facts must leave prompt unchanged, got:\n%s", got)
	}
	if got := scopeFrameworkPatterns(prompt, detectedLanguageSet(&repoFacts{Languages: []string{"XML", "YAML"}})); got != prompt {
		t.Errorf("non-language facts must leave prompt unchanged, got:\n%s", got)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("prompt missing required substring %q\n---prompt---\n%s", needle, haystack)
	}
}
