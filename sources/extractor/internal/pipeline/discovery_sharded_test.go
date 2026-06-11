package pipeline

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/agents/core"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
)

// recordSink captures emitted events for assertions.
type recordSink struct {
	mu sync.Mutex
	ev []events.Event
}

func (r *recordSink) Emit(e events.Event) {
	r.mu.Lock()
	r.ev = append(r.ev, e)
	r.mu.Unlock()
}

func (r *recordSink) byKind(kind string) []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []events.Event
	for _, e := range r.ev {
		if string(e.Kind) == kind {
			out = append(out, e)
		}
	}
	return out
}

// scopeFake returns one distinct item per call, tagging the item's source file
// with the SCOPE directory found in the prompt so we can prove each shard ran
// with its own scope.
type scopeFake struct {
	mu      sync.Mutex
	prompts []string
	n       int
}

func (f *scopeFake) Enabled() bool                                         { return true }
func (f *scopeFake) CreateSession(context.Context, string) (string, error) { return "s", nil }
func (f *scopeFake) DeleteSession(context.Context, string, string) error   { return nil }
func (f *scopeFake) PromptText(context.Context, string, string, string) (string, error) {
	return "{}", nil
}
func (f *scopeFake) PromptStructured(_ context.Context, _, _, prompt string, _ map[string]any) (map[string]any, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.n++
	id := f.n
	f.mu.Unlock()
	// Return one unique, type-correct item per call.
	item := map[string]any{
		"type":             "http_route",
		"name":             "GET /r" + Itoa(id),
		"summary":          "route",
		"confidence":       0.9,
		"source_locations": []any{map[string]any{"file": "src/api/a/C.java", "start_line": id, "end_line": id}},
	}
	return map[string]any{"items": []any{item}}, nil
}

// TestShardedDiscoveryRunsAllShards verifies a large objective fans out into
// child shard jobs, each prompt carries a SCOPE directive, results merge, and
// child events carry the parent id.
func TestShardedDiscoveryRunsAllShards(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.DiscoveryASTHints = true
	sink := &recordSink{}
	fake := &scopeFake{}
	idx := bigIndex([]string{"src/api/a", "src/api/b", "src/api/c", "src/api/d", "src/api/e", "src/api/f"}, 20)
	o := &orchestrator{cfg: cfg, oc: fake, sink: sink, astIndex: idx, runDir: t.TempDir()}
	o.store = &core.CheckpointStore{RunDir: o.runDir}

	obj := objByType(t, "http_route")
	shards := planDiscoveryShards(idx, obj, "")
	if len(shards) < 2 {
		t.Fatalf("precondition: expected sharding, got %d shards", len(shards))
	}

	items, err := o.runDiscoveryOne(context.Background(), obj, &repoFacts{})
	if err != nil {
		t.Fatalf("runDiscoveryOne: %v", err)
	}

	// One LLM call per shard.
	if fake.n != len(shards) {
		t.Fatalf("expected %d shard LLM calls, got %d", len(shards), fake.n)
	}
	// Merged: one item per shard (each fake call returns a unique item).
	if len(items) != len(shards) {
		t.Fatalf("expected %d merged items, got %d", len(shards), len(items))
	}
	// Every shard prompt carried a SCOPE directive.
	for _, p := range fake.prompts {
		if !strings.Contains(p, "SCOPE: This is one shard") {
			t.Fatalf("shard prompt missing SCOPE directive:\n%s", p)
		}
	}
	// Child job events carry ParentID = discover.<obj>.
	parent := "discover." + obj.ID
	childStarts := 0
	for _, e := range sink.byKind(string(events.KindJobStarted)) {
		if e.ParentID == parent {
			childStarts++
			if !strings.Contains(e.JobID, ".shard.") {
				t.Fatalf("child job id missing .shard.: %s", e.JobID)
			}
		}
	}
	if childStarts != len(shards) {
		t.Fatalf("expected %d child job_started events, got %d", len(shards), childStarts)
	}
	// Parent completed event present.
	foundParentDone := false
	for _, e := range sink.byKind(string(events.KindJobCompleted)) {
		if e.JobID == parent {
			foundParentDone = true
		}
	}
	if !foundParentDone {
		t.Fatal("missing parent job_completed event")
	}
}

// TestSmallObjectiveStaysSingleCall proves no behavioural change for small
// objectives: one whole-repo call, no shard children.
func TestSmallObjectiveStaysSingleCall(t *testing.T) {
	cfg := config.Default()
	sink := &recordSink{}
	fake := &scopeFake{}
	idx := bigIndex([]string{"src/api"}, 5) // 5 candidates, below soft target
	o := &orchestrator{cfg: cfg, oc: fake, sink: sink, astIndex: idx, runDir: t.TempDir()}
	o.store = &core.CheckpointStore{RunDir: o.runDir}

	obj := objByType(t, "http_route")
	if _, err := o.runDiscoveryOne(context.Background(), obj, &repoFacts{}); err != nil {
		t.Fatal(err)
	}
	if fake.n != 1 {
		t.Fatalf("expected exactly 1 LLM call (single path), got %d", fake.n)
	}
	for _, p := range fake.prompts {
		if strings.Contains(p, "SCOPE: This is one shard") {
			t.Fatal("small objective must not carry a shard SCOPE directive")
		}
	}
}
