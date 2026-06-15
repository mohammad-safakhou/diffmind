// Package runstate owns persisted pipeline state and backward-compatible
// checkpoint readers.
package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// CheckpointStore persists and loads per-stage checkpoints under a run's
// state directory, so stage packages can checkpoint/resume without depending
// on the orchestrator. RunDir is the artifact root for the run (the same
// value the orchestrator holds as runDir); checkpoints live under
// <RunDir>/StateDir.
type CheckpointStore struct {
	RunDir string
}

// StateDir is the subdirectory under runDir where each stage's
// intermediate output is persisted on its way to the next stage. The
// retry command reads these files to fast-forward to the failed stage
// without re-running everything that already worked.
const StateDir = "state"

// ---- discovery per-objective checkpoint ----
//
// Without this, a single objective failing mid-stage causes all
// already-completed objectives to be re-run on retry, wasting LLM
// calls that succeeded.

const discoverEntitiesJSONL = "discover_entities.jsonl"

// DiscoveryCheckpointEntry is one row of state/discover_entities.jsonl.
//
// Two row shapes share the file:
//   - whole-objective rows (Sharded=false): one per completed objective; the
//     top-level discovery skip logic keys off these.
//   - per-shard rows (Sharded=true): one per completed shard of a sharded
//     objective, for fine-grained resume within a single objective.
//
// Sharded/ShardIndex are omitempty so legacy rows (written before sharding
// existed) decode as whole-objective rows unchanged.
type DiscoveryCheckpointEntry struct {
	// ObjectiveID is the unique identifier of the completed objective.
	ObjectiveID string `json:"objective_id"`
	// Items is the list of entities discovered for this objective (or shard).
	// Empty slice (not nil) means it ran and found nothing.
	Items      []extraction.Candidate `json:"items"`
	Sharded    bool                   `json:"sharded,omitempty"`
	ShardIndex int                    `json:"shard_index,omitempty"`
	WrittenAt  time.Time              `json:"written_at"`
}

// AppendDiscoveryObjective appends one completed objective's result to the
// per-item checkpoint. Best-effort; errors are logged and swallowed.
func (s *CheckpointStore) AppendDiscoveryObjective(entry DiscoveryCheckpointEntry) {
	if strings.TrimSpace(s.RunDir) == "" {
		return
	}
	dir := filepath.Join(s.RunDir, StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	entry.WrittenAt = time.Now().UTC()
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := filepath.Join(dir, discoverEntitiesJSONL)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// LoadDiscoveryCheckpoint reads the per-objective discovery checkpoint.
// Returns a map keyed by objective_id. Missing file returns empty map.
func (s *CheckpointStore) LoadDiscoveryCheckpoint(dir string) map[string]DiscoveryCheckpointEntry {
	out := map[string]DiscoveryCheckpointEntry{}
	if strings.TrimSpace(dir) == "" {
		return out
	}
	path := filepath.Join(dir, discoverEntitiesJSONL)
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry DiscoveryCheckpointEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.ObjectiveID == "" || entry.Sharded {
			// Per-shard rows are resume detail within an objective; only
			// whole-objective rows drive the top-level skip decision.
			continue
		}
		out[entry.ObjectiveID] = entry
	}
	return out
}

// AppendDiscoveryShard records one completed shard of a sharded objective so a
// retry resumes only the shards that did not finish.
func (s *CheckpointStore) AppendDiscoveryShard(objID string, shardIndex int, items []extraction.Candidate) {
	s.AppendDiscoveryObjective(DiscoveryCheckpointEntry{
		ObjectiveID: objID,
		Items:       items,
		Sharded:     true,
		ShardIndex:  shardIndex,
	})
}

// LoadDiscoveryShardCheckpoint returns the completed shards for one objective,
// keyed by shard index. Missing file / no shards → empty map.
func (s *CheckpointStore) LoadDiscoveryShardCheckpoint(dir, objID string) map[int][]extraction.Candidate {
	out := map[int][]extraction.Candidate{}
	if strings.TrimSpace(dir) == "" {
		return out
	}
	b, err := os.ReadFile(filepath.Join(dir, discoverEntitiesJSONL))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry DiscoveryCheckpointEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Sharded && entry.ObjectiveID == objID {
			out[entry.ShardIndex] = entry.Items
		}
	}
	return out
}

// ---- reexamination per-item checkpoint ----
//
// Unlike the detail stage (which writes one JSONL line per entity) the
// reexamination stage had no per-item checkpoint — it only wrote
// reexamination.json when the whole stage succeeded. A single prompt
// failure halted the stage and on retry ALL suspects were re-run, even
// the ones that had already been confirmed/rejected.
//
// The fix: write one JSONL line per completed suspect immediately after
// runReexamineOne returns (success OR explicit rejection). Only hard
// errors (stuck, timeout, 5xx) skip the write so those items are
// retried. On resume, LoadReexaminationCheckpoint tells the stage which
// suspects it can skip.

const reexamEntitiesJSONL = "reexam_entities.jsonl"

// ReexamCheckpointEntry is one row of state/reexam_entities.jsonl.
type ReexamCheckpointEntry struct {
	// Key is objective_id::safe_seed_name, same scheme as detail.
	Key string `json:"key"`
	// Outcome is "confirmed", "rejected", or "clean" (was never suspect).
	// We only write "confirmed" and "rejected"; "clean" seeds never enter
	// the suspect list and are always passed through.
	Outcome string `json:"outcome"`
	// Seed is the (possibly corrected) seed after re-examination.
	// Populated only for outcome=="confirmed".
	Seed *extraction.Candidate `json:"seed,omitempty"`
	// Unresolved is populated only for outcome=="rejected".
	Unresolved *model.UnresolvedItem `json:"unresolved,omitempty"`
	WrittenAt  time.Time             `json:"written_at"`
}

func ReexamKey(objectiveID, seedName string) string {
	return objectiveID + "::" + safeJobID(seedName)
}

// AppendReexamEntity appends one completed reexamination result to the
// per-item checkpoint. Best-effort; errors are logged and swallowed.
func (s *CheckpointStore) AppendReexamEntity(entry ReexamCheckpointEntry) {
	if strings.TrimSpace(s.RunDir) == "" {
		return
	}
	dir := filepath.Join(s.RunDir, StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	entry.WrittenAt = time.Now().UTC()
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := filepath.Join(dir, reexamEntitiesJSONL)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// LoadReexaminationCheckpoint reads the per-item checkpoint written by
// previous runs. Returns a map keyed by ReexamKey. Missing file returns
// an empty map (caller treats it as "nothing done yet").
func (s *CheckpointStore) LoadReexaminationCheckpoint(dir string) map[string]ReexamCheckpointEntry {
	out := map[string]ReexamCheckpointEntry{}
	if strings.TrimSpace(dir) == "" {
		return out
	}
	path := filepath.Join(dir, reexamEntitiesJSONL)
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry ReexamCheckpointEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Key == "" {
			continue
		}
		out[entry.Key] = entry
	}
	return out
}

func safeJobID(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.' || r == '/' || r == ':':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}
