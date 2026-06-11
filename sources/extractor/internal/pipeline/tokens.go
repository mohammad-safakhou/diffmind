package pipeline

import (
	"context"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/llmrun"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

type tokenBucket = llmrun.TokenBucket

var stageFromJob = llmrun.StageFromJob

// recordPromptTokens reads the final session state for sessionID and
// records its tokens under the given jobID. Stage attribution is
// derived from the jobID. Designed to be called immediately after a
// successful prompt POST returns; failures are tolerated (token
// reads are diagnostic, not load-bearing).
//
// Returns the bucket that was updated so the caller can attach the
// per-call totals to its llm_call_completed event payload. nil means
// "no token info available" (test fake or HTTP error); callers must
// handle the nil case gracefully.
func (o *orchestrator) recordPromptTokens(ctx context.Context, sessionID, jobID string) *tokenBucket {
	if o == nil || o.tokens == nil || sessionID == "" {
		return nil
	}
	// Short bounded context so a slow OpenCode response can't add
	// material latency to the orchestrator's main loop after the
	// real work is done. 3 seconds is plenty for a ~500-byte GET
	// on localhost.
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s, err := o.tokens.GetSession(readCtx, sessionID, o.sessionDir)
	if err != nil || s.ID == "" {
		return nil
	}
	return o.tokenAgg.Record(jobID, s)
}

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
