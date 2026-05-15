package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
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
	verbose          verbosePrompter // optional: only set when oc is *opencode.Client
	pauser           pauseHandler    // optional: only set when oc is *opencode.Client
	wd               *watchdog       // optional: nil if pauser is nil
	sink             events.Sink     // never nil (NoopSink fallback)
	captureDir       string          // optional: dir where prompt/response files are written
	runDir           string          // optional: artifact root for state + failure report

	sessionMu       sync.Mutex
	sharedSessionID string
}

// emit is the convenience wrapper used by every stage to publish a single
// event. We always go through this so callers don't have to deal with nil
// sinks or non-blocking semantics.
func (o *orchestrator) emit(e events.Event) {
	if o.sink == nil {
		return
	}
	o.sink.Emit(e)
}

// RunOptions carries optional dependencies for Run. Sink is the live event
// stream consumed by the dashboard; CaptureDir is a directory under the run
// dir where each LLM call's prompt + response is persisted for
// click-to-view; RunDir, when set, is the run's artifact directory and
// enables on-disk state persistence + failure report writes for retry.
//
// Resume lets a retry command resume a previously-failed run by
// fast-forwarding past the stages that already completed successfully
// and re-using the snapshot directory the failed run left behind.
// SnapshotPath, when non-empty, instructs the orchestrator to skip
// snapshot creation and use the supplied directory as the session
// directory directly. ResumeFromDir, when non-empty, is the path to
// the previous run's state directory (typically <runDir>/state); the
// orchestrator loads what it can from there and skips matching stages.
type RunOptions struct {
	Sink          events.Sink
	CaptureDir    string
	RunDir        string
	ResumeFromDir string
	SnapshotPath  string
}

// Run is the public entrypoint used by internal/app. It returns an aggregated
// Result or an error if the pipeline could not even get started. This thin
// wrapper preserves the original signature; new callers should use RunWith.
func Run(ctx context.Context, cfg config.Config, repoPath string, oc openCodeAPI) (Result, error) {
	return RunWith(ctx, cfg, repoPath, oc, RunOptions{})
}

// RunWith is the full entrypoint that accepts a live event sink and an
// optional capture directory. Callers from the CLI typically pass an empty
// RunOptions; the dashboard wires in a live Sink and a per-run capture dir.
func RunWith(ctx context.Context, cfg config.Config, repoPath string, oc openCodeAPI, opts RunOptions) (Result, error) {
	if oc == nil || !oc.Enabled() {
		return Result{}, fmt.Errorf("opencode is required for extraction")
	}
	if cfg.Runtime.Workers <= 0 {
		cfg.Runtime.Workers = 6
	}
	if cfg.Runtime.MaxCatalogItems <= 0 {
		cfg.Runtime.MaxCatalogItems = 80
	}
	sink := opts.Sink
	if sink == nil {
		sink = events.NoopSink{}
	}

	sourceSessionDir, subDir := detectMonorepo(repoPath)

	// Materialize an isolated snapshot of the source tree, OR re-attach
	// to the snapshot left behind by a previously-failed run when the
	// caller is invoking us via `diffmind retry`. Re-attaching skips
	// the (potentially large) byte-for-byte copy and guarantees the
	// retry sees the exact same working tree the failing run saw.
	var snap *snapshot.Snapshot
	if strings.TrimSpace(opts.SnapshotPath) != "" {
		s, err := snapshot.Reattach(sourceSessionDir, opts.SnapshotPath)
		if err != nil {
			return Result{}, fmt.Errorf("reattach snapshot %q: %w", opts.SnapshotPath, err)
		}
		snap = s
	} else {
		s, err := snapshot.Create(sourceSessionDir, "")
		if err != nil {
			return Result{}, fmt.Errorf("snapshot: %w", err)
		}
		snap = s
	}

	o := &orchestrator{
		cfg:              cfg,
		repoPath:         repoPath,
		sourceSessionDir: sourceSessionDir,
		sessionDir:       snap.Path,
		subDir:           subDir,
		snap:             snap,
		oc:               oc,
		sink:             sink,
		captureDir:       opts.CaptureDir,
		runDir:           opts.RunDir,
	}
	if o.captureDir != "" {
		_ = os.MkdirAll(o.captureDir, 0o755)
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
		o.verbose = newVerboseBridge(v)
	case pauseHandler:
		o.pauser = v
	}
	// Also accept fakes that implement verbosePrompter directly.
	if o.verbose == nil {
		if vp, ok := oc.(verbosePrompter); ok {
			o.verbose = vp
		}
	}
	if o.pauser != nil {
		o.wd = newWatchdog(o.pauser, o.sessionDir, 2*time.Second)
		o.wd.SetSink(sink)
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
	progress.SetSink(sink)
	defer progress.Close()

	util.Info("agents.orchestrator", "multi-step pipeline starting", map[string]any{
		"repo": repoPath, "source_session_dir": sourceSessionDir, "snapshot": snap.Path, "sub_dir": subDir,
		"workers": cfg.Runtime.Workers, "max_catalog_items": cfg.Runtime.MaxCatalogItems,
		"reuse_session": cfg.Runtime.ReuseOpenCodeSession, "min_confidence": cfg.Quality.MinConfidence,
	})
	o.emit(events.Event{
		Kind:    events.KindRunStarted,
		Message: "extraction pipeline started",
		Payload: map[string]any{
			"repo":               repoPath,
			"snapshot":           snap.Path,
			"sub_dir":            subDir,
			"workers":            cfg.Runtime.Workers,
			"max_catalog_items":  cfg.Runtime.MaxCatalogItems,
			"min_confidence":     cfg.Quality.MinConfidence,
			"skip_reexamination": cfg.Runtime.SkipReexamination,
		},
	})

	warnings := make([]string, 0)
	unresolved := make([]model.UnresolvedItem, 0)
	state := IntermediateState{ExposureObjs: map[string]string{}}
	start := time.Now()

	// Load any previously-saved stage state for resume.
	resumeRf, resumeSeeds, resumeExpObjs, resumeReexam, resumeExposures, resumeDeps := o.loadResumeState(opts.ResumeFromDir)

	// haltFailure builds the partial result we hand back to internal/app
	// when a stage fails. It always preserves the snapshot path and the
	// state captured up to (but not including) the failing stage so the
	// retry command has a consistent base to resume from.
	haltFailure := func(stage, jobID, objectiveID, entityName string, err error, extra map[string]any) (Result, error) {
		f := &Failure{
			Stage:        stage,
			JobID:        jobID,
			ObjectiveID:  objectiveID,
			EntityName:   entityName,
			Error:        err.Error(),
			ErrorClass:   classifyError(err),
			HTTPStatus:   extractHTTPStatus(err.Error()),
			OccurredAt:   time.Now().UTC(),
			Extra:        extra,
			PromptPath:   o.captureFilePath(jobID, "prompt", "txt"),
			ResponsePath: o.captureFilePath(jobID, "response", "json"),
			SnapshotPath: o.snap.Path,
		}
		o.emit(events.Event{
			Kind: events.KindRunFailed, Status: events.StatusFailed, Stage: stage, JobID: jobID,
			Message: err.Error(),
			Payload: map[string]any{
				"stage":         stage,
				"job_id":        jobID,
				"objective_id":  objectiveID,
				"entity_name":   entityName,
				"error_class":   f.ErrorClass,
				"http_status":   f.HTTPStatus,
				"prompt_path":   f.PromptPath,
				"response_path": f.ResponsePath,
				"elapsed_ms":    time.Since(start).Milliseconds(),
			},
		})
		// The snapshot must be retained for retry: instruct the deferred
		// closer to keep the directory.
		o.snap.Retain()
		// Persist failure-report + intermediate state to disk so the
		// operator (and `diffmind retry`) have everything they need.
		o.writeFailureReport(f)
		o.persistStageState("failure_state.json", state)
		return Result{
			Exposures:    nil,
			Dependencies: nil,
			Connections:  nil,
			Unresolved:   reconcile.DedupeUnresolved(unresolved),
			Warnings:     reconcile.DedupeWarnings(warnings),
			Failure:      f,
			SnapshotPath: o.snap.Path,
			Intermediate: state,
		}, fmt.Errorf("%s stage failed at %s: %w", stage, jobID, err)
	}

	// --- Stage 0: repo facts ---
	var rf *repoFacts
	if resumeRf != nil {
		util.Info("agents.orchestrator", "resume: skipping repo_facts (loaded from state)", nil)
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "repo_facts", Status: events.StatusSkipped, Message: "resumed from state"})
		rf = resumeRf
	} else {
		progress.StartPhase("repo_facts", 1, 0, 10, "Collecting a compact tech-stack snapshot of the repository.")
		o.emit(events.Event{Kind: events.KindStageStarted, Stage: "repo_facts", Status: events.StatusRunning, Payload: map[string]any{"total": 1, "tip": "Collecting a compact tech-stack snapshot of the repository."}})
		got, err := o.runRepoFacts(ctx)
		progress.Advance()
		progress.CompletePhase()
		if err != nil {
			return haltFailure("repo_facts", "repo_facts", "", "", err, nil)
		}
		rf = got
		o.persistStageState("repo_facts.json", rf)
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "repo_facts", Status: events.StatusSuccess, Payload: map[string]any{"facts_present": rf != nil}})
	}
	state.RepoFacts = rf

	// --- Stage 1: per-objective discovery ---
	seeds := make([]detailJob, 0)
	exposureObjectives := map[string]objectives.Objective{}
	if resumeSeeds != nil {
		util.Info("agents.orchestrator", "resume: skipping discovery (loaded from state)", map[string]any{
			"seeds": len(resumeSeeds),
		})
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "discovery", Status: events.StatusSkipped, Message: "resumed from state"})
		seeds = resumeSeeds
		// Rebuild the type -> Objective map from the resumed seeds.
		for _, s := range seeds {
			if s.Objective.Kind == model.KindExposure {
				exposureObjectives[s.Objective.Type] = s.Objective
				state.ExposureObjs[s.Objective.Type] = s.Objective.ID
			}
		}
		// Honour any stored exposure_objectives map (the seed list may
		// not cover every type if discovery returned 0 items).
		for k, v := range resumeExpObjs {
			state.ExposureObjs[k] = v
		}
	} else {
		allObjectives := objectives.Default()
		progress.StartPhase("discovery", len(allObjectives), 10, 35, "Discovering exposures and dependencies per objective in parallel.")
		o.emit(events.Event{Kind: events.KindStageStarted, Stage: "discovery", Status: events.StatusRunning, Payload: map[string]any{"total": len(allObjectives), "tip": "Discovering exposures and dependencies per objective in parallel."}})
		for _, obj := range allObjectives {
			o.emit(events.Event{
				Kind: events.KindJobPending, Stage: "discovery", JobID: "discover." + obj.ID,
				Status:  events.StatusPending,
				Payload: map[string]any{"objective_id": obj.ID, "kind": string(obj.Kind), "type": obj.Type, "description": obj.Description},
			})
		}
		discovery := o.runDiscovery(ctx, allObjectives, rf, progress.Advance)
		progress.CompletePhase()

		var firstDiscoveryErr error
		var firstDiscoveryObj objectives.Objective
		for _, d := range discovery {
			if d.Err == nil {
				continue
			}
			if errors.Is(d.Err, context.Canceled) || errors.Is(d.Err, context.DeadlineExceeded) {
				continue
			}
			if firstDiscoveryErr == nil {
				firstDiscoveryErr = d.Err
				firstDiscoveryObj = d.Objective
			}
		}
		if firstDiscoveryErr != nil {
			o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "discovery", Status: events.StatusFailed})
			return haltFailure("discovery", "discover."+firstDiscoveryObj.ID, firstDiscoveryObj.ID, firstDiscoveryObj.Description, firstDiscoveryErr, nil)
		}
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "discovery", Status: events.StatusSuccess})

		for _, d := range discovery {
			if d.Objective.Kind == model.KindExposure {
				exposureObjectives[d.Objective.Type] = d.Objective
				state.ExposureObjs[d.Objective.Type] = d.Objective.ID
			}
			for _, it := range d.Items {
				seeds = append(seeds, detailJob{Objective: d.Objective, Seed: it})
			}
		}
		state.DiscoverySeeds = append([]detailJob(nil), seeds...)
		o.persistStageState("discovery.json", state.DiscoverySeeds)
		o.persistStageState("exposure_objectives.json", state.ExposureObjs)
		util.Info("agents.orchestrator", "discovery completed", map[string]any{
			"seeds": len(seeds), "total": len(discovery),
		})
	}
	state.DiscoverySeeds = append([]detailJob(nil), seeds...)

	// --- Stage 2: confidence-gated re-examination ---
	var reexamined []detailJob
	if resumeReexam != nil {
		util.Info("agents.orchestrator", "resume: skipping reexamination (loaded from state)", map[string]any{
			"clean": len(resumeReexam),
		})
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "reexamination", Status: events.StatusSkipped, Message: "resumed from state"})
		reexamined = resumeReexam
	} else if !o.cfg.Runtime.SkipReexamination {
		suspects := 0
		for i := range seeds {
			seedCopy := seeds[i].Seed
			if _, _, needs := shouldReexamine(seeds[i].Objective, &seedCopy, o.cfg.Quality.MinConfidence); needs {
				suspects++
			}
		}
		progress.StartPhase("reexamination", suspects, 35, 45, "Re-asking the model to confirm or reject low-signal candidates.")
		o.emit(events.Event{Kind: events.KindStageStarted, Stage: "reexamination", Status: events.StatusRunning, Payload: map[string]any{"total": suspects, "tip": "Re-asking the model to confirm or reject low-signal candidates."}})
		cleaned, unresolvedRe, reexamErr, reexamFailedSeed := o.runReexamination(ctx, seeds, rf, progress.Advance)
		progress.CompletePhase()
		if reexamErr != nil {
			o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "reexamination", Status: events.StatusFailed})
			return haltFailure("reexamination",
				"reexamine."+reexamFailedSeed.Obj.ID+"."+safeJobID(reexamFailedSeed.Seed.Name),
				reexamFailedSeed.Obj.ID, reexamFailedSeed.Seed.Name, reexamErr, nil)
		}
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "reexamination", Status: events.StatusSuccess})
		reexamined = cleaned
		unresolved = append(unresolved, unresolvedRe...)
	} else {
		reexamined = seeds
		util.Info("agents.orchestrator", "stage 2 re-examination skipped by config", nil)
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "reexamination", Status: events.StatusSkipped, Message: "skipped by config"})
	}
	state.ReexamSeeds = append([]detailJob(nil), reexamined...)
	o.persistStageState("reexamination.json", state.ReexamSeeds)

	// --- Stage 3: detail enrichment ---
	exposures := make([]model.Exposure, 0)
	dependencies := make([]model.Dependency, 0)
	if resumeExposures != nil || resumeDeps != nil {
		util.Info("agents.orchestrator", "resume: skipping detail (loaded from state)", map[string]any{
			"exposures": len(resumeExposures), "dependencies": len(resumeDeps),
		})
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "detail", Status: events.StatusSkipped, Message: "resumed from state"})
		exposures = resumeExposures
		dependencies = resumeDeps
	} else {
		progress.StartPhase("detail", len(reexamined), 45, 70, "Enriching each verified entity with evidence, IO contract, and details.")
		o.emit(events.Event{Kind: events.KindStageStarted, Stage: "detail", Status: events.StatusRunning, Payload: map[string]any{"total": len(reexamined), "tip": "Enriching each verified entity with evidence, IO contract, and details."}})
		details := o.runDetailBatch(ctx, reexamined, rf, progress.Advance)
		progress.CompletePhase()

		var firstDetailErr error
		var firstDetailJob detailJob
		for i, d := range details {
			if d.Err == nil {
				continue
			}
			if errors.Is(d.Err, context.Canceled) || errors.Is(d.Err, context.DeadlineExceeded) {
				continue
			}
			if firstDetailErr == nil {
				firstDetailErr = d.Err
				if i < len(reexamined) {
					firstDetailJob = reexamined[i]
				} else {
					firstDetailJob = detailJob{Objective: d.Objective}
				}
			}
		}
		if firstDetailErr != nil {
			o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "detail", Status: events.StatusFailed})
			return haltFailure("detail",
				"detail."+firstDetailJob.Objective.ID+"."+safeJobID(firstDetailJob.Seed.Name),
				firstDetailJob.Objective.ID, firstDetailJob.Seed.Name, firstDetailErr, nil)
		}
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "detail", Status: events.StatusSuccess})

		for _, d := range details {
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
		state.DetailExposures = append([]model.Exposure(nil), exposures...)
		state.DetailDependency = append([]model.Dependency(nil), dependencies...)
		o.persistStageState("detail_exposures.json", state.DetailExposures)
		o.persistStageState("detail_dependencies.json", state.DetailDependency)
	}
	state.DetailExposures = append([]model.Exposure(nil), exposures...)
	state.DetailDependency = append([]model.Dependency(nil), dependencies...)

	// --- Stage 4: connection mapping ---
	progress.StartPhase("connections", len(exposures), 70, 90, "Mapping conditional exposure-to-dependency paths per exposure.")
	o.emit(events.Event{Kind: events.KindStageStarted, Stage: "connections", Status: events.StatusRunning, Payload: map[string]any{"total": len(exposures), "tip": "Mapping conditional exposure-to-dependency paths per exposure."}})
	conns, connUnresolved, connErr, connFailedExposure := o.runConnectionsBatch(ctx, exposures, dependencies, exposureObjectives, rf, progress.Advance)
	progress.CompletePhase()
	if connErr != nil {
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "connections", Status: events.StatusFailed})
		return haltFailure("connections",
			"connections."+connFailedExposure,
			"", connFailedExposure, connErr, nil)
	}
	o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "connections", Status: events.StatusSuccess, Payload: map[string]any{"connections": len(conns)}})
	unresolved = append(unresolved, connUnresolved...)
	state.Connections = append([]model.Connection(nil), conns...)
	o.persistStageState("connections.json", state.Connections)

	// --- Stage 5: reconcile/filter ---
	progress.StartPhase("reconcile", 1, 90, 98, "Reconciling entities and dropping orphan connections.")
	o.emit(events.Event{Kind: events.KindStageStarted, Stage: "reconcile", Status: events.StatusRunning, Payload: map[string]any{"total": 1, "tip": "Reconciling entities and dropping orphan connections."}})
	conns, orphanUnresolved := reconcile.FilterConnections(conns, exposures, dependencies)
	unresolved = append(unresolved, orphanUnresolved...)
	// Final ordering for determinism.
	sort.Slice(exposures, func(i, j int) bool { return exposures[i].ID < exposures[j].ID })
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })
	progress.Advance()
	progress.CompletePhase()
	o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "reconcile", Status: events.StatusSuccess})

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
	finalKind := events.KindRunCompleted
	finalStatus := events.StatusSuccess
	if ctx.Err() != nil {
		finalKind = events.KindRunCancelled
		finalStatus = events.StatusCancelled
	}
	// "Empty" runs (no entities at all) are technically successful but
	// almost always indicate a misconfiguration: wrong provider, empty
	// repo, or a model that produced nothing usable. Mark the payload
	// with empty=true so the dashboard can surface a yellow banner.
	empty := len(result.Exposures) == 0 && len(result.Dependencies) == 0 && len(result.Connections) == 0
	o.emit(events.Event{
		Kind: finalKind, Status: finalStatus,
		Payload: map[string]any{
			"exposures":    len(result.Exposures),
			"dependencies": len(result.Dependencies),
			"connections":  len(result.Connections),
			"unresolved":   len(result.Unresolved),
			"warnings":     len(result.Warnings),
			"elapsed_ms":   time.Since(start).Milliseconds(),
			"empty":        empty,
		},
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
//
// There is intentionally no retry: an LLM call is expensive enough that
// silently re-issuing it on a transient blip burns money and can mask the
// real failure. A failure here propagates up to the orchestrator which
// halts the whole run; the operator inspects the failure report and runs
// `diffmind retry <run-id>` once they have fixed (or accepted) the cause.
//
// The function emits an llm_call_started/llm_call_completed pair around
// the PromptStructured call and persists the prompt + response under
// captureDir/<role>.{prompt,response}.{txt,json,raw} so the dashboard
// can show the full payloads on demand. When the structured slot is
// missing from the OpenCode response (some providers don't honor
// json_schema) we fall back to a plain-text prompt and JSON-scrape the
// reply.
func (o *orchestrator) promptAgent(ctx context.Context, role, prompt string, schema map[string]any) (map[string]any, error) {
	sessionID, cleanupFn, err := o.acquireSession(ctx, role)
	if err != nil {
		return nil, err
	}
	o.emit(events.Event{
		Kind: events.KindLLMCallStarted, JobID: role, Status: events.StatusRunning,
		Payload: map[string]any{
			"session_id":   sessionID,
			"prompt_len":   len(prompt),
			"snapshot_dir": o.sessionDir,
		},
	})
	o.persistPrompt(role, prompt)
	util.Trace("agents.agent", "prompt start", map[string]any{"role": role, "session_id": sessionID, "prompt_len": len(prompt)})
	started := time.Now()

	var payload map[string]any
	var rawBody []byte
	var textBody string
	if o.verbose != nil {
		payload, rawBody, textBody, err = o.verbose.PromptStructuredVerboseRaw(ctx, sessionID, o.sessionDir, prompt, schema)
	} else {
		payload, err = o.oc.PromptStructured(ctx, sessionID, o.sessionDir, prompt, schema)
	}

	// Always persist whatever we got — successful payload, raw bytes, or
	// the parsed-text remnants. This is gold for diagnosing why a run
	// produced nothing.
	o.persistResponseBundle(role, payload, rawBody, textBody)

	if err != nil {
		// Free-text fallback: many providers either don't honor the
		// json_schema slot or strip it before returning. If the structured
		// slot was empty (the canonical "no structured payload" error) we
		// re-issue the same logical request as plain text with a strict
		// "respond ONLY with valid JSON" footer, then scrape the JSON out.
		if isNoStructuredPayload(err) {
			fallback, fbErr := o.fallbackPromptText(ctx, role, sessionID, prompt, schema)
			if fbErr == nil && fallback != nil {
				dur := time.Since(started)
				o.persistResponseBundle(role, fallback, rawBody, textBody)
				o.emit(events.Event{
					Kind: events.KindLLMCallCompleted, JobID: role, Status: events.StatusSuccess,
					Message: "structured slot empty; recovered via free-text fallback",
					Payload: map[string]any{
						"session_id":    sessionID,
						"duration_ms":   dur.Milliseconds(),
						"prompt_len":    len(prompt),
						"response_keys": mapKeys(fallback),
						"fallback":      "text",
					},
				})
				if cleanupFn != nil {
					cleanupFn()
				}
				return fallback, nil
			}
			if fbErr != nil {
				err = fmt.Errorf("%w; text fallback also failed: %v", err, fbErr)
			}
		}

		// Best-effort abort: if the call timed out or was cancelled, the
		// server-side session may still be running (or paused waiting on a
		// permission request). Aborting frees server resources and lets
		// the watchdog reclaim the session.
		o.bestEffortAbort(role, sessionID)
		if cleanupFn != nil {
			cleanupFn()
		}
		o.emit(events.Event{
			Kind: events.KindLLMCallCompleted, JobID: role, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{
				"session_id":   sessionID,
				"duration_ms":  time.Since(started).Milliseconds(),
				"raw_preview":  previewBytes(rawBody, 360),
				"text_preview": previewString(textBody, 360),
			},
		})
		return nil, fmt.Errorf("%s prompt: %w", role, err)
	}
	if cleanupFn != nil {
		cleanupFn()
	}
	o.emit(events.Event{
		Kind: events.KindLLMCallCompleted, JobID: role, Status: events.StatusSuccess,
		Payload: map[string]any{
			"session_id":    sessionID,
			"duration_ms":   time.Since(started).Milliseconds(),
			"prompt_len":    len(prompt),
			"response_keys": mapKeys(payload),
		},
	})
	util.Trace("agents.agent", "prompt ok", map[string]any{"role": role, "session_id": sessionID})
	return payload, nil
}

// fallbackPromptText re-asks the same prompt without json_schema constraints
// and parses JSON out of the free-text reply. The model is given an
// explicit suffix demanding pure JSON. Returns (parsed, nil) on success.
func (o *orchestrator) fallbackPromptText(ctx context.Context, role, sessionID, prompt string, schema map[string]any) (map[string]any, error) {
	textPrompt := prompt + "\n\nIMPORTANT FORMAT REQUIREMENT:\n" +
		"Reply with a SINGLE JSON object that matches the structure described above.\n" +
		"Do NOT include any explanatory prose before or after the JSON.\n" +
		"Do NOT wrap the JSON in markdown code fences.\n" +
		"If you have nothing to report, reply with: {\"items\": []}\n"

	text, err := o.oc.PromptText(ctx, sessionID, o.sessionDir, textPrompt)
	if err != nil {
		return nil, err
	}
	parsed := scrapeJSONObject(text)
	if parsed == nil {
		return nil, fmt.Errorf("free-text reply did not contain a JSON object (preview=%s)", previewString(text, 240))
	}
	_ = schema // schema is unused in fallback parsing; kept for parity.
	return parsed, nil
}

// scrapeJSONObject pulls the first JSON object out of arbitrary text.
// Tries a direct unmarshal, fenced ```json blocks, then a brace-balanced
// scan. Returns nil if no object can be recovered.
func scrapeJSONObject(text string) map[string]any {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	if m, ok := tryJSONObject(t); ok {
		return m
	}
	for _, block := range extractFencedBlocks(t) {
		if m, ok := tryJSONObject(block); ok {
			return m
		}
	}
	if candidate, ok := scanBalancedObject(t); ok {
		if m, ok := tryJSONObject(candidate); ok {
			return m
		}
	}
	return nil
}

func tryJSONObject(s string) (map[string]any, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		return m, true
	}
	return nil, false
}

func extractFencedBlocks(s string) []string {
	out := []string{}
	for _, sep := range []string{"```json", "```"} {
		i := 0
		for {
			start := strings.Index(s[i:], sep)
			if start < 0 {
				break
			}
			start += i + len(sep)
			// Skip optional newline immediately after the fence.
			if start < len(s) && s[start] == '\n' {
				start++
			}
			end := strings.Index(s[start:], "```")
			if end < 0 {
				break
			}
			out = append(out, strings.TrimSpace(s[start:start+end]))
			i = start + end + 3
		}
		if len(out) > 0 {
			break
		}
	}
	return out
}

func scanBalancedObject(s string) (string, bool) {
	start := strings.Index(s, "{")
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	esc := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if esc {
				esc = false
				continue
			}
			if ch == '\\' {
				esc = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func isNoStructuredPayload(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no structured payload")
}

func previewBytes(b []byte, n int) string { return previewString(string(b), n) }
func previewString(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	if len(s) <= n {
		return s
	}
	return s[:n] + "\u2026"
}

// persistPrompt writes the prompt text to <captureDir>/<role>.prompt.txt
// (best-effort). Filenames mirror the role so the dashboard can fetch them
// later via /api/runs/{id}/job/{jobID}.
func (o *orchestrator) persistPrompt(role, prompt string) {
	if o.captureDir == "" || role == "" {
		return
	}
	path := filepath.Join(o.captureDir, safeJobID(role)+".prompt.txt")
	_ = os.WriteFile(path, []byte(prompt), 0o644)
}

// captureFilePath returns the absolute path where a prompt/response
// artifact for the given jobID would be persisted, regardless of
// whether the file currently exists. The failure report records this
// path so the operator can find the exact bytes the LLM saw and
// returned even before the file lands on disk.
func (o *orchestrator) captureFilePath(jobID, kind, ext string) string {
	if o.captureDir == "" || jobID == "" {
		return ""
	}
	return filepath.Join(o.captureDir, safeJobID(jobID)+"."+kind+"."+ext)
}

func (o *orchestrator) persistResponse(role string, payload map[string]any) {
	o.persistResponseBundle(role, payload, nil, "")
}

// persistResponseBundle writes any combination of (parsed payload, raw HTTP
// body, free-text reply) we managed to capture for a single LLM call.
// Each artifact lands in <captureDir>/<role>.<suffix>:
//   - .response.json — the parsed structured payload (success path)
//   - .response.raw  — the entire HTTP body, used for debugging when
//     structured parsing fails
//   - .response.text — the concatenated text parts, useful when the
//     provider returned prose instead of JSON
//
// All writes are best-effort; capture is a debugging aid and must never
// take down a run.
func (o *orchestrator) persistResponseBundle(role string, payload map[string]any, raw []byte, text string) {
	if o.captureDir == "" || role == "" {
		return
	}
	base := filepath.Join(o.captureDir, safeJobID(role))
	if payload != nil {
		if b, err := json.MarshalIndent(payload, "", "  "); err == nil {
			_ = os.WriteFile(base+".response.json", b, 0o644)
		}
	}
	if len(raw) > 0 {
		_ = os.WriteFile(base+".response.raw", raw, 0o644)
	}
	if strings.TrimSpace(text) != "" {
		_ = os.WriteFile(base+".response.text", []byte(text), 0o644)
	}
}

func mapKeys(m map[string]any) []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
	o.emit(events.Event{
		Kind: events.KindSessionAborted, JobID: role,
		Payload: map[string]any{"session_id": sessionID},
	})
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
		o.emit(events.Event{
			Kind: events.KindSessionCreated, JobID: role,
			Payload: map[string]any{"session_id": sid, "shared": true, "directory": o.sessionDir},
		})
		util.Debug("agents.agent", "shared session created", map[string]any{"role": role, "session_id": sid})
		return sid, nil, nil
	}
	sid, err := o.oc.CreateSession(ctx, o.sessionDir)
	if err != nil {
		return "", nil, fmt.Errorf("%s create session: %w", role, err)
	}
	o.wd.Track(sid)
	o.emit(events.Event{
		Kind: events.KindSessionCreated, JobID: role,
		Payload: map[string]any{"session_id": sid, "shared": false, "directory": o.sessionDir},
	})
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
