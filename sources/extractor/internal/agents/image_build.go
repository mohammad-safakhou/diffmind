package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/indexer"
	"github.com/mohammad-safakhou/diffmind/internal/indexerbuild/recipe"
	"github.com/mohammad-safakhou/diffmind/internal/langdetect"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// kickoffImageBuild spawns the goroutine that builds the SCIP
// indexer image in parallel with Stages 1-3 of the pipeline. It
// returns immediately. The orchestrator's runIndexStage later
// blocks on `<-o.buildDone` to consume the result.
//
// When cfg.Indexer.Disabled is true OR there are no supported
// languages, we still send a buildOutcome (Skipped or Err) so the
// index stage's wait completes promptly.
func (o *orchestrator) kickoffImageBuild(ctx context.Context, rf *repoFacts) {
	if o.buildDone == nil {
		o.buildDone = make(chan struct{})
	}
	go o.runImageBuild(ctx, rf)
}

// runImageBuild is the workhorse: derive a recipe.Plan from the
// stage-0 LanguageFacts, then invoke indexer.Builder to build
// every base image + the composite. Streams events into the bus
// so the dashboard's graph node tracks state transitions:
//
//   - "index.build" stage started/in_progress
//   - per-base job_started / job_completed
//   - composite job_started / job_completed
//   - stage_completed (success or failure)
//
// On test paths where o.indexerOverride is set, the build is
// SKIPPED — tests inject a fake indexer.Indexer that doesn't
// need a real image.
func (o *orchestrator) runImageBuild(ctx context.Context, rf *repoFacts) {
	defer close(o.buildDone)

	// Indexer disabled by config → skip.
	if o.cfg.Indexer.Disabled {
		o.buildResult = buildOutcome{Skipped: true}
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index.build", Status: events.StatusSkipped,
			Message: "indexer disabled by config",
		})
		return
	}

	// Test override → skip; the override is a fake driver.
	if o.indexerOverride != nil {
		o.buildResult = buildOutcome{Skipped: true}
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index.build", Status: events.StatusSkipped,
			Message: "test override active; no real image build",
		})
		return
	}

	// If the user pinned a specific image via cfg.Indexer.Image,
	// honour it and skip the recipe build. The image either
	// exists locally (instant success) or fails the docker run
	// later (fail-soft, shallow matcher fallback).
	if explicit := strings.TrimSpace(o.cfg.Indexer.Image); explicit != "" {
		o.buildResult = buildOutcome{Image: explicit}
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index.build", Status: events.StatusSkipped,
			Message: "using explicit indexer image: " + explicit,
		})
		return
	}

	// Resolve language facts. We accept either the stage-0 LLM
	// facts OR an empty repoFacts; in the latter case the
	// orchestrator still works because langdetect already ran
	// inside runRepoFacts.
	facts := languageFactsForRecipe(rf)
	if len(facts) == 0 {
		o.buildResult = buildOutcome{
			Skipped: true,
		}
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index.build", Status: events.StatusSkipped,
			Message: "no supported languages detected; connections will use shallow matcher",
		})
		return
	}

	plan, err := recipe.Generate(facts)
	if err != nil {
		o.buildResult = buildOutcome{Err: err}
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index.build", Status: events.StatusFailed,
			Message: "recipe generation failed: " + err.Error(),
		})
		return
	}

	resolvedJSON, _ := json.Marshal(plan.Resolved)
	o.emit(events.Event{
		Kind: events.KindStageStarted, Stage: "index.build", Status: events.StatusRunning,
		Payload: map[string]any{
			"composite":   plan.Composite.Tag,
			"bases":       len(plan.Base),
			"resolved":    json.RawMessage(resolvedJSON),
			"tip":         "Building per-language base images and composing the SCIP indexer image.",
		},
	})

	b := indexer.NewBuilder()
	streamer := newImageBuildStreamer(o, "index.build")
	b.Stderr = streamer
	b.Stdout = streamer

	// Emit per-base job_started events BEFORE invoking the
	// EnsureRecipe (which builds sequentially). We re-emit
	// job_completed/failed once the build returns.
	for _, j := range plan.Base {
		o.emit(events.Event{
			Kind: events.KindJobPending, Stage: "index.build",
			JobID:  "index.build." + sanitiseJobName(j.Tag),
			Status: events.StatusPending,
			Payload: map[string]any{
				"tag":      j.Tag,
				"language": string(j.Language),
				"kind":     j.Kind,
			},
		})
	}
	o.emit(events.Event{
		Kind: events.KindJobPending, Stage: "index.build",
		JobID:   "index.build.composite",
		Status:  events.StatusPending,
		Payload: map[string]any{"tag": plan.Composite.Tag, "kind": "composite"},
	})

	start := time.Now()
	res := b.EnsureRecipeSingleflight(ctx, plan)
	totalDur := time.Since(start).Milliseconds()

	// Translate per-job results into events.
	failed := false
	for i, br := range res.Bases {
		jobID := "index.build." + sanitiseJobName(plan.Base[i].Tag)
		if br.Err != nil {
			failed = true
			o.emit(events.Event{
				Kind: events.KindJobFailed, Stage: "index.build", JobID: jobID,
				Status:  events.StatusFailed,
				Message: br.Err.Error(),
				Payload: map[string]any{
					"tag":      br.Tag,
					"log_tail": truncate(br.LogTail, 4000),
					"built":    br.Built,
				},
			})
			continue
		}
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "index.build", JobID: jobID,
			Status: events.StatusSuccess,
			Payload: map[string]any{
				"tag":   br.Tag,
				"built": br.Built,
				"cached": !br.Built,
			},
		})
	}
	if res.Composite.Err != nil {
		failed = true
		o.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "index.build", JobID: "index.build.composite",
			Status:  events.StatusFailed,
			Message: res.Composite.Err.Error(),
			Payload: map[string]any{
				"tag":      res.Composite.Tag,
				"log_tail": truncate(res.Composite.LogTail, 4000),
				"built":    res.Composite.Built,
			},
		})
	} else {
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "index.build", JobID: "index.build.composite",
			Status: events.StatusSuccess,
			Payload: map[string]any{
				"tag":    res.Composite.Tag,
				"built":  res.Composite.Built,
				"cached": !res.Composite.Built,
			},
		})
	}

	if failed {
		// Compose a single error that aggregates the per-job
		// failures so haltFailure-style summaries are concise.
		var msgs []string
		for _, br := range res.Bases {
			if br.Err != nil {
				msgs = append(msgs, fmt.Sprintf("%s: %s", br.Tag, br.Err))
			}
		}
		if res.Composite.Err != nil {
			msgs = append(msgs, fmt.Sprintf("composite %s: %s", res.Composite.Tag, res.Composite.Err))
		}
		o.buildResult = buildOutcome{
			Err:          fmt.Errorf("indexer image build failed: %s", strings.Join(msgs, "; ")),
			PlanResolved: string(resolvedJSON),
		}
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "index.build", Status: events.StatusFailed,
			Message: msgs[0],
			Payload: map[string]any{
				"duration_ms": totalDur,
				"composite":   plan.Composite.Tag,
			},
		})
		util.Error("agents.image_build", "image build failed; cancelling pipeline (fail-fast)", map[string]any{
			"composite": plan.Composite.Tag,
			"failures":  len(msgs),
		})
		// FAIL-FAST: cancel the pipeline-wide context so any
		// in-flight LLM call in Stages 1-3 exits promptly. The
		// orchestrator's main loop will then reach
		// runIndexStage, observe o.buildResult.Err, and emit a
		// proper halt failure via haltFailure(). The downstream
		// stages (connections, reconcile) never run.
		//
		// Note: cancelling here AFTER setting buildResult and
		// emitting the stage_completed event guarantees the
		// dashboard sees the failure cause BEFORE the
		// run_failed event arrives.
		if o.pipelineCancel != nil {
			o.pipelineCancel()
		}
		return
	}

	o.buildResult = buildOutcome{
		Image:        res.Composite.Tag,
		PlanResolved: string(resolvedJSON),
	}
	o.emit(events.Event{
		Kind: events.KindStageCompleted, Stage: "index.build", Status: events.StatusSuccess,
		Payload: map[string]any{
			"duration_ms": totalDur,
			"composite":   plan.Composite.Tag,
			"bases":       len(plan.Base),
		},
	})
}

// waitForImageBuild blocks until the build goroutine has reported
// its outcome. Returns (image, err, skipped). The caller is the
// index stage, which uses image to override the indexer
// RunRequest's image field.
//
// Per the user's design choice we BLOCK INDEFINITELY here — no
// timeout. The build either completes (success or hard error) or
// the parent context is cancelled.
func (o *orchestrator) waitForImageBuild(ctx context.Context) (string, error, bool) {
	if o.buildDone == nil {
		// No build was kicked off (e.g. test paths that bypass
		// the parallel build entirely). Treat as a "skipped"
		// outcome with no image; the index stage will skip too.
		return "", nil, true
	}
	o.emit(events.Event{
		Kind: events.KindStageProgress, Stage: "index",
		Message: "waiting on indexer image build",
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err(), false
	case <-o.buildDone:
	}
	return o.buildResult.Image, o.buildResult.Err, o.buildResult.Skipped
}

// languageFactsForRecipe converts the stage-0 facts into the
// shape recipe.Generate expects. We prefer the marker-file
// derived LanguageFacts (deterministic) and fall back to the LLM
// "Languages" list when nothing was detected.
//
// The fallback path emits Facts with empty Version so the recipe
// resolver picks the default LTS for each language.
func languageFactsForRecipe(rf *repoFacts) []langdetect.Fact {
	if rf == nil {
		return nil
	}
	if len(rf.LanguageFacts) > 0 {
		out := make([]langdetect.Fact, 0, len(rf.LanguageFacts))
		for _, f := range rf.LanguageFacts {
			out = append(out, langdetect.Fact{
				Language:         langdetect.Language(f.Language),
				Version:          f.Version,
				BuildTool:        f.BuildTool,
				BuildToolVersion: f.BuildToolVersion,
				Sources:          f.Sources,
			})
		}
		return out
	}
	// LLM-only fallback. Each "language" string from the LLM
	// becomes a Fact with empty Version → recipe falls back to
	// the language default.
	out := make([]langdetect.Fact, 0, len(rf.Languages))
	for _, name := range rf.Languages {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		// Map common LLM aliases to our canonical Language IDs.
		switch name {
		case "ts":
			name = "typescript"
		case "js":
			name = "javascript"
		case "c#", "c sharp":
			name = "csharp"
		case "f#":
			name = "fsharp"
		case "golang":
			name = "go"
		}
		out = append(out, langdetect.Fact{Language: langdetect.Language(name)})
	}
	return out
}

// sanitiseJobName turns a Docker tag into a job-id-safe string.
// Used so the dashboard's event key is stable and readable.
func sanitiseJobName(tag string) string {
	out := make([]rune, 0, len(tag))
	for _, r := range tag {
		switch {
		case r == ':' || r == '/' || r == ' ':
			out = append(out, '.')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

// _ uses sync so future expansions (e.g. a singleflight on
// buildDone re-init) compile without adding a fresh import.
var _ = sync.Mutex{}
