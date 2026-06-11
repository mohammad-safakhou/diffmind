package llmrun

import (
	"strings"
	"sync"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

type TokenTotals struct {
	mu      sync.Mutex
	byStage map[string]*TokenBucket
	byJob   map[string]*TokenBucket
}

type TokenBucket struct {
	Calls      int     `json:"calls"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Reasoning  int     `json:"reasoning"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Cost       float64 `json:"cost"`
}

func (b *TokenBucket) Add(state SessionState) {
	b.Calls++
	b.Input += state.Input
	b.Output += state.Output
	b.Reasoning += state.Reasoning
	b.CacheRead += state.CacheRead
	b.CacheWrite += state.CacheWrite
	b.Cost += state.Cost
}

func (b TokenBucket) Total() int {
	return b.Input + b.Output + b.Reasoning
}

func (t *TokenTotals) Record(jobID string, state SessionState) *TokenBucket {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byStage == nil {
		t.byStage = map[string]*TokenBucket{}
	}
	if t.byJob == nil {
		t.byJob = map[string]*TokenBucket{}
	}
	stage := StageFromJob(jobID)
	stageBucket := t.byStage[stage]
	if stageBucket == nil {
		stageBucket = &TokenBucket{}
		t.byStage[stage] = stageBucket
	}
	stageBucket.Add(state)
	runBucket := t.byStage[""]
	if runBucket == nil {
		runBucket = &TokenBucket{}
		t.byStage[""] = runBucket
	}
	runBucket.Add(state)
	jobBucket := &TokenBucket{}
	jobBucket.Add(state)
	t.byJob[jobID] = jobBucket
	copy := *jobBucket
	return &copy
}

func (t *TokenTotals) Stage(stage string) *TokenBucket {
	t.mu.Lock()
	defer t.mu.Unlock()
	bucket := t.byStage[stage]
	if bucket == nil {
		return nil
	}
	copy := *bucket
	return &copy
}

func (t *TokenTotals) All() map[string]model.TokenBucket {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.byStage) == 0 {
		return nil
	}
	totals := make(map[string]model.TokenBucket, len(t.byStage))
	for stage, bucket := range t.byStage {
		if bucket == nil {
			continue
		}
		if stage == "" {
			stage = "total"
		}
		totals[stage] = model.TokenBucket{
			Calls:      bucket.Calls,
			Input:      bucket.Input,
			Output:     bucket.Output,
			Reasoning:  bucket.Reasoning,
			CacheRead:  bucket.CacheRead,
			CacheWrite: bucket.CacheWrite,
			Total:      bucket.Total(),
			Cost:       bucket.Cost,
		}
	}
	return totals
}

func StageFromJob(jobID string) string {
	if jobID == "" {
		return "other"
	}
	if jobID == "repo_facts" {
		return "repo_facts"
	}
	head := jobID
	if separator := strings.Index(jobID, "."); separator > 0 {
		head = jobID[:separator]
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
