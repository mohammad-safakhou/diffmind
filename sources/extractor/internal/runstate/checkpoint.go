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
	"github.com/mohammad-safakhou/diffmind/internal/util"
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

// detailEntitiesJSONL is the append-only per-entity checkpoint
// written by the detail stage's worker pool. Each line is one
// `DetailCheckpointEntry`. We use JSON-lines (not a single JSON
// object) so a partial write on a crash never corrupts already-
// persisted entries; the worst case is a torn last line, which
// LoadDetailCheckpoint detects and ignores.
const detailEntitiesJSONL = "detail_entities.jsonl"

// DetailCheckpointEntry is one row of state/detail_entities.jsonl.
// The Key field is the deterministic identifier the orchestrator
// uses to decide whether a seed has already been enriched on a
// previous run (see DetailEntityKey). Exposure and Dependency are
// optional pointers: a successful enrichment produces exactly one
// of them; an explicit "model could not enrich" still produces an
// entry (with neither populated) so we don't re-attempt the same
// seed on every retry — the model already told us it can't.
type DetailCheckpointEntry struct {
	Key         string            `json:"key"`
	ObjectiveID string            `json:"objective_id"`
	SeedName    string            `json:"seed_name"`
	Exposure    *model.Exposure   `json:"exposure,omitempty"`
	Dependency  *model.Dependency `json:"dependency,omitempty"`
	Skipped     bool              `json:"skipped,omitempty"` // model returned nil — keep seed as-is
	WrittenAt   time.Time         `json:"written_at"`
}

// DetailEntityKey is the deterministic key the orchestrator uses
// to look up whether a seed has been enriched. Composed of the
// objective id (always present, kind+type+namespace) plus the
// safe-jobid'd seed name. Two different seeds with the same name
// under the same objective WOULD collide — but the orchestrator
// already requires names to be unique within an objective for the
// stable-ID generation, so this is safe.
func DetailEntityKey(objectiveID, seedName string) string {
	return objectiveID + "::" + safeJobID(seedName)
}

// AppendDetailEntity writes one entry to the detail checkpoint file
// in append-only mode. Best-effort: any I/O error is logged and
// swallowed because losing one checkpoint line is never worse than
// what the operator faces today (re-running the whole stage).
func (s *CheckpointStore) AppendDetailEntity(entry DetailCheckpointEntry) {
	if strings.TrimSpace(s.RunDir) == "" {
		return
	}
	dir := filepath.Join(s.RunDir, StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		util.Warn("agents.state", "could not create state dir for checkpoint", map[string]any{"dir": dir, "error": err})
		return
	}
	path := filepath.Join(dir, detailEntitiesJSONL)
	entry.WrittenAt = time.Now().UTC()
	b, err := json.Marshal(entry)
	if err != nil {
		util.Warn("agents.state", "could not marshal detail checkpoint entry", map[string]any{"key": entry.Key, "error": err})
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		util.Warn("agents.state", "could not open detail checkpoint", map[string]any{"path": path, "error": err})
		return
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		util.Warn("agents.state", "could not append detail checkpoint", map[string]any{"path": path, "error": err})
	}
}

// LoadDetailCheckpoint reads the per-entity detail checkpoint for
// a previously-failed run. Returns a map keyed by entity key (see
// DetailEntityKey). A torn last line is ignored. Missing file
// returns an empty map (not an error) — the caller treats "no
// checkpoint" as "all seeds need processing".
func (s *CheckpointStore) LoadDetailCheckpoint(dir string) map[string]DetailCheckpointEntry {
	out := map[string]DetailCheckpointEntry{}
	if strings.TrimSpace(dir) == "" {
		return out
	}
	path := filepath.Join(dir, detailEntitiesJSONL)
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry DetailCheckpointEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Torn last line or schema drift; skip and keep going.
			util.Trace("agents.resume", "skipping malformed detail checkpoint line", map[string]any{"error": err})
			continue
		}
		if entry.Key == "" {
			continue
		}
		out[entry.Key] = entry
	}
	return out
}

// ---- discovery per-objective checkpoint ----
//
// Without this, a single objective failing mid-stage causes all
// already-completed objectives to be re-run on retry, wasting LLM
// calls that succeeded. The pattern mirrors detail_entities.jsonl.

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
