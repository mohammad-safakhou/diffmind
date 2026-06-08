package eval

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// TestCheapAccuracyFloor is the hermetic CI guardrail: the deterministic floor
// must perfectly recover the spring-crud fixture's labeled facts (2 routes, 2
// db ops, 2 connections) with no LLM. A regression in any deterministic stage
// drops F1 below the threshold and fails here.
func TestCheapAccuracyFloor(t *testing.T) {
	fixtures := map[string]float64{
		"spring-crud": 1.0, // floor should be perfect on this fixture
	}
	cfg := config.Default()
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.Workers = 4

	for name, minF1 := range fixtures {
		dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "eval", name))
		if err != nil {
			t.Fatalf("%s: abs: %v", name, err)
		}
		rep, err := RunCheap(context.Background(), dir, cfg)
		if err != nil {
			t.Fatalf("%s: RunCheap: %v", name, err)
		}
		if rep.Overall.F1 < minF1 {
			t.Errorf("%s: overall F1 %.3f below threshold %.3f\n%s", name, rep.Overall.F1, minF1, renderForTest(rep))
		}
	}
}

func renderForTest(rep Report) string {
	var b stringsBuilder
	RenderTable(&b, rep)
	return b.String()
}

// stringsBuilder is a tiny io.Writer wrapper so the test can render the table
// into a string for the failure message without importing strings.Builder
// methods that don't satisfy io.Writer directly (Builder does satisfy it; this
// just keeps the helper explicit).
type stringsBuilder struct{ data []byte }

func (s *stringsBuilder) Write(p []byte) (int, error) {
	s.data = append(s.data, p...)
	return len(p), nil
}
func (s *stringsBuilder) String() string { return string(s.data) }
