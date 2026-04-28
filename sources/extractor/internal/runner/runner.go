// Package runner owns the singleton lifecycle of an active DiffMind run on
// a server. The dashboard (internal/ui) calls into Runner to start, observe,
// and cancel a run; only one run is allowed to be active at a time.
//
// Why a singleton: the OpenCode server is a single shared resource and we
// already saturate it with workers=16 LLM calls in flight; running multiple
// extractions in parallel would just contend on the same provider quota.
// Queueing is out of scope for v1.
package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/app"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/util"
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

// State is a snapshot of the singleton's current state, returned to UI
// callers polling /api/runs.
type State struct {
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	RepoPath   string    `json:"repo_path,omitempty"`
	RunDir     string    `json:"run_dir,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Runner is the singleton run manager.
type Runner struct {
	bus     *events.Bus
	baseDir string

	mu     sync.RWMutex
	state  State
	cancel context.CancelFunc
	doneCh chan struct{}
}

// New constructs a Runner. baseDir is the artifacts root (where each run
// gets its own subdirectory).
func New(baseDir string, bus *events.Bus) *Runner {
	return &Runner{bus: bus, baseDir: baseDir, state: State{Status: StatusIdle}}
}

// State returns the current snapshot.
func (r *Runner) State() State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Start kicks off a new run. Returns ErrBusy if another run is already
// active. The returned RunID is allocated synchronously so the UI can open
// the SSE stream immediately, even before the orchestrator emits its first
// event.
var ErrBusy = errors.New("runner: another run is already in progress")

// StartParams carries everything Start needs.
type StartParams struct {
	RepoPath string
	Config   config.Config
}

// Start launches a goroutine that runs the pipeline. The function returns
// as soon as the run has been registered and the events sink is wired.
func (r *Runner) Start(parent context.Context, p StartParams) (string, error) {
	r.mu.Lock()
	if r.state.Status == StatusRunning || r.state.Status == StatusCancelling {
		r.mu.Unlock()
		return "", ErrBusy
	}
	runID := time.Now().UTC().Format("20060102T150405Z")
	persistDir := ""
	if r.baseDir != "" {
		persistDir = filepath.Join(r.baseDir, runID)
	}
	sink, err := r.bus.StartRun(runID, persistDir)
	if err != nil {
		r.mu.Unlock()
		return "", fmt.Errorf("runner: start bus: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.doneCh = make(chan struct{})
	r.state = State{
		RunID:     runID,
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
		RepoPath:  p.RepoPath,
	}
	r.mu.Unlock()

	go r.execute(ctx, runID, sink, p)
	return runID, nil
}

// execute performs the actual work; it is the goroutine launched by Start.
func (r *Runner) execute(ctx context.Context, runID string, sink events.Sink, p StartParams) {
	defer close(r.doneCh)

	out, err := app.Run(ctx, app.RunInput{
		RepoPath: p.RepoPath,
		Config:   p.Config,
		Sink:     sink,
		RunID:    runID,
	})

	r.mu.Lock()
	r.state.FinishedAt = time.Now().UTC()
	r.state.RunDir = out.RunDir
	if ctx.Err() != nil {
		r.state.Status = StatusCancelled
		if err != nil {
			r.state.Error = err.Error()
		}
	} else if err != nil {
		r.state.Status = StatusFailed
		r.state.Error = err.Error()
	} else {
		r.state.Status = StatusCompleted
	}
	r.cancel = nil
	r.mu.Unlock()

	r.bus.FinishRun(runID)
	util.Info("runner", "run finished", map[string]any{"run_id": runID, "status": r.state.Status})
}

// Cancel asks the active run to stop. Safe to call when nothing is running.
func (r *Runner) Cancel() error {
	r.mu.Lock()
	if r.state.Status != StatusRunning {
		r.mu.Unlock()
		return nil
	}
	r.state.Status = StatusCancelling
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Wait blocks until the active run completes (or returns immediately if
// nothing is running). Useful for tests.
func (r *Runner) Wait() {
	r.mu.RLock()
	done := r.doneCh
	r.mu.RUnlock()
	if done == nil {
		return
	}
	<-done
}
