package reconcile

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func TestRunnerDedupesSortsAndDropsOrphans(t *testing.T) {
	exposure := model.Exposure{BaseEntity: model.BaseEntity{ID: "exp", Type: "http_route", Name: "GET /x"}}
	dependency := model.Dependency{BaseEntity: model.BaseEntity{ID: "dep", Type: "outbound_http", Name: "GET /y"}}
	valid := model.Connection{ID: "z", FromExposureID: "exp", ToDependencyID: "dep"}
	orphan := model.Connection{ID: "a", FromExposureID: "missing", ToDependencyID: "dep"}

	got := (Runner{}).Run(Input{
		Exposures:    []model.Exposure{exposure, exposure},
		Dependencies: []model.Dependency{dependency, dependency},
		Connections:  []model.Connection{valid, orphan},
	})

	if len(got.Exposures) != 1 || len(got.Dependencies) != 1 {
		t.Fatalf("dedupe failed: exposures=%d dependencies=%d", len(got.Exposures), len(got.Dependencies))
	}
	if len(got.Connections) != 1 || got.Connections[0].ID != "z" {
		t.Fatalf("connections = %+v", got.Connections)
	}
	if len(got.Unresolved) != 1 {
		t.Fatalf("unresolved = %+v", got.Unresolved)
	}
}
