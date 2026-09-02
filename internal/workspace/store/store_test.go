package store

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

func TestProjectCRUD(t *testing.T) {
	s := newTestStore(t)

	// Empty list.
	ps, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Fatalf("expected no projects, got %d", len(ps))
	}

	// Create.
	p, err := s.CreateProject(Project{Name: "DEFAULT"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "default" {
		t.Fatalf("slug = %q, want default", p.ID)
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("created_at not set")
	}

	// Get.
	got, err := s.GetProject(p.ID)
	if err != nil || got.Name != "DEFAULT" {
		t.Fatalf("get: %v %+v", err, got)
	}

	// Update.
	upd, err := s.UpdateProject(p.ID, func(pr *Project) { pr.Instruction = "hello"; pr.SearchRoots = []string{"/x"} })
	if err != nil {
		t.Fatal(err)
	}
	if upd.Instruction != "hello" || len(upd.SearchRoots) != 1 {
		t.Fatalf("update not applied: %+v", upd)
	}

	// Delete.
	if err := s.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProject(p.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestProjectSlugCollision(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateProject(Project{Name: "My Project"})
	b, _ := s.CreateProject(Project{Name: "My Project"})
	if a.ID != "my-project" {
		t.Fatalf("first slug = %q", a.ID)
	}
	if b.ID != "my-project-2" {
		t.Fatalf("second slug = %q, want my-project-2", b.ID)
	}
}

func TestIngestionPersistence(t *testing.T) {
	s := newTestStore(t)
	project, err := s.CreateProject(Project{Name: "Company graph"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateIngestion(project.ID, Ingestion{Provider: "github", Source: "example", Phase: "discovering"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != IngestionRunning || created.StartedAt.IsZero() {
		t.Fatalf("created ingestion = %+v", created)
	}
	created.Status = IngestionCompleted
	created.Phase = "complete"
	created.Imported = 3
	created.GraphRunID = "graph-1"
	created.FinishedAt = time.Now().UTC()
	if err := s.SaveIngestion(project.ID, *created); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetIngestion(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != IngestionCompleted || got.Imported != 3 || got.GraphRunID != "graph-1" || got.UpdatedAt.IsZero() {
		t.Fatalf("persisted ingestion = %+v", got)
	}
}

func TestRepoCRUDDoesNotTouchSource(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreateProject(Project{Name: "p"})

	src := t.TempDir() // stands in for a real source repo
	r, err := s.CreateRepo(p.ID, Repo{Path: src})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name == "" || r.Kind != "service_repo" {
		t.Fatalf("repo defaults wrong: %+v", r)
	}

	repos, _ := s.ListRepos(p.ID)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	if _, err := s.UpdateRepo(p.ID, r.ID, func(rr *Repo) { rr.Instruction = "ovr" }); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteRepo(p.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	// Source repo must still exist on disk after metadata delete.
	if !dirExists(src) {
		t.Fatal("source repo was deleted — DeleteRepo must only remove metadata")
	}
}

func TestPackCRUD(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreateProject(Project{Name: "p"})

	body := []byte(`{"name":"Helm Identity","applies_to":{"kind":"service_repo"},"extractions":[]}`)
	id, err := s.CreatePack(p.ID, "Helm Identity", body)
	if err != nil {
		t.Fatal(err)
	}
	if id != "helm-identity" {
		t.Fatalf("pack id = %q", id)
	}

	metas, _ := s.ListPacks(p.ID)
	if len(metas) != 1 || metas[0].Name != "Helm Identity" {
		t.Fatalf("list packs = %+v", metas)
	}

	raw, err := s.GetPack(p.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("empty pack body")
	}

	if err := s.PutPack(p.ID, id, []byte(`{"name":"Updated"}`)); err != nil {
		t.Fatal(err)
	}
	metas, _ = s.ListPacks(p.ID)
	if metas[0].Name != "Updated" {
		t.Fatalf("pack name after update = %q", metas[0].Name)
	}

	if err := s.DeletePack(p.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPack(p.ID, id); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRunCRUD(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreateProject(Project{Name: "p"})

	m, err := s.CreateRun(p.ID, RunManifest{Repos: []RunRepoRef{{RepoID: "r1", DiffMindRunID: "run1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == "" || m.Status != RunRunning {
		t.Fatalf("run init wrong: %+v", m)
	}

	m.Status = RunCompleted
	m.EdgeCount = 3
	if err := s.SaveRun(p.ID, *m); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRun(p.ID, m.ID)
	if got.Status != RunCompleted || got.EdgeCount != 3 {
		t.Fatalf("saved run wrong: %+v", got)
	}

	runs, _ := s.ListRuns(p.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	if err := s.DeleteRun(p.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRun(p.ID, m.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestEffectivePrecedence(t *testing.T) {
	proj := &Project{Instruction: "project-instr"}
	repoNoOverride := &Repo{}
	repoOverride := &Repo{Instruction: "repo-instr", PackIDs: []string{"a", "b"}}

	if got := EffectiveInstruction(proj, repoNoOverride); got != "project-instr" {
		t.Fatalf("fallback instruction = %q", got)
	}
	if got := EffectiveInstruction(proj, repoOverride); got != "repo-instr" {
		t.Fatalf("override instruction = %q", got)
	}
	if got := EffectivePackIDs(proj, repoNoOverride); got != nil {
		t.Fatalf("expected nil (project fallback), got %v", got)
	}
	if got := EffectivePackIDs(proj, repoOverride); len(got) != 2 {
		t.Fatalf("override pack ids = %v", got)
	}
}
