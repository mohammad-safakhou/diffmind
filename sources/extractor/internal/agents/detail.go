package agents

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// runDetailBatch executes Stage 3. Detail enrichment turns each
// stage-2 seed (a confirmed candidate with a name, type, and
// source_locations) into a fully-enriched entity with method/path,
// table, transaction context, evidence snippets, etc.
//
// Architecture (this version):
//
//  1. Group seeds into objective-homogenous batches using
//     detailGroups (file-path + name affinity). Soft target 8 per
//     batch, hard cap 12. For a typical Spring Boot service this
//     compresses ~150 single-entity LLM calls down to ~20 batched
//     calls without sacrificing quality.
//
//  2. Skip seeds that are already in the per-entity checkpoint
//     (state/detail_entities.jsonl). This makes retries cheap:
//     even if the stage failed at entity 75 of 156, the retry only
//     pays for entities 76+.
//
//  3. Workers run BATCHES (not single seeds). The batch worker
//     issues one LLM call, receives N enriched items, and writes
//     each to the checkpoint immediately. A mid-batch crash loses
//     at most the in-flight batch's worth.
//
// Fail-fast: the first batch failure cancels the stage context;
// peer batches see ctx.Err() and return PeerCancelled results.
// The orchestrator picks the first non-collateral failure as the
// root cause.
func (o *orchestrator) runDetailBatch(ctx context.Context, jobs []detailJob, rf *repoFacts, onResult func()) []detailResult {
	if len(jobs) == 0 {
		return nil
	}

	// Load the per-entity checkpoint from disk. This is empty on a
	// fresh run; on a retry it contains every entity that was
	// successfully enriched (or explicitly skipped by the model) on
	// the previous attempt.
	checkpoint := map[string]detailCheckpointEntry{}
	if o.runDir != "" {
		checkpoint = o.loadDetailCheckpoint(o.runDir + "/" + stateDir)
	}

	// Partition jobs into "already done" (from the checkpoint, or complete
	// deterministic seeds ready to use without an LLM call) and "pending"
	// (will be batched + sent to the LLM).
	var pending []detailJob
	carriedResults := make([]detailResult, 0, len(checkpoint))
	carriedMessages := map[string]string{}
	for _, j := range jobs {
		key := detailEntityKey(j.Objective.ID, j.Seed.Name)
		entry, ok := checkpoint[key]
		if !ok {
			if isCompleteDeterministicSeed(j.Objective, &j.Seed) {
				seed := j.Seed
				carriedResults = append(carriedResults, detailResult{Objective: j.Objective, SeedName: j.Seed.Name, Item: &seed})
				carriedMessages[key] = "complete deterministic seed"
				if checkpointEntry, ok := o.detailCheckpointForSeed(j); ok {
					o.appendDetailEntity(checkpointEntry)
				}
			} else {
				pending = append(pending, j)
			}
			continue
		}
		// Reconstruct the detailResult from the checkpoint.
		res := detailResult{Objective: j.Objective, SeedName: j.Seed.Name}
		switch {
		case entry.Exposure != nil:
			res.Item = exposureToEntity(*entry.Exposure)
		case entry.Dependency != nil:
			res.Item = dependencyToEntity(*entry.Dependency)
		case entry.Skipped:
			// Model previously said "can't enrich". Honour that.
			res.Item = nil
		default:
			// Corrupt entry; treat as pending.
			pending = append(pending, j)
			continue
		}
		carriedResults = append(carriedResults, res)
		carriedMessages[key] = "resumed from per-entity checkpoint"
	}
	if len(checkpoint) > 0 {
		util.Info("agents.detail", "loaded detail checkpoint", map[string]any{
			"checkpointed": len(checkpoint),
			"reused":       len(carriedResults),
			"pending":      len(pending),
			"total":        len(jobs),
		})
	}

	// Quickly emit "skipped" job events for the checkpointed entries
	// so the dashboard's pipeline strip shows progress.
	for _, r := range carriedResults {
		jobID := "detail." + r.Objective.ID + "." + safeJobID(r.SeedName)
		key := detailEntityKey(r.Objective.ID, r.SeedName)
		message := carriedMessages[key]
		if message == "" {
			message = "resumed from per-entity checkpoint"
		}
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "detail", JobID: jobID, Status: events.StatusSkipped,
			Message:  message,
			ParentID: "discover." + r.Objective.ID,
			Payload: map[string]any{
				"objective_id":  r.Objective.ID,
				"name":          r.SeedName,
				"resumed":       message == "resumed from per-entity checkpoint",
				"deterministic": message == "complete deterministic seed",
			},
		})
		if onResult != nil {
			onResult()
		}
	}

	if len(pending) == 0 {
		util.Info("agents.detail", "all detail entities already checkpointed; nothing to do", map[string]any{
			"total": len(jobs),
		})
		return carriedResults
	}

	// Group the pending jobs into batches of related entities.
	batches := detailGroups(pending)
	util.Info("agents.detail", "detail batches assembled", map[string]any{
		"pending_entities": len(pending),
		"batches":          len(batches),
		"avg_size":         float64(len(pending)) / float64(len(batches)),
	})

	// Emit per-batch and per-entity placeholder events BEFORE any
	// worker starts. This is what lets the dashboard's LiveGraph
	// pre-render the batch nodes (with their entity children) so
	// the user can see the structure of the work before any LLM
	// call returns. The unique batch id is composed of the
	// objective ID plus the first seed's safe name — the same
	// formula runDetailBatchOne uses, so they always match.
	for _, b := range batches {
		if len(b) == 0 {
			continue
		}
		batchID := detailBatchJobID(b)
		// Build a short preview list of the seed names so the
		// dashboard can show "12: GET /a, GET /b, GET /c, … (9 more)"
		// without having to fetch each entity record.
		names := make([]string, 0, len(b))
		for _, j := range b {
			names = append(names, j.Seed.Name)
		}
		o.emit(events.Event{
			Kind: events.KindJobPending, Stage: "detail", JobID: batchID, Status: events.StatusPending,
			ParentID: "discover." + b[0].Objective.ID,
			Payload: map[string]any{
				"batch":        true,
				"batch_size":   len(b),
				"objective_id": b[0].Objective.ID,
				"seed_names":   names,
				"name":         batchDisplayName(b),
			},
		})
		for _, j := range b {
			jobID := "detail." + j.Objective.ID + "." + safeJobID(j.Seed.Name)
			o.emit(events.Event{
				Kind: events.KindJobPending, Stage: "detail", JobID: jobID, Status: events.StatusPending,
				// Crucially: parent is the BATCH node, not the
				// objective. This makes the graph render batches
				// as the intermediate layer.
				ParentID: batchID,
				Payload: map[string]any{
					"objective_id": j.Objective.ID,
					"name":         j.Seed.Name,
					"type":         j.Seed.Type,
					"batch_id":     batchID,
					"batch_size":   len(b),
				},
			})
		}
	}

	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(batches) {
		workers = len(batches)
	}

	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	batchCh := make(chan []detailJob)
	resCh := make(chan detailResult)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for batch := range batchCh {
				if stageCtx.Err() != nil {
					// Peer-cancelled: another worker tripped fail-fast.
					// Surface a PeerCancelled result for every entity
					// in this batch so progress tallies stay accurate.
					for _, j := range batch {
						resCh <- detailResult{
							Objective:     j.Objective,
							SeedName:      j.Seed.Name,
							Err:           stageCtx.Err(),
							PeerCancelled: true,
						}
					}
					continue
				}
				results, err := o.runDetailBatchOne(stageCtx, batch, rf)
				if err != nil {
					// The batch as a whole failed. Surface the error
					// against EACH entity in the batch (so the
					// orchestrator's "first failure" logic still
					// has names to attribute) and trip fail-fast.
					for _, j := range batch {
						resCh <- detailResult{
							Objective: j.Objective,
							SeedName:  j.Seed.Name,
							Err:       err,
						}
					}
					cancel()
					continue
				}
				// Success path: stream individual results back to the
				// collector AND append each to the on-disk checkpoint
				// so a future retry can skip them.
				for _, r := range results {
					o.checkpointDetailResult(r)
					resCh <- r
				}
			}
		}(i + 1)
	}

	go func() {
		for _, b := range batches {
			batchCh <- b
		}
		close(batchCh)
		wg.Wait()
		close(resCh)
	}()

	out := make([]detailResult, 0, len(jobs))
	out = append(out, carriedResults...)
	for r := range resCh {
		out = append(out, r)
		if onResult != nil {
			onResult()
		}
	}
	return out
}

// checkpointDetailResult appends a successful detail result to the
// per-entity checkpoint so a future retry can skip this entity.
// Failed results are NOT checkpointed (we want the retry to redo
// them); explicit nil-item results ARE checkpointed (the model
// definitively said "can't enrich", honour that).
func (o *orchestrator) checkpointDetailResult(r detailResult) {
	if r.Err != nil {
		return
	}
	entry := detailCheckpointEntry{
		Key:         detailEntityKey(r.Objective.ID, r.SeedName),
		ObjectiveID: r.Objective.ID,
		SeedName:    r.SeedName,
	}
	if r.Item == nil {
		entry.Skipped = true
		o.appendDetailEntity(entry)
		return
	}
	// Convert the enriched llmEntity into the final model.* shape
	// that matches what the assembler would produce. We do this here
	// (eagerly) so the checkpoint already holds the assembled value;
	// on resume we can hand it back without re-running toBase().
	base, ur := toBase(o.repoPath, r.Objective, *r.Item, o.cfg.Quality.MinConfidence)
	if ur != nil {
		// The result didn't survive validation (low confidence, no
		// source_location, etc.). Don't checkpoint as a success;
		// let the orchestrator's normal unresolved-handling kick in.
		return
	}
	if r.Objective.Kind == model.KindExposure {
		exp := model.Exposure{BaseEntity: base}
		entry.Exposure = &exp
	} else {
		dep := model.Dependency{BaseEntity: base}
		entry.Dependency = &dep
	}
	o.appendDetailEntity(entry)
}

// runDetailBatchOne issues ONE LLM call for a batch of related
// seeds and returns one detailResult per input seed. If the model
// returns fewer items than seeds, missing entries surface as
// Skipped=true results (with no error) — same effect as today's
// "item: null" path for single calls.
//
// The function is intentionally short on policy: every decision
// the orchestrator wants to make (fail-fast, retry, checkpoint) is
// made by the caller based on the returned slice.
func (o *orchestrator) runDetailBatchOne(ctx context.Context, batch []detailJob, rf *repoFacts) ([]detailResult, error) {
	if len(batch) == 0 {
		return nil, nil
	}
	obj := batch[0].Objective
	batchJobID := detailBatchJobID(batch)

	seeds := make([]llmEntity, len(batch))
	for i, j := range batch {
		seeds[i] = j.Seed
	}

	started := time.Now()

	// Emit a BATCH-level job_started so the LiveGraph and Timeline
	// can render this as an intermediate node (parent of the
	// entities, child of the objective). This is the unit of work
	// the LLM actually processes, and the dashboard's
	// "X/N batches" counter reads off these events.
	names := make([]string, 0, len(batch))
	for _, j := range batch {
		names = append(names, j.Seed.Name)
	}
	o.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "detail", JobID: batchJobID, Status: events.StatusRunning,
		ParentID: "discover." + obj.ID,
		Payload: map[string]any{
			"batch":        true,
			"batch_size":   len(batch),
			"objective_id": obj.ID,
			"seed_names":   names,
			"name":         batchDisplayName(batch),
		},
	})

	// Emit a job_started event for EACH entity in the batch so the
	// dashboard's per-entity progress bar still advances. ParentID
	// is the batch node, not the objective — that's what makes the
	// graph render entities under their batch.
	for _, j := range batch {
		entityJobID := "detail." + j.Objective.ID + "." + safeJobID(j.Seed.Name)
		o.emit(events.Event{
			Kind: events.KindJobStarted, Stage: "detail", JobID: entityJobID, Status: events.StatusRunning,
			ParentID: batchJobID,
			Payload: map[string]any{
				"objective_id": j.Objective.ID,
				"name":         j.Seed.Name,
				"type":         j.Seed.Type,
				"batch_id":     batchJobID,
				"batch_size":   len(batch),
			},
		})
	}

	prompt := buildDetailBatchPrompt(obj, seeds, rf, o.subDir, o.hintsFor(obj, nil))
	schema := entityListSchemaForObjective(obj)
	payload, err := o.promptAgent(ctx, batchJobID, prompt, schema)
	dur := time.Since(started)
	if err != nil {
		// Emit job_failed for the BATCH and for each entity in it.
		// The batch event surfaces the failure at the intermediate
		// graph node; the entity events keep the per-entity tally
		// correct on the pipeline strip.
		o.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "detail", JobID: batchJobID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{
				"batch":        true,
				"batch_size":   len(batch),
				"objective_id": obj.ID,
				"duration_ms":  dur.Milliseconds(),
			},
		})
		for _, j := range batch {
			entityJobID := "detail." + j.Objective.ID + "." + safeJobID(j.Seed.Name)
			o.emit(events.Event{
				Kind: events.KindJobFailed, Stage: "detail", JobID: entityJobID, Status: events.StatusFailed,
				Message: err.Error(),
				Payload: map[string]any{
					"objective_id": j.Objective.ID,
					"name":         j.Seed.Name,
					"batch_id":     batchJobID,
					"duration_ms":  dur.Milliseconds(),
				},
			})
		}
		return nil, err
	}

	// Parse the response. The model is contractually expected to
	// return one item per input seed in the same order under
	// payload.items. Be tolerant of older / smaller models that
	// might return the single-entity {"item": {...}} shape — treat
	// that as a 1-item array. Also tolerant of {"items": null} from
	// schema validators that wrote the field but had nothing for it.
	items := parseEntities(payload["items"])
	if len(items) == 0 {
		if single, ok := payload["item"]; ok && single != nil {
			if it := parseSingleEntity(single); it != nil {
				items = []llmEntity{*it}
			}
		}
	}
	o.pathMapper().applyToEntities(items)

	results := make([]detailResult, 0, len(batch))
	for i, j := range batch {
		entityJobID := "detail." + j.Objective.ID + "." + safeJobID(j.Seed.Name)
		var item *llmEntity
		if i < len(items) {
			it := items[i]
			// Guard against the LLM rewriting the entity into
			// something of a different type. Same logic as the
			// single-entity path.
			forceObjectiveType(j.Objective, &it)
			if strings.TrimSpace(it.Name) == "" {
				it.Name = j.Seed.Name
			}
			merged := mergeEnrichment(j.Seed, it)
			item = &merged
		}
		// The model marked this seed as "details_complete: false"?
		// Treat it the same as item==nil — keep the seed as-is.
		if item != nil {
			if v, ok := item.Details["details_complete"]; ok {
				if b, ok := v.(bool); ok && !b {
					o.emit(events.Event{
						Kind: events.KindJobCompleted, Stage: "detail", JobID: entityJobID, Status: events.StatusSkipped,
						Message: "model marked seed details_complete:false",
						Payload: map[string]any{
							"objective_id": j.Objective.ID, "name": j.Seed.Name,
							"batch_id": batchJobID, "duration_ms": dur.Milliseconds(),
						},
					})
					results = append(results, detailResult{
						Objective: j.Objective, SeedName: j.Seed.Name, Item: nil,
					})
					continue
				}
			}
			o.emit(events.Event{
				Kind: events.KindJobCompleted, Stage: "detail", JobID: entityJobID, Status: events.StatusSuccess,
				Payload: map[string]any{
					"objective_id": j.Objective.ID,
					"name":         item.Name,
					"type":         item.Type,
					"confidence":   item.Confidence,
					"batch_id":     batchJobID,
					"duration_ms":  dur.Milliseconds(),
				},
			})
		} else {
			o.emit(events.Event{
				Kind: events.KindJobCompleted, Stage: "detail", JobID: entityJobID, Status: events.StatusSkipped,
				Message: "model omitted seed from batch response",
				Payload: map[string]any{
					"objective_id": j.Objective.ID, "name": j.Seed.Name,
					"batch_id": batchJobID, "duration_ms": dur.Milliseconds(),
				},
			})
		}
		results = append(results, detailResult{
			Objective: j.Objective, SeedName: j.Seed.Name, Item: item,
		})
	}

	// Batch-level completion event — surfaces the per-batch
	// duration and the number of items the model returned vs.
	// asked. The LiveGraph reads this to mark the batch node
	// green / yellow / red.
	succeeded, skipped := 0, 0
	for _, r := range results {
		if r.Item != nil {
			succeeded++
		} else {
			skipped++
		}
	}
	o.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "detail", JobID: batchJobID, Status: events.StatusSuccess,
		Payload: map[string]any{
			"batch":        true,
			"batch_size":   len(batch),
			"objective_id": obj.ID,
			"duration_ms":  dur.Milliseconds(),
			"succeeded":    succeeded,
			"skipped":      skipped,
		},
	})
	return results, nil
}

// detailBatchJobID is the deterministic identifier we use for the
// batch node in the events graph. Composed of the objective ID +
// a "batch." prefix + the safe-jobid'd name of the first seed.
// Two batches in the same objective with different first seeds
// always get different IDs. If somehow two batches share the same
// first seed name (extremely unlikely; would require duplicate
// names in the seed list) the IDs collide, but at worst that
// means the two batches share a node in the graph — not a
// correctness issue downstream.
func detailBatchJobID(batch []detailJob) string {
	if len(batch) == 0 {
		return "detail.batch.empty"
	}
	first := batch[0].Seed.Name
	if first == "" {
		first = "anon"
	}
	return "detail." + batch[0].Objective.ID + ".batch." + safeJobID(first)
}

// batchDisplayName returns a human-friendly label used in the
// dashboard's graph + timeline. Shows the first 2-3 seed names plus
// a "+N more" tail when the batch is bigger. Keeps the label short
// enough to fit in a node.
func batchDisplayName(batch []detailJob) string {
	if len(batch) == 0 {
		return "batch (empty)"
	}
	const preview = 2
	parts := []string{}
	for i, j := range batch {
		if i >= preview {
			break
		}
		parts = append(parts, j.Seed.Name)
	}
	out := "batch: " + strings.Join(parts, ", ")
	if len(batch) > preview {
		out += " (+" + itoa(len(batch)-preview) + " more)"
	}
	return out
}

// exposureToEntity / dependencyToEntity rebuild an llmEntity from a
// previously-persisted model entity. We use these on the resume
// path: the checkpoint already stores the fully-assembled
// model.Exposure / model.Dependency, so we need to round-trip back
// to llmEntity for the orchestrator's downstream code that still
// works in llmEntity-space until the next assembly point.
func exposureToEntity(e model.Exposure) *llmEntity {
	return entityFromBase(e.BaseEntity)
}
func dependencyToEntity(d model.Dependency) *llmEntity {
	return entityFromBase(d.BaseEntity)
}

func entityFromBase(b model.BaseEntity) *llmEntity {
	e := &llmEntity{
		Type:       b.Type,
		Name:       b.Name,
		Summary:    b.Summary,
		Confidence: b.Confidence,
		Tags:       append([]string(nil), b.Tags...),
		Actions:    append([]string(nil), b.KeyActions...),
		Details:    map[string]any{},
	}
	for k, v := range b.Details {
		e.Details[k] = v
	}
	for _, loc := range b.Locations {
		e.Locations = append(e.Locations, llmLocation{
			File: loc.File, StartLine: loc.StartLine, EndLine: loc.EndLine,
		})
	}
	for _, ev := range b.Evidence {
		e.Evidence = append(e.Evidence, llmEvidence{
			File:      ev.Location.File,
			StartLine: ev.Location.StartLine,
			EndLine:   ev.Location.EndLine,
			Snippet:   ev.Snippet,
			Source:    ev.Source,
		})
	}
	for _, in := range b.Inputs {
		e.Inputs = append(e.Inputs, llmInput{
			Name:        in.Name,
			Type:        in.Type,
			Required:    in.Required,
			Description: in.Description,
		})
	}
	return e
}

// mergeEnrichment overlays the detail response on top of the seed.
// Enriched fields win when non-empty; otherwise seed values are
// preserved. Unchanged from the single-entity path.
func mergeEnrichment(seed, enriched llmEntity) llmEntity {
	out := enriched
	out.Type = seed.Type
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
