package agents

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// tokenTotals aggregates token / cost counters across the lifetime
// of a run, broken down by pipeline stage. We aggregate from
// per-prompt session reads: each promptAgent call ends with a GET
// /session/{id} which returns the cumulative tokens for that session
// — and because every promptAgent call uses a fresh session, that
// cumulative number IS that prompt's tokens.
//
// Concurrency: every stage runs jobs in parallel via worker pools.
// Each pool's results funnel back through a single result-collector
// goroutine that is the only thing calling Add(). We still hold a
// mutex because future refactors could fan out the writes, and the
// cost is negligible compared to an HTTP round-trip.
type tokenTotals struct {
	mu sync.Mutex

	// byStage maps stage name -> totals (per-stage). The empty
	// string key holds the cross-stage total so summary code can
	// stay simple.
	byStage map[string]*tokenBucket

	// byJob maps job ID -> totals so the DetailDrawer can render
	// per-job cost. Bounded only by the run's total prompt count
	// (~120 jobs typical, well under a MB of memory).
	byJob map[string]*tokenBucket
}

// tokenBucket is the accumulator for one slice of the run. We sum
// raw integer counters so a 32-bit overflow is virtually impossible
// (run totals on DiffMind are typically <1M tokens; we'd need
// 2,000+ runs of that size to overflow int64).
type tokenBucket struct {
	Calls      int     `json:"calls"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Reasoning  int     `json:"reasoning"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Cost       float64 `json:"cost"`
}

func (b *tokenBucket) add(s sessionState) {
	b.Calls++
	b.Input += s.Input
	b.Output += s.Output
	b.Reasoning += s.Reasoning
	b.CacheRead += s.CacheRead
	b.CacheWrite += s.CacheWrite
	b.Cost += s.Cost
}

// Total returns the user-facing total (input + output + reasoning).
// Cache reads/writes are kept separate because providers bill them
// at a different rate and the dashboard renders them on a second
// line.
func (b tokenBucket) Total() int {
	return b.Input + b.Output + b.Reasoning
}

// stageFromJob extracts the stage name from a job id of the form
// "stage.objective.entity..." (e.g. "discover.exposure.http_route"
// -> "discovery"). We map the verb prefix that promptAgent uses to
// the stage name surfaced in events.
//
// The mapping is intentionally lenient: anything unrecognised is
// filed under "other". That gives a deterministic fallback if a
// future stage uses a new verb without us remembering to add it
// here.
func stageFromJob(jobID string) string {
	if jobID == "" {
		return "other"
	}
	if jobID == "repo_facts" {
		return "repo_facts"
	}
	head := jobID
	if i := strings.Index(jobID, "."); i > 0 {
		head = jobID[:i]
	}
	switch head {
	case "repo_facts":
		return "repo_facts"
	case "discover":
		return "discovery"
	case "reexamine":
		return "reexamination"
	case "detail":
		return "detail"
	case "connections":
		return "connections"
	default:
		return "other"
	}
}

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
	o.tokenAgg.mu.Lock()
	defer o.tokenAgg.mu.Unlock()
	if o.tokenAgg.byStage == nil {
		o.tokenAgg.byStage = map[string]*tokenBucket{}
	}
	if o.tokenAgg.byJob == nil {
		o.tokenAgg.byJob = map[string]*tokenBucket{}
	}
	stage := stageFromJob(jobID)
	stageBucket := o.tokenAgg.byStage[stage]
	if stageBucket == nil {
		stageBucket = &tokenBucket{}
		o.tokenAgg.byStage[stage] = stageBucket
	}
	stageBucket.add(s)
	runBucket := o.tokenAgg.byStage[""]
	if runBucket == nil {
		runBucket = &tokenBucket{}
		o.tokenAgg.byStage[""] = runBucket
	}
	runBucket.add(s)
	jobBucket := &tokenBucket{}
	jobBucket.add(s)
	o.tokenAgg.byJob[jobID] = jobBucket
	return jobBucket
}

// snapshotStage returns a copy of the stage's running total, safe
// to embed in an event payload. The empty string returns the run
// total. Returns nil when no data has been recorded yet.
func (o *orchestrator) snapshotStage(stage string) *tokenBucket {
	o.tokenAgg.mu.Lock()
	defer o.tokenAgg.mu.Unlock()
	b, ok := o.tokenAgg.byStage[stage]
	if !ok || b == nil {
		return nil
	}
	cp := *b
	return &cp
}

// snapshotAll returns a copy of every stage's totals as
// model.TokenBucket so callers outside the agents package can embed
// the result directly. The empty-string key from byStage is
// re-keyed to "total" for SPA / manifest readability.
func (o *orchestrator) snapshotAll() map[string]model.TokenBucket {
	o.tokenAgg.mu.Lock()
	defer o.tokenAgg.mu.Unlock()
	if len(o.tokenAgg.byStage) == 0 {
		return nil
	}
	out := make(map[string]model.TokenBucket, len(o.tokenAgg.byStage))
	for k, v := range o.tokenAgg.byStage {
		if v == nil {
			continue
		}
		key := k
		if key == "" {
			key = "total"
		}
		out[key] = model.TokenBucket{
			Calls:      v.Calls,
			Input:      v.Input,
			Output:     v.Output,
			Reasoning:  v.Reasoning,
			CacheRead:  v.CacheRead,
			CacheWrite: v.CacheWrite,
			Total:      v.Total(),
			Cost:       v.Cost,
		}
	}
	return out
}
