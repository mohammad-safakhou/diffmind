package eval

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// TestCheapAccuracyFloor is the hermetic CI guardrail: the deterministic floor
// must perfectly recover each fixture's labeled deterministic facts with no
// LLM, including the labeled concrete instances (the downstream contract). A
// regression in any deterministic stage drops F1 below the threshold or
// produces an instance mismatch and fails here.
func TestCheapAccuracyFloor(t *testing.T) {
	fixtures := map[string]float64{
		"spring-crud":    1.0, // routes + JPA db ops + connections
		"sqs-producer":   1.0, // route + SqsTemplate publish + connection
		"sqs-consumer":   1.0, // @SqsListener + zero-hop db write + instances
		"go-stdlib":      1.0, // net/http mux routes + raw-SQL db ops + connections
		"django-app":     1.0, // urls.py routes + Django ORM read + connection
		"go-gorm":        1.0, // net/http routes + GORM ops (literal + LocalTypes) + connections
		"node-sequelize": 1.0, // express routes + corroborated Sequelize ops
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
		for _, o := range rep.Objectives {
			for _, im := range o.InstanceMismatches {
				t.Errorf("%s: %s %s: instance want %q got %q", name, o.Objective, im.Key, im.Want, im.Got)
			}
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
