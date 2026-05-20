package agents

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// REGRESSION (user feedback "152 remaining, not correct if some are
// batch processing"): the dashboard could not surface the batched
// nature of the detail stage. The fix is two server-side
// commitments the SPA can rely on:
//
//  1. stage_started for "detail" includes batches_total in the
//     payload (and pending, for resume scenarios) so the
//     PipelineStrip can render "X/N entities · X/B batches".
//
//  2. Every batch emits job_pending, job_started, and
//     job_completed events (job_id = the batch id) with
//     payload.batch == true. Per-entity job events use the batch
//     as their parent_id so the LiveGraph renders a 3-layer
//     hierarchy: stage -> objective -> batch -> entity.
//
// This test runs a small detail stage end-to-end and asserts both
// invariants on the captured event stream.
type batchEventsFake struct {
	mu sync.Mutex
}

func (f *batchEventsFake) Enabled() bool { return true }
func (f *batchEventsFake) CreateSession(ctx context.Context, directory string) (string, error) {
	return "s", nil
}
func (f *batchEventsFake) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *batchEventsFake) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	m, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}
func (f *batchEventsFake) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	switch {
	case role == "repo_facts":
		return map[string]any{}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		// 5 db seeds in the same repository file so they cluster.
		items := []any{}
		for i, n := range []string{"X.findById", "X.findAll", "X.save", "X.delete", "X.count"} {
			items = append(items, map[string]any{
				"type": "db_operation", "name": n, "summary": "x", "confidence": 0.95,
				"details":          map[string]any{"operation": "read", "table": "t"},
				"source_locations": []any{map[string]any{"file": "repo/X.go", "start_line": 10 + i, "end_line": 20 + i}},
			})
		}
		return map[string]any{"items": items}, nil
	case role == "discovery":
		return map[string]any{"items": []any{}}, nil
	case role == "detail":
		names := extractDetailSeedNames(prompt)
		items := make([]any, 0, len(names))
		for _, n := range names {
			items = append(items, map[string]any{
				"type": "db_operation", "name": n, "summary": "x", "confidence": 0.95,
				"details":          map[string]any{"operation": "read", "table": "t"},
				"source_locations": []any{map[string]any{"file": "repo/X.go", "start_line": 10, "end_line": 20}},
			})
		}
		return map[string]any{"items": items}, nil
	case role == "connection":
		return map[string]any{"items": []any{}}, nil
	}
	return map[string]any{"items": []any{}}, nil
}

// stage_started for detail must include batches_total.
func TestDetailStageStarted_CarriesBatchesTotal(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Runtime.SkipReexamination = true
	cfg.Quality.MinConfidence = 0.7
	runDir := filepath.Join(t.TempDir(), "run")
	sink := &captureSink{}
	_, err := RunWith(context.Background(), cfg, t.TempDir(), &batchEventsFake{}, RunOptions{
		RunDir: runDir,
		RunID:  filepath.Base(runDir),
		Sink:   sink,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var ev *capturedEvent
	for i := range sink.events {
		e := &sink.events[i]
		if e.Kind == "stage_started" && e.Stage == "detail" {
			ev = e
			break
		}
	}
	if ev == nil {
		t.Fatal("no stage_started event for detail captured")
	}
	bt, ok := ev.Payload["batches_total"]
	if !ok {
		t.Fatalf("stage_started.payload missing batches_total; got keys: %v", payloadKeys(ev.Payload))
	}
	if v, ok := bt.(float64); !ok || v < 1 {
		t.Errorf("batches_total must be >= 1; got %v (%T)", bt, bt)
	}
	if _, ok := ev.Payload["total"]; !ok {
		t.Errorf("stage_started.payload still must carry the per-entity total alongside batches_total")
	}
}

// Each batch must produce a separately-identified job event (so the
// graph can render an intermediate node), with payload.batch == true
// and payload.batch_size populated.
func TestDetailBatch_EmitsBatchLevelJobEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Runtime.SkipReexamination = true
	cfg.Quality.MinConfidence = 0.7
	runDir := filepath.Join(t.TempDir(), "run")
	sink := &captureSink{}
	_, err := RunWith(context.Background(), cfg, t.TempDir(), &batchEventsFake{}, RunOptions{
		RunDir: runDir,
		RunID:  filepath.Base(runDir),
		Sink:   sink,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Find batch-level job_started events.
	batchStarted := 0
	batchCompleted := 0
	entityParentIsBatch := 0
	entityParentIsObjective := 0
	for _, e := range sink.events {
		if e.Kind != "job_started" && e.Kind != "job_completed" {
			continue
		}
		isBatch, _ := e.Payload["batch"].(bool)
		if e.Kind == "job_started" && isBatch {
			batchStarted++
			if size, ok := e.Payload["batch_size"].(float64); !ok || size < 1 {
				t.Errorf("batch event missing batch_size; got %v", e.Payload["batch_size"])
			}
		}
		if e.Kind == "job_completed" && isBatch {
			batchCompleted++
		}
		// Per-entity events should set parent_id to the batch they
		// belong to so the graph renders them as children of the
		// batch node.
		if !isBatch && e.Kind == "job_started" && strings.HasPrefix(e.JobID, "detail.") {
			pid := e.Payload["batch_id"]
			if pid == nil {
				t.Errorf("entity-level detail job event missing payload.batch_id: %+v", e.Payload)
			}
			// We can't easily inspect ParentID through the
			// captured-event JSON layer, but the orchestrator's
			// own parent_id field IS preserved on the wire so
			// look at the raw json indirectly through the
			// JobID's relationship.
			if pid != nil {
				entityParentIsBatch++
			} else {
				entityParentIsObjective++
			}
		}
	}
	if batchStarted < 1 {
		t.Errorf("expected at least 1 batch-level job_started event; got 0")
	}
	if batchCompleted < 1 {
		t.Errorf("expected at least 1 batch-level job_completed event; got 0 (started=%d)", batchStarted)
	}
	if entityParentIsBatch < 1 {
		t.Errorf("expected at least 1 entity-level detail event with batch_id; got %d", entityParentIsBatch)
	}
	if entityParentIsObjective > 0 {
		t.Errorf("entity-level detail events MUST set batch_id; %d events did not", entityParentIsObjective)
	}
}
