package connections

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func TestChooseDepsForSymbolKeepsMultipleNarrowOperations(t *testing.T) {
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{ID: "read", Type: "cache_operation", Name: "read redis", Locations: []model.Location{{File: "cache.py", StartLine: 10, EndLine: 10}}}},
		{BaseEntity: model.BaseEntity{ID: "write", Type: "cache_operation", Name: "write redis", Locations: []model.Location{{File: "cache.py", StartLine: 20, EndLine: 20}}}},
	}
	got := chooseDepsForSymbol("send_to_cache", deps)
	if len(got) != 2 {
		t.Fatalf("narrow operations in the same target symbol must all survive, got %d: %+v", len(got), got)
	}
}

func TestChooseDepsForSymbolStillCollapsesBroadFallbacks(t *testing.T) {
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{ID: "short", Type: "db_operation", Name: "Repo", Locations: []model.Location{{File: "Repo.java", StartLine: 1, EndLine: 50}}}},
		{BaseEntity: model.BaseEntity{ID: "long", Type: "db_operation", Name: "LongerRepositoryName", Locations: []model.Location{{File: "Repo.java", StartLine: 1, EndLine: 50}}}},
	}
	got := chooseDepsForSymbol("Service.call", deps)
	if len(got) != 1 || got[0].ID != "long" {
		t.Fatalf("broad fallback should keep the most specific dependency, got %+v", got)
	}
}
