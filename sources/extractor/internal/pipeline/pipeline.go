package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/runstate"
	"github.com/mohammad-safakhou/diffmind/internal/snapshot"
	connectionstage "github.com/mohammad-safakhou/diffmind/internal/stage/connections"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	reconcile "github.com/mohammad-safakhou/diffmind/internal/stage/reconcile"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// orchestrator wires the pipeline stages together and owns the shared
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
	store            *runstate.CheckpointStore
	// tokenAgg accumulates per-stage and per-run token totals from
	// every promptAgent call's final /session/{id} read. Updated
	// from the orchestrator's main goroutine after each prompt
	// completes; no synchronisation needed.
	tokenAgg   llmrun.TokenTotals
	sessionMu  sync.Mutex
	sessions   *llmrun.SessionManager
	executorMu sync.Mutex
	llm        *llmrun.Executor

	// astIndex is set by the ast_index stage and holds the tree-sitter
	// project analysis used by the connections stage and discovery hints.
	// Read-only after the ast_index stage completes.
	astIndex *astpkg.ProjectIndex

	// pipelineCancel cancels the orchestrator-wide context. Retained
	// because some error-handling paths still invoke it.
	pipelineCancel context.CancelFunc

	discoveryConfirmed map[string][]llmEntity

	// clients holds the connection backbones surfaced by the connection_client
	// discovery objective. They are not graph nodes; the deterministic
	// propagation pass resolves each to an instance and fans it to the
	// operations that use it. Populated at the discovery split (or loaded from
	// connection_clients.json on resume).
	clients []model.ConnectionClient
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

	// Pipeline-wide cancellable context. Every stage receives this child so a
	// root failure or operator cancellation stops pending work promptly.
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
	o.store = &runstate.CheckpointStore{RunDir: opts.RunDir}
	// All stages read `ctx`; alias the cancellable child so the
	// existing call sites stay untouched.
	ctx = pipelineCtx
	if o.captureDir != "" {
		_ = os.MkdirAll(o.captureDir, 0o755)
	}
	o.wireBridges(oc)
	if o.pauser != nil {
		o.wd = llmrun.NewWatchdog(o.pauser, o.sessionDir, 2*time.Second)
		o.wd.SetSink(sink)
		o.wd.Start(ctx)
	}
	o.sessionManager()
	defer func() {
		if o.wd != nil {
			o.wd.Stop()
		}
	}()
	defer o.sessionManager().Close()
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
	resumeRf, resumeSeeds, resumeExpObjs, resumeReexam := o.loadResumeState(opts.ResumeFromDir)

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
	// Discovery consumes its framework bindings and objective hints, so the
	// index is intentionally synchronous and precedes every extraction stage.
	// If it fails, discovery remains LLM-only and connections fall back to the
	// shallow matcher.
	if err := o.runASTIndexStage(ctx); err != nil {
		util.Warn("agents.orchestrator", "ast_index stage failed; connections will use shallow matcher", map[string]any{"error": err.Error()})
		// Non-fatal: allow the rest of the pipeline to proceed.
	}

	// --- Stage 1: per-objective discovery ---
	seeds := make([]detailJob, 0)
	exposureObjectives := map[string]objectives.Objective{}
	if resumeSeeds != nil {
		util.Info("agents.orchestrator", "resume: skipping discovery (loaded from state)", map[string]any{
			"seeds": len(resumeSeeds),
		})
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "discovery", Status: events.StatusSkipped, Message: "resumed from state"})
		seeds = resumeSeeds
		o.clients = o.loadClientsState(opts.ResumeFromDir)
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
		o.discoveryConfirmed = discoverystage.DeterministicByObjective(deterministic)
		progress.StartPhase("discovery", len(allObjectives), 10, 55, "Discovering exposures and dependencies per objective in parallel.")
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
		discovery = discoverystage.MergeDiscoveryResults(discovery, deterministic)
		o.emitStageCompleted("discovery", events.StatusSuccess, nil)

		for _, d := range discovery {
			if d.Objective.Kind == model.KindClient {
				// Clients are connection backbones, not graph nodes: collect
				// them for deterministic instance propagation and never turn
				// them into detail seeds.
				o.clients = append(o.clients, discoverystage.ClientsFromCandidates(d.Items)...)
				continue
			}
			if d.Objective.Kind == model.KindExposure {
				exposureObjectives[d.Objective.Type] = d.Objective
				state.ExposureObjs[d.Objective.Type] = d.Objective.ID
			}
			for _, it := range d.Items {
				seeds = append(seeds, detailJob{Objective: d.Objective, Seed: it})
			}
		}
		o.persistStageState("connection_clients.json", o.clients)
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
			if _, _, needs := extraction.ShouldReexamine(seeds[i].Objective, &seedCopy, o.cfg.Quality.MinConfidence); needs {
				suspects++
			}
		}
		progress.StartPhase("reexamination", suspects, 55, 65, "Re-asking the model to confirm or reject low-signal candidates.")
		o.emit(events.Event{Kind: events.KindStageStarted, Stage: "reexamination", Status: events.StatusRunning, Payload: map[string]any{"total": suspects, "tip": "Re-asking the model to confirm or reject low-signal candidates."}})
		cleaned, unresolvedRe, reexamErr, reexamFailedSeed := o.runReexamination(ctx, seeds, rf, progress.Advance)
		progress.CompletePhase()
		if reexamErr != nil {
			o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "reexamination", Status: events.StatusFailed})
			return haltFailure("reexamination",
				"reexamine."+reexamFailedSeed.Obj.ID+"."+extraction.SafeJobID(reexamFailedSeed.Seed.Name),
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

	// --- Stage 3: seed → entity conversion (deterministic) ---
	// Discovery (+reexamination) already decided WHAT exists and the seeds carry
	// their identity-bearing details, so we convert the verified seeds straight
	// to entities with no LLM. The high-value fields an LLM detail pass used to
	// add (auth, IO-contract inputs) are recovered deterministically from the
	// AST in the post-conversion block below. Resume re-derives this instantly
	// from the persisted reexamination seeds, so nothing is reloaded from disk.
	exposures := make([]model.Exposure, 0)
	dependencies := make([]model.Dependency, 0)
	exposures, dependencies = o.seedsToEntities(reexamined, &unresolved)
	exposures = reconcile.DedupeExposures(exposures)
	dependencies = reconcile.DedupeDependencies(dependencies)
	state.Exposures = append([]model.Exposure(nil), exposures...)
	state.Dependencies = append([]model.Dependency(nil), dependencies...)
	o.persistStageState("entities_exposures.json", state.Exposures)
	o.persistStageState("entities_dependencies.json", state.Dependencies)
	if o.astIndex != nil {
		dependencies = connectionstage.AugmentDependencies(o.astIndex, exposures, dependencies, o.cfg.Quality.MinConfidence)
		discoverystage.StampInferredDBPlatform(o.astIndex, dependencies) // P7: configured platform for deterministic db ops
		discoverystage.HarvestPhysicalTables(o.astIndex, dependencies)   // entity CLASS -> physical table (identity correction)
		// Merge the LLM-discovered backbones with the deterministic AST client floor
		// (so clients exist even when the model misses them), then resolve each to a
		// concrete instance and fan it to the ops that use it (multi-datastore). The
		// single-resource StampInstanceRefs runs inside as the additive safety net.
		o.clients = discoverystage.MergeClients(o.clients, discoverystage.DetectClients(o.astIndex))
		o.clients = discoverystage.PropagateClientInstances(o.astIndex, o.clients, exposures, dependencies)
		o.persistStageState("connection_clients.json", o.clients)
		discoverystage.EnrichExposuresFromAnnotations(o.astIndex, exposures) // B′ T1: recover auth/security from handler annotations
		discoverystage.EnrichExposuresFromParams(o.astIndex, exposures)      // B′ T2: recover IO-contract inputs from handler signature
		dependencies = reconcile.DedupeDependencies(dependencies)
		state.Dependencies = append([]model.Dependency(nil), dependencies...)
		o.persistStageState("entities_dependencies.json", state.Dependencies)
	}

	// --- Stage 4: connection mapping ---
	progress.StartPhase("connections", len(exposures), 65, 82, "Mapping conditional exposure-to-dependency paths per exposure.")
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

	// --- Stage 4.5: LLM connection repair (A1) ---
	// Exposures the deterministic walk left with zero connections get one
	// evidence-gated LLM pass over the closed set of existing dependency IDs.
	// Strictly additive and fail-soft: a repair error degrades to the walk's
	// result, never fails the run. It is its OWN visible stage (not folded into
	// connections) because it makes an LLM call — surfacing it keeps the
	// progress bar moving instead of appearing to stall after connections.
	progress.StartPhase("connection_repair", 1, 82, 90, "Repairing exposures the deterministic walk left with no connections.")
	o.emit(events.Event{Kind: events.KindStageStarted, Stage: "connection_repair", Status: events.StatusRunning, Payload: map[string]any{"total": 1, "tip": "Repairing exposures the deterministic walk left with no connections."}})
	repairOut, repairErr := (connectionstage.RepairRunner{
		Prompt:        o.promptAgent,
		PathMapper:    o.PathMapper(),
		MinConfidence: o.cfg.Quality.MinConfidence,
	}).Run(ctx, connectionstage.RepairInput{
		Index: o.astIndex, Exposures: exposures, Dependencies: dependencies,
		Connections: conns, RepoFacts: rf, SubDir: o.subDir,
	})
	if repairErr != nil {
		warnings = append(warnings, "connection repair failed (kept deterministic connections): "+repairErr.Error())
	} else if repairOut.Dangling > 0 {
		conns = append(conns, repairOut.Connections...)
		unresolved = append(unresolved, repairOut.Rejected...)
	}
	progress.Advance()
	progress.CompletePhase()
	o.emitStageCompleted("connection_repair", events.StatusSuccess, map[string]any{
		"dangling": repairOut.Dangling, "added": len(repairOut.Connections), "rejected": len(repairOut.Rejected),
	})
	state.Connections = append([]model.Connection(nil), conns...)
	o.persistStageState("connections.json", state.Connections)

	// --- Stage 5: reconcile/filter ---
	progress.StartPhase("reconcile", 1, 90, 98, "Reconciling entities and dropping orphan connections.")
	o.emit(events.Event{Kind: events.KindStageStarted, Stage: "reconcile", Status: events.StatusRunning, Payload: map[string]any{"total": 1, "tip": "Reconciling entities and dropping orphan connections."}})
	reconciled := (reconcile.Runner{}).Run(reconcile.Input{
		Exposures: exposures, Dependencies: dependencies, Connections: conns, Unresolved: unresolved,
	})
	exposures = reconciled.Exposures
	dependencies = reconciled.Dependencies
	conns = reconciled.Connections
	unresolved = reconciled.Unresolved
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
		Clients:      o.clients,
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

// seedsToEntities converts verified seeds straight into model entities via
// extraction.ToBase. Discovery owns identity and richness; this deterministic
// conversion is the only canonical path.
func (o *orchestrator) seedsToEntities(jobs []detailJob, unresolved *[]model.UnresolvedItem) ([]model.Exposure, []model.Dependency) {
	exposures := make([]model.Exposure, 0, len(jobs))
	dependencies := make([]model.Dependency, 0, len(jobs))
	for _, j := range jobs {
		seed := j.Seed
		base, ur := extraction.ToBase(o.repoPath, j.Objective, seed, o.cfg.Quality.MinConfidence)
		if ur != nil {
			*unresolved = append(*unresolved, *ur)
			continue
		}
		if j.Objective.Kind == model.KindExposure {
			exposures = append(exposures, model.Exposure{BaseEntity: base})
		} else {
			dependencies = append(dependencies, model.Dependency{BaseEntity: base})
		}
	}
	return exposures, dependencies
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
		"discovery_verify": cfg.Runtime.DiscoveryVerify, "discovery_verify_mode": cfg.Runtime.DiscoveryVerifyMode,
		"discovery_verify_samples": cfg.Runtime.DiscoveryVerifySamples, "discovery_framework_scope": cfg.Runtime.DiscoveryFrameworkScope,
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
			"discovery_ast_hints":            cfg.Runtime.DiscoveryASTHints,
			"discovery_verify":               cfg.Runtime.DiscoveryVerify,
			"discovery_verify_mode":          cfg.Runtime.DiscoveryVerifyMode,
			"discovery_verify_samples":       cfg.Runtime.DiscoveryVerifySamples,
			"discovery_framework_scope":      cfg.Runtime.DiscoveryFrameworkScope,
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
		o.pauser = llmrun.NewPauseBridge(v)
		o.verbose = llmrun.NewVerboseBridge(v)
		o.tokens = llmrun.NewTokenBridge(v)
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

func (o *orchestrator) sessionManager() *llmrun.SessionManager {
	o.sessionMu.Lock()
	defer o.sessionMu.Unlock()
	if o.sessions == nil {
		o.sessions = llmrun.NewSessionManager(llmrun.SessionOptions{
			Client:      o.oc,
			Pauser:      o.pauser,
			Tracker:     o.wd,
			Sink:        o.sink,
			Directory:   o.sessionDir,
			Reuse:       o.cfg.Runtime.ReuseOpenCodeSession,
			Cleanup:     o.cfg.Runtime.CleanupOpenCodeSessions,
			DeleteDelay: time.Duration(o.cfg.Runtime.OpenCodeDeleteDelaySec) * time.Second,
		})
	}
	return o.sessions
}

func (o *orchestrator) executor() *llmrun.Executor {
	o.executorMu.Lock()
	defer o.executorMu.Unlock()
	if o.llm == nil {
		o.llm = llmrun.NewExecutor(llmrun.ExecutorOptions{
			Client:     o.oc,
			Verbose:    o.verbose,
			Tokens:     o.tokens,
			Sessions:   o.sessionManager(),
			Captures:   llmrun.CaptureStore{Dir: o.captureDir},
			Totals:     &o.tokenAgg,
			Sink:       o.sink,
			Directory:  o.sessionDir,
			RetryCount: o.cfg.Runtime.PromptRetryCount,
			Liveness: llmrun.LivenessConfig{
				IdleTimeout:  time.Duration(o.cfg.Runtime.IdleTimeoutSec) * time.Second,
				MaxCall:      time.Duration(o.cfg.Runtime.MaxCallSeconds) * time.Second,
				PollInterval: time.Duration(o.cfg.Runtime.LivenessPollSec) * time.Second,
			},
		})
	}
	return o.llm
}

// buildFailure builds the partial result we hand back to internal/app when a
// stage fails. It preserves the snapshot path and the state captured up to (but
// not including) the failing stage so `diffmind retry` has a consistent base to
// resume from. start/state/unresolved/warnings are passed by the caller so the
// live accumulators are captured at the moment of the halt.
func (o *orchestrator) buildFailure(ctx context.Context, start time.Time, state IntermediateState, unresolved []model.UnresolvedItem, warnings []string, stage, jobID, objectiveID, entityName string, err error, extra map[string]any) (Result, error) {
	errClass := llmrun.ClassifyError(err)
	// Only attach a numeric HTTP status when the error is actually
	// HTTP-shaped (4xx/5xx/rate-limit). For schema/network/timeout
	// failures the error message frequently includes 3-digit numbers in
	// other contexts (token counts, byte sizes) the regex would otherwise
	// pick up as a phantom status.
	var httpStatus int
	if llmrun.ShouldReportHTTPStatus(errClass) {
		httpStatus = llmrun.ExtractHTTPStatus(err.Error())
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
	return extraction.NewPathMapper(o.snap.Path, o.snap.SourcePath)
}

func (o *orchestrator) promptAgent(ctx context.Context, role, prompt string, schema map[string]any) (map[string]any, error) {
	return o.executor().Prompt(ctx, role, prompt, schema)
}

// captureFilePath returns the absolute path where a prompt/response
// artifact for the given jobID would be persisted, regardless of
// whether the file currently exists. The failure report records this
// path so the operator can find the exact bytes the LLM saw and
// returned even before the file lands on disk.
func (o *orchestrator) captureFilePath(jobID, kind, ext string) string {
	return (llmrun.CaptureStore{Dir: o.captureDir}).Path(jobID, kind, ext)
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
