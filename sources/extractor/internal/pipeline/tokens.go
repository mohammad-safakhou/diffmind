package pipeline

import (
	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

type tokenBucket = llmrun.TokenBucket

var stageFromJob = llmrun.StageFromJob

// snapshotStage returns a copy of the stage's running total, safe
// to embed in an event payload. The empty string returns the run
// total. Returns nil when no data has been recorded yet.
func (o *orchestrator) snapshotStage(stage string) *tokenBucket {
	return o.tokenAgg.Stage(stage)
}

// snapshotAll returns a copy of every stage's totals as
// model.TokenBucket so callers outside the agents package can embed
// the result directly. The empty-string key from byStage is
// re-keyed to "total" for SPA / manifest readability.
func (o *orchestrator) snapshotAll() map[string]model.TokenBucket {
	return o.tokenAgg.All()
}
