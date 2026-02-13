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
