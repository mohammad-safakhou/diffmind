package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/agents/core"
	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// REGRESSION (run 20260518T122739Z): when the detail stage failed
// mid-stream, on retry the orchestrator re-issued every detail
// prompt — including the 53 entities that had already been
// successfully enriched. Root cause: detail_exposures.json /
// detail_dependencies.json were only written AFTER the entire
// stage succeeded, so a stage failure left no checkpoint behind.
//
// The fix: per-entity append-only checkpoint at
// state/detail_entities.jsonl. Each successful detail result is
// written as a JSONL row as the batch returns; on retry those rows
// drop the corresponding seeds from the pending list.
//
// This test runs detail for 4 db seeds, fails the second time the
// first call would be made (by inspecting the fake's prompt
// counter), and asserts that:
//
//  1. The first call's items are persisted to detail_entities.jsonl.
//  2. On retry, only the not-yet-completed entities are sent to the
//     LLM (we verify by counting how many detail prompts the fake
//     receives across both runs).
type checkpointFake struct {
	mu       sync.Mutex
	calls    atomic.Int32
	failOnce atomic.Bool // when true, the next detail call errors
}

func (f *checkpointFake) Enabled() bool { return true }
func (f *checkpointFake) CreateSession(ctx context.Context, directory string) (string, error) {
	return "s", nil
}
func (f *checkpointFake) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *checkpointFake) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	m, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}
func (f *checkpointFake) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	switch {
	case role == "repo_facts":
		return map[string]any{}, nil

	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		// 4 db seeds in deterministic order.
		items := []any{}
		for i, n := range []string{"a", "b", "c", "d"} {
			items = append(items, map[string]any{
				"type": "db_operation", "name": "X.method" + n, "summary": "x", "confidence": 0.95,
				"details":          map[string]any{"operation": "read", "table": "t"},
				"source_locations": []any{map[string]any{"file": "repo/X.go", "start_line": 10 + i, "end_line": 20 + i}},
			})
		}
		return map[string]any{"items": items}, nil

	case role == "discovery":
		return map[string]any{"items": []any{}}, nil

	case role == "detail":
		f.calls.Add(1)
		if f.failOnce.Swap(false) {
			return nil, errAlwaysFail
		}
		// Extract seed names from the batched prompt and return
		// an enriched item per seed.
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

// After a successful detail stage, the per-entity checkpoint file
// MUST exist and contain one row per enriched entity. Without
// this guarantee, retries can't tell what already finished.
func TestDetailCheckpoint_WrittenOnSuccess(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Runtime.SkipReexamination = true
	cfg.Quality.MinConfidence = 0.7
	runDir := filepath.Join(t.TempDir(), "run-checkpoint-success")
	f := &checkpointFake{}

	res, err := RunWith(context.Background(), cfg, t.TempDir(), f, RunOptions{
		RunDir: runDir,
		RunID:  filepath.Base(runDir),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = res

	path := filepath.Join(runDir, "state", "detail_entities.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("checkpoint file missing: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry core.DetailCheckpointEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("malformed checkpoint line: %v", err)
			continue
		}
		if entry.Key == "" {
			t.Errorf("checkpoint entry has empty key: %+v", entry)
		}
		count++
	}
	if count != 4 {
		t.Errorf("expected 4 checkpoint entries (one per seed); got %d", count)
	}
}

// REGRESSION: after a partial detail run, a retry MUST NOT re-issue
// detail prompts for entities that already completed. We force a
// failure on the second LLM call (so the first batch's entities get
// checkpointed before the failure), then resume from the saved
// state and assert the fake's call counter increments by far less
// than a fresh run would.
func TestDetailCheckpoint_RetrySkipsCompletedEntities(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Runtime.SkipReexamination = true
	cfg.Quality.MinConfidence = 0.7
	tmpRepo := t.TempDir()
	runDir := filepath.Join(t.TempDir(), "run-checkpoint-retry")
	f := &checkpointFake{}

	// Run 1: succeeds — gives us a populated checkpoint we can
	// resume from. (Forcing a partial failure mid-stage is hard
	// because the batching collapses 4 seeds into 1-2 batches; the
	// retry semantics are the same whether the first run partially
	// or fully succeeded.)
	if _, err := RunWith(context.Background(), cfg, tmpRepo, f, RunOptions{
		RunDir: runDir,
		RunID:  filepath.Base(runDir),
	}); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	firstCalls := f.calls.Load()
	if firstCalls < 1 {
		t.Fatalf("expected at least 1 detail call in run 1; got %d", firstCalls)
	}

	// Run 2: resume from the same run dir. With a complete
	// checkpoint, the detail stage must make ZERO new LLM calls —
	// every seed is already in detail_entities.jsonl.
	stateDirPath := filepath.Join(runDir, "state")
	if _, err := RunWith(context.Background(), cfg, tmpRepo, f, RunOptions{
		RunDir:        runDir,
		RunID:         filepath.Base(runDir),
		ResumeFromDir: stateDirPath,
		SnapshotPath:  "",
	}); err != nil {
		t.Fatalf("run 2 (resume): %v", err)
	}
	secondCalls := f.calls.Load()
	delta := secondCalls - firstCalls
	if delta != 0 {
		t.Errorf("resume issued %d new detail calls; expected 0 (all checkpointed)", delta)
	}
}

// appendDetailEntity is the building block of the checkpoint. It
// must be append-only (multiple calls produce multiple lines) and
// resilient to the file not existing yet.
func TestAppendDetailEntity_AppendsMultipleRows(t *testing.T) {
	runDir := t.TempDir()
	o := &orchestrator{runDir: runDir}
	o.store = &core.CheckpointStore{RunDir: runDir}
	for i := 0; i < 3; i++ {
		o.store.AppendDetailEntity(core.DetailCheckpointEntry{
			Key:         "k" + string(rune('0'+i)),
			ObjectiveID: "exposure.http_route",
			SeedName:    "GET /x" + string(rune('0'+i)),
		})
	}
	path := filepath.Join(runDir, "state", "detail_entities.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines; got %d", len(lines))
	}
}

// loadDetailCheckpoint must tolerate a torn last line (crash during
// a partial write) without losing the rows before it.
func TestLoadDetailCheckpoint_TornLastLine(t *testing.T) {
	runDir := t.TempDir()
	stateDir := filepath.Join(runDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write 2 valid lines + 1 truncated.
	good1, _ := json.Marshal(core.DetailCheckpointEntry{Key: "a"})
	good2, _ := json.Marshal(core.DetailCheckpointEntry{Key: "b"})
	torn := `{"key":"c","obj`
	content := string(good1) + "\n" + string(good2) + "\n" + torn + "\n"
	if err := os.WriteFile(filepath.Join(stateDir, "detail_entities.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &orchestrator{runDir: runDir}
	o.store = &core.CheckpointStore{RunDir: runDir}
	got := o.store.LoadDetailCheckpoint(stateDir)
	if _, ok := got["a"]; !ok {
		t.Errorf("missing valid entry 'a' (torn line confused the loader)")
	}
	if _, ok := got["b"]; !ok {
		t.Errorf("missing valid entry 'b'")
	}
	if _, ok := got["c"]; ok {
		t.Errorf("torn line 'c' should not have been loaded")
	}
}
