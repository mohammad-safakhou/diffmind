package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// TestShardedDiscoveryResumesCheckpointedShards verifies that shards already
// recorded in the checkpoint are restored without an LLM call, and only the
// missing shards re-run.
func TestShardedDiscoveryResumesCheckpointedShards(t *testing.T) {
	runDir := t.TempDir()
	cfg := config.Default()
	sink := &recordSink{}
	fake := &scopeFake{}
	idx := bigIndex([]string{"src/api/a", "src/api/b", "src/api/c", "src/api/d", "src/api/e", "src/api/f"}, 20)
	o := &orchestrator{cfg: cfg, oc: fake, sink: sink, astIndex: idx, runDir: runDir}

	obj := objByType(t, "http_route")
	shards := planDiscoveryShards(idx, obj, "")
	if len(shards) < 2 {
		t.Fatalf("precondition: expected sharding, got %d", len(shards))
	}

	// Pre-write all shards but the last as completed checkpoints.
	for i := 0; i < len(shards)-1; i++ {
		o.appendDiscoveryShard(obj.ID, shards[i].Index, []llmEntity{{Type: "http_route", Name: "cached-" + itoa(i), Confidence: 0.9}})
	}

	items, err := o.runDiscoveryOne(context.Background(), obj, &repoFacts{})
	if err != nil {
		t.Fatal(err)
	}
	// Only the one missing shard should have triggered an LLM call.
	if fake.n != 1 {
		t.Fatalf("expected exactly 1 LLM call (last shard only), got %d", fake.n)
	}
	// Merged result includes the cached items + the freshly-run shard's item.
	if len(items) != len(shards) {
		t.Fatalf("expected %d merged items, got %d", len(shards), len(items))
	}
}

// TestLegacyCheckpointLineLoads guards backward compatibility: a checkpoint
// row written before sharding existed (no sharded/shard_index fields) must
// still load as a whole-objective entry.
func TestLegacyCheckpointLineLoads(t *testing.T) {
	runDir := t.TempDir()
	o := &orchestrator{runDir: runDir}
	dir := filepath.Join(runDir, stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy line: no "sharded"/"shard_index" keys.
	legacy := `{"objective_id":"exposure.http_route","items":[{"type":"http_route","name":"GET /legacy","confidence":0.9}],"written_at":"2026-01-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, discoverEntitiesJSONL), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := o.loadDiscoveryCheckpoint(dir)
	entry, ok := cp["exposure.http_route"]
	if !ok {
		t.Fatal("legacy whole-objective line did not load")
	}
	if len(entry.Items) != 1 || entry.Items[0].Name != "GET /legacy" {
		t.Fatalf("legacy items wrong: %+v", entry.Items)
	}
	// And it must NOT be mistaken for a shard.
	if shardCP := o.loadDiscoveryShardCheckpoint(dir, "exposure.http_route"); len(shardCP) != 0 {
		t.Fatalf("legacy line wrongly read as shard checkpoint: %+v", shardCP)
	}
}
