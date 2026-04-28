package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/reconcile"
	"github.com/mohammad-safakhou/diffmind/internal/snapshot"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// orchestrator wires the six pipeline stages together and owns the shared
// OpenCode session (when enabled). It keeps the "thin orchestrator" contract
// from the plan: no business logic, only stage sequencing and lifecycle.
//
// repoPath is the user-supplied path; it is the value reported in artifacts.
// sourceSessionDir is the resolved git root or repoPath, against which the
// snapshot is taken. sessionDir is what we hand to OpenCode and is ALWAYS
// the snapshot path (not the user's filesystem). snap is non-nil while the
// pipeline is running and is removed by Close().
type orchestrator struct {
	cfg              config.Config
	repoPath         string
	sourceSessionDir string
	sessionDir       string
	subDir           string
	snap             *snapshot.Snapshot
	oc               openCodeAPI
	pauser           pauseHandler // optional: only set when oc is *opencode.Client
	wd               *watchdog    // optional: nil if pauser is nil

	sessionMu       sync.Mutex
	sharedSessionID string
}

// Run is the public entrypoint used by internal/app. It returns an aggregated
// Result or an error if the pipeline could not even get started.
func Run(ctx context.Context, cfg config.Config, repoPath string, oc openCodeAPI) (Result, error) {
	if oc == nil || !oc.Enabled() {
		return Result{}, fmt.Errorf("opencode is required for extraction")
	}
	if cfg.Runtime.Workers <= 0 {
		cfg.Runtime.Workers = 16
	}
	if cfg.Runtime.MaxCatalogItems <= 0 {
		cfg.Runtime.MaxCatalogItems = 80
	}

	sourceSessionDir, subDir := detectMonorepo(repoPath)

	// Materialize an isolated snapshot of the source tree. OpenCode sessions
	// can edit/create/delete files inside the directory they are bound to;
	// pointing them at the user's repo would risk concurrent writes between
	// our parallel workers and could mutate the user's files. The snapshot
	// is a real, independent copy under a random temp dir; we always remove
	// it on the way out, even on error.
	snap, err := snapshot.Create(sourceSessionDir, "")
	if err != nil {
		return Result{}, fmt.Errorf("snapshot: %w", err)
	}

	o := &orchestrator{
		cfg:              cfg,
		repoPath:         repoPath,
		sourceSessionDir: sourceSessionDir,
		sessionDir:       snap.Path,
		subDir:           subDir,
		snap:             snap,
		oc:               oc,
	}
	// Wire the pause handler so the watchdog can auto-reply to
	// permission/clarification prompts. We accept either:
	//   1. the real opencode client, via the pauseBridge adapter, OR
	//   2. any fake that already implements pauseHandler (used in tests).
	// Test fakes that implement only openCodeAPI don't get a watchdog, which
	// is fine — they cannot pause.
	switch v := oc.(type) {
	case *opencode.Client:
		o.pauser = newPauseBridge(v)
	case pauseHandler:
		o.pauser = v
	}
	if o.pauser != nil {
		o.wd = newWatchdog(o.pauser, o.sessionDir, 2*time.Second)
		o.wd.Start(ctx)
	}
	defer func() {
		if o.wd != nil {
			o.wd.Stop()
		}
	}()
	defer o.closeSharedSession()
	defer func() {
		if err := snap.Close(); err != nil {
			util.Warn("agents.orchestrator", "snapshot close failed", map[string]any{"error": err})
		}
	}()

	progress := newProgressReporter()
	defer progress.Close()

	util.Info("agents.orchestrator", "multi-step pipeline starting", map[string]any{
		"repo": repoPath, "source_session_dir": sourceSessionDir, "snapshot": snap.Path, "sub_dir": subDir,
		"workers": cfg.Runtime.Workers, "max_catalog_items": cfg.Runtime.MaxCatalogItems,
		"reuse_session": cfg.Runtime.ReuseOpenCodeSession, "min_confidence": cfg.Quality.MinConfidence,
	})

	warnings := make([]string, 0)
	unresolved := make([]model.UnresolvedItem, 0)
	start := time.Now()

	// --- Stage 0: repo facts ---
	progress.StartPhase("repo_facts", 1, 0, 10, "Collecting a compact tech-stack snapshot of the repository.")
	rf, err := o.runRepoFacts(ctx)
	if err != nil {
		warnings = append(warnings, "repo_facts extraction failed: "+err.Error())
	}
	progress.Advance()
	progress.CompletePhase()

	// --- Stage 1: per-objective discovery ---
	allObjectives := objectives.Default()
	progress.StartPhase("discovery", len(allObjectives), 10, 35, "Discovering exposures and dependencies per objective in parallel.")
	discovery := o.runDiscovery(ctx, allObjectives, rf, progress.Advance)
	progress.CompletePhase()

	// Collect seeds and per-objective warnings.
	seeds := make([]detailJob, 0)
	exposureObjectives := map[string]objectives.Objective{}
	for _, d := range discovery {
		if d.Err != nil {
			warnings = append(warnings, "discovery failed for "+d.Objective.ID+": "+d.Err.Error())
			unresolved = append(unresolved, model.UnresolvedItem{
				Kind: d.Objective.Kind, Type: d.Objective.Type, Name: d.Objective.Description,
				ReasonCode: "agent_failure", Reason: d.Err.Error(),
			})
			continue
		}
		if d.Objective.Kind == model.KindExposure {
			exposureObjectives[d.Objective.Type] = d.Objective
		}
		for _, it := range d.Items {
			seeds = append(seeds, detailJob{Objective: d.Objective, Seed: it})
		}
	}
	util.Info("agents.orchestrator", "discovery completed", map[string]any{"seeds": len(seeds)})

	// --- Stage 2: confidence-gated re-examination ---
	var reexamined []detailJob
	if !o.cfg.Runtime.SkipReexamination {
		// Estimate suspects for progress reporting.
		suspects := 0
		for _, s := range seeds {
			if _, _, needs := shouldReexamine(s.Objective, s.Seed, o.cfg.Quality.MinConfidence); needs {
				suspects++
			}
		}
		progress.StartPhase("reexamination", suspects, 35, 45, "Re-asking the model to confirm or reject low-signal candidates.")
		cleaned, unresolvedRe := o.runReexamination(ctx, seeds, rf, progress.Advance)
		progress.CompletePhase()
		reexamined = cleaned
		unresolved = append(unresolved, unresolvedRe...)
	} else {
		reexamined = seeds
		util.Info("agents.orchestrator", "stage 2 re-examination skipped by config", nil)
	}

	// --- Stage 3: detail enrichment ---
	progress.StartPhase("detail", len(reexamined), 45, 70, "Enriching each verified entity with evidence, IO contract, and details.")
	details := o.runDetailBatch(ctx, reexamined, rf, progress.Advance)
	progress.CompletePhase()

	exposures := make([]model.Exposure, 0)
	dependencies := make([]model.Dependency, 0)
	for _, d := range details {
		if d.Err != nil {
			warnings = append(warnings, "detail enrichment failed for "+d.Objective.ID+": "+d.Err.Error())
			unresolved = append(unresolved, model.UnresolvedItem{
				Kind: d.Objective.Kind, Type: d.Objective.Type, Name: d.Objective.Description,
				ReasonCode: "agent_failure", Reason: d.Err.Error(),
			})
			continue
		}
		if d.Item == nil {
			continue
		}
		base, ur := toBase(o.repoPath, d.Objective.Kind, *d.Item, o.cfg.Quality.MinConfidence)
		if ur != nil {
			unresolved = append(unresolved, *ur)
			continue
		}
		if d.Objective.Kind == model.KindExposure {
			exposures = append(exposures, model.Exposure{BaseEntity: base})
		} else {
			dependencies = append(dependencies, model.Dependency{BaseEntity: base})
		}
	}
	util.Info("agents.orchestrator", "detail completed", map[string]any{
		"exposures": len(exposures), "dependencies": len(dependencies),
	})

	// Reconcile entities BEFORE connection mapping so the catalog is dedup'd.
	exposures = reconcile.DedupeExposures(exposures)
	dependencies = reconcile.DedupeDependencies(dependencies)

	// --- Stage 4: connection mapping ---
	progress.StartPhase("connections", len(exposures), 70, 90, "Mapping conditional exposure-to-dependency paths per exposure.")
	conns, connUnresolved := o.runConnectionsBatch(ctx, exposures, dependencies, exposureObjectives, rf, progress.Advance)
	progress.CompletePhase()
	unresolved = append(unresolved, connUnresolved...)

	// --- Stage 5: reconcile/filter ---
	progress.StartPhase("reconcile", 1, 90, 98, "Reconciling entities and dropping orphan connections.")
	conns, orphanUnresolved := reconcile.FilterConnections(conns, exposures, dependencies)
	unresolved = append(unresolved, orphanUnresolved...)
	// Final ordering for determinism.
	sort.Slice(exposures, func(i, j int) bool { return exposures[i].ID < exposures[j].ID })
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })
	progress.Advance()
	progress.CompletePhase()

	result := Result{
		Exposures:    exposures,
		Dependencies: dependencies,
		Connections:  conns,
		Unresolved:   reconcile.DedupeUnresolved(unresolved),
		Warnings:     reconcile.DedupeWarnings(warnings),
	}
	util.Info("agents.orchestrator", "pipeline completed", map[string]any{
		"exposures":    len(result.Exposures),
		"dependencies": len(result.Dependencies),
		"connections":  len(result.Connections),
		"unresolved":   len(result.Unresolved),
		"warnings":     len(result.Warnings),
		"elapsed_ms":   time.Since(start).Milliseconds(),
	})
	return result, nil
}

// ----------------------------------------------------------------------------
// Session + prompting helpers.
// ----------------------------------------------------------------------------

// pathMapper returns the (cached) helper that rewrites snapshot-relative
// paths back to source-relative paths in agent responses.
func (o *orchestrator) pathMapper() *pathMapper {
	if o.snap == nil {
		return nil
	}
	return newPathMapper(o.snap.Path, o.snap.SourcePath)
}

// promptAgent is the single point where every stage talks to the OpenCode
// server. It owns session lifecycle (shared vs per-call) and logs role+size
// metrics so the run is observable. On prompt failure (timeout, context
// cancel, or server error) it best-effort aborts the session so the server
// is not left holding a paused agent.
func (o *orchestrator) promptAgent(ctx context.Context, role, prompt string, schema map[string]any) (map[string]any, error) {
	sessionID, cleanupFn, err := o.acquireSession(ctx, role)
	if err != nil {
		return nil, err
	}
	util.Trace("agents.agent", "prompt start", map[string]any{"role": role, "session_id": sessionID, "prompt_len": len(prompt)})
	payload, err := o.oc.PromptStructured(ctx, sessionID, o.sessionDir, prompt, schema)
	if err != nil {
		// Best-effort abort: if the call timed out or was cancelled, the
		// server-side session may still be running (or paused waiting on a
		// permission request). Aborting frees server resources and lets
		// the watchdog reclaim the session.
		o.bestEffortAbort(role, sessionID)
		if cleanupFn != nil {
			cleanupFn()
		}
		return nil, fmt.Errorf("%s prompt: %w", role, err)
	}
	if cleanupFn != nil {
		cleanupFn()
	}
	util.Trace("agents.agent", "prompt ok", map[string]any{"role": role, "session_id": sessionID})
	return payload, nil
}

// bestEffortAbort tries to abort a session via the pause handler. We use a
// fresh, short-lived context so a cancelled parent context doesn't prevent
// us from cleaning up.
func (o *orchestrator) bestEffortAbort(role, sessionID string) {
	if o.pauser == nil || sessionID == "" {
		return
	}
	abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.pauser.AbortSession(abortCtx, sessionID, o.sessionDir); err != nil {
		util.Debug("agents.agent", "abort session failed", map[string]any{"role": role, "session_id": sessionID, "error": err})
		return
	}
	util.Trace("agents.agent", "session aborted", map[string]any{"role": role, "session_id": sessionID})
}

// acquireSession returns a session id and an optional cleanup function.
// Cleanup is a no-op for shared sessions; per-call sessions are deleted by a
// deferred goroutine (with configurable delay) when cleanup is enabled.
// New session ids are registered with the watchdog so it can auto-reply to
// any permission / clarification prompt the agent might emit.
func (o *orchestrator) acquireSession(ctx context.Context, role string) (string, func(), error) {
	if o.cfg.Runtime.ReuseOpenCodeSession {
		o.sessionMu.Lock()
		defer o.sessionMu.Unlock()
		if strings.TrimSpace(o.sharedSessionID) != "" {
			return o.sharedSessionID, nil, nil
		}
		sid, err := o.oc.CreateSession(ctx, o.sessionDir)
		if err != nil {
			return "", nil, fmt.Errorf("%s create shared session: %w", role, err)
		}
		o.sharedSessionID = sid
		o.wd.Track(sid)
		util.Debug("agents.agent", "shared session created", map[string]any{"role": role, "session_id": sid})
		return sid, nil, nil
	}
	sid, err := o.oc.CreateSession(ctx, o.sessionDir)
	if err != nil {
		return "", nil, fmt.Errorf("%s create session: %w", role, err)
	}
	o.wd.Track(sid)
	cleanup := func() { o.maybeScheduleDelete(role, sid) }
	return sid, cleanup, nil
}

func (o *orchestrator) maybeScheduleDelete(role, sessionID string) {
	if !o.cfg.Runtime.CleanupOpenCodeSessions || strings.TrimSpace(sessionID) == "" {
		return
	}
	delay := o.cfg.Runtime.OpenCodeDeleteDelaySec
	if delay <= 0 {
		delay = 5
	}
	go func() {
		time.Sleep(time.Duration(delay) * time.Second)
		if err := o.oc.DeleteSession(context.Background(), sessionID, o.sessionDir); err != nil {
			util.Warn("agents.agent", "session delete failed", map[string]any{"role": role, "session_id": sessionID, "error": err})
			return
		}
		o.wd.Untrack(sessionID)
		util.Trace("agents.agent", "session deleted", map[string]any{"role": role, "session_id": sessionID})
	}()
}

func (o *orchestrator) closeSharedSession() {
	if !o.cfg.Runtime.ReuseOpenCodeSession || !o.cfg.Runtime.CleanupOpenCodeSessions {
		return
	}
	o.sessionMu.Lock()
	sid := strings.TrimSpace(o.sharedSessionID)
	o.sharedSessionID = ""
	o.sessionMu.Unlock()
	if sid == "" {
		return
	}
	if err := o.oc.DeleteSession(context.Background(), sid, o.sessionDir); err != nil {
		util.Warn("agents.agent", "shared session delete failed", map[string]any{"session_id": sid, "error": err})
	}
	o.wd.Untrack(sid)
}

// ----------------------------------------------------------------------------
// Monorepo detection.
// ----------------------------------------------------------------------------

// detectMonorepo walks upward from repoPath looking for a .git directory.
// If repoPath is itself a git repo (or no git root is found) it returns
// (repoPath, ""). Otherwise it returns the git root as the session directory
// and the relative sub-path that the prompts should constrain scope to.
func detectMonorepo(repoPath string) (string, string) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return repoPath, ""
	}
	if _, err := os.Stat(filepath.Join(absPath, ".git")); err == nil {
		return repoPath, ""
	}
	dir := absPath
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return repoPath, ""
		}
		if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
			rel, err := filepath.Rel(parent, absPath)
			if err == nil {
				return parent, rel
			}
			return repoPath, ""
		}
		dir = parent
	}
}

// mustJSON is used only for log enrichment; it never fails in practice
// because we only pass marshalable inputs.
func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ = mustJSON // kept for optional debug traces
