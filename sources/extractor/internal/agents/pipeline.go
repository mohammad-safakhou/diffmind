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

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
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
	tokens           tokenReader     // optional: only set when oc is *opencode.Client
	wd               *watchdog       // optional: nil if pauser is nil
	sink             events.Sink     // never nil (NoopSink fallback)
	captureDir       string          // optional: dir where prompt/response files are written
	runDir           string          // optional: artifact root for state + failure report

	// tokenAgg accumulates per-stage and per-run token totals from
	// every promptAgent call's final /session/{id} read. Updated
	// from the orchestrator's main goroutine after each prompt
	// completes; no synchronisation needed.
	tokenAgg tokenTotals

	// astIndex is set by the ast_index stage and holds the tree-sitter
	// project analysis used by the connections stage and discovery hints.
	// Read-only after the ast_index stage completes.
	astIndex *astpkg.ProjectIndex

	// pipelineCancel cancels the orchestrator-wide context. Retained
	// because some error-handling paths still invoke it.
	pipelineCancel context.CancelFunc

	discoveryConfirmed map[string][]llmEntity

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

// emitStageCompleted emits a stage_completed event, merging the
// stage's cumulative token totals into the payload. The token map
// is consistent in shape regardless of whether the underlying client
// implemented token reads — when tokens are unavailable we just skip
// the field so the SPA renders "—" gracefully.
func (o *orchestrator) emitStageCompleted(stage, status string, extra map[string]any) {
	payload := map[string]any{}
	for k, v := range extra {
		payload[k] = v
	}
	if tb := o.snapshotStage(stage); tb != nil {
		payload["tokens"] = tokenBucketPayload(tb)
	}
	o.emit(events.Event{
		Kind: events.KindStageCompleted, Stage: stage, Status: status,
		Payload: payload,
	})
}

// tokenBucketPayload renders a tokenBucket as a SPA-friendly map.
// Centralised so every consumer (stage_completed, llm_call_completed,
// etc.) emits the same shape.
func tokenBucketPayload(b *tokenBucket) map[string]any {
	if b == nil {
		return nil
	}
	return map[string]any{
		"calls":       b.Calls,
		"input":       b.Input,
		"output":      b.Output,
		"reasoning":   b.Reasoning,
		"cache_read":  b.CacheRead,
		"cache_write": b.CacheWrite,
		"total":       b.Total(),
		"cost":        b.Cost,
	}
}

// modelBucketPayload renders a model.TokenBucket (the variant
// returned by snapshotAll) using the same shape as
// tokenBucketPayload above. We have two structs because the agents
// package uses tokenBucket as an internal mutable accumulator while
// model.TokenBucket is the public wire format.
func modelBucketPayload(b model.TokenBucket) map[string]any {
	return map[string]any{
		"calls":       b.Calls,
		"input":       b.Input,
		"output":      b.Output,
		"reasoning":   b.Reasoning,
		"cache_read":  b.CacheRead,
		"cache_write": b.CacheWrite,
		"total":       b.Total,
		"cost":        b.Cost,
	}
}

// RunOptions carries optional dependencies for Run. Sink is the live event
// stream consumed by the dashboard; CaptureDir is a directory under the run
// dir where each LLM call's prompt + response is persisted for
// click-to-view; RunDir, when set, is the run's artifact directory and
// enables on-disk state persistence + failure report writes for retry;
// RunID, when set, is used as the leaf name of the snapshot directory
// so the path is stable and human-readable (see snapshot.Create).
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
	RunID         string
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
	// Defensive sanitization: enforce the invariant that the raw
	// transport timeout is always bigger than the watchdog's hard
	// ceiling. Without this guard, a stale or hand-rolled config can
	// set OpenCode.TimeoutSec=300 and silently neuter the entire
	// watchdog by firing the http.Client.Timeout first — exactly the
	// failure mode of runs 20260518T113418Z and 20260518T115925Z.
	if fixes := cfg.Sanitize(); len(fixes) > 0 {
		for _, f := range fixes {
			util.Warn("agents.orchestrator", "config sanitized", map[string]any{
				"field": f.Field, "was": f.Was, "adjusted": f.Adjusted, "reason": f.Reason,
			})
		}
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
	//
	// New runs use a stable parent (~/.diffmind/snapshots) and the run
	// ID as the leaf name, so the snapshot path is short, predictable,
	// and lacks any random tokens that LLM tool calls can hallucinate.
	var snap *snapshot.Snapshot
	if strings.TrimSpace(opts.SnapshotPath) != "" {
		s, err := snapshot.Reattach(sourceSessionDir, opts.SnapshotPath)
		if err != nil {
			return Result{}, fmt.Errorf("reattach snapshot %q: %w", opts.SnapshotPath, err)
		}
		snap = s
	} else {
		s, err := snapshot.Create(sourceSessionDir, snapshot.DefaultParent(), opts.RunID)
		if err != nil {
			return Result{}, fmt.Errorf("snapshot: %w", err)
		}
		snap = s
	}

	// Pipeline-wide cancellable context. We hand `ctx` (= pipelineCtx)
	// to every stage so a hard image-build failure in the parallel
	// goroutine can yank every in-flight LLM call (Stages 1-3) via
	// pipelineCancel and let the pipeline halt promptly with a
	// clear failure report instead of letting wasted LLM work
	// drag on for minutes after the build has already failed.
	pipelineCtx, pipelineCancel := context.WithCancel(ctx)
	defer pipelineCancel()

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
		pipelineCancel:   pipelineCancel,
	}
	// All stages read `ctx`; alias the cancellable child so the
	// existing call sites stay untouched.
	ctx = pipelineCtx
	if o.captureDir != "" {
		_ = os.MkdirAll(o.captureDir, 0o755)
	}
	o.wireBridges(oc)
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

	progress := NewProgressReporter()
	progress.SetSink(sink)
	defer progress.Close()

	o.emitRunStarted()

	warnings := make([]string, 0)
	unresolved := make([]model.UnresolvedItem, 0)
	state := IntermediateState{ExposureObjs: map[string]string{}}
	start := time.Now()

	// Load any previously-saved stage state for resume.
	resumeRf, resumeSeeds, resumeExpObjs, resumeReexam, resumeExposures, resumeDeps := o.loadResumeState(opts.ResumeFromDir)

	// haltFailure forwards to buildFailure with the live accumulators. Kept
	// as a closure so the per-stage call sites stay terse; the body lives in
	// buildFailure (it captures the current state/unresolved/warnings at the
	// moment of the call, exactly as before).
	haltFailure := func(stage, jobID, objectiveID, entityName string, err error, extra map[string]any) (Result, error) {
		return o.buildFailure(ctx, start, state, unresolved, warnings, stage, jobID, objectiveID, entityName, err, extra)
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
		o.emitStageCompleted("repo_facts", events.StatusSuccess, map[string]any{"facts_present": rf != nil})
	}
	state.RepoFacts = rf

	// --- Stage 0b: AST index (tree-sitter) ---
	// Runs in parallel with discovery/reexamination/detail LLM stages.
	// The result is consumed by the connections stage.
	// We run it synchronously here so it's complete before connections.
	// For large repos (>500 files) this takes 5-30s — well within the
	// time LLM stages need. If it fails, we log a warning and continue;
	// connections will fall back to the shallow matcher.
	if err := o.runASTIndexStage(ctx); err != nil {
		util.Warn("agents.orchestrator", "ast_index stage failed; connections will use shallow matcher", map[string]any{"error": err.Error()})
		// Non-fatal: allow the rest of the pipeline to proceed.
	}

	// --- Stage 0c: infrastructure inventory ---
	// Its output is consumed only by the UI (state/infrastructure.json), not by
	// core extraction, so it can be skipped to save a full LLM call (X6).
	var infra *InfrastructureInventory
	if o.astIndex != nil && len(o.astIndex.Configs) > 0 && !o.cfg.Runtime.SkipInfrastructure {
		inv, err := o.runInfrastructureStage(ctx, rf)
		if err != nil {
			util.Warn("agents.orchestrator", "infrastructure stage failed; continuing", map[string]any{"error": err.Error()})
		} else {
			infra = inv
		}
	}
	_ = infra // used later when we wire endpoint resolution into detail stage

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
		// Deterministic discovery always runs first. Its high-precision findings
		// (a) seed the LLM's KNOWN_CONFIRMED_ITEMS so the model stops
		// re-enumerating the mechanical bulk, and (b) are merged into the final
		// discovery set below. Safe no-op when the AST index is unavailable.
		deterministic := o.runDeterministicDiscovery(ctx, allObjectives)
		o.discoveryConfirmed = deterministicByObjective(deterministic)
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
		o.discoveryConfirmed = nil
		progress.CompletePhase()

		var firstDiscoveryErr error
		var firstDiscoveryObj objectives.Objective
		for _, d := range discovery {
			if d.Err == nil {
				continue
			}
			// Workers that exited because a sibling already tripped
			// fail-fast explicitly flag PeerCancelled. Anything else
			// — including a per-call http.Client timeout that wraps
			// context.DeadlineExceeded — is a real root-cause error
			// and must surface. The previous heuristic
			// (errors.Is(ctx.Canceled/DeadlineExceeded)) silently
			// swallowed 300s HTTP timeouts; see
			// .diffmind/runs/20260515T123031Z for the cautionary
			// tale.
			if d.PeerCancelled {
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
		// Merge the deterministic floor into the LLM's findings (semantic
		// dedup per objective) to form the final discovery set.
		discovery = mergeDiscoveryResults(discovery, deterministic)
		o.emitStageCompleted("discovery", events.StatusSuccess, nil)

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
				"reexamine."+reexamFailedSeed.Obj.ID+"."+SafeJobID(reexamFailedSeed.Seed.Name),
				reexamFailedSeed.Obj.ID, reexamFailedSeed.Seed.Name, reexamErr, nil)
		}
		o.emitStageCompleted("reexamination", events.StatusSuccess, nil)
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
		// Pre-compute batch counts so stage_started carries both
		// "entities total" AND "batches total". The dashboard renders
		// "X/N entities · X/B batches" to communicate the batched
		// nature of detail work. We compute over the same set the
		// detail stage will see — and we account for the per-entity
		// checkpoint so a resume shows the truly-pending count, not
		// the original 152.
		previewPending := reexamined
		if o.runDir != "" {
			checkpoint := o.loadDetailCheckpoint(o.runDir + "/" + stateDir)
			if len(checkpoint) > 0 {
				filtered := make([]detailJob, 0, len(reexamined))
				for _, j := range reexamined {
					if _, ok := checkpoint[detailEntityKey(j.Objective.ID, j.Seed.Name)]; ok {
						continue
					}
					filtered = append(filtered, j)
				}
				previewPending = filtered
			}
		}
		if len(previewPending) > 0 {
			filtered := make([]detailJob, 0, len(previewPending))
			for _, j := range previewPending {
				seed := j.Seed
				if isCompleteDeterministicSeed(j.Objective, &seed) {
					continue
				}
				filtered = append(filtered, j)
			}
			previewPending = filtered
		}
		previewBatches := DetailGroups(previewPending)
		o.emit(events.Event{
			Kind: events.KindStageStarted, Stage: "detail", Status: events.StatusRunning,
			Payload: map[string]any{
				"total":         len(reexamined),     // total entities in scope
				"pending":       len(previewPending), // entities NOT yet checkpointed
				"batches_total": len(previewBatches),
				"tip":           "Enriching each verified entity with evidence, IO contract, and details.",
			},
		})
		details := o.runDetailBatch(ctx, reexamined, rf, progress.Advance)
		progress.CompletePhase()

		// Results arrive in completion order (workers run in parallel),
		// so we cannot index back into `reexamined` to recover the seed
		// — that would attribute the failure to the wrong job. Each
		// detailResult now carries SeedName explicitly so we can
		// reconstruct the failing job_id correctly.
		var firstDetailErr error
		var firstDetailObjID, firstDetailSeed string
		for _, d := range details {
			if d.Err == nil {
				continue
			}
			// Skip peer-cancelled siblings (see discovery for
			// rationale). Per-call HTTP timeouts must surface.
			if d.PeerCancelled {
				continue
			}
			if firstDetailErr == nil {
				firstDetailErr = d.Err
				firstDetailObjID = d.Objective.ID
				firstDetailSeed = d.SeedName
			}
		}
		if firstDetailErr != nil {
			o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "detail", Status: events.StatusFailed})
			return haltFailure("detail",
				"detail."+firstDetailObjID+"."+SafeJobID(firstDetailSeed),
				firstDetailObjID, firstDetailSeed, firstDetailErr, nil)
		}
		o.emitStageCompleted("detail", events.StatusSuccess, nil)

		// Detail is an ENRICHMENT stage, not a gatekeeper. Discovery (+
		// reexamination) already decided WHAT exists; detail only adds
		// method/path, table, evidence, etc. So it must never silently drop
		// or re-identify an entity: a seed the model failed to enrich falls
		// back to its discovered form, and the discovered type/name are always
		// preserved over whatever detail returned. This keeps the count and
		// identity stable across runs regardless of detail-stage variance.
		seedByKey := make(map[string]llmEntity, len(reexamined))
		for _, j := range reexamined {
			seedByKey[detailEntityKey(j.Objective.ID, j.Seed.Name)] = j.Seed
		}
		for _, d := range details {
			seed, hasSeed := seedByKey[detailEntityKey(d.Objective.ID, d.SeedName)]
			item := d.Item
			if item == nil {
				if !hasSeed {
					continue
				}
				s := seed
				item = &s
			} else if hasSeed {
				// Preserve the discovered identity; keep detail's enrichment.
				// Type, name AND identity-bearing details (path, operation,
				// table, …) are pinned to the seed so detail can never silently
				// re-identify an entity and break its dedup/eval key (C2).
				item.Type = seed.Type
				if strings.TrimSpace(seed.Name) != "" {
					item.Name = seed.Name
				}
				pinIdentityDetails(item, seed)
			}
			base, ur := ToBase(o.repoPath, d.Objective, *item, o.cfg.Quality.MinConfidence)
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
	if o.astIndex != nil {
		dependencies = augmentDependenciesFromAST(o.astIndex, exposures, dependencies, o.cfg.Quality.MinConfidence)
		stampInferredDBPlatform(o.astIndex, dependencies) // P7: configured platform for deterministic db ops
		dependencies = reconcile.DedupeDependencies(dependencies)
		state.DetailDependency = append([]model.Dependency(nil), dependencies...)
		o.persistStageState("detail_dependencies.json", state.DetailDependency)
	}

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
	o.emitStageCompleted("connections", events.StatusSuccess, map[string]any{"connections": len(conns)})
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
	o.emitStageCompleted("reconcile", events.StatusSuccess, nil)

	return o.assembleResult(ctx, start, exposures, dependencies, conns, unresolved, warnings), nil
}

// assembleResult builds the final Result, logs the completion summary, and
// emits the terminal run event (completed/cancelled) with token totals.
func (o *orchestrator) assembleResult(ctx context.Context, start time.Time, exposures []model.Exposure, dependencies []model.Dependency, conns []model.Connection, unresolved []model.UnresolvedItem, warnings []string) Result {
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
	terminalPayload := map[string]any{
		"exposures":    len(result.Exposures),
		"dependencies": len(result.Dependencies),
		"connections":  len(result.Connections),
		"unresolved":   len(result.Unresolved),
		"warnings":     len(result.Warnings),
		"elapsed_ms":   time.Since(start).Milliseconds(),
		"empty":        empty,
	}
	// Attach token totals (run-wide and per-stage) to the terminal event so
	// the SPA can render a final cost summary without walking every
	// llm_call_completed event in its buffer.
	if all := o.snapshotAll(); all != nil {
		tokensOut := map[string]any{}
		for stage, tb := range all {
			tokensOut[stage] = modelBucketPayload(tb)
		}
		terminalPayload["tokens"] = tokensOut
		result.Tokens = all
	}
	o.emit(events.Event{
		Kind: finalKind, Status: finalStatus,
		Payload: terminalPayload,
	})
	return result
}

// emitRunStarted logs and emits the run_started event with the resolved config
// (effective timeouts included, so a failed run can be diagnosed from the
// event alone without diffing configs).
func (o *orchestrator) emitRunStarted() {
	cfg := o.cfg
	util.Info("agents.orchestrator", "multi-step pipeline starting", map[string]any{
		"repo": o.repoPath, "source_session_dir": o.sourceSessionDir, "snapshot": o.snap.Path, "sub_dir": o.subDir,
		"workers": cfg.Runtime.Workers, "max_catalog_items": cfg.Runtime.MaxCatalogItems,
		"reuse_session": cfg.Runtime.ReuseOpenCodeSession, "min_confidence": cfg.Quality.MinConfidence,
		"opencode_transport_timeout_sec": cfg.OpenCode.TimeoutSec,
		"idle_timeout_sec":               cfg.Runtime.IdleTimeoutSec,
		"prompt_retry_count":             cfg.Runtime.PromptRetryCount,
		"max_call_sec":                   cfg.Runtime.MaxCallSeconds,
		"liveness_poll_sec":              cfg.Runtime.LivenessPollSec,
	})
	o.emit(events.Event{
		Kind:    events.KindRunStarted,
		Message: "extraction pipeline started",
		Payload: map[string]any{
			"repo":                           o.repoPath,
			"snapshot":                       o.snap.Path,
			"sub_dir":                        o.subDir,
			"workers":                        cfg.Runtime.Workers,
			"max_catalog_items":              cfg.Runtime.MaxCatalogItems,
			"min_confidence":                 cfg.Quality.MinConfidence,
			"skip_reexamination":             cfg.Runtime.SkipReexamination,
			"opencode_transport_timeout_sec": cfg.OpenCode.TimeoutSec,
			"idle_timeout_sec":               cfg.Runtime.IdleTimeoutSec,
			"prompt_retry_count":             cfg.Runtime.PromptRetryCount,
			"max_call_sec":                   cfg.Runtime.MaxCallSeconds,
			"liveness_poll_sec":              cfg.Runtime.LivenessPollSec,
		},
	})
}

// wireBridges connects the orchestrator to the OpenCode client (or a test
// fake). The real *opencode.Client is adapted via pause/verbose/token bridges;
// fakes that implement pauseHandler/verbosePrompter/tokenReader directly are
// accepted as-is. A fake implementing only openCodeAPI gets no watchdog, which
// is fine — it cannot pause.
func (o *orchestrator) wireBridges(oc openCodeAPI) {
	switch v := oc.(type) {
	case *opencode.Client:
		o.pauser = newPauseBridge(v)
		o.verbose = newVerboseBridge(v)
		o.tokens = &tokenBridge{c: v}
	case pauseHandler:
		o.pauser = v
	}
	if o.verbose == nil {
		if vp, ok := oc.(verbosePrompter); ok {
			o.verbose = vp
		}
	}
	if o.tokens == nil {
		if tr, ok := oc.(tokenReader); ok {
			o.tokens = tr
		}
	}
}

// buildFailure builds the partial result we hand back to internal/app when a
// stage fails. It preserves the snapshot path and the state captured up to (but
// not including) the failing stage so `diffmind retry` has a consistent base to
// resume from. start/state/unresolved/warnings are passed by the caller so the
// live accumulators are captured at the moment of the halt.
func (o *orchestrator) buildFailure(ctx context.Context, start time.Time, state IntermediateState, unresolved []model.UnresolvedItem, warnings []string, stage, jobID, objectiveID, entityName string, err error, extra map[string]any) (Result, error) {
	errClass := ClassifyError(err)
	// Only attach a numeric HTTP status when the error is actually
	// HTTP-shaped (4xx/5xx/rate-limit). For schema/network/timeout
	// failures the error message frequently includes 3-digit numbers in
	// other contexts (token counts, byte sizes) the regex would otherwise
	// pick up as a phantom status.
	var httpStatus int
	if ShouldReportHTTPStatus(errClass) {
		httpStatus = ExtractHTTPStatus(err.Error())
	}
	// `cancelled` means the run was halted by an external cancel (user
	// clicked Cancel, parent context expired) — NOT a build-driven halt,
	// which is a real FAILURE even though fail-fast propagates cancellation
	// through the pipeline context. The reattributed_from_cancel marker
	// lets us tell them apart.
	buildDriven := false
	if extra != nil {
		if v, ok := extra["reattributed_from_cancel"]; ok {
			if b, ok2 := v.(bool); ok2 {
				buildDriven = b
			}
		}
	}
	cancelled := ctx.Err() != nil && !buildDriven
	f := &Failure{
		Stage:        stage,
		JobID:        jobID,
		ObjectiveID:  objectiveID,
		EntityName:   entityName,
		Error:        err.Error(),
		ErrorClass:   errClass,
		HTTPStatus:   httpStatus,
		OccurredAt:   time.Now().UTC(),
		Extra:        extra,
		PromptPath:   o.captureFilePath(jobID, "prompt", "txt"),
		ResponsePath: o.captureFilePath(jobID, "response", "json"),
		SnapshotPath: o.snap.Path,
		Cancelled:    cancelled,
	}
	// If the parent context is dead the halt was user-initiated: emit
	// KindRunCancelled so the dashboard's status pill flips to "cancelled".
	// The failure report is still written so retry knows what was in flight.
	terminalKind := events.KindRunFailed
	terminalStatus := events.StatusFailed
	if cancelled {
		terminalKind = events.KindRunCancelled
		terminalStatus = events.StatusCancelled
	}
	haltPayload := map[string]any{
		"stage":         stage,
		"job_id":        jobID,
		"objective_id":  objectiveID,
		"entity_name":   entityName,
		"error_class":   f.ErrorClass,
		"http_status":   f.HTTPStatus,
		"prompt_path":   f.PromptPath,
		"response_path": f.ResponsePath,
		"elapsed_ms":    time.Since(start).Milliseconds(),
		"cancelled":     cancelled,
	}
	if all := o.snapshotAll(); all != nil {
		tokensOut := map[string]any{}
		for s, tb := range all {
			tokensOut[s] = modelBucketPayload(tb)
		}
		haltPayload["tokens"] = tokensOut
	}
	o.emit(events.Event{
		Kind: terminalKind, Status: terminalStatus, Stage: stage, JobID: jobID,
		Message: err.Error(),
		Payload: haltPayload,
	})
	// The snapshot must be retained for retry.
	o.snap.Retain()
	o.writeFailureReport(f)
	o.persistStageState("failure_state.json", state)
	return Result{
		Unresolved:   reconcile.DedupeUnresolved(unresolved),
		Warnings:     reconcile.DedupeWarnings(warnings),
		Failure:      f,
		SnapshotPath: o.snap.Path,
		Intermediate: state,
	}, fmt.Errorf("%s stage failed at %s: %w", stage, jobID, err)
}

// ----------------------------------------------------------------------------
// Session + prompting helpers.
// ----------------------------------------------------------------------------

// pathMapper returns the (cached) helper that rewrites snapshot-relative
// paths back to source-relative paths in agent responses.
func (o *orchestrator) PathMapper() *PathMapper {
	if o.snap == nil {
		return nil
	}
	return NewPathMapper(o.snap.Path, o.snap.SourcePath)
}

// promptAgent is the single point where every stage talks to the OpenCode
// server. It owns session lifecycle (shared vs per-call) and logs role+size
// metrics so the run is observable. On prompt failure (timeout, context
// cancel, or server error) it best-effort aborts the session so the server
// is not left holding a paused agent.
//
// promptAgent retries only liveness-stuck prompts. Auth/schema/quota/provider
// failures still propagate immediately because re-issuing those calls burns
// money and masks the real cause. RetryCount is the number of retries AFTER
// the initial attempt.
func (o *orchestrator) promptAgent(ctx context.Context, role, prompt string, schema map[string]any) (map[string]any, error) {
	retries := o.cfg.Runtime.PromptRetryCount
	if retries < 0 {
		retries = 0
	}
	attempts := retries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		payload, err := o.promptAgentOnce(ctx, role, prompt, schema, attempt, attempts)
		if err == nil {
			return payload, nil
		}
		lastErr = err
		if !errors.Is(err, ErrStuck) || attempt == attempts || ctx.Err() != nil {
			return nil, err
		}

		o.resetSharedSessionAfterStuckRetry()
		o.emit(events.Event{
			Kind: events.KindLog, JobID: role,
			Message: "prompt stuck; retrying",
			Payload: map[string]any{
				"attempt":      attempt,
				"next_attempt": attempt + 1,
				"max_attempts": attempts,
				"error":        err.Error(),
			},
		})
		util.Warn("agents.agent", "prompt stuck; retrying", map[string]any{
			"role": role, "attempt": attempt, "max_attempts": attempts, "error": err.Error(),
		})
	}
	return nil, lastErr
}

// promptAgentOnce emits an llm_call_started/llm_call_completed pair around
// the PromptStructured call and persists the prompt + response under
// captureDir/<role>.{prompt,response}.{txt,json,raw} so the dashboard
// can show the full payloads on demand. When the structured slot is
// missing from the OpenCode response (some providers don't honor
// json_schema) we fall back to a plain-text prompt and JSON-scrape the
// reply.
func (o *orchestrator) promptAgentOnce(ctx context.Context, role, prompt string, schema map[string]any, attempt, attempts int) (map[string]any, error) {
	sessionID, cleanupFn, err := o.acquireSession(ctx, role)
	if err != nil {
		return nil, err
	}
	o.emit(events.Event{
		Kind: events.KindLLMCallStarted, JobID: role, Status: events.StatusRunning,
		Payload: map[string]any{
			"session_id":   sessionID,
			"attempt":      attempt,
			"max_attempts": attempts,
			"prompt_len":   len(prompt),
			"snapshot_dir": o.sessionDir,
		},
	})
	o.persistPrompt(role, prompt)
	util.Trace("agents.agent", "prompt start", map[string]any{"role": role, "session_id": sessionID, "prompt_len": len(prompt)})
	started := time.Now()

	stopWatch, watchDone := o.startLivenessWatch(ctx, role, sessionID)

	var payload map[string]any
	var rawBody []byte
	var textBody string
	if o.verbose != nil {
		payload, rawBody, textBody, err = o.verbose.PromptStructuredVerboseRaw(ctx, sessionID, o.sessionDir, prompt, schema)
	} else {
		payload, err = o.oc.PromptStructured(ctx, sessionID, o.sessionDir, prompt, schema)
	}

	// Stop the watchdog and collect its verdict synchronously.
	stopWatch()
	report := <-watchDone
	err = o.relabelIfStuck(err, report, role, sessionID, len(prompt))

	// Always persist whatever we got — successful payload, raw bytes, or the
	// parsed-text remnants. Gold for diagnosing why a run produced nothing.
	o.persistResponseBundle(role, payload, rawBody, textBody)

	if err != nil {
		return o.handlePromptError(ctx, role, sessionID, prompt, schema, err, started, rawBody, textBody, attempt, attempts, cleanupFn)
	}
	o.emitPromptSuccess(ctx, role, sessionID, payload, len(prompt), started, attempt, attempts, cleanupFn)
	return payload, nil
}

// startLivenessWatch wires the liveness watchdog around an in-flight prompt
// POST: it polls OpenCode and, if the agent stops making progress (and is not
// waiting on a permission), aborts the session so the POST returns a
// cancellation error we re-label as ErrStuck. It only runs against the real
// *opencode.Client (gated on o.verbose, like the pause-bridge); test fakes
// return synchronously and never trigger liveness. The returned channel is
// always drainable — a non-watchdogged call receives a nil report — so the
// caller can unconditionally `<-watchDone`.
func (o *orchestrator) startLivenessWatch(ctx context.Context, role, sessionID string) (context.CancelFunc, chan *livenessReport) {
	watchCtx, stopWatch := context.WithCancel(ctx)
	watchDone := make(chan *livenessReport, 1)
	watchStarted := false
	if o.verbose != nil {
		if client, ok := o.oc.(livenessClient); ok {
			if ab, ok := o.oc.(livenessAborter); ok {
				probe := &openCodeLivenessProbe{oc: client, sessionID: sessionID, directory: o.sessionDir}
				abort := &openCodeAborter{oc: ab, sessionID: sessionID, directory: o.sessionDir}
				cfg := livenessConfig{
					IdleTimeout:  time.Duration(o.cfg.Runtime.IdleTimeoutSec) * time.Second,
					MaxCall:      time.Duration(o.cfg.Runtime.MaxCallSeconds) * time.Second,
					PollInterval: time.Duration(o.cfg.Runtime.LivenessPollSec) * time.Second,
				}
				go func() { watchDone <- runLiveness(watchCtx, cfg, probe, abort, role, o.sink) }()
				watchStarted = true
			}
		}
	}
	if !watchStarted {
		watchDone <- nil
	}
	return stopWatch, watchDone
}

// relabelIfStuck rewrites the prompt error to ErrStuck when the liveness
// watchdog aborted the call, so the failure report shows class=stuck instead
// of cancelled/timeout. Returns err unchanged when there was no abort.
func (o *orchestrator) relabelIfStuck(err error, report *livenessReport, role, sessionID string, promptLen int) error {
	if report == nil || !report.Aborted {
		return err
	}
	stuckCause := report.Reason
	util.Warn("agents.agent", "prompt declared stuck by liveness watchdog", map[string]any{
		"role":         role,
		"session_id":   sessionID,
		"prompt_len":   promptLen,
		"reason":       stuckCause,
		"last_tool":    report.LastTool,
		"original_err": ErrString(err),
	})
	return NewStuckError(stuckCause)
}

// handlePromptError runs the failure path: free-text fallback (for providers
// that drop the json_schema slot), best-effort session abort, cleanup, and the
// failed-call event. Returns (fallback, nil) when the fallback recovered a
// payload, else (nil, wrapped error).
func (o *orchestrator) handlePromptError(ctx context.Context, role, sessionID, prompt string, schema map[string]any, err error, started time.Time, rawBody []byte, textBody string, attempt, attempts int, cleanupFn func()) (map[string]any, error) {
	// Free-text fallback: many providers either don't honor the json_schema
	// slot or strip it before returning. If the structured slot was empty
	// (the canonical "no structured payload" error) re-issue the same
	// logical request as plain text with a strict "respond ONLY with valid
	// JSON" footer, then scrape the JSON out.
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
					"attempt":       attempt,
					"max_attempts":  attempts,
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

	// Best-effort abort: a timed-out/cancelled call may leave the
	// server-side session running (or paused on a permission request).
	// Aborting frees server resources and lets the watchdog reclaim it.
	o.bestEffortAbort(role, sessionID)
	if cleanupFn != nil {
		cleanupFn()
	}
	o.emit(events.Event{
		Kind: events.KindLLMCallCompleted, JobID: role, Status: events.StatusFailed,
		Message: err.Error(),
		Payload: map[string]any{
			"session_id":   sessionID,
			"attempt":      attempt,
			"max_attempts": attempts,
			"duration_ms":  time.Since(started).Milliseconds(),
			"raw_preview":  previewBytes(rawBody, 360),
			"text_preview": previewString(textBody, 360),
		},
	})
	return nil, fmt.Errorf("%s prompt: %w", role, err)
}

// emitPromptSuccess records token totals and emits the successful-call event.
// Token totals are read BEFORE cleanupFn() — cleanup may delete the session
// server-side, after which /session/{id} would 404. The read is bounded to 3s.
func (o *orchestrator) emitPromptSuccess(ctx context.Context, role, sessionID string, payload map[string]any, promptLen int, started time.Time, attempt, attempts int, cleanupFn func()) {
	tokens := o.recordPromptTokens(ctx, sessionID, role)
	if cleanupFn != nil {
		cleanupFn()
	}
	payloadOut := map[string]any{
		"session_id":    sessionID,
		"attempt":       attempt,
		"max_attempts":  attempts,
		"duration_ms":   time.Since(started).Milliseconds(),
		"prompt_len":    promptLen,
		"response_keys": mapKeys(payload),
	}
	if tokens != nil {
		payloadOut["tokens"] = map[string]any{
			"input":       tokens.Input,
			"output":      tokens.Output,
			"reasoning":   tokens.Reasoning,
			"cache_read":  tokens.CacheRead,
			"cache_write": tokens.CacheWrite,
			"total":       tokens.Total(),
			"cost":        tokens.Cost,
		}
	}
	o.emit(events.Event{
		Kind: events.KindLLMCallCompleted, JobID: role, Status: events.StatusSuccess,
		Payload: payloadOut,
	})
	util.Trace("agents.agent", "prompt ok", map[string]any{"role": role, "session_id": sessionID})
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
	path := filepath.Join(o.captureDir, SafeJobID(role)+".prompt.txt")
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
	return filepath.Join(o.captureDir, SafeJobID(jobID)+"."+kind+"."+ext)
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
	base := filepath.Join(o.captureDir, SafeJobID(role))
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
		if o.wd != nil {
			o.wd.Track(sid)
		}
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
	if o.wd != nil {
		o.wd.Track(sid)
	}
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
		if o.wd != nil {
			o.wd.Untrack(sessionID)
		}
		util.Trace("agents.agent", "session deleted", map[string]any{"role": role, "session_id": sessionID})
	}()
}

func (o *orchestrator) resetSharedSessionAfterStuckRetry() {
	if !o.cfg.Runtime.ReuseOpenCodeSession {
		return
	}
	o.sessionMu.Lock()
	sid := strings.TrimSpace(o.sharedSessionID)
	o.sharedSessionID = ""
	o.sessionMu.Unlock()
	if sid == "" || o.wd == nil {
		return
	}
	if o.wd != nil {
		o.wd.Untrack(sid)
	}
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
