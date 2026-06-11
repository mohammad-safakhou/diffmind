package agents

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/runstate"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// reexamineTrigger describes why a seed was flagged for re-examination.
type reexamineTrigger struct {
	Seed     llmEntity
	Obj      objectives.Objective
	Reason   string
	ReasonID string
}

// runReexamination executes Stage 2 in parallel. It takes the raw discovery
// seeds, splits them into "clean" and "needs re-ask" groups, fires a targeted
// LLM re-ask for each suspect seed. Clean seeds from discovery pass
// through unchanged; confirmed seeds from re-ask replace the suspect
// originals; rejected seeds become Unresolved.
//
// Returns (cleanSeeds, unresolved, firstErr, firstFailedTrigger). On
// failure the orchestrator halts the run; the trigger gives it the
// objective + seed name needed for the failure report.
func (o *orchestrator) runReexamination(
	ctx context.Context,
	seeds []detailJob,
	rf *repoFacts,
	onResult func(),
) ([]detailJob, []model.UnresolvedItem, error, reexamineTrigger) {
	var noTrigger reexamineTrigger
	if len(seeds) == 0 {
		return nil, nil, nil, noTrigger
	}

	// Load per-item checkpoint so a retry can skip already-completed suspects.
	checkpoint := o.store.LoadReexaminationCheckpoint(filepath.Join(o.runDir, stateDir))

	cleanJobs := make([]detailJob, 0, len(seeds))
	suspect := make([]reexamineTrigger, 0)
	unresolved := make([]model.UnresolvedItem, 0)

	for i := range seeds {
		// shouldReexamine MUTATES seeds[i].Seed.Details when it can
		// derive missing fields from the name/summary. Take a pointer so
		// the cleanJobs slice carries the enriched entity forward.
		reasonID, reason, needs := shouldReexamine(seeds[i].Objective, &seeds[i].Seed, o.cfg.Quality.MinConfidence)
		if !needs {
			cleanJobs = append(cleanJobs, seeds[i])
			continue
		}

		key := runstate.ReexamKey(seeds[i].Objective.ID, seeds[i].Seed.Name)
		if entry, done := checkpoint[key]; done {
			// This suspect was already resolved in a prior run.
			// Restore the outcome without re-asking the model.
			switch entry.Outcome {
			case "confirmed":
				if entry.Seed != nil {
					cleanJobs = append(cleanJobs, detailJob{Objective: seeds[i].Objective, Seed: *entry.Seed})
				}
			case "rejected":
				if entry.Unresolved != nil {
					unresolved = append(unresolved, *entry.Unresolved)
				}
			}
			if onResult != nil {
				onResult()
			}
			continue
		}

		suspect = append(suspect, reexamineTrigger{
			Seed:     seeds[i].Seed,
			Obj:      seeds[i].Objective,
			Reason:   reason,
			ReasonID: reasonID,
		})
	}

	if len(suspect) == 0 {
		skipped := len(checkpoint)
		util.Info("agents.reexamine", "no suspects to re-examine", map[string]any{
			"total": len(seeds), "skipped_from_checkpoint": skipped,
		})
		return cleanJobs, unresolved, nil, noTrigger
	}

	util.Info("agents.reexamine", "re-examination starting", map[string]any{
		"total": len(seeds), "clean": len(cleanJobs), "suspect": len(suspect),
		"skipped_from_checkpoint": len(checkpoint),
	})

	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(suspect) {
		workers = len(suspect)
	}

	type resultEnv struct {
		Trigger       reexamineTrigger
		Item          *llmEntity
		Err           error
		PeerCancelled bool
	}
	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan reexamineTrigger)
	results := make(chan resultEnv)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for t := range jobs {
				if stageCtx.Err() != nil {
					// Peer-cancelled: another worker tripped
					// fail-fast before this job ran.
					results <- resultEnv{Trigger: t, Err: stageCtx.Err(), PeerCancelled: true}
					continue
				}
				item, err := o.runReexamineOne(stageCtx, t, rf)
				results <- resultEnv{Trigger: t, Item: item, Err: err}
				if err != nil {
					cancel()
				}
			}
		}(i + 1)
	}
	go func() {
		for _, t := range suspect {
			jobs <- t
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var firstErr error
	var firstTrigger reexamineTrigger
	for r := range results {
		if onResult != nil {
			onResult()
		}
		if r.Err != nil {
			u := model.UnresolvedItem{
				Kind:       r.Trigger.Obj.Kind,
				Type:       r.Trigger.Obj.Type,
				Name:       r.Trigger.Seed.Name,
				ReasonCode: "reexamine_failure",
				Reason:     r.Err.Error(),
				Confidence: r.Trigger.Seed.Confidence,
			}
			unresolved = append(unresolved, u)
			// Capture this as the root cause unless it's a
			// peer-cancelled sibling (which the worker flagged
			// explicitly). A per-call HTTP timeout wraps
			// context.DeadlineExceeded but happens while the parent
			// is still alive — those MUST surface, not get silently
			// filtered out.
			if firstErr == nil && !r.PeerCancelled {
				firstErr = r.Err
				firstTrigger = r.Trigger
			}
			// Do NOT checkpoint a hard error — the item must be
			// retried. PeerCancelled siblings also skip the
			// checkpoint because they were never actually attempted.
			continue
		}
		if r.Item == nil {
			// The LLM said "not real". C3: a single negative must NOT delete
			// evidence-backed architecture. Only drop when the rejection is
			// CORROBORATED by the seed being structurally unverifiable (no
			// source location to point at). Otherwise downgrade confidence and
			// RETAIN the item (errs toward keeping real findings).
			seed := r.Trigger.Seed
			if seedStructurallyUnverifiable(seed) {
				u := model.UnresolvedItem{
					Kind:       r.Trigger.Obj.Kind,
					Type:       r.Trigger.Obj.Type,
					Name:       seed.Name,
					ReasonCode: "rejected_on_reexamination",
					Reason:     "Re-examination rejected an unverifiable candidate (no source location; trigger: " + r.Trigger.ReasonID + ")",
					Confidence: seed.Confidence,
					Evidence:   ToEvidence(seed.Evidence),
				}
				unresolved = append(unresolved, u)
				o.store.AppendReexamEntity(runstate.ReexamCheckpointEntry{
					Key:        runstate.ReexamKey(r.Trigger.Obj.ID, seed.Name),
					Outcome:    "rejected",
					Unresolved: &u,
				})
				continue
			}
			// Evidence-backed but doubted: retain, downgraded and tagged.
			retained := seed
			retained.Confidence = downgradeConfidence(seed.Confidence, o.cfg.Quality.MinConfidence)
			retained.Tags = appendUniqueTag(retained.Tags, "reexamination_doubted")
			cleanJobs = append(cleanJobs, detailJob{Objective: r.Trigger.Obj, Seed: retained})
			o.store.AppendReexamEntity(runstate.ReexamCheckpointEntry{
				Key:     runstate.ReexamKey(r.Trigger.Obj.ID, seed.Name),
				Outcome: "confirmed",
				Seed:    &retained,
			})
			continue
		}
		// Higher of the two confidences wins; keep the corrected item.
		if r.Item.Confidence < r.Trigger.Seed.Confidence {
			r.Item.Confidence = r.Trigger.Seed.Confidence
		}
		cleanJobs = append(cleanJobs, detailJob{Objective: r.Trigger.Obj, Seed: *r.Item})
		o.store.AppendReexamEntity(runstate.ReexamCheckpointEntry{
			Key:     runstate.ReexamKey(r.Trigger.Obj.ID, r.Trigger.Seed.Name),
			Outcome: "confirmed",
			Seed:    r.Item,
		})
	}

	util.Info("agents.reexamine", "re-examination completed", map[string]any{
		"clean_after": len(cleanJobs), "unresolved": len(unresolved),
	})
	return cleanJobs, unresolved, firstErr, firstTrigger
}

// seedStructurallyUnverifiable reports whether a rejected seed lacks the
// minimum to be a real, locatable fact (no name, no type, or no source
// location). Only such a seed may be deleted on a single LLM "no" (C3) — the
// rejection is then corroborated by the absence of verifiable evidence.
func seedStructurallyUnverifiable(seed llmEntity) bool {
	if strings.TrimSpace(seed.Name) == "" || strings.TrimSpace(seed.Type) == "" {
		return true
	}
	for _, l := range seed.Locations {
		if strings.TrimSpace(l.File) != "" {
			return false
		}
	}
	return true
}

// downgradeConfidence lowers a doubted seed's confidence while keeping it at or
// above the run's MinConfidence floor so the retained item still survives the
// downstream confidence gate.
func downgradeConfidence(conf, minConf float64) float64 {
	lowered := conf * 0.7
	if lowered < minConf {
		return minConf
	}
	return lowered
}

func appendUniqueTag(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func (o *orchestrator) runReexamineOne(ctx context.Context, t reexamineTrigger, rf *repoFacts) (*llmEntity, error) {
	prompt := BuildReexaminePrompt(t.Obj, t.Seed, t.ReasonID+": "+t.Reason, rf, o.subDir, o.hintsFor(t.Obj, nil))
	schema := EntityListSchemaForObjective(t.Obj)
	jobID := "reexamine." + t.Obj.ID + "." + SafeJobID(t.Seed.Name)
	started := time.Now()
	o.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "reexamination", JobID: jobID, Status: events.StatusRunning,
		Payload: map[string]any{"objective_id": t.Obj.ID, "name": t.Seed.Name, "trigger": t.ReasonID},
	})
	payload, err := o.promptAgent(ctx, jobID, prompt, schema)
	if err != nil {
		o.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "reexamination", JobID: jobID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{"objective_id": t.Obj.ID, "name": t.Seed.Name, "duration_ms": time.Since(started).Milliseconds()},
		})
		return nil, err
	}
	items := ParseEntities(payload["items"])
	kept := items[:0]
	for i := range items {
		if ForceObjectiveType(t.Obj, &items[i]) {
			kept = append(kept, items[i])
		}
	}
	items = kept
	o.PathMapper().ApplyToEntities(items)
	resolution := "rejected"
	if len(items) > 0 {
		resolution = "rescued"
	}
	o.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "reexamination", JobID: jobID, Status: events.StatusSuccess,
		Payload: map[string]any{
			"objective_id": t.Obj.ID, "name": t.Seed.Name, "resolution": resolution, "duration_ms": time.Since(started).Milliseconds(),
		},
	})
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}
