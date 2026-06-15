package discovery

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/runstate"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type PromptFunc func(context.Context, string, string, map[string]any) (map[string]any, error)
type EventFunc func(events.Event)

type Runner struct {
	Workers         int
	RunDir          string
	SubDir          string
	ASTHintsEnabled bool
	Index           *astpkg.ProjectIndex
	Store           *runstate.CheckpointStore
	Prompt          PromptFunc
	Emit            EventFunc
	PathMapper      *extraction.PathMapper
	Confirmed       map[string][]extraction.Candidate

	// FrameworkScope enables the riskier framework-label prompt trim (config
	// DiscoveryFrameworkScope; default OFF). Language scoping is always on.
	FrameworkScope bool
	// MinConfidence is the run's confidence floor, used by the verification
	// pass to floor a downgraded-but-retained item so it survives the later gate.
	MinConfidence float64
	// VerifyMode ("" off, "reask", "ksample") and VerifySamples drive the
	// optional Stage-1.5 verification pass, gated to HighVariance objectives.
	VerifyMode    string
	VerifySamples int
}

type RunInput struct {
	Objectives []objectives.Objective
	RepoFacts  *extraction.RepoFacts
	Progress   func()
}

type RunOutput struct {
	Results []extraction.DiscoveryResult
}

// runDiscovery executes Stage 1: one LLM call per objective in parallel.
// Workers are bounded by cfg.Runtime.Workers and each call uses the
// objective-specific discovery prompt plus the cached repo_facts context.
//
// Fail-fast semantics: the first objective that fails cancels a child
// context which causes every still-pending worker to exit on its next
// ctx-check. Already-running peers are not interrupted server-side, but
// their result (success or failure) is still collected so the failure
// report includes everything that actually happened. Workers that never
// got picked simply never start.
func (r Runner) Run(ctx context.Context, input RunInput) RunOutput {
	results := r.run(ctx, input.Objectives, input.RepoFacts, input.Progress)
	return RunOutput{Results: results}
}

func (r Runner) run(ctx context.Context, objs []objectives.Objective, rf *extraction.RepoFacts, onResult func()) []extraction.DiscoveryResult {
	if len(objs) == 0 {
		return nil
	}

	// Load per-objective checkpoint so a retry skips already-completed objectives.
	checkpoint := map[string]runstate.DiscoveryCheckpointEntry{}
	if r.Store != nil {
		checkpoint = r.Store.LoadDiscoveryCheckpoint(filepath.Join(r.RunDir, runstate.StateDir))
	}

	// Objectives that are already in the checkpoint are satisfied immediately
	// without any LLM call. We still add them to `out` so the rest of the
	// pipeline sees a complete result set.
	out := make([]extraction.DiscoveryResult, 0, len(objs))
	pending := make([]objectives.Objective, 0, len(objs))
	for _, obj := range objs {
		if entry, done := checkpoint[obj.ID]; done {
			out = append(out, extraction.DiscoveryResult{Objective: obj, Items: entry.Items})
			if onResult != nil {
				onResult()
			}
			util.Info("agents.discovery", "objective skipped (loaded from checkpoint)", map[string]any{
				"objective": obj.ID, "items": len(entry.Items),
			})
			continue
		}
		pending = append(pending, obj)
	}

	if len(pending) == 0 {
		return out
	}

	workers := r.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(pending) {
		workers = len(pending)
	}

	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan objectives.Objective)
	results := make(chan extraction.DiscoveryResult)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for obj := range jobs {
				if stageCtx.Err() != nil {
					// Another worker already failed; surface the
					// cancellation as a "peer-cancelled" result so
					// progress still advances and the orchestrator
					// sees a complete result-set. The flag lets the
					// orchestrator tell this apart from a per-call
					// HTTP timeout (which would also surface a
					// context.DeadlineExceeded but is a root cause).
					results <- extraction.DiscoveryResult{Objective: obj, Err: stageCtx.Err(), PeerCancelled: true}
					continue
				}
				util.Debug("agents.discovery", "worker picked objective", map[string]any{"worker": workerID, "objective": obj.ID})
				items, err := r.RunObjective(stageCtx, obj, rf)
				if err == nil && r.Store != nil {
					// Checkpoint the success immediately so a mid-stage
					// failure on a later objective won't re-run this one.
					r.Store.AppendDiscoveryObjective(runstate.DiscoveryCheckpointEntry{
						ObjectiveID: obj.ID,
						Items:       items,
					})
				}
				results <- extraction.DiscoveryResult{Objective: obj, Items: items, Err: err}
				if err != nil {
					// First failure trips the kill-switch. Subsequent
					// workers will short-circuit via the ctx.Err()
					// check above.
					cancel()
				}
			}
		}(i + 1)
	}

	go func() {
		for _, obj := range pending {
			jobs <- obj
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	for r := range results {
		out = append(out, r)
		if onResult != nil {
			onResult()
		}
	}
	return out
}

// runDiscoveryOne discovers one objective. For a large objective it splits the
// work into directory-scoped shards (orchestrator-driven sub-agents) run as
// child jobs and merges the results; small objectives keep the single
// whole-repo call. The parent job (discover.<obj.ID>) brackets either path.
func (r Runner) RunObjective(ctx context.Context, obj objectives.Objective, rf *extraction.RepoFacts) ([]extraction.Candidate, error) {
	jobID := "discover." + obj.ID
	started := time.Now()
	r.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "discovery", JobID: jobID, Status: events.StatusRunning,
		Payload: map[string]any{"objective_id": obj.ID, "kind": string(obj.Kind), "type": obj.Type},
	})

	shards := PlanShards(r.Index, obj, r.SubDir)

	var items []extraction.Candidate
	var err error
	if len(shards) == 0 {
		items, err = r.runShard(ctx, obj, rf, nil, jobID)
	} else {
		items, err = r.runSharded(ctx, obj, rf, shards)
	}
	if err != nil {
		r.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "discovery", JobID: jobID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{"objective_id": obj.ID, "duration_ms": time.Since(started).Milliseconds()},
		})
		return nil, err
	}

	// Stage 1.5: optional, config-gated verification of the discovered items.
	// Gated to HighVariance objectives (the LLM-only ones with no strong AST
	// floor) so cost stays bounded. Fail-soft: any verify error keeps the
	// un-verified items and the objective still succeeds. Runs BEFORE path
	// mapping so the single ApplyToEntities below maps the merged result once.
	if r.VerifyMode != "" && obj.HighVariance {
		items = r.verifyItems(ctx, obj, rf, items)
	}

	if r.PathMapper != nil {
		r.PathMapper.ApplyToEntities(items)
	}
	extraction.SortLLMEntities(items)
	util.Info("agents.discovery", "objective discovery completed", map[string]any{
		"objective": obj.ID, "items": len(items), "shards": len(shards),
	})
	previewNames := make([]string, 0, len(items))
	for _, it := range items {
		previewNames = append(previewNames, it.Name)
	}
	r.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "discovery", JobID: jobID, Status: events.StatusSuccess,
		Payload: map[string]any{
			"objective_id": obj.ID,
			"kind":         string(obj.Kind),
			"type":         obj.Type,
			"items":        len(items),
			"item_names":   previewNames,
			"shards":       len(shards),
			"duration_ms":  time.Since(started).Milliseconds(),
		},
	})
	return items, nil
}

// runDiscoveryShard runs a single discovery LLM call and returns the parsed,
// type-filtered entities. When shard is nil it is the whole-repo call (jobID
// = discover.<obj.ID>, behaviour identical to the pre-sharding code). When
// shard is non-nil the prompt carries a SCOPE directive and shard-scoped AST
// hints, and jobID is the shard's child id.
func (r Runner) runShard(ctx context.Context, obj objectives.Objective, rf *extraction.RepoFacts, shard *Shard, jobID string) ([]extraction.Candidate, error) {
	hints := r.hintsFor(obj, nil)
	var scope []string
	if shard != nil {
		hints = shard.Hints
		if !r.ASTHintsEnabled {
			hints = objectiveHints{}
		}
		scope = shard.Dirs
	}
	prompt := extraction.BuildDiscoveryPrompt(obj, rf, r.SubDir, hints, scope, r.Confirmed[obj.ID], r.FrameworkScope)
	schema := extraction.EntityListSchemaForObjective(obj)
	payload, err := r.Prompt(ctx, jobID, prompt, schema)
	if err != nil {
		return nil, err
	}
	items := extraction.ParseEntities(payload["items"])
	kept := items[:0]
	for i := range items {
		if extraction.ForceObjectiveType(obj, &items[i]) && !extraction.IsNoResultSentinel(obj, items[i]) {
			kept = append(kept, items[i])
		}
	}
	return kept, nil
}

// runDiscoverySharded runs an objective's shards as child jobs and merges the
// results. Shards run SEQUENTIALLY so total discovery concurrency stays bounded
// by the objective worker pool (runDiscovery) — adding parallel shard fan-out
// on top of that pool would multiply in-flight OpenCode calls past the worker
// budget. Smaller per-shard prompts already shorten each call; wall-clock for a
// heavy objective grows, which is the accepted cost of higher recall.
func (r Runner) runSharded(ctx context.Context, obj objectives.Objective, rf *extraction.RepoFacts, shards []Shard) ([]extraction.Candidate, error) {
	parentID := "discover." + obj.ID
	// Resume: shards already checkpointed on a prior attempt are restored
	// without an LLM call; only the missing shards re-run.
	done := map[int][]extraction.Candidate{}
	if r.Store != nil {
		done = r.Store.LoadDiscoveryShardCheckpoint(filepath.Join(r.RunDir, runstate.StateDir), obj.ID)
	}
	results := make([][]extraction.Candidate, 0, len(shards))
	for i := range shards {
		shard := shards[i]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		childID := fmt.Sprintf("%s.shard.%d", parentID, shard.Index)
		if cached, ok := done[shard.Index]; ok {
			r.emit(events.Event{
				Kind: events.KindJobCompleted, Stage: "discovery", JobID: childID, ParentID: parentID, Status: events.StatusSkipped,
				Payload: map[string]any{"objective_id": obj.ID, "shard": shard.Index, "items": len(cached), "resumed": true},
			})
			results = append(results, cached)
			continue
		}
		shardStarted := time.Now()
		r.emit(events.Event{
			Kind: events.KindJobStarted, Stage: "discovery", JobID: childID, ParentID: parentID, Status: events.StatusRunning,
			Payload: map[string]any{"objective_id": obj.ID, "shard": shard.Index, "dirs": shard.Dirs, "files": len(shard.Files)},
		})
		items, err := r.runShard(ctx, obj, rf, &shard, childID)
		if err != nil {
			r.emit(events.Event{
				Kind: events.KindJobFailed, Stage: "discovery", JobID: childID, ParentID: parentID, Status: events.StatusFailed,
				Message: err.Error(),
				Payload: map[string]any{"objective_id": obj.ID, "shard": shard.Index, "duration_ms": time.Since(shardStarted).Milliseconds()},
			})
			return nil, err
		}
		// Checkpoint the shard immediately so a later shard's failure doesn't
		// force this one to re-run on retry.
		if r.Store != nil {
			r.Store.AppendDiscoveryShard(obj.ID, shard.Index, items)
		}
		r.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "discovery", JobID: childID, ParentID: parentID, Status: events.StatusSuccess,
			Payload: map[string]any{"objective_id": obj.ID, "shard": shard.Index, "items": len(items), "duration_ms": time.Since(shardStarted).Milliseconds()},
		})
		results = append(results, items)
	}
	return MergeShardEntities(obj, results), nil
}

func (r Runner) hintsFor(objective objectives.Objective, fileScope []string) objectiveHints {
	if !r.ASTHintsEnabled {
		return objectiveHints{}
	}
	return BuildObjectiveHints(r.Index, objective, r.SubDir, fileScope)
}

// verifyItems dispatches the configured verification strategy for one
// objective's discovered items. Always returns a usable set (the input on any
// fail-soft path), never an error — verification can only help recall/precision,
// never break the run.
func (r Runner) verifyItems(ctx context.Context, obj objectives.Objective, rf *extraction.RepoFacts, items []extraction.Candidate) []extraction.Candidate {
	switch r.VerifyMode {
	case "ksample":
		return r.verifyKSample(ctx, obj, rf, items)
	case "reask":
		return r.verifyReask(ctx, obj, rf, items)
	default:
		return items
	}
}

// verifyReask re-opens the discovered items in one extra call to confirm/correct
// them and surface anything the first pass missed, then merges KEEP-biased. With
// nothing discovered there is nothing to anchor a re-ask, so it is a no-op (the
// ksample mode is the path that hunts from scratch).
func (r Runner) verifyReask(ctx context.Context, obj objectives.Objective, rf *extraction.RepoFacts, items []extraction.Candidate) []extraction.Candidate {
	if len(items) == 0 {
		return items
	}
	parentID := "discover." + obj.ID
	jobID := parentID + ".verify"
	started := time.Now()
	r.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "discovery", JobID: jobID, ParentID: parentID, Status: events.StatusRunning,
		Payload: map[string]any{"objective_id": obj.ID, "mode": "reask", "in": len(items)},
	})
	prompt := extraction.BuildDiscoveryVerifyPrompt(obj, rf, r.SubDir, items, r.hintsFor(obj, nil))
	schema := extraction.EntityListSchemaForObjective(obj)
	payload, err := r.Prompt(ctx, jobID, prompt, schema)
	if err != nil {
		r.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "discovery", JobID: jobID, ParentID: parentID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{"objective_id": obj.ID, "mode": "reask", "duration_ms": time.Since(started).Milliseconds()},
		})
		return items // fail-soft: keep the un-verified items
	}
	verified := extraction.ParseEntities(payload["items"])
	kept := verified[:0]
	for i := range verified {
		if extraction.ForceObjectiveType(obj, &verified[i]) && !extraction.IsNoResultSentinel(obj, verified[i]) {
			kept = append(kept, verified[i])
		}
	}
	merged := MergeVerify(obj, items, kept, r.MinConfidence)
	r.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "discovery", JobID: jobID, ParentID: parentID, Status: events.StatusSuccess,
		Payload: map[string]any{
			"objective_id": obj.ID, "mode": "reask",
			"in": len(items), "out": len(merged), "duration_ms": time.Since(started).Milliseconds(),
		},
	})
	return merged
}

// verifyKSample draws K-1 additional INDEPENDENT whole-repo samples and unions
// them with the first pass (never intersects, so a sample can only add recall).
// Each extra sample is a fresh whole-repo call with no sharding and no
// checkpoint reuse — re-running the sharded path would reload the first run's
// shard checkpoints and collapse every sample into one. Per-sample fail-soft.
func (r Runner) verifyKSample(ctx context.Context, obj objectives.Objective, rf *extraction.RepoFacts, items []extraction.Candidate) []extraction.Candidate {
	k := r.VerifySamples
	if k < 2 {
		return items // need at least one extra sample to add anything
	}
	parentID := "discover." + obj.ID
	batches := [][]extraction.Candidate{items}
	for s := 2; s <= k; s++ {
		if ctx.Err() != nil {
			break
		}
		jobID := fmt.Sprintf("%s.verify.sample.%d", parentID, s)
		started := time.Now()
		r.emit(events.Event{
			Kind: events.KindJobStarted, Stage: "discovery", JobID: jobID, ParentID: parentID, Status: events.StatusRunning,
			Payload: map[string]any{"objective_id": obj.ID, "mode": "ksample", "sample": s},
		})
		extra, err := r.runShard(ctx, obj, rf, nil, jobID)
		if err != nil {
			r.emit(events.Event{
				Kind: events.KindJobFailed, Stage: "discovery", JobID: jobID, ParentID: parentID, Status: events.StatusFailed,
				Message: err.Error(),
				Payload: map[string]any{"objective_id": obj.ID, "mode": "ksample", "sample": s, "duration_ms": time.Since(started).Milliseconds()},
			})
			continue // fail-soft: skip this sample, keep going
		}
		r.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "discovery", JobID: jobID, ParentID: parentID, Status: events.StatusSuccess,
			Payload: map[string]any{"objective_id": obj.ID, "mode": "ksample", "sample": s, "items": len(extra), "duration_ms": time.Since(started).Milliseconds()},
		})
		batches = append(batches, extra)
	}
	return MergeShardEntities(obj, batches)
}

func (r Runner) emit(event events.Event) {
	if r.Emit != nil {
		r.Emit(event)
	}
}
