package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestProjectAccessPersistenceCASAndRevocation(t *testing.T) {
	s := newTestStore(t)
	p, err := s.CreateProject(Project{Name: "access"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.GetProjectAccess(p.ID)
	if err != nil || a.Revision != 0 || len(a.Members) != 0 {
		t.Fatalf("default %+v %v", a, err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.PutProjectAccess(p.ID, 0, map[string]string{"alice": "editor"})
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("CAS winners=%d", winners.Load())
	}
	reopened, err := New(s.HomeDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err = reopened.GetProjectAccess(p.ID)
	if err != nil || a.Revision != 1 || a.Members["alice"] != "editor" || a.UpdatedAt.IsZero() {
		t.Fatalf("reopened %+v %v", a, err)
	}
	if _, err := s.PutProjectAccess(p.ID, 0, map[string]string{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revoke: %v", err)
	}
	if _, err := s.PutProjectAccess(p.ID, 1, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetProjectAccess(p.ID)
	if len(a.Members) != 0 || a.Revision != 2 {
		t.Fatalf("revocation %+v", a)
	}
}
func TestProjectAccessValidationAndCorruptionFailClosed(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreateProject(Project{Name: "access"})
	for _, members := range []map[string]string{{"": "viewer"}, {" alice": "viewer"}, {"a\nb": "editor"}, {"alice": "admin"}, {"alice": "unknown"}} {
		if _, err := s.PutProjectAccess(p.ID, 0, members); err == nil {
			t.Fatalf("accepted %+v", members)
		}
	}
	for _, pid := range []string{"../escape", "missing", "a/b", "."} {
		if _, err := s.GetProjectAccess(pid); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unsafe/missing %s: %v", pid, err)
		}
	}
	policy := filepath.Join(s.projectDir(p.ID), "access.json")
	for _, body := range []string{`{`, `{"version":2,"revision":1,"members":{}}`, `{"version":1,"revision":1,"members":{"alice":"admin"}}`, `{"version":1,"revision":1,"members":{}} {}`} {
		if err := os.WriteFile(policy, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetProjectAccess(p.ID); err == nil {
			t.Fatal("corrupt policy allowed")
		}
		if _, err := s.PutProjectAccess(p.ID, 0, map[string]string{"alice": "viewer"}); err == nil {
			t.Fatal("corrupt policy silently replaced")
		}
	}
}
