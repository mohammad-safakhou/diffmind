package connections

import (
	"context"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func TestRunnerUsesShallowFallbackWithoutIndex(t *testing.T) {
	runner := Runner{
		Report: func(_, _, _ int, mode string) {
			if mode != "no_index" {
				t.Fatalf("mode = %q", mode)
			}
		},
	}
	out := runner.Run(context.Background(), Input{
		Exposures: []model.Exposure{{BaseEntity: model.BaseEntity{
			ID: "exposure", Name: "GET /orders",
			Details: map[string]any{"dependencies": []any{map[string]any{"name": "OrderRepository"}}},
		}}},
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{ID: "dependency", Name: "OrderRepository"}}},
	})
	if len(out.Connections) != 1 {
		t.Fatalf("output = %+v", out)
	}
}
