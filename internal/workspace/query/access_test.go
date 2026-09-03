package query

import (
	"errors"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestScopedQueryDiscoveryDefaultsAndTraversal(t *testing.T) {
	q, allowed, run := testQueryService(t)
	private, _ := q.store.CreateProject(store.Project{Name: "hidden-company"})
	revoked := false
	scoped := NewWithAccess(q.store, func(pid string) error {
		if pid != allowed || revoked {
			return store.ErrNotFound
		}
		return nil
	})
	ps, err := scoped.Projects()
	if err != nil || len(ps) != 1 || ps[0].ID != allowed {
		t.Fatalf("projects %+v %v", ps, err)
	}
	p, err := scoped.ResolveProject("")
	if err != nil || p.ID != allowed {
		t.Fatalf("sole visible %+v %v", p, err)
	}
	if _, err := scoped.Summary("", run); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{private.ID, "absent", "../" + private.ID} {
		if _, err := scoped.ResolveProject(id); !errors.Is(err, store.ErrNotFound) || strings.Contains(err.Error(), private.ID) {
			t.Fatalf("hidden selection %v", err)
		}
	}
	if _, _, err := scoped.Load(allowed, "../../"+private.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("run traversal %v", err)
	}
	revoked = true
	ps, err = scoped.Projects()
	if err != nil || len(ps) != 0 {
		t.Fatalf("revoked projects %+v %v", ps, err)
	}
	if _, err := scoped.ResolveProject(""); !errors.Is(err, ErrNoProjects) {
		t.Fatalf("revoked default %v", err)
	}
	if _, err := scoped.Summary(allowed, run); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked read %v", err)
	}
}
