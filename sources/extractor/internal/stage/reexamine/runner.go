package reexamine

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/runstate"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type PromptFunc func(context.Context, string, string, map[string]any) (map[string]any, error)
type HintFunc func(objectives.Objective) extraction.ObjectiveHints
type EventFunc func(events.Event)

type Runner struct {
	Workers       int
	RunDir        string
	SubDir        string
	MinConfidence float64
	Store         *runstate.CheckpointStore
	Prompt        PromptFunc
	Hints         HintFunc
	Emit          EventFunc
	PathMapper    *extraction.PathMapper
}

type RunInput struct {
	Seeds     []extraction.DetailJob
	RepoFacts *extraction.RepoFacts
	Progress  func()
}

type RunOutput struct {
	Jobs          []extraction.DetailJob
	Unresolved    []model.UnresolvedItem
	Err           error
	FailedTrigger Trigger
}

// Trigger describes why a seed was flagged for re-examination.
type Trigger struct {
	Seed     extraction.Candidate
	Obj      objectives.Objective
	Reason   string
	ReasonID string
}

type (
	detailJob = extraction.DetailJob
	llmEntity = extraction.Candidate
	repoFacts = extraction.RepoFacts
)

// runReexamination executes Stage 2 in parallel. It takes the raw discovery
// seeds, splits them into "clean" and "needs re-ask" groups, fires a targeted
// LLM re-ask for each suspect seed. Clean seeds from discovery pass
// through unchanged; confirmed seeds from re-ask replace the suspect
// originals; rejected seeds become Unresolved.
//
// Returns (cleanSeeds, unresolved, firstErr, firstFailedTrigger). On
// failure the orchestrator halts the run; the trigger gives it the
// objective + seed name needed for the failure report.
func (r Runner) Run(ctx context.Context, input RunInput) RunOutput {
	jobs, unresolved, err, trigger := r.run(ctx, input.Seeds, input.RepoFacts, input.Progress)
	return RunOutput{Jobs: jobs, Unresolved: unresolved, Err: err, FailedTrigger: trigger}
}

func (r Runner) run(
	ctx context.Context,
	seeds []detailJob,
	rf *repoFacts,
	onResult func(),
) ([]detailJob, []model.UnresolvedItem, error, Trigger) {
	var noTrigger Trigger
	if len(seeds) == 0 {
		return nil, nil, nil, noTrigger
	}

	// Load per-item checkpoint so a retry can skip already-completed suspects.
	checkpoint := map[string]runstate.ReexamCheckpointEntry{}
	if r.Store != nil {
		checkpoint = r.Store.LoadReexaminationCheckpoint(filepath.Join(r.RunDir, runstate.StateDir))
	}

	cleanJobs := make([]detailJob, 0, len(seeds))
	suspect := make([]Trigger, 0)
	unresolved := make([]model.UnresolvedItem, 0)

	for i := range seeds {
		// shouldReexamine MUTATES seeds[i].Seed.Details when it can
		// derive missing fields from the name/summary. Take a pointer so
		// the cleanJobs slice carries the enriched entity forward.
		decision := (Policy{}).Run(Input{
			Objective: seeds[i].Objective, Candidate: seeds[i].Seed,
			MinConfidence: r.MinConfidence,
		})
		seeds[i].Seed = decision.Candidate
		if !decision.Needed {
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

		suspect = append(suspect, Trigger{
			Seed:     seeds[i].Seed,
			Obj:      seeds[i].Objective,
			Reason:   decision.Reason,
			ReasonID: decision.ReasonCode,
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

	workers := r.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(suspect) {
		workers = len(suspect)
	}

	type resultEnv struct {
		Trigger       Trigger
		Item          *llmEntity
		Err           error
		PeerCancelled bool
	}
	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan Trigger)
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
				item, err := r.runOne(stageCtx, t, rf)
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
	var firstTrigger Trigger
	for result := range results {
		if onResult != nil {
			onResult()
		}
		if result.Err != nil {
			u := model.UnresolvedItem{
				Kind:       result.Trigger.Obj.Kind,
				Type:       result.Trigger.Obj.Type,
				Name:       result.Trigger.Seed.Name,
				ReasonCode: "reexamine_failure",
				Reason:     result.Err.Error(),
				Confidence: result.Trigger.Seed.Confidence,
			}
			unresolved = append(unresolved, u)
			// Capture this as the root cause unless it's a
			// peer-cancelled sibling (which the worker flagged
			// explicitly). A per-call HTTP timeout wraps
			// context.DeadlineExceeded but happens while the parent
			// is still alive — those MUST surface, not get silently
			// filtered out.
			if firstErr == nil && !result.PeerCancelled {
				firstErr = result.Err
				firstTrigger = result.Trigger
			}
			// Do NOT checkpoint a hard error — the item must be
			// retried. PeerCancelled siblings also skip the
			// checkpoint because they were never actually attempted.
			continue
		}
		if result.Item == nil {
			// The LLM said "not real". C3: a single negative must NOT delete
			// evidence-backed architecture. Only drop when the rejection is
			// CORROBORATED by the seed being structurally unverifiable (no
			// source location to point at). Otherwise downgrade confidence and
			// RETAIN the item (errs toward keeping real findings).
			seed := result.Trigger.Seed
			if SeedStructurallyUnverifiable(seed) {
				u := model.UnresolvedItem{
					Kind:       result.Trigger.Obj.Kind,
					Type:       result.Trigger.Obj.Type,
					Name:       seed.Name,
					ReasonCode: "rejected_on_reexamination",
					Reason:     "Re-examination rejected an unverifiable candidate (no source location; trigger: " + result.Trigger.ReasonID + ")",
					Confidence: seed.Confidence,
					Evidence:   extraction.ToEvidence(seed.Evidence),
				}
				unresolved = append(unresolved, u)
				r.appendCheckpoint(runstate.ReexamCheckpointEntry{
					Key:        runstate.ReexamKey(result.Trigger.Obj.ID, seed.Name),
					Outcome:    "rejected",
					Unresolved: &u,
				})
				continue
			}
			// Evidence-backed but doubted: retain, downgraded and tagged.
			retained := seed
			retained.Confidence = DowngradeConfidence(seed.Confidence, r.MinConfidence)
			retained.Tags = AppendUniqueTag(retained.Tags, "reexamination_doubted")
			cleanJobs = append(cleanJobs, detailJob{Objective: result.Trigger.Obj, Seed: retained})
			r.appendCheckpoint(runstate.ReexamCheckpointEntry{
				Key:     runstate.ReexamKey(result.Trigger.Obj.ID, seed.Name),
				Outcome: "confirmed",
				Seed:    &retained,
			})
			continue
		}
		// Higher of the two confidences wins; keep the corrected item.
		if result.Item.Confidence < result.Trigger.Seed.Confidence {
			result.Item.Confidence = result.Trigger.Seed.Confidence
		}
		cleanJobs = append(cleanJobs, detailJob{Objective: result.Trigger.Obj, Seed: *result.Item})
		r.appendCheckpoint(runstate.ReexamCheckpointEntry{
			Key:     runstate.ReexamKey(result.Trigger.Obj.ID, result.Trigger.Seed.Name),
			Outcome: "confirmed",
			Seed:    result.Item,
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
func SeedStructurallyUnverifiable(seed extraction.Candidate) bool {
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
func DowngradeConfidence(conf, minConf float64) float64 {
	lowered := conf * 0.7
	if lowered < minConf {
		return minConf
	}
	return lowered
}

func AppendUniqueTag(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func (r Runner) runOne(ctx context.Context, trigger Trigger, rf *repoFacts) (*llmEntity, error) {
	hints := extraction.ObjectiveHints{}
	if r.Hints != nil {
		hints = r.Hints(trigger.Obj)
	}
	prompt := extraction.BuildReexaminePrompt(trigger.Obj, trigger.Seed, trigger.ReasonID+": "+trigger.Reason, rf, r.SubDir, hints)
	schema := extraction.EntityListSchemaForObjective(trigger.Obj)
	jobID := "reexamine." + trigger.Obj.ID + "." + extraction.SafeJobID(trigger.Seed.Name)
	started := time.Now()
	r.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "reexamination", JobID: jobID, Status: events.StatusRunning,
		Payload: map[string]any{"objective_id": trigger.Obj.ID, "name": trigger.Seed.Name, "trigger": trigger.ReasonID},
	})
	payload, err := r.Prompt(ctx, jobID, prompt, schema)
	if err != nil {
		r.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "reexamination", JobID: jobID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{"objective_id": trigger.Obj.ID, "name": trigger.Seed.Name, "duration_ms": time.Since(started).Milliseconds()},
		})
		return nil, err
	}
	items := extraction.ParseEntities(payload["items"])
	kept := items[:0]
	for i := range items {
		if extraction.ForceObjectiveType(trigger.Obj, &items[i]) {
			kept = append(kept, items[i])
		}
	}
	items = kept
	if r.PathMapper != nil {
		r.PathMapper.ApplyToEntities(items)
	}
	resolution := "rejected"
	if len(items) > 0 {
		resolution = "rescued"
	}
	r.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "reexamination", JobID: jobID, Status: events.StatusSuccess,
		Payload: map[string]any{
			"objective_id": trigger.Obj.ID, "name": trigger.Seed.Name, "resolution": resolution, "duration_ms": time.Since(started).Milliseconds(),
		},
	})
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func (r Runner) appendCheckpoint(entry runstate.ReexamCheckpointEntry) {
	if r.Store != nil {
		r.Store.AppendReexamEntity(entry)
	}
}

func (r Runner) emit(event events.Event) {
	if r.Emit != nil {
		r.Emit(event)
	}
}
