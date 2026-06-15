package extraction

import (
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func objByType(t *testing.T, typ string) objectives.Objective {
	t.Helper()
	for _, o := range objectives.Default() {
		if o.Type == typ {
			return o
		}
	}
	t.Fatalf("no objective of type %q", typ)
	return objectives.Objective{}
}

// TestBoundaryAndBadExampleRenderForConfusablePairs verifies the optional
// BOUNDARY/BAD_EXAMPLE blocks render for a confusable objective (http_route)
// and stay empty for one without them (scheduled_job).
func TestBoundaryAndBadExampleRenderForConfusablePairs(t *testing.T) {
	route := objByType(t, "http_route")
	if b := BoundaryBlock(route); !strings.Contains(b, "BOUNDARY") || !strings.Contains(strings.ToLower(b), "webhook") {
		t.Errorf("http_route BoundaryBlock should name the webhook neighbour:\n%s", b)
	}
	if b := BadExampleBlock(route); !strings.Contains(b, "BAD_EXAMPLE") || !strings.Contains(b, "WRONG") {
		t.Errorf("http_route BadExampleBlock should show a misclassified item:\n%s", b)
	}

	job := objByType(t, "scheduled_job")
	if b := BoundaryBlock(job); b != "" {
		t.Errorf("scheduled_job has no boundary; want empty, got:\n%s", b)
	}
	if b := BadExampleBlock(job); b != "" {
		t.Errorf("scheduled_job has no negative example; want empty, got:\n%s", b)
	}
}

// TestHardRulesExhaustivenessGatedByHighVariance verifies the enumerate-verify
// rule is always present, while the extra exhaustiveness line appears only for
// HighVariance objectives.
func TestHardRulesExhaustivenessGatedByHighVariance(t *testing.T) {
	highVar := objByType(t, "queue_publish") // HighVariance: LLM-only
	lowVar := objByType(t, "http_route")     // not HighVariance: strong AST floor

	hv := BuildDiscoveryPrompt(highVar, nil, "", ObjectiveHints{}, nil, nil, false)
	lv := BuildDiscoveryPrompt(lowVar, nil, "", ObjectiveHints{}, nil, nil, false)

	for _, p := range []string{hv, lv} {
		if !strings.Contains(p, "ENUMERATE then VERIFY") {
			t.Errorf("every discovery prompt must carry the enumerate-then-verify rule:\n%s", p)
		}
	}
	if !strings.Contains(hv, "BE EXHAUSTIVE") {
		t.Errorf("HighVariance objective must carry the exhaustiveness line:\n%s", hv)
	}
	if strings.Contains(lv, "BE EXHAUSTIVE") {
		t.Errorf("non-HighVariance objective must NOT carry the exhaustiveness line:\n%s", lv)
	}
}

// TestScopeFrameworkLabelsTrimsAbsentFrameworks verifies the gated framework
// trim drops bullets for frameworks the repo shows no trace of, keeps detected
// ones (generously), and is a no-op on an unknown set.
func TestScopeFrameworkLabelsTrimsAbsentFrameworks(t *testing.T) {
	prompt := strings.Join([]string{
		"PATTERNS:",
		"- Spring Boot: @RestController",
		"- Spring Data JPA: @Repository",
		"- Redis: RedisTemplate",
		"- DynamoDB: DynamoDBMapper",
		"- Raw SQL: PreparedStatement",
	}, "\n")

	// A Spring repo with no Redis/Dynamo: Spring bullets survive (generous
	// substring match against "spring-data-jpa"), datastores it lacks are cut,
	// and the unmapped "Raw SQL" bullet is always kept.
	detected := map[string]bool{"spring boot": true, "spring-data-jpa": true}
	got := ScopeFrameworkLabels(prompt, detected)
	for _, keep := range []string{"Spring Boot", "Spring Data JPA", "Raw SQL"} {
		if !strings.Contains(got, keep) {
			t.Errorf("expected scoped prompt to keep %q\n%s", keep, got)
		}
	}
	for _, drop := range []string{"Redis", "DynamoDB"} {
		if strings.Contains(got, drop) {
			t.Errorf("expected scoped prompt to drop %q\n%s", drop, got)
		}
	}

	// Unknown/empty detected set → no filtering at all.
	if out := ScopeFrameworkLabels(prompt, nil); out != prompt {
		t.Errorf("nil detected set must leave prompt unchanged, got:\n%s", out)
	}
	if out := ScopeFrameworkLabels(prompt, DetectedFrameworkSet(nil)); out != prompt {
		t.Errorf("nil repo_facts must leave prompt unchanged, got:\n%s", out)
	}
}

// TestBuildDiscoveryPromptFrameworkScopeGate verifies the framework trim only
// fires when the caller opts in, leaving the default prompt untrimmed.
func TestBuildDiscoveryPromptFrameworkScopeGate(t *testing.T) {
	obj := objByType(t, "db_operation") // its prompt lists Redis/DynamoDB/...
	rf := &RepoFacts{Frameworks: []string{"Spring Boot"}, Languages: []string{"Java"}}

	off := BuildDiscoveryPrompt(obj, rf, "", ObjectiveHints{}, nil, nil, false)
	on := BuildDiscoveryPrompt(obj, rf, "", ObjectiveHints{}, nil, nil, true)

	// "DynamoDBMapper" appears only in the "- DynamoDB:" bullet, so it tracks
	// the bullet itself (unlike the bare token "DynamoDB", which also shows up
	// in a non-bullet prose enumeration that scoping must not touch).
	if !strings.Contains(off, "DynamoDBMapper") {
		t.Errorf("framework scope OFF must leave the DynamoDB bullet in place")
	}
	if strings.Contains(on, "DynamoDBMapper") {
		t.Errorf("framework scope ON must trim the undetected DynamoDB bullet")
	}
}
