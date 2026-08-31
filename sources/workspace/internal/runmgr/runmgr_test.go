package runmgr

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/store"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// copyDir recursively copies src into dst.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

// setup builds a store + central DiffMind runs dir populated from the sample
// fixtures, and returns the manager, project id, and the two repo refs.
func setup(t *testing.T) (*Manager, string, []store.RunRepoRef) {
	t.Helper()
	wd, _ := os.Getwd()
	root := filepath.Join(wd, "..", "..")

	beRuns := t.TempDir()
	copyDir(t, filepath.Join(root, "testdata", "sample_diffmind_output", "order-service", ".diffmind", "runs", "run_001"), filepath.Join(beRuns, "order-run"))
	copyDir(t, filepath.Join(root, "testdata", "sample_diffmind_output", "billing-service", ".diffmind", "runs", "run_001"), filepath.Join(beRuns, "billing-run"))

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.CreateProject(store.Project{Name: "demo"})
	orderRepo, _ := st.CreateRepo(p.ID, store.Repo{Name: "order-service", Path: filepath.Join(root, "testdata", "sample_service_repos", "order-service"), Kind: "service_repo"})
	billingRepo, _ := st.CreateRepo(p.ID, store.Repo{Name: "billing-service", Path: filepath.Join(root, "testdata", "sample_service_repos", "billing-service"), Kind: "service_repo"})

	m := New(st, util.NewLogger(util.LevelInfo), beRuns)
	refs := []store.RunRepoRef{
		{RepoID: orderRepo.ID, DiffMindRunID: "order-run"},
		{RepoID: billingRepo.ID, DiffMindRunID: "billing-run"},
	}
	return m, p.ID, refs
}

func TestRunManagerFullRun(t *testing.T) {
	m, pid, refs := setup(t)

	run, err := m.Start(pid, refs, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	m.WaitFor(pid, run.ID)

	got, err := m.store.GetRun(pid, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunCompleted {
		t.Fatalf("status = %s, want completed (err=%s)", got.Status, got.Error)
	}
	if got.ServiceCount < 2 {
		t.Fatalf("service count = %d, want >= 2", got.ServiceCount)
	}
	if got.EdgeCount == 0 {
		t.Fatalf("edge count was not persisted")
	}
	if got.GraphQuality == nil {
		t.Fatalf("graph quality stats were not persisted")
	}

	runDir := m.store.RunDir(pid, run.ID)
	for _, f := range []string{"graph.json", "events.jsonl", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(runDir, f)); err != nil {
			t.Fatalf("expected %s in run dir: %v", f, err)
		}
	}
}

func TestRunManagerConcurrent(t *testing.T) {
	m, pid, refs := setup(t)
	r1, err := m.Start(pid, refs, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := m.Start(pid, refs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID == r2.ID {
		t.Fatalf("two runs share id %s", r1.ID)
	}
	m.WaitFor(pid, r1.ID)
	m.WaitFor(pid, r2.ID)
	for _, id := range []string{r1.ID, r2.ID} {
		got, _ := m.store.GetRun(pid, id)
		if got.Status != store.RunCompleted {
			t.Fatalf("run %s status = %s", id, got.Status)
		}
	}
}

func TestRunManagerSSE(t *testing.T) {
	m, pid, refs := setup(t)
	run, err := m.Start(pid, refs, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, cancel, err := m.Subscribe(pid, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	seen := map[string]bool{}
	timeout := time.After(30 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				if seen["run_started"] && seen["run_completed"] {
					return
				}
				t.Fatalf("stream closed before terminal event; saw %v", seen)
			}
			seen[e.Type] = true
			if seen["run_started"] && seen["run_completed"] {
				return
			}
		case <-timeout:
			t.Fatalf("timed out; saw %v", seen)
		}
	}
}

func TestRunManagerDeleteAfterFinish(t *testing.T) {
	m, pid, refs := setup(t)
	run, _ := m.Start(pid, refs, nil)
	m.WaitFor(pid, run.ID)
	if err := m.store.DeleteRun(pid, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.GetRun(pid, run.ID); err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
