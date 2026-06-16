package repostore

import (
	"path/filepath"
	"testing"
)

func TestUpsertGetDelete(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	r, err := s.Upsert(Repo{Path: "/repos/orders"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if r.ID == "" || r.Name != "orders" {
		t.Fatalf("unexpected repo %+v", r)
	}
	if r.ID != IDForPath("/repos/orders") {
		t.Fatalf("id not path-derived")
	}

	// Upsert by the same path updates in place (no duplicate).
	r2, _ := s.Upsert(Repo{Path: "/repos/orders", FilePath: "/repos/orders/diffmind.yaml"})
	if r2.ID != r.ID {
		t.Fatalf("same path produced a different id")
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(list))
	}
	if list[0].FilePath != "/repos/orders/diffmind.yaml" {
		t.Fatalf("file path not updated: %q", list[0].FilePath)
	}
	if list[0].CreatedAt != r.CreatedAt {
		t.Fatalf("created_at should be preserved on update")
	}

	got, ok, _ := s.Get(r.ID)
	if !ok || got.ID != r.ID {
		t.Fatalf("get failed")
	}

	if err := s.Delete(r.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = s.List()
	if len(list) != 0 {
		t.Fatalf("expected 0 repos after delete, got %d", len(list))
	}
}

func TestPathCleaned(t *testing.T) {
	s := NewStore(t.TempDir())
	r, err := s.Upsert(Repo{Path: "/repos/orders/"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Path != filepath.Clean("/repos/orders/") {
		t.Fatalf("path not cleaned: %q", r.Path)
	}
	if r.ID != IDForPath("/repos/orders") {
		t.Fatalf("cleaned path should produce the canonical id")
	}
}
