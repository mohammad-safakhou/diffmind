package corpus

import "testing"

func TestEvaluateExpectations(t *testing.T) {
	expect := expectation{
		MinEntities:   3,
		RequiredTypes: []string{"Endpoint", "RuntimeUnit"},
	}
	counts := map[string]int{
		"Endpoint": 1,
	}
	failures := evaluateExpectations(expect, 2, counts)
	if len(failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(failures))
	}
}

func TestSanitizeName(t *testing.T) {
	if got := sanitizeName("Service/API:Main"); got != "service-api-main" {
		t.Fatalf("unexpected sanitize result: %q", got)
	}
}

func TestCaseConfidence(t *testing.T) {
	expect := expectation{
		MinEntities:   1,
		RequiredTypes: []string{"Endpoint", "RuntimeUnit"},
	}
	if got := caseConfidence(expect, 1); got <= 0 || got >= 1 {
		t.Fatalf("expected partial confidence, got %f", got)
	}
	if got := caseConfidence(expectation{}, 0); got != 1.0 {
		t.Fatalf("expected default confidence 1.0, got %f", got)
	}
}
