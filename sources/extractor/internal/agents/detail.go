package agents

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// runDetailBatch executes Stage 3 in parallel. For every verified seed we
// send a focused detail-enrichment prompt and merge the result back onto
// the seed. If the agent returns item=null we keep the seed as-is (Stage
// 2 already confirmed it); we do not drop it silently.
//
// Fail-fast semantics mirror runDiscovery: the first detail failure
// cancels a child context which causes still-pending workers to exit
// immediately. Already-running peers complete and their results are
// included in the returned slice so the failure report can show the
// full picture.
func (o *orchestrator) runDetailBatch(ctx context.Context, jobs []detailJob, rf *repoFacts, onResult func()) []detailResult {
	if len(jobs) == 0 {
		return nil
	}
	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}

	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobCh := make(chan detailJob)
	resCh := make(chan detailResult)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := range jobCh {
				if stageCtx.Err() != nil {
					// Peer-cancelled: another worker tripped
					// fail-fast before this job ran. Flag it so the
					// orchestrator skips it when looking for the
					// root-cause failure.
					resCh <- detailResult{Objective: j.Objective, SeedName: j.Seed.Name, Err: stageCtx.Err(), PeerCancelled: true}
					continue
				}
				util.Trace("agents.detail", "worker picked entity", map[string]any{
					"worker": workerID, "objective": j.Objective.ID, "name": j.Seed.Name,
				})
				item, err := o.runDetailOne(stageCtx, j, rf)
				resCh <- detailResult{Objective: j.Objective, SeedName: j.Seed.Name, Item: item, Err: err}
				if err != nil {
					cancel()
				}
			}
		}(i + 1)
	}
	go func() {
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)
		wg.Wait()
		close(resCh)
	}()
	out := make([]detailResult, 0, len(jobs))
	for r := range resCh {
		out = append(out, r)
		if onResult != nil {
			onResult()
		}
	}
	return out
}

func (o *orchestrator) runDetailOne(ctx context.Context, j detailJob, rf *repoFacts) (*llmEntity, error) {
	prompt := buildDetailPrompt(j.Objective, j.Seed, rf, o.subDir)
	schema := entitySingleSchema()
	jobID := "detail." + j.Objective.ID + "." + safeJobID(j.Seed.Name)
	started := time.Now()
	o.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "detail", JobID: jobID, Status: events.StatusRunning,
		ParentID: "discover." + j.Objective.ID,
		Payload:  map[string]any{"objective_id": j.Objective.ID, "name": j.Seed.Name, "type": j.Seed.Type},
	})
	payload, err := o.promptAgent(ctx, jobID, prompt, schema)
	if err != nil {
		o.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "detail", JobID: jobID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{"objective_id": j.Objective.ID, "name": j.Seed.Name, "duration_ms": time.Since(started).Milliseconds()},
		})
		return nil, err
	}
	item := parseSingleEntity(payload["item"])
	if item == nil {
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "detail", JobID: jobID, Status: events.StatusSkipped,
			Message: "detail extractor returned nil",
			Payload: map[string]any{"objective_id": j.Objective.ID, "name": j.Seed.Name, "duration_ms": time.Since(started).Milliseconds()},
		})
		return nil, nil
	}
	o.pathMapper().applyToEntity(item)
	// Guard against the LLM silently rewriting the entity into something of a
	// different type. If the type diverges, keep the seed's type authoritative.
	if strings.TrimSpace(item.Type) == "" {
		item.Type = j.Seed.Type
	}
	if strings.TrimSpace(item.Name) == "" {
		item.Name = j.Seed.Name
	}
	merged := mergeEnrichment(j.Seed, *item)
	o.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "detail", JobID: jobID, Status: events.StatusSuccess,
		Payload: map[string]any{
			"objective_id": j.Objective.ID, "name": merged.Name, "type": merged.Type,
			"confidence":  merged.Confidence,
			"duration_ms": time.Since(started).Milliseconds(),
		},
	})
	return &merged, nil
}

// mergeEnrichment overlays the detail response on top of the seed. Enriched
// fields win when non-empty; otherwise seed values are preserved.
func mergeEnrichment(seed, enriched llmEntity) llmEntity {
	out := enriched
	out.Type = preferNonEmpty(enriched.Type, seed.Type)
	out.Name = preferNonEmpty(enriched.Name, seed.Name)
	out.Summary = preferNonEmpty(enriched.Summary, seed.Summary)
	if len(out.Actions) == 0 {
		out.Actions = seed.Actions
	}
	if len(out.Tags) == 0 {
		out.Tags = seed.Tags
	}
	if len(out.Inputs) == 0 {
		out.Inputs = seed.Inputs
	}
	if len(out.Locations) == 0 {
		out.Locations = seed.Locations
	}
	if len(out.Evidence) == 0 {
		out.Evidence = seed.Evidence
	}
	if out.Confidence < seed.Confidence {
		out.Confidence = seed.Confidence
	}
	if len(out.Details) == 0 {
		out.Details = seed.Details
	} else {
		// Fill in gaps rather than discarding seed details.
		for k, v := range seed.Details {
			if _, exists := out.Details[k]; !exists {
				out.Details[k] = v
			}
		}
	}
	return out
}

func preferNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
