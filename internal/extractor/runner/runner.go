// Package runner owns the lifecycle of DiffMind runs on a server. The
// dashboard (internal/ui) calls into Runner to start, observe, cancel, and
// delete runs.
//
// Unlike the original singleton design, the Runner is a manager keyed by run
// ID: any number of runs may be active at once. Each run carries its own
// cancellation function and done channel, so cancelling or waiting on one run
// never affects another. Per-run state is mirrored to
// <baseDir>/<run_id>/run_state.json so it survives a process restart and can
// be reloaded on demand.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/app"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/config"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/events"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

// Status values surfaced to the UI.
const (
	StatusIdle       = "idle"
	StatusRunning    = "running"
	StatusCancelling = "cancelling"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
)

// stateFileName is the per-run state mirror written under the run directory.
const stateFileName = "run_state.json"

// State is a snapshot of a single run's status, returned to UI callers.
type State struct {
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	RepoPath   string    `json:"repo_path,omitempty"`
	RunDir     string    `json:"run_dir,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// LifecycleEvent is broadcast to aggregate (homepage) subscribers whenever a
// run is created, started, or reaches a terminal state. It is deliberately
// small: the homepage uses it only to know which rows to refresh.
type LifecycleEvent struct {
	Type     string    `json:"type"` // created | started | finished
	RunID    string    `json:"run_id"`
	Status   string    `json:"status"`
	RepoPath string    `json:"repo_path,omitempty"`
	At       time.Time `json:"at"`
}

// run is the per-run bookkeeping held in memory while a run is active (and
// briefly after it finishes, until the process exits).
type run struct {
	state  State
	cancel context.CancelFunc
	doneCh chan struct{}
}

// Runner is the multi-run manager.
type Runner struct {
	bus     *events.Bus
	baseDir string
	app     Application

	mu   sync.RWMutex
	runs map[string]*run

	subsMu sync.Mutex
	subs   map[chan LifecycleEvent]struct{}
}

// Application is the execution boundary owned by Runner. Tests and alternate
// frontends can provide an implementation without coupling lifecycle
// management to the concrete app package.
type Application interface {
	Run(context.Context, app.RunInput) (app.RunOutput, error)
}

type defaultApplication struct{}

func (defaultApplication) Run(ctx context.Context, input app.RunInput) (app.RunOutput, error) {
	return app.Run(ctx, input)
}

// New constructs a Runner. baseDir is the artifacts root (where each run gets
// its own subdirectory).
func New(baseDir string, bus *events.Bus) *Runner {
	return NewWithApplication(baseDir, bus, defaultApplication{})
}

func NewWithApplication(baseDir string, bus *events.Bus, application Application) *Runner {
	if application == nil {
		application = defaultApplication{}
	}
	return &Runner{
		bus:     bus,
		baseDir: baseDir,
		app:     application,
		runs:    map[string]*run{},
		subs:    map[chan LifecycleEvent]struct{}{},
	}
}

// StartParams carries everything Start needs.
type StartParams struct {
	RepoPath string
	Config   config.Config
}

// Start launches a new run in the background and returns its allocated run id.
// It never blocks on the work and never refuses a concurrent run.
func (r *Runner) Start(parent context.Context, p StartParams) (string, error) {
	runID := r.allocateRunID()
	persistDir := r.persistDir(runID)
	sink, err := r.bus.StartRun(runID, persistDir)
	if err != nil {
		return "", fmt.Errorf("runner: start bus: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	rn := &run{
		cancel: cancel,
		doneCh: make(chan struct{}),
		state: State{
			RunID:     runID,
			Status:    StatusRunning,
			StartedAt: time.Now().UTC(),
			RepoPath:  p.RepoPath,
			RunDir:    persistDir,
		},
	}

	r.mu.Lock()
	r.runs[runID] = rn
	r.mu.Unlock()

	r.persistState(rn.state)
	r.emitLifecycle(LifecycleEvent{Type: "created", RunID: runID, Status: StatusRunning, RepoPath: p.RepoPath, At: rn.state.StartedAt})
	r.emitLifecycle(LifecycleEvent{Type: "started", RunID: runID, Status: StatusRunning, RepoPath: p.RepoPath, At: rn.state.StartedAt})

	go r.execute(ctx, rn, runID, sink, p)
	return runID, nil
}

// execute is the goroutine launched by Start.
func (r *Runner) execute(ctx context.Context, rn *run, runID string, sink events.Sink, p StartParams) {
	defer close(rn.doneCh)

	out, err := r.app.Run(ctx, app.RunInput{
		RepoPath: p.RepoPath,
		Config:   p.Config,
		Sink:     sink,
		RunID:    runID,
	})
	r.finish(rn, runID, ctx, out.RunDir, err)
}

// finish records the terminal state of a run, persists it, notifies the bus,
// and broadcasts the lifecycle event. ctx is the run's context; a cancelled
// context maps to StatusCancelled regardless of the returned error.
func (r *Runner) finish(rn *run, runID string, ctx context.Context, runDir string, err error) {
	r.mu.Lock()
	rn.state.FinishedAt = time.Now().UTC()
	if runDir != "" {
		rn.state.RunDir = runDir
	}
	switch {
	case ctx.Err() != nil:
		rn.state.Status = StatusCancelled
		if err != nil {
			rn.state.Error = err.Error()
		}
	case err != nil:
		rn.state.Status = StatusFailed
		rn.state.Error = err.Error()
	default:
		rn.state.Status = StatusCompleted
	}
	rn.cancel = nil
	final := rn.state
	r.mu.Unlock()

	r.persistState(final)
	r.bus.FinishRun(runID)
	r.emitLifecycle(LifecycleEvent{Type: "finished", RunID: runID, Status: final.Status, RepoPath: final.RepoPath, At: final.FinishedAt})
	util.Info("runner", "run finished", map[string]any{"run_id": runID, "status": final.Status})
}

// Cancel asks a single run to stop. Unknown or already-finished runs are a
// no-op (nil error).
func (r *Runner) Cancel(runID string) error {
	r.mu.Lock()
	rn := r.runs[runID]
	if rn == nil || rn.state.Status != StatusRunning {
		r.mu.Unlock()
		return nil
	}
	rn.state.Status = StatusCancelling
	cancel := rn.cancel
	state := rn.state
	r.mu.Unlock()

	r.persistState(state)
	if cancel != nil {
		cancel()
	}
	return nil
}

// CancelAll cancels every running run. Useful for shutdown and tests.
func (r *Runner) CancelAll() {
	r.mu.RLock()
	ids := make([]string, 0, len(r.runs))
	for id := range r.runs {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	for _, id := range ids {
		_ = r.Cancel(id)
	}
}

// State returns the snapshot for a single run. It first checks in-memory runs,
// then falls back to the persisted run_state.json so a fresh process (or a run
// that finished before this Runner existed) still resolves.
func (r *Runner) State(runID string) (State, bool) {
	r.mu.RLock()
	rn := r.runs[runID]
	if rn != nil {
		st := rn.state
		r.mu.RUnlock()
		return st, true
	}
	r.mu.RUnlock()

	if st, err := LoadState(r.baseDir, runID); err == nil {
		return st, true
	}
	return State{}, false
}

// States returns a snapshot of every in-memory run, newest first.
func (r *Runner) States() []State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]State, 0, len(r.runs))
	for _, rn := range r.runs {
		out = append(out, rn.state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// ActiveIDs returns the ids of runs that are currently running or cancelling.
func (r *Runner) ActiveIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.runs))
	for id, rn := range r.runs {
		if rn.state.Status == StatusRunning || rn.state.Status == StatusCancelling {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Wait blocks until every currently-active run has finished. Safe to call when
// nothing is running.
func (r *Runner) Wait() {
	r.mu.RLock()
	dones := make([]chan struct{}, 0, len(r.runs))
	for _, rn := range r.runs {
		if rn.doneCh != nil {
			dones = append(dones, rn.doneCh)
		}
	}
	r.mu.RUnlock()
	for _, d := range dones {
		<-d
	}
}

// WaitFor blocks until the given run has finished (or returns immediately if it
// is unknown).
func (r *Runner) WaitFor(runID string) {
	r.mu.RLock()
	rn := r.runs[runID]
	var done chan struct{}
	if rn != nil {
		done = rn.doneCh
	}
	r.mu.RUnlock()
	if done != nil {
		<-done
	}
}

// SubscribeLifecycle registers an aggregate-event subscriber. The returned
// cancel function unregisters it and closes the channel.
func (r *Runner) SubscribeLifecycle() (<-chan LifecycleEvent, func()) {
	ch := make(chan LifecycleEvent, 64)
	r.subsMu.Lock()
	r.subs[ch] = struct{}{}
	r.subsMu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			r.subsMu.Lock()
			delete(r.subs, ch)
			r.subsMu.Unlock()
			close(ch)
		})
	}
	return ch, cancel
}

// emitLifecycle fans an event out to every subscriber, dropping it for any
// subscriber whose buffer is full (the homepage re-fetches on reconnect).
func (r *Runner) emitLifecycle(e LifecycleEvent) {
	r.subsMu.Lock()
	defer r.subsMu.Unlock()
	for ch := range r.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// allocateRunID generates a timestamp-based id, appending a numeric suffix if
// the second-resolution timestamp collides with an in-memory or on-disk run.
func (r *Runner) allocateRunID() string {
	base := time.Now().UTC().Format("20060102T150405Z")
	candidate := base
	for i := 2; r.runIDTaken(candidate); i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

func (r *Runner) runIDTaken(runID string) bool {
	r.mu.RLock()
	_, inMem := r.runs[runID]
	r.mu.RUnlock()
	if inMem {
		return true
	}
	if r.baseDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(r.baseDir, runID))
	return err == nil
}

func (r *Runner) persistDir(runID string) string {
	if r.baseDir == "" {
		return ""
	}
	return filepath.Join(r.baseDir, runID)
}

// persistState mirrors a run's state to <runDir>/run_state.json. Best-effort:
// a write failure is logged but never aborts a run.
func (r *Runner) persistState(st State) {
	dir := r.persistDir(st.RunID)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		util.Warn("runner", "persist state mkdir failed", map[string]any{"run_id": st.RunID, "error": err})
		return
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), b, 0o644); err != nil {
		util.Warn("runner", "persist state write failed", map[string]any{"run_id": st.RunID, "error": err})
	}
}

// LoadState reads the persisted run_state.json for a run. Returns an error if
// the file is missing or unparseable.
func LoadState(baseDir, runID string) (State, error) {
	if baseDir == "" {
		return State{}, fmt.Errorf("runner: empty base dir")
	}
	b, err := os.ReadFile(filepath.Join(baseDir, runID, stateFileName))
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, fmt.Errorf("runner: parse run_state.json: %w", err)
	}
	if st.RunID == "" {
		st.RunID = runID
	}
	return st, nil
}
