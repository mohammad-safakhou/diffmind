package connections

import (
	"context"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func TestRunnerUsesShallowFallbackWithoutIndex(t *testing.T) {
	called := false
	runner := Runner{
		BuildShallow: func([]model.Exposure, []model.Dependency, float64) ([]model.Connection, []model.UnresolvedItem) {
			called = true
			return []model.Connection{{ID: "connection"}}, nil
		},
		Report: func(_, _, _ int, mode string) {
			if mode != "no_index" {
				t.Fatalf("mode = %q", mode)
			}
		},
	}
	out := runner.Run(context.Background(), Input{
		Exposures:    []model.Exposure{{BaseEntity: model.BaseEntity{ID: "exposure"}}},
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{ID: "dependency"}}},
	})
	if !called || len(out.Connections) != 1 {
		t.Fatalf("output = %+v", out)
	}
}
