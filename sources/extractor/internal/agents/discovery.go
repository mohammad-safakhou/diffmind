package agents

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/agents/core"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/langdetect"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// runRepoFacts executes Stage 0. It is a single LLM call that produces a
// compact snapshot of the repository. Failures are non-fatal: downstream
// stages will still run, they will just receive a nil repoFacts and have to
// rediscover the tech stack themselves.
func (o *orchestrator) runRepoFacts(ctx context.Context) (*repoFacts, error) {
	prompt := BuildRepoFactsPrompt(o.subDir)
	schema := RepoFactsSchema()
	payload, err := o.promptAgent(ctx, "repo_facts", prompt, schema)
	if err != nil {
		util.Warn("agents.repo_facts", "repo facts extraction failed", map[string]any{"error": err})
		return nil, err
	}
	rf := ParseRepoFacts(payload)

	// Augment the LLM-derived facts with deterministic marker-
	// file inspection. The result feeds the parallel image build:
	// if the LLM forgot to mention a language version (which
	// happens regularly) the marker scan picks it up.
	//
	// Failures here are non-fatal — we just leave LanguageFacts
	// empty and let the index stage's recipe fall back to
	// language defaults.
	if rf != nil {
		facts, derr := langdetect.Inspect(ctx, o.sessionDir)
		if derr != nil {
			util.Warn("agents.repo_facts", "marker-file language detection failed", map[string]any{"error": derr})
		} else {
			for _, f := range facts {
				rf.LanguageFacts = append(rf.LanguageFacts, langFact{
					Language:         string(f.Language),
					Version:          f.Version,
					BuildTool:        f.BuildTool,
					BuildToolVersion: f.BuildToolVersion,
					Sources:          f.Sources,
				})
			}
		}
		util.Info("agents.repo_facts", "repo facts gathered", map[string]any{
			"languages":      len(rf.Languages),
			"frameworks":     len(rf.Frameworks),
			"build_files":    len(rf.BuildFiles),
			"config_files":   len(rf.ConfigFiles),
			"language_facts": len(rf.LanguageFacts),
		})
	}
	return rf, nil
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
func (o *orchestrator) runDiscovery(ctx context.Context, objs []objectives.Objective, rf *repoFacts, onResult func()) []discoveryResult {
	if len(objs) == 0 {
		return nil
	}

	// Load per-objective checkpoint so a retry skips already-completed objectives.
	checkpoint := o.store.LoadDiscoveryCheckpoint(o.runDir + "/" + stateDir)

	// Objectives that are already in the checkpoint are satisfied immediately
	// without any LLM call. We still add them to `out` so the rest of the
	// pipeline sees a complete result set.
	out := make([]discoveryResult, 0, len(objs))
	pending := make([]objectives.Objective, 0, len(objs))
	for _, obj := range objs {
		if entry, done := checkpoint[obj.ID]; done {
			out = append(out, discoveryResult{Objective: obj, Items: entry.Items})
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

	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(pending) {
		workers = len(pending)
	}

	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan objectives.Objective)
	results := make(chan discoveryResult)
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
					results <- discoveryResult{Objective: obj, Err: stageCtx.Err(), PeerCancelled: true}
					continue
				}
				util.Debug("agents.discovery", "worker picked objective", map[string]any{"worker": workerID, "objective": obj.ID})
				items, err := o.runDiscoveryOne(stageCtx, obj, rf)
				if err == nil {
					// Checkpoint the success immediately so a mid-stage
					// failure on a later objective won't re-run this one.
					o.store.AppendDiscoveryObjective(core.DiscoveryCheckpointEntry{
						ObjectiveID: obj.ID,
						Items:       items,
					})
				}
				results <- discoveryResult{Objective: obj, Items: items, Err: err}
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
func (o *orchestrator) runDiscoveryOne(ctx context.Context, obj objectives.Objective, rf *repoFacts) ([]llmEntity, error) {
	jobID := "discover." + obj.ID
	started := time.Now()
	o.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "discovery", JobID: jobID, Status: events.StatusRunning,
		Payload: map[string]any{"objective_id": obj.ID, "kind": string(obj.Kind), "type": obj.Type},
	})

	shards := planDiscoveryShards(o.astIndex, obj, o.subDir)

	var items []llmEntity
	var err error
	if len(shards) == 0 {
		items, err = o.runDiscoveryShard(ctx, obj, rf, nil, jobID)
	} else {
		items, err = o.runDiscoverySharded(ctx, obj, rf, shards)
	}
	if err != nil {
		o.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "discovery", JobID: jobID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{"objective_id": obj.ID, "duration_ms": time.Since(started).Milliseconds()},
		})
		return nil, err
	}

	o.PathMapper().ApplyToEntities(items)
	SortLLMEntities(items)
	util.Info("agents.discovery", "objective discovery completed", map[string]any{
		"objective": obj.ID, "items": len(items), "shards": len(shards),
	})
	previewNames := make([]string, 0, len(items))
	for _, it := range items {
		previewNames = append(previewNames, it.Name)
	}
	o.emit(events.Event{
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
func (o *orchestrator) runDiscoveryShard(ctx context.Context, obj objectives.Objective, rf *repoFacts, shard *discoveryShard, jobID string) ([]llmEntity, error) {
	hints := o.hintsFor(obj, nil)
	var scope []string
	if shard != nil {
		hints = shard.Hints
		if !o.cfg.Runtime.DiscoveryASTHints {
			hints = objectiveHints{}
		}
		scope = shard.Dirs
	}
	prompt := BuildDiscoveryPrompt(obj, rf, o.subDir, hints, scope, o.discoveryConfirmed[obj.ID])
	schema := EntityListSchemaForObjective(obj)
	payload, err := o.promptAgent(ctx, jobID, prompt, schema)
	if err != nil {
		return nil, err
	}
	items := ParseEntities(payload["items"])
	kept := items[:0]
	for i := range items {
		if ForceObjectiveType(obj, &items[i]) {
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
func (o *orchestrator) runDiscoverySharded(ctx context.Context, obj objectives.Objective, rf *repoFacts, shards []discoveryShard) ([]llmEntity, error) {
	parentID := "discover." + obj.ID
	// Resume: shards already checkpointed on a prior attempt are restored
	// without an LLM call; only the missing shards re-run.
	done := o.store.LoadDiscoveryShardCheckpoint(filepath.Join(o.runDir, stateDir), obj.ID)
	results := make([][]llmEntity, 0, len(shards))
	for i := range shards {
		shard := shards[i]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		childID := fmt.Sprintf("%s.shard.%d", parentID, shard.Index)
		if cached, ok := done[shard.Index]; ok {
			o.emit(events.Event{
				Kind: events.KindJobCompleted, Stage: "discovery", JobID: childID, ParentID: parentID, Status: events.StatusSkipped,
				Payload: map[string]any{"objective_id": obj.ID, "shard": shard.Index, "items": len(cached), "resumed": true},
			})
			results = append(results, cached)
			continue
		}
		shardStarted := time.Now()
		o.emit(events.Event{
			Kind: events.KindJobStarted, Stage: "discovery", JobID: childID, ParentID: parentID, Status: events.StatusRunning,
			Payload: map[string]any{"objective_id": obj.ID, "shard": shard.Index, "dirs": shard.Dirs, "files": len(shard.Files)},
		})
		items, err := o.runDiscoveryShard(ctx, obj, rf, &shard, childID)
		if err != nil {
			o.emit(events.Event{
				Kind: events.KindJobFailed, Stage: "discovery", JobID: childID, ParentID: parentID, Status: events.StatusFailed,
				Message: err.Error(),
				Payload: map[string]any{"objective_id": obj.ID, "shard": shard.Index, "duration_ms": time.Since(shardStarted).Milliseconds()},
			})
			return nil, err
		}
		// Checkpoint the shard immediately so a later shard's failure doesn't
		// force this one to re-run on retry.
		o.store.AppendDiscoveryShard(obj.ID, shard.Index, items)
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "discovery", JobID: childID, ParentID: parentID, Status: events.StatusSuccess,
			Payload: map[string]any{"objective_id": obj.ID, "shard": shard.Index, "items": len(items), "duration_ms": time.Since(shardStarted).Milliseconds()},
		})
		results = append(results, items)
	}
	return mergeShardEntities(obj, results), nil
}
