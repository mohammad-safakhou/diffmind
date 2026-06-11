package pipeline

import (
	"context"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func (o *orchestrator) runDeterministicDiscovery(ctx context.Context, objectives []objectives.Objective) []discoveryResult {
	started := time.Now()
	o.emit(events.Event{
		Kind: events.KindStageStarted, Stage: "deterministic_discovery", Status: events.StatusRunning,
	})
	if err := ctx.Err(); err != nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "deterministic_discovery",
			Status: events.StatusFailed, Message: err.Error(),
		})
		return nil
	}
	if o.astIndex == nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "deterministic_discovery",
			Status: events.StatusSkipped, Message: "AST index unavailable",
		})
		return nil
	}

	out := (discoverystage.DeterministicRunner{}).Run(discoverystage.DeterministicInput{
		Index: o.astIndex, Objectives: objectives, PathMapper: o.PathMapper(),
	})
	o.persistStageState("deterministic_frameworks.json", out.Report)
	for _, result := range out.Results {
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "deterministic_discovery",
			JobID: "deterministic." + result.Objective.ID, Status: events.StatusSuccess,
			Payload: map[string]any{
				"objective_id": result.Objective.ID,
				"kind":         string(result.Objective.Kind),
				"type":         result.Objective.Type,
				"items":        len(result.Items),
			},
		})
	}
	o.persistStageState("deterministic_discovery.json", out.Results)
	o.emitStageCompleted("deterministic_discovery", events.StatusSuccess, map[string]any{
		"items": out.Items, "objectives": len(out.Results),
		"duration_ms": time.Since(started).Milliseconds(),
	})
	util.Info("agents.deterministic_discovery", "deterministic discovery completed", map[string]any{
		"items": out.Items, "objectives": len(out.Results),
	})
	return out.Results
}
