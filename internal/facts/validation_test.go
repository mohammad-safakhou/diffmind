package facts

import (
	"errors"
	"testing"
)

func TestValidateBundleRejectsMissingEvidence(t *testing.T) {
	bundle := Bundle{
		Evidence: []Evidence{},
		Facts: []Fact{NewFact("Endpoint", map[string]any{"path": "/health"}, nil, 0.9, Provenance{
			AnalyzerID: "analyzer.test", AnalyzerVersion: "v1", Deterministic: true,
		})},
	}

	err := ValidateBundle(bundle)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !errors.Is(err, ErrMissingEvidence) {
		t.Fatalf("expected ErrMissingEvidence, got %v", err)
	}
}

func TestValidateBundleAcceptsValidFactEvidence(t *testing.T) {
	ev := NewEvidence("snap1", "main.go", 1, 1, 1, 12, "func main()")
	fact := NewFact("RuntimeUnit", map[string]any{"name": "main"}, []string{ev.ID}, 0.95, Provenance{
		AnalyzerID: "analyzer.runtime", AnalyzerVersion: "v1", Deterministic: true, Inferred: false,
	})
	bundle := Bundle{Evidence: []Evidence{ev}, Facts: []Fact{fact}}

	if err := ValidateBundle(bundle); err != nil {
		t.Fatalf("expected valid bundle, got error: %v", err)
	}
}

func TestHashSnippetStable(t *testing.T) {
	a := HashSnippet("  hello world\n")
	b := HashSnippet("hello world")
	if a != b {
		t.Fatalf("hash should be stable for trimmed snippets")
	}
}
