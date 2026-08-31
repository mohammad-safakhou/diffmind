package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
)

// newTestRunner builds a Runner backed by a temp dir and a real bus.
func newTestRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	base := t.TempDir()
	return New(base, events.NewBus(1000)), base
}

// registerRun injects a run directly into the manager without launching the
// real pipeline, so the manager's bookkeeping can be tested in isolation. It
// returns the run's done channel and a function that marks the run finished.
func registerRun(r *Runner, id, repo string) (*run, chan struct{}) {
	done := make(chan struct{})
	rn := &run{
		doneCh: done,
		cancel: func() {},
		state: State{
			RunID:     id,
			Status:    StatusRunning,
			StartedAt: time.Now().UTC(),
			RepoPath:  repo,
			RunDir:    r.persistDir(id),
		},
	}
	r.mu.Lock()
	r.runs[id] = rn
	r.mu.Unlock()
	r.persistState(rn.state)
	return rn, done
}

// TestConcurrentRunsTracked verifies the manager holds multiple active runs at
// once without one displacing another — the core of removing the singleton.
func TestConcurrentRunsTracked(t *testing.T) {
	r, _ := newTestRunner(t)
	registerRun(r, "run-a", "/repo/a")
	registerRun(r, "run-b", "/repo/b")
	registerRun(r, "run-c", "/repo/c")

	active := r.ActiveIDs()
	if len(active) != 3 {
		t.Fatalf("expected 3 active runs, got %d: %v", len(active), active)
	}
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		st, ok := r.State(id)
		if !ok || st.Status != StatusRunning {
			t.Fatalf("run %s not tracked as running: ok=%v st=%+v", id, ok, st)
		}
	}
}

// TestCancelTargetsOneRun verifies cancellation is scoped to a single run.
func TestCancelTargetsOneRun(t *testing.T) {
	r, _ := newTestRunner(t)
	cancelledA := false
	rnA, _ := registerRun(r, "run-a", "/repo/a")
	rnA.cancel = func() { cancelledA = true }
	cancelledB := false
	rnB, _ := registerRun(r, "run-b", "/repo/b")
	rnB.cancel = func() { cancelledB = true }

	if err := r.Cancel("run-a"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !cancelledA {
		t.Fatalf("run-a cancel func not invoked")
	}
	if cancelledB {
		t.Fatalf("run-b was cancelled but should not have been")
	}
	stA, _ := r.State("run-a")
	if stA.Status != StatusCancelling {
		t.Fatalf("run-a status = %s, want cancelling", stA.Status)
	}
	stB, _ := r.State("run-b")
	if stB.Status != StatusRunning {
		t.Fatalf("run-b status = %s, want running", stB.Status)
	}
}

// TestCancelUnknownRunIsNoop guards the idempotent-cancel contract.
func TestCancelUnknownRunIsNoop(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.Cancel("does-not-exist"); err != nil {
		t.Fatalf("cancel unknown run should be a nil-error no-op, got %v", err)
	}
}

// TestStatePersistenceAndReload verifies run_state.json is written and a fresh
// Runner over the same base dir can read it back.
func TestStatePersistenceAndReload(t *testing.T) {
	r, base := newTestRunner(t)
	rn, _ := registerRun(r, "run-x", "/repo/x")

	// File should exist on disk after registration.
	statePath := filepath.Join(base, "run-x", stateFileName)
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("run_state.json not persisted: %v", err)
	}

	// Mutate to a terminal state and persist again.
	rn.state.Status = StatusCompleted
	rn.state.FinishedAt = time.Now().UTC()
	r.persistState(rn.state)

	// A brand-new Runner (simulating a process restart) must resolve the
	// run from disk via LoadState / State fallback.
	fresh := New(base, events.NewBus(10))
	st, ok := fresh.State("run-x")
	if !ok {
		t.Fatalf("fresh runner could not reload run-x")
	}
	if st.Status != StatusCompleted {
		t.Fatalf("reloaded status = %s, want completed", st.Status)
	}
	if st.RepoPath != "/repo/x" {
		t.Fatalf("reloaded repo = %s, want /repo/x", st.RepoPath)
	}

	// LoadState should also parse the raw file directly.
	loaded, err := LoadState(base, "run-x")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.RunID != "run-x" {
		t.Fatalf("LoadState run id = %q", loaded.RunID)
	}
}

// TestLoadStateMissing returns an error rather than a zero value masquerading
// as a real run.
func TestLoadStateMissing(t *testing.T) {
	base := t.TempDir()
	if _, err := LoadState(base, "nope"); err == nil {
		t.Fatalf("expected error loading missing run state")
	}
}

// TestAllocateRunIDUnique ensures two allocations in the same second do not
// collide.
func TestAllocateRunIDUnique(t *testing.T) {
	r, _ := newTestRunner(t)
	id1 := r.allocateRunID()
	// Register it so the next allocation sees the collision.
	registerRun(r, id1, "/repo")
	id2 := r.allocateRunID()
	if id1 == id2 {
		t.Fatalf("allocateRunID returned duplicate id %q", id1)
	}
}

// TestLifecycleBroadcast verifies subscribers receive lifecycle events and that
// unsubscribing closes the channel.
func TestLifecycleBroadcast(t *testing.T) {
	r, _ := newTestRunner(t)
	ch, cancel := r.SubscribeLifecycle()

	r.emitLifecycle(LifecycleEvent{Type: "created", RunID: "run-a", Status: StatusRunning})
	select {
	case e := <-ch:
		if e.Type != "created" || e.RunID != "run-a" {
			t.Fatalf("unexpected event %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive lifecycle event")
	}

	cancel()
	if _, open := <-ch; open {
		t.Fatal("channel should be closed after cancel")
	}
}

// TestPersistStateRoundTripJSON guards the on-disk schema (fields the SPA and
// DiffMind discovery rely on).
func TestPersistStateRoundTripJSON(t *testing.T) {
	r, base := newTestRunner(t)
	st := State{RunID: "rt", Status: StatusFailed, RepoPath: "/r", Error: "boom", StartedAt: time.Now().UTC()}
	r.persistState(st)
	b, err := os.ReadFile(filepath.Join(base, "rt", stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	for _, k := range []string{"run_id", "status", "repo_path", "error"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("run_state.json missing field %q", k)
		}
	}
}
