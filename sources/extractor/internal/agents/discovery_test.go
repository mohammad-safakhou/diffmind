package agents

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// TestDiscoveryCheckpointSkipsCompletedObjectives is the regression guard
// for the "retry re-runs all discovery objectives" bug.
//
// Scenario: 3 objectives. A and B succeed; C fails (stuck/timeout). The
// stage halts. On retry, A and B must be restored from the checkpoint
// without new LLM calls; only C should be re-asked.
func TestDiscoveryCheckpointSkipsCompletedObjectives(t *testing.T) {
	runDir := t.TempDir()

	cfg := config.Default()
	cfg.Runtime.Workers = 4
	o := &orchestrator{cfg: cfg, runDir: runDir}

	// Pre-populate checkpoint with A and B already done.
	objA := objectives.Objective{ID: "exposure.http_route", Kind: "exposure", Type: "http_route"}
	objB := objectives.Objective{ID: "dependency.db_operation", Kind: "dependency", Type: "db_operation"}
	objC := objectives.Objective{ID: "dependency.outbound_http", Kind: "dependency", Type: "outbound_http"}

	o.appendDiscoveryObjective(discoveryCheckpointEntry{
		ObjectiveID: objA.ID,
		Items:       []llmEntity{{Type: "http_route", Name: "GET /x", Confidence: 0.95}},
	})
	o.appendDiscoveryObjective(discoveryCheckpointEntry{
		ObjectiveID: objB.ID,
		Items:       []llmEntity{},
	})

	// Count actual LLM calls.
	var llmCalls atomic.Int32
	fakeOC := &fakeDiscoveryOC{onCall: func() { llmCalls.Add(1) }}
	o.oc = fakeOC

	// Run discovery over all 3 objectives.
	rf := &repoFacts{}
	results := o.runDiscovery(context.Background(), []objectives.Objective{objA, objB, objC}, rf, nil)

	calls := int(llmCalls.Load())
	if calls != 1 {
		t.Errorf("expected exactly 1 LLM call (for C only), got %d — A and/or B were not skipped from checkpoint", calls)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results (one per objective), got %d", len(results))
	}

	// A should have carried its item through.
	for _, r := range results {
		if r.Objective.ID == objA.ID {
			if len(r.Items) != 1 || r.Items[0].Name != "GET /x" {
				t.Errorf("objective A: expected carried item 'GET /x', got %+v", r.Items)
			}
		}
	}
}

// fakeDiscoveryOC is a minimal OpenCode fake that counts PromptStructured
// calls and returns an empty items list (simulating "nothing found" for C).
type fakeDiscoveryOC struct {
	onCall func()
}

func (f *fakeDiscoveryOC) Enabled() bool { return true }
func (f *fakeDiscoveryOC) CreateSession(_ context.Context, _ string) (string, error) {
	return "s", nil
}
func (f *fakeDiscoveryOC) DeleteSession(_ context.Context, _, _ string) error { return nil }
func (f *fakeDiscoveryOC) PromptText(_ context.Context, _, _, _ string) (string, error) {
	return "{}", nil
}
func (f *fakeDiscoveryOC) PromptStructured(_ context.Context, _, _, _ string, _ map[string]any) (map[string]any, error) {
	if f.onCall != nil {
		f.onCall()
	}
	return map[string]any{"items": []any{}}, nil
}
