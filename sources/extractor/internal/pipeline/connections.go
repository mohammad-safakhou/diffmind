package pipeline

import (
	"context"
	"fmt"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	connectionstage "github.com/mohammad-safakhou/diffmind/internal/stage/connections"
)

// runConnectionsBatch is the pipeline boundary for the deterministic
// connections stage. The stage owns connection derivation and fallback policy;
// the pipeline owns progress and externally visible events.
func (o *orchestrator) runConnectionsBatch(
	ctx context.Context,
	exposures []model.Exposure,
	dependencies []model.Dependency,
	_ map[string]objectives.Objective,
	_ *repoFacts,
	onResult func(),
) ([]model.Connection, []model.UnresolvedItem, error, string) {
	out := (connectionstage.Runner{Report: o.emitConnectionsAggregate}).Run(ctx, connectionstage.Input{
		Index:         o.astIndex,
		Exposures:     exposures,
		Dependencies:  dependencies,
		MinConfidence: o.cfg.Quality.MinConfidence,
		Workers:       o.cfg.Runtime.Workers,
		Progress:      onResult,
	})
	return out.Connections, out.Unresolved, nil, ""
}

func (o *orchestrator) emitConnectionsAggregate(
	exposures, connections, exposuresWithoutPaths int, source string,
) {
	o.emit(events.Event{
		Kind: events.KindLog, Stage: "connections", JobID: "connections.summary",
		Message: fmt.Sprintf("%d connections across %d exposures (%d with no paths)",
			connections, exposures, exposuresWithoutPaths),
		Payload: map[string]any{
			"connections":             connections,
			"exposures":               exposures,
			"exposures_without_paths": exposuresWithoutPaths,
			"source":                  source,
		},
	})
}
