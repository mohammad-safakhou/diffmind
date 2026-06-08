package agents

import (
	"context"
	"path/filepath"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func buildFloorForFixture(t *testing.T, rel string) Result {
	t.Helper()
	repo, err := filepath.Abs(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx, err := astpkg.Build(context.Background(), repo, "java", 4, nil)
	if err != nil {
		t.Fatalf("ast build: %v", err)
	}
	cfg := config.Default()
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.Workers = 4
	return DeterministicFloor(context.Background(), idx, repo, cfg)
}

// TestDeterministicFloorSpringCRUD locks the cheap-mode entry point: the floor
// must recover both annotated routes, both repository operations, and the two
// connections wiring them, with no LLM. A regression in any deterministic stage
// (Spring binding → call resolution → db deriver → AST connection walk) breaks
// this test instead of silently degrading a full run.
func TestDeterministicFloorSpringCRUD(t *testing.T) {
	res := buildFloorForFixture(t, "testdata/eval/spring-crud/repo")

	wantExp := map[string]bool{"GET /orders/{id}": false, "POST /orders": false}
	for _, e := range res.Exposures {
		if e.Type != "http_route" {
			t.Errorf("unexpected exposure type %q", e.Type)
		}
		if _, ok := wantExp[e.Name]; ok {
			wantExp[e.Name] = true
		}
	}
	for name, found := range wantExp {
		if !found {
			t.Errorf("missing http_route %q (got %+v)", name, exposureNames(res.Exposures))
		}
	}

	wantDB := map[string]bool{"read|order": false, "write|order": false}
	for _, d := range res.Dependencies {
		if d.Type != "db_operation" {
			continue
		}
		key := fmtDetail(d.BaseEntity, "operation") + "|" + fmtDetail(d.BaseEntity, "table")
		if _, ok := wantDB[key]; ok {
			wantDB[key] = true
		}
	}
	for key, found := range wantDB {
		if !found {
			t.Errorf("missing db_operation %q", key)
		}
	}

	if len(res.Connections) != 2 {
		t.Errorf("expected 2 connections, got %d", len(res.Connections))
	}
}

func exposureNames(in []model.Exposure) []string {
	out := make([]string, 0, len(in))
	for _, e := range in {
		out = append(out, e.Name)
	}
	return out
}

func fmtDetail(b model.BaseEntity, key string) string {
	if b.Details == nil {
		return ""
	}
	if v, ok := b.Details[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
