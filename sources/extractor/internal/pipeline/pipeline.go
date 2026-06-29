package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/provenance"
	"github.com/mohammad-safakhou/diffmind/internal/runstate"
	connectionstage "github.com/mohammad-safakhou/diffmind/internal/stage/connections"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	reconcile "github.com/mohammad-safakhou/diffmind/internal/stage/reconcile"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// orchestrator wires the deterministic extraction stages together. It owns no
// model/client state: DiffMind is now AST/config driven end to end.
type orchestrator struct {
	cfg        config.Config
	repoPath   string
	sourceRoot string
	subDir     string
	sink       events.Sink
	runDir     string
	store      *runstate.CheckpointStore
	astIndex   *astpkg.ProjectIndex
	clients    []model.ConnectionClient
}

func (o *orchestrator) emit(e events.Event) {
	if o.sink != nil {
		o.sink.Emit(e)
	}
}

func (o *orchestrator) emitStageCompleted(stage, status string, extra map[string]any) {
	payload := map[string]any{}
	for k, v := range extra {
		payload[k] = v
	}
	o.emit(events.Event{
		Kind:    events.KindStageCompleted,
		Stage:   stage,
		Status:  status,
		Payload: payload,
	})
}

// RunOptions carries optional dependencies for Run.
type RunOptions struct {
	Sink          events.Sink
	CaptureDir    string
	RunDir        string
	RunID         string
	ResumeFromDir string
}

// Run is the public deterministic entrypoint used by internal/app.
func Run(ctx context.Context, cfg config.Config, repoPath string) (Result, error) {
	return RunWith(ctx, cfg, repoPath, RunOptions{})
}

func RunWith(ctx context.Context, cfg config.Config, repoPath string, opts RunOptions) (Result, error) {
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
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

	sourceRoot, subDir := detectMonorepo(repoPath)

	pipelineCtx, pipelineCancel := context.WithCancel(ctx)
	defer pipelineCancel()

	o := &orchestrator{
		cfg:        cfg,
		repoPath:   repoPath,
		sourceRoot: sourceRoot,
		subDir:     subDir,
		sink:       sink,
		runDir:     opts.RunDir,
		store:      &runstate.CheckpointStore{RunDir: opts.RunDir},
	}

	o.emitRunStarted()

	warnings := make([]string, 0)
	unresolved := make([]model.UnresolvedItem, 0)
	state := IntermediateState{ExposureObjs: map[string]string{}}
	start := time.Now()

	haltFailure := func(stage, jobID, objectiveID, entityName string, err error, extra map[string]any) (Result, error) {
		return o.buildFailure(pipelineCtx, start, state, unresolved, warnings, stage, jobID, objectiveID, entityName, err, extra)
	}
	return o.runDeterministicOnly(pipelineCtx, start, state, unresolved, warnings, haltFailure)
}

func (o *orchestrator) runDeterministicOnly(
	ctx context.Context,
	start time.Time,
	state IntermediateState,
	unresolved []model.UnresolvedItem,
	warnings []string,
	haltFailure func(stage, jobID, objectiveID, entityName string, err error, extra map[string]any) (Result, error),
) (Result, error) {
	util.Info("agents.orchestrator", "deterministic pipeline starting", map[string]any{
		"repo": o.repoPath,
	})

	if err := o.runASTIndexStage(ctx); err != nil {
		return haltFailure("ast_index", "ast_index", "", "", err, nil)
	}

	allObjectives := objectives.Default()
	deterministic := o.runDeterministicDiscovery(ctx, allObjectives)

	seeds := make([]detailJob, 0)
	exposureObjectives := map[string]objectives.Objective{}
	for _, d := range deterministic {
		if d.Objective.Kind == model.KindClient {
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
	state.DiscoverySeeds = append([]detailJob(nil), seeds...)
	o.persistStageState("connection_clients.json", o.clients)
	o.persistStageState("discovery.json", state.DiscoverySeeds)
	o.persistStageState("exposure_objectives.json", state.ExposureObjs)

	exposures, dependencies := o.seedsToEntities(seeds, &unresolved)
	exposures = reconcile.DedupeExposures(exposures)
	dependencies = reconcile.DedupeDependencies(dependencies)
	provenance.NormalizeDeterministic(exposures, dependencies, nil)
	state.Exposures = append([]model.Exposure(nil), exposures...)
	state.Dependencies = append([]model.Dependency(nil), dependencies...)
	o.persistStageState("entities_exposures.json", state.Exposures)
	o.persistStageState("entities_dependencies.json", state.Dependencies)

	if o.astIndex != nil {
		dependencies = connectionstage.AugmentDependencies(o.astIndex, exposures, dependencies, o.cfg.Quality.MinConfidence)
		discoverystage.StampInferredDBPlatform(o.astIndex, dependencies)
		discoverystage.HarvestPhysicalTables(o.astIndex, dependencies)
		o.clients = discoverystage.MergeClients(o.clients, discoverystage.DetectClients(o.astIndex))
		o.clients = discoverystage.PropagateClientInstances(o.astIndex, o.clients, exposures, dependencies)
		o.persistStageState("connection_clients.json", o.clients)
		discoverystage.EnrichExposuresFromAnnotations(o.astIndex, exposures)
		discoverystage.EnrichExposuresFromParams(o.astIndex, exposures)
		dependencies = reconcile.DedupeDependencies(dependencies)
		provenance.NormalizeDeterministic(exposures, dependencies, nil)
		state.Dependencies = append([]model.Dependency(nil), dependencies...)
		o.persistStageState("entities_dependencies.json", state.Dependencies)
	}

	o.emit(events.Event{Kind: events.KindStageStarted, Stage: "connections", Status: events.StatusRunning, Payload: map[string]any{"total": len(exposures), "tip": "Mapping deterministic exposure-to-dependency paths per exposure."}})
	conns, connUnresolved, connErr, connFailedExposure := o.runConnectionsBatch(ctx, exposures, dependencies, exposureObjectives, nil)
	if connErr != nil {
		o.emit(events.Event{Kind: events.KindStageCompleted, Stage: "connections", Status: events.StatusFailed})
		return haltFailure("connections", "connections."+connFailedExposure, "", connFailedExposure, connErr, nil)
	}
	o.emitStageCompleted("connections", events.StatusSuccess, map[string]any{"connections": len(conns)})
	unresolved = append(unresolved, connUnresolved...)
	provenance.NormalizeDeterministic(exposures, dependencies, conns)
	state.Connections = append([]model.Connection(nil), conns...)
	o.persistStageState("connections.json", state.Connections)

	o.emit(events.Event{Kind: events.KindStageStarted, Stage: "reconcile", Status: events.StatusRunning, Payload: map[string]any{"total": 1, "tip": "Reconciling deterministic entities and dropping orphan connections."}})
	reconciled := (reconcile.Runner{}).Run(reconcile.Input{
		Exposures: exposures, Dependencies: dependencies, Connections: conns, Unresolved: unresolved,
	})
	exposures = reconciled.Exposures
	dependencies = reconciled.Dependencies
	conns = reconciled.Connections
	unresolved = reconciled.Unresolved
	provenance.NormalizeDeterministic(exposures, dependencies, conns)
	o.emitStageCompleted("reconcile", events.StatusSuccess, nil)

	result := o.assembleResult(ctx, start, exposures, dependencies, conns, unresolved, warnings)
	result.Intermediate = state
	return result, nil
}

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
	empty := len(result.Exposures) == 0 && len(result.Dependencies) == 0 && len(result.Connections) == 0
	o.emit(events.Event{
		Kind:   finalKind,
		Status: finalStatus,
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
	return result
}

// seedsToEntities converts deterministic discovery seeds into model entities.
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

func (o *orchestrator) emitRunStarted() {
	cfg := o.cfg
	util.Info("agents.orchestrator", "deterministic pipeline starting", map[string]any{
		"repo": o.repoPath, "source_root": o.sourceRoot, "sub_dir": o.subDir,
		"pipeline": cfg.Pipeline(), "workers": cfg.Runtime.Workers, "min_confidence": cfg.Quality.MinConfidence,
	})
	o.emit(events.Event{
		Kind:    events.KindRunStarted,
		Message: "deterministic extraction pipeline started",
		Payload: map[string]any{
			"repo":           o.repoPath,
			"source_root":    o.sourceRoot,
			"sub_dir":        o.subDir,
			"pipeline":       cfg.Pipeline(),
			"workers":        cfg.Runtime.Workers,
			"min_confidence": cfg.Quality.MinConfidence,
		},
	})
}

func (o *orchestrator) buildFailure(ctx context.Context, start time.Time, state IntermediateState, unresolved []model.UnresolvedItem, warnings []string, stage, jobID, objectiveID, entityName string, err error, extra map[string]any) (Result, error) {
	cancelled := ctx.Err() != nil
	f := &Failure{
		Stage:       stage,
		JobID:       jobID,
		ObjectiveID: objectiveID,
		EntityName:  entityName,
		Error:       err.Error(),
		ErrorClass:  "deterministic",
		OccurredAt:  time.Now().UTC(),
		Extra:       extra,
		SourceRoot:  o.sourceRoot,
		Cancelled:   cancelled,
	}
	terminalKind := events.KindRunFailed
	terminalStatus := events.StatusFailed
	if cancelled {
		terminalKind = events.KindRunCancelled
		terminalStatus = events.StatusCancelled
	}
	o.emit(events.Event{
		Kind: terminalKind, Status: terminalStatus, Stage: stage, JobID: jobID,
		Message: err.Error(),
		Payload: map[string]any{
			"stage":        stage,
			"job_id":       jobID,
			"objective_id": objectiveID,
			"entity_name":  entityName,
			"error_class":  f.ErrorClass,
			"elapsed_ms":   time.Since(start).Milliseconds(),
			"cancelled":    cancelled,
		},
	})
	o.writeFailureReport(f)
	o.persistStageState("failure_state.json", state)
	return Result{
		Unresolved:   reconcile.DedupeUnresolved(unresolved),
		Warnings:     reconcile.DedupeWarnings(warnings),
		Failure:      f,
		SourceRoot:   o.sourceRoot,
		Intermediate: state,
	}, fmt.Errorf("%s stage failed at %s: %w", stage, jobID, err)
}

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
