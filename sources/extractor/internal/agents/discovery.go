package agents

import (
	"context"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// runRepoFacts executes Stage 0. It is a single LLM call that produces a
// compact snapshot of the repository. Failures are non-fatal: downstream
// stages will still run, they will just receive a nil repoFacts and have to
// rediscover the tech stack themselves.
func (o *orchestrator) runRepoFacts(ctx context.Context) (*repoFacts, error) {
	prompt := buildRepoFactsPrompt(o.subDir)
	schema := repoFactsSchema()
	payload, err := o.promptAgent(ctx, "repo_facts", prompt, schema)
	if err != nil {
		util.Warn("agents.repo_facts", "repo facts extraction failed", map[string]any{"error": err})
		return nil, err
	}
	rf := parseRepoFacts(payload)
	if rf != nil {
		util.Info("agents.repo_facts", "repo facts gathered", map[string]any{
			"languages":    len(rf.Languages),
			"frameworks":   len(rf.Frameworks),
			"build_files":  len(rf.BuildFiles),
			"config_files": len(rf.ConfigFiles),
		})
	}
	return rf, nil
}

// runDiscovery executes Stage 1: one LLM call per objective in parallel.
// Workers are bounded by cfg.Runtime.Workers and each call uses the
// objective-specific discovery prompt plus the cached repo_facts context.
func (o *orchestrator) runDiscovery(ctx context.Context, objs []objectives.Objective, rf *repoFacts, onResult func()) []discoveryResult {
	if len(objs) == 0 {
		return nil
	}
	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(objs) {
		workers = len(objs)
	}
	jobs := make(chan objectives.Objective)
	results := make(chan discoveryResult)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for obj := range jobs {
				util.Debug("agents.discovery", "worker picked objective", map[string]any{"worker": workerID, "objective": obj.ID})
				items, err := o.runDiscoveryOne(ctx, obj, rf)
				results <- discoveryResult{Objective: obj, Items: items, Err: err}
			}
		}(i + 1)
	}

	go func() {
		for _, obj := range objs {
			jobs <- obj
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	out := make([]discoveryResult, 0, len(objs))
	for r := range results {
		out = append(out, r)
		if onResult != nil {
			onResult()
		}
	}
	return out
}

func (o *orchestrator) runDiscoveryOne(ctx context.Context, obj objectives.Objective, rf *repoFacts) ([]llmEntity, error) {
	jobID := "discover." + obj.ID
	started := time.Now()
	o.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "discovery", JobID: jobID, Status: events.StatusRunning,
		Payload: map[string]any{"objective_id": obj.ID, "kind": string(obj.Kind), "type": obj.Type},
	})
	prompt := buildDiscoveryPrompt(obj, rf, o.subDir)
	schema := entityListSchema()
	payload, err := o.promptAgent(ctx, jobID, prompt, schema)
	if err != nil {
		o.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "discovery", JobID: jobID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{"objective_id": obj.ID, "duration_ms": time.Since(started).Milliseconds()},
		})
		return nil, err
	}
	items := parseEntities(payload["items"])
	o.pathMapper().applyToEntities(items)
	util.Info("agents.discovery", "objective discovery completed", map[string]any{
		"objective": obj.ID, "items": len(items),
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
			"duration_ms":  time.Since(started).Milliseconds(),
		},
	})
	return items, nil
}
