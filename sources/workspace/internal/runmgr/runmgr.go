// Package runmgr is the DiffMind graph-run manager. It wraps the orchestrator
// pipeline as a cancellable background job, persists run state via the store,
// streams progress over an in-memory hub (+ events.jsonl on disk), and imposes
// no concurrency limit: any number of graph runs may execute at once.
package runmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/store"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Manager owns all in-flight and recently-finished graph runs.
type Manager struct {
	store           *store.Store
	log             *util.Logger
	diffmindRunsDir string

	mu   sync.Mutex
	runs map[string]*activeRun // key = pid/runID
}

// activeRun holds the live state of one run.
type activeRun struct {
	mu      sync.Mutex
	seq     int
	history []Event
	subs    map[chan Event]struct{}
	file    *os.File
	enc     *json.Encoder
	cancel  context.CancelFunc
	doneCh  chan struct{}
	done    bool
}

// New constructs a Manager. diffmindRunsDir is the central DiffMind runs
// directory graph runs read artifacts from.
func New(st *store.Store, log *util.Logger, diffmindRunsDir string) *Manager {
	return &Manager{
		store:           st,
		log:             log,
		diffmindRunsDir: diffmindRunsDir,
		runs:            map[string]*activeRun{},
	}
}

func key(pid, runID string) string { return pid + "/" + runID }

// Start creates a graph run, persists it, and launches the pipeline in the
// background. It returns the created run manifest immediately.
func (m *Manager) Start(pid string, repos []store.RunRepoRef, options map[string]any) (*store.RunManifest, error) {
	if len(repos) == 0 {
		return nil, fmt.Errorf("at least one repo is required")
	}
	manifest, err := m.store.CreateRun(pid, store.RunManifest{Repos: repos, Options: options, Status: store.RunRunning})
	if err != nil {
		return nil, err
	}

	runDir := m.store.RunDir(pid, manifest.ID)
	f, err := os.OpenFile(filepath.Join(runDir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open events log: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ar := &activeRun{
		subs:   map[chan Event]struct{}{},
		file:   f,
		enc:    json.NewEncoder(f),
		cancel: cancel,
		doneCh: make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[key(pid, manifest.ID)] = ar
	m.mu.Unlock()

	go m.execute(ctx, pid, *manifest, ar)
	return manifest, nil
}

// execute runs the pipeline and records terminal state.
func (m *Manager) execute(ctx context.Context, pid string, manifest store.RunManifest, ar *activeRun) {
	defer close(ar.doneCh)

	ar.emit(Event{Type: "run_started", At: time.Now().UTC(), Message: fmt.Sprintf("graph run %s started", manifest.ID)})

	cfg, warn, err := m.buildConfig(pid, manifest)
	for _, w := range warn {
		ar.emit(Event{Type: "log", Status: "warn", Message: w, At: time.Now().UTC()})
	}
	if err != nil {
		m.finish(pid, manifest, ar, ctx, nil, err)
		return
	}

	client := buildClient(cfg)
	pipeline := orchestrator.NewPipeline(cfg, client, m.log)
	pipeline.Progress = func(ev orchestrator.ProgressEvent) {
		typ := "phase_" + ev.Status
		switch ev.Status {
		case "started", "completed", "failed":
			typ = "phase_" + ev.Status
		default:
			typ = "log"
		}
		ar.emit(Event{Type: typ, Stage: ev.Stage, Status: ev.Status, Message: ev.Message, At: time.Now().UTC()})
	}

	result, err := pipeline.RunCtx(ctx)
	m.finish(pid, manifest, ar, ctx, result, err)
}

// finish records terminal state, persists the manifest, emits the terminal
// event, and tears down the run's resources.
func (m *Manager) finish(pid string, manifest store.RunManifest, ar *activeRun, ctx context.Context, result *orchestrator.RunResult, err error) {
	manifest.FinishedAt = time.Now().UTC()
	var termEvent string
	switch {
	case ctx.Err() != nil:
		manifest.Status = store.RunCancelled
		termEvent = "run_cancelled"
		if err != nil {
			manifest.Error = err.Error()
		}
	case err != nil:
		manifest.Status = store.RunFailed
		manifest.Error = err.Error()
		termEvent = "run_failed"
	default:
		manifest.Status = store.RunCompleted
		termEvent = "run_completed"
		if result != nil {
			manifest.ServiceCount = result.ServiceCount
			manifest.EdgeCount = result.EdgeCount
		}
	}
	if e := m.store.SaveRun(pid, manifest); e != nil {
		m.log.Error("save run manifest failed", "error", e.Error())
	}
	ar.emit(Event{Type: termEvent, Status: manifest.Status, At: manifest.FinishedAt, Message: manifest.Error})

	ar.mu.Lock()
	ar.done = true
	if ar.file != nil {
		_ = ar.file.Close()
		ar.file = nil
	}
	for ch := range ar.subs {
		close(ch)
	}
	ar.subs = map[chan Event]struct{}{}
	ar.mu.Unlock()

	m.log.Info("graph run finished", "project", pid, "run", manifest.ID, "status", manifest.Status)
}

// buildConfig assembles a pipeline config from the project, its repos, the
// selected DiffMind runs, and the effective OpenCode settings.
func (m *Manager) buildConfig(pid string, manifest store.RunManifest) (*config.Config, []string, error) {
	var warnings []string
	project, err := m.store.GetProject(pid)
	if err != nil {
		return nil, warnings, err
	}

	global, err := config.LoadGlobal()
	if err != nil {
		warnings = append(warnings, "failed to load global config: "+err.Error())
		global = &config.GlobalConfig{}
	}

	oc := global.OpenCode
	if project.OpenCode != nil {
		oc = mergeOpenCode(oc, *project.OpenCode)
	}

	cfg := &config.Config{
		OpenCode:   oc,
		Blueprints: config.BlueprintsConfig{Dirs: []string{m.store.BlueprintsDir(pid)}},
		Artifacts:  config.ArtifactsConfig{BaseDir: m.store.RunDir(pid, manifest.ID)},
	}

	for _, ref := range manifest.Repos {
		repo, err := m.store.GetRepo(pid, ref.RepoID)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("repo %s not found; skipping", ref.RepoID))
			continue
		}
		entry := config.RepoEntry{Name: repo.Name, Path: repo.Path}
		if ref.DiffMindRunID != "" {
			entry.DiffMindArtifacts = filepath.Join(m.diffmindRunsDir, ref.DiffMindRunID)
		}
		if repo.Kind == "infra_repo" {
			cfg.Repos.InfraRepos = append(cfg.Repos.InfraRepos, entry)
		} else {
			cfg.Repos.ServiceRepos = append(cfg.Repos.ServiceRepos, entry)
		}
	}
	if len(cfg.Repos.ServiceRepos) == 0 && len(cfg.Repos.InfraRepos) == 0 {
		return nil, warnings, fmt.Errorf("no valid repos resolved for run")
	}
	return cfg, warnings, nil
}

// Cancel stops an in-flight run. Unknown/finished runs are a no-op.
func (m *Manager) Cancel(pid, runID string) {
	m.mu.Lock()
	ar := m.runs[key(pid, runID)]
	m.mu.Unlock()
	if ar == nil {
		return
	}
	ar.mu.Lock()
	done := ar.done
	cancel := ar.cancel
	ar.mu.Unlock()
	if done || cancel == nil {
		return
	}
	// Reflect the cancelling state immediately so the UI updates.
	if mft, err := m.store.GetRun(pid, runID); err == nil && mft.Status == store.RunRunning {
		mft.Status = store.RunCancelling
		_ = m.store.SaveRun(pid, *mft)
	}
	cancel()
}

// WaitFor blocks until a run finishes (test helper).
func (m *Manager) WaitFor(pid, runID string) {
	m.mu.Lock()
	ar := m.runs[key(pid, runID)]
	m.mu.Unlock()
	if ar != nil {
		<-ar.doneCh
	}
}

// IsActive reports whether a run is currently executing.
func (m *Manager) IsActive(pid, runID string) bool {
	m.mu.Lock()
	ar := m.runs[key(pid, runID)]
	m.mu.Unlock()
	if ar == nil {
		return false
	}
	ar.mu.Lock()
	defer ar.mu.Unlock()
	return !ar.done
}

// Subscribe returns a channel of events for a run. It replays the run's history
// first (live in-memory for an active run, or events.jsonl for a finished one)
// then streams live events. The returned cancel function unsubscribes.
func (m *Manager) Subscribe(pid, runID string) (<-chan Event, func(), error) {
	m.mu.Lock()
	ar := m.runs[key(pid, runID)]
	m.mu.Unlock()

	out := make(chan Event, 256)

	if ar == nil {
		// Finished (or never in this process): replay from disk and close.
		go func() {
			defer close(out)
			m.replayFromDisk(pid, runID, out)
		}()
		return out, func() {}, nil
	}

	ar.mu.Lock()
	if ar.done {
		hist := append([]Event(nil), ar.history...)
		ar.mu.Unlock()
		go func() {
			defer close(out)
			for _, e := range hist {
				out <- e
			}
		}()
		return out, func() {}, nil
	}
	// Live: replay current history, then attach.
	live := make(chan Event, 256)
	ar.subs[live] = struct{}{}
	hist := append([]Event(nil), ar.history...)
	ar.mu.Unlock()

	stop := make(chan struct{})
	go func() {
		defer close(out)
		for _, e := range hist {
			select {
			case out <- e:
			case <-stop:
				return
			}
		}
		for {
			select {
			case e, ok := <-live:
				if !ok {
					return
				}
				select {
				case out <- e:
				case <-stop:
					return
				}
			case <-stop:
				return
			}
		}
	}()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			close(stop)
			ar.mu.Lock()
			delete(ar.subs, live)
			ar.mu.Unlock()
		})
	}
	return out, cancel, nil
}

func (m *Manager) replayFromDisk(pid, runID string, out chan<- Event) {
	data, err := os.ReadFile(filepath.Join(m.store.RunDir(pid, runID), "events.jsonl"))
	if err != nil {
		return
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var e Event
		if err := dec.Decode(&e); err != nil {
			return
		}
		out <- e
	}
}

// emit appends an event to history, persists it, and fans it out to live
// subscribers (non-blocking).
func (ar *activeRun) emit(e Event) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	if ar.done {
		return
	}
	ar.seq++
	e.Seq = ar.seq
	ar.history = append(ar.history, e)
	if ar.enc != nil {
		_ = ar.enc.Encode(e)
	}
	for ch := range ar.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func mergeOpenCode(base config.OpenCodeConfig, over store.OpenCodeConfig) config.OpenCodeConfig {
	if over.BaseURL != "" {
		base.BaseURL = over.BaseURL
	}
	if over.ProviderID != "" {
		base.ProviderID = over.ProviderID
	}
	if over.ModelID != "" {
		base.ModelID = over.ModelID
	}
	if over.Variant != "" {
		base.Variant = over.Variant
	}
	if over.Timeout != 0 {
		base.Timeout = over.Timeout
	}
	if over.Username != "" {
		base.Username = over.Username
	}
	if over.Password != "" {
		base.Password = over.Password
	}
	return base
}

func buildClient(cfg *config.Config) *opencode.Client {
	if cfg.OpenCode.BaseURL == "" {
		return nil
	}
	return opencode.NewClient(
		cfg.OpenCode.BaseURL,
		cfg.OpenCode.ProviderID,
		cfg.OpenCode.ModelID,
		cfg.OpenCode.Variant,
		cfg.OpenCode.Username,
		cfg.OpenCode.Password,
		time.Duration(cfg.OpenCode.Timeout)*time.Second,
	)
}
