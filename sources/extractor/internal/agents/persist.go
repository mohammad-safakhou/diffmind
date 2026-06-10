package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// fileExists reports whether the given path resolves to anything on
// disk (file or directory). Empty path returns false. Used by the
// failure-report renderer so we never advertise files we didn't
// actually write.
func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// stateDir is the subdirectory under runDir where each stage's
// intermediate output is persisted on its way to the next stage. The
// retry command reads these files to fast-forward to the failed stage
// without re-running everything that already worked.
const stateDir = "state"

// detailEntitiesJSONL is the append-only per-entity checkpoint
// written by the detail stage's worker pool. Each line is one
// `detailCheckpointEntry`. We use JSON-lines (not a single JSON
// object) so a partial write on a crash never corrupts already-
// persisted entries; the worst case is a torn last line, which
// loadDetailCheckpoint detects and ignores.
const detailEntitiesJSONL = "detail_entities.jsonl"

// detailCheckpointEntry is one row of state/detail_entities.jsonl.
// The Key field is the deterministic identifier the orchestrator
// uses to decide whether a seed has already been enriched on a
// previous run (see detailEntityKey). Exposure and Dependency are
// optional pointers: a successful enrichment produces exactly one
// of them; an explicit "model could not enrich" still produces an
// entry (with neither populated) so we don't re-attempt the same
// seed on every retry — the model already told us it can't.
type detailCheckpointEntry struct {
	Key         string            `json:"key"`
	ObjectiveID string            `json:"objective_id"`
	SeedName    string            `json:"seed_name"`
	Exposure    *model.Exposure   `json:"exposure,omitempty"`
	Dependency  *model.Dependency `json:"dependency,omitempty"`
	Skipped     bool              `json:"skipped,omitempty"` // model returned nil — keep seed as-is
	WrittenAt   time.Time         `json:"written_at"`
}

// detailEntityKey is the deterministic key the orchestrator uses
// to look up whether a seed has been enriched. Composed of the
// objective id (always present, kind+type+namespace) plus the
// safe-jobid'd seed name. Two different seeds with the same name
// under the same objective WOULD collide — but the orchestrator
// already requires names to be unique within an objective for the
// stable-ID generation, so this is safe.
func detailEntityKey(objectiveID, seedName string) string {
	return objectiveID + "::" + SafeJobID(seedName)
}

// appendDetailEntity writes one entry to the detail checkpoint file
// in append-only mode. Best-effort: any I/O error is logged and
// swallowed because losing one checkpoint line is never worse than
// what the operator faces today (re-running the whole stage).
func (o *orchestrator) appendDetailEntity(entry detailCheckpointEntry) {
	if strings.TrimSpace(o.runDir) == "" {
		return
	}
	dir := filepath.Join(o.runDir, stateDir)
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

// loadDetailCheckpoint reads the per-entity detail checkpoint for
// a previously-failed run. Returns a map keyed by entity key (see
// detailEntityKey). A torn last line is ignored. Missing file
// returns an empty map (not an error) — the caller treats "no
// checkpoint" as "all seeds need processing".
func (o *orchestrator) loadDetailCheckpoint(dir string) map[string]detailCheckpointEntry {
	out := map[string]detailCheckpointEntry{}
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
		var entry detailCheckpointEntry
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

// resumeState is the bundle returned by loadResumeState. The struct
// (rather than a positional return) makes new fields backwards-
// compatible: adding the detail checkpoint here would have been an
// ugly 7-value return.
type resumeState struct {
	RepoFacts        *repoFacts
	Seeds            []detailJob
	ExposureObjs     map[string]string
	Reexam           []detailJob
	Exposures        []model.Exposure
	Dependencies     []model.Dependency
	DetailCheckpoint map[string]detailCheckpointEntry
}

// loadResumeState reads previously-saved per-stage outputs from
// `<runDir>/state/*.json`. It returns one tuple value per stage; nil
// means "not found, re-execute". Any read/parse error for a single
// file logs a warning and returns nil for that stage so a corrupt
// state file doesn't prevent the operator from retrying with the
// remaining stages still skipped.
func (o *orchestrator) loadResumeState(dir string) (
	rf *repoFacts,
	seeds []detailJob,
	expObjs map[string]string,
	reexam []detailJob,
	exposures []model.Exposure,
	deps []model.Dependency,
) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		util.Warn("agents.resume", "state dir missing or not a directory", map[string]any{"dir": dir, "error": err})
		return
	}
	read := func(name string, into any) bool {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		if err := json.Unmarshal(b, into); err != nil {
			util.Warn("agents.resume", "could not parse state file", map[string]any{"path": path, "error": err})
			return false
		}
		return true
	}
	rf = &repoFacts{}
	if !read("repo_facts.json", rf) {
		rf = nil
	}
	if !read("discovery.json", &seeds) {
		seeds = nil
	}
	if !read("exposure_objectives.json", &expObjs) {
		expObjs = nil
	}
	if !read("reexamination.json", &reexam) {
		reexam = nil
	}
	if !read("detail_exposures.json", &exposures) {
		exposures = nil
	}
	if !read("detail_dependencies.json", &deps) {
		deps = nil
	}
	util.Info("agents.resume", "loaded resume state", map[string]any{
		"dir":                 dir,
		"repo_facts":          rf != nil,
		"discovery":           len(seeds),
		"exposure_objs":       len(expObjs),
		"reexamination":       len(reexam),
		"detail_exposures":    len(exposures),
		"detail_dependencies": len(deps),
	})
	return
}

// ---- discovery per-objective checkpoint ----
//
// Without this, a single objective failing mid-stage causes all
// already-completed objectives to be re-run on retry, wasting LLM
// calls that succeeded. The pattern mirrors detail_entities.jsonl.

const discoverEntitiesJSONL = "discover_entities.jsonl"

// discoveryCheckpointEntry is one row of state/discover_entities.jsonl.
//
// Two row shapes share the file:
//   - whole-objective rows (Sharded=false): one per completed objective; the
//     top-level discovery skip logic keys off these.
//   - per-shard rows (Sharded=true): one per completed shard of a sharded
//     objective, for fine-grained resume within a single objective.
//
// Sharded/ShardIndex are omitempty so legacy rows (written before sharding
// existed) decode as whole-objective rows unchanged.
type discoveryCheckpointEntry struct {
	// ObjectiveID is the unique identifier of the completed objective.
	ObjectiveID string `json:"objective_id"`
	// Items is the list of entities discovered for this objective (or shard).
	// Empty slice (not nil) means it ran and found nothing.
	Items      []llmEntity `json:"items"`
	Sharded    bool        `json:"sharded,omitempty"`
	ShardIndex int         `json:"shard_index,omitempty"`
	WrittenAt  time.Time   `json:"written_at"`
}

// appendDiscoveryObjective appends one completed objective's result to the
// per-item checkpoint. Best-effort; errors are logged and swallowed.
func (o *orchestrator) appendDiscoveryObjective(entry discoveryCheckpointEntry) {
	if strings.TrimSpace(o.runDir) == "" {
		return
	}
	dir := filepath.Join(o.runDir, stateDir)
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

// loadDiscoveryCheckpoint reads the per-objective discovery checkpoint.
// Returns a map keyed by objective_id. Missing file returns empty map.
func (o *orchestrator) loadDiscoveryCheckpoint(dir string) map[string]discoveryCheckpointEntry {
	out := map[string]discoveryCheckpointEntry{}
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
		var entry discoveryCheckpointEntry
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

// appendDiscoveryShard records one completed shard of a sharded objective so a
// retry resumes only the shards that did not finish.
func (o *orchestrator) appendDiscoveryShard(objID string, shardIndex int, items []llmEntity) {
	o.appendDiscoveryObjective(discoveryCheckpointEntry{
		ObjectiveID: objID,
		Items:       items,
		Sharded:     true,
		ShardIndex:  shardIndex,
	})
}

// loadDiscoveryShardCheckpoint returns the completed shards for one objective,
// keyed by shard index. Missing file / no shards → empty map.
func (o *orchestrator) loadDiscoveryShardCheckpoint(dir, objID string) map[int][]llmEntity {
	out := map[int][]llmEntity{}
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
		var entry discoveryCheckpointEntry
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
// retried. On resume, loadReexaminationCheckpoint tells the stage which
// suspects it can skip.

const reexamEntitiesJSONL = "reexam_entities.jsonl"

// reexamCheckpointEntry is one row of state/reexam_entities.jsonl.
type reexamCheckpointEntry struct {
	// Key is objective_id::safe_seed_name, same scheme as detail.
	Key string `json:"key"`
	// Outcome is "confirmed", "rejected", or "clean" (was never suspect).
	// We only write "confirmed" and "rejected"; "clean" seeds never enter
	// the suspect list and are always passed through.
	Outcome string `json:"outcome"`
	// Seed is the (possibly corrected) seed after re-examination.
	// Populated only for outcome=="confirmed".
	Seed *llmEntity `json:"seed,omitempty"`
	// Unresolved is populated only for outcome=="rejected".
	Unresolved *model.UnresolvedItem `json:"unresolved,omitempty"`
	WrittenAt  time.Time             `json:"written_at"`
}

func reexamKey(objectiveID, seedName string) string {
	return objectiveID + "::" + SafeJobID(seedName)
}

// appendReexamEntity appends one completed reexamination result to the
// per-item checkpoint. Best-effort; errors are logged and swallowed.
func (o *orchestrator) appendReexamEntity(entry reexamCheckpointEntry) {
	if strings.TrimSpace(o.runDir) == "" {
		return
	}
	dir := filepath.Join(o.runDir, stateDir)
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

// loadReexaminationCheckpoint reads the per-item checkpoint written by
// previous runs. Returns a map keyed by reexamKey. Missing file returns
// an empty map (caller treats it as "nothing done yet").
func (o *orchestrator) loadReexaminationCheckpoint(dir string) map[string]reexamCheckpointEntry {
	out := map[string]reexamCheckpointEntry{}
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
		var entry reexamCheckpointEntry
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

// persistStageState writes a single stage's output as JSON to
// <runDir>/state/<filename>. It is best-effort: errors are logged but
// never abort the run because we don't want artifact persistence to
// turn a working run into a failure.
func (o *orchestrator) persistStageState(filename string, payload any) {
	if strings.TrimSpace(o.runDir) == "" {
		return
	}
	dir := filepath.Join(o.runDir, stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		util.Warn("agents.state", "could not create state dir", map[string]any{"dir": dir, "error": err})
		return
	}
	path := filepath.Join(dir, filename)
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		util.Warn("agents.state", "could not marshal state", map[string]any{"file": filename, "error": err})
		return
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		util.Warn("agents.state", "could not write state", map[string]any{"path": path, "error": err})
	}
}

// writeFailureReport writes both run_failure.json and run_failure.md
// alongside whatever partial artifacts were saved. The JSON is the
// machine-readable contract used by `diffmind retry`; the markdown is
// the human-readable summary the operator skims to decide what to do.
//
// The function is forgiving: any I/O error is logged and swallowed
// because the caller is already returning a hard error to the user; we
// don't want the failure-reporting path to mask the original problem.
func (o *orchestrator) writeFailureReport(f *Failure) {
	if f == nil || strings.TrimSpace(o.runDir) == "" {
		return
	}
	if err := os.MkdirAll(o.runDir, 0o755); err != nil {
		util.Warn("agents.failure", "could not create run dir", map[string]any{"dir": o.runDir, "error": err})
		return
	}
	jsonPath := filepath.Join(o.runDir, "run_failure.json")
	if b, err := json.MarshalIndent(f, "", "  "); err == nil {
		_ = os.WriteFile(jsonPath, append(b, '\n'), 0o644)
	} else {
		util.Warn("agents.failure", "could not marshal failure report", map[string]any{"error": err})
	}
	mdPath := filepath.Join(o.runDir, "run_failure.md")
	_ = os.WriteFile(mdPath, []byte(renderFailureMarkdown(f, o.runDir, o.snap.Path)), 0o644)
}

// renderFailureMarkdown produces the operator-facing summary. We keep
// it terse on purpose: every section is something the operator needs
// to act on, so adding fluff would just bury the signal.
func renderFailureMarkdown(f *Failure, runDir, snapshotPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Run failure\n\n")
	fmt.Fprintf(&b, "- **Stage**: `%s`\n", f.Stage)
	if f.JobID != "" {
		fmt.Fprintf(&b, "- **Job ID**: `%s`\n", f.JobID)
	}
	if f.ObjectiveID != "" {
		fmt.Fprintf(&b, "- **Objective**: `%s`\n", f.ObjectiveID)
	}
	if f.EntityName != "" {
		fmt.Fprintf(&b, "- **Entity**: `%s`\n", f.EntityName)
	}
	fmt.Fprintf(&b, "- **Error class**: `%s`\n", f.ErrorClass)
	if f.HTTPStatus > 0 {
		fmt.Fprintf(&b, "- **HTTP status**: `%d`\n", f.HTTPStatus)
	}
	if !f.OccurredAt.IsZero() {
		fmt.Fprintf(&b, "- **At**: %s\n", f.OccurredAt.Format(time.RFC3339))
	}
	if f.SessionID != "" {
		fmt.Fprintf(&b, "- **OpenCode session**: `%s`\n", f.SessionID)
	}

	fmt.Fprintf(&b, "\n## Error message\n\n```\n%s\n```\n", f.Error)

	fmt.Fprintf(&b, "\n## Files to inspect\n\n")
	// Only list files that actually exist on disk. The capture pipeline
	// writes .json on a structured success, .raw + .text on a parsed-
	// text or failed-parse path, and may write only a subset of those
	// when the call short-circuited. Listing files that were never
	// written would send the operator to read a non-existent path.
	if fileExists(f.PromptPath) {
		fmt.Fprintf(&b, "- Prompt: `%s`\n", f.PromptPath)
	}
	if f.ResponsePath != "" {
		base := strings.TrimSuffix(f.ResponsePath, filepath.Ext(f.ResponsePath))
		for _, suffix := range []string{".json", ".raw", ".text"} {
			path := base + suffix
			if !fileExists(path) {
				continue
			}
			label := map[string]string{
				".json": "Response (parsed JSON)",
				".raw":  "Response (raw HTTP body)",
				".text": "Response (text fallback)",
			}[suffix]
			fmt.Fprintf(&b, "- %s: `%s`\n", label, path)
		}
	}
	if runDir != "" {
		fmt.Fprintf(&b, "- Run dir: `%s`\n", runDir)
		events := filepath.Join(runDir, "events.jsonl")
		if fileExists(events) {
			fmt.Fprintf(&b, "- Events log: `%s`\n", events)
		}
	}
	if snapshotPath != "" && fileExists(snapshotPath) {
		fmt.Fprintf(&b, "- Snapshot (retained for retry): `%s`\n", snapshotPath)
	}

	fmt.Fprintf(&b, "\n## How to retry\n\n")
	fmt.Fprintf(&b, "After inspecting the prompt/response and fixing the underlying issue, replay the failed stage with:\n\n")
	fmt.Fprintf(&b, "```sh\n")
	fmt.Fprintf(&b, "diffmind retry %s\n", filepath.Base(runDir))
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "The retry command reads `state/*.json`, re-attaches to the retained snapshot, and runs the failing stage forward. Earlier successful stages are not re-executed.\n")

	if f.ErrorClass == "rate_limit" {
		fmt.Fprintf(&b, "\n> Tip: this looks like a rate-limit. Lower `runtime.workers` or pause until the provider quota resets before retrying.\n")
	}
	if f.ErrorClass == "schema" {
		fmt.Fprintf(&b, "\n> Tip: the model didn't honour the requested JSON schema. Inspect `*.response.raw` to see the actual reply and consider switching providers/models.\n")
	}
	if f.ErrorClass == "timeout" {
		fmt.Fprintf(&b, "\n> Tip: the call timed out. Increase `opencode.timeout_sec` and/or shrink `runtime.max_catalog_items` so each prompt is smaller.\n")
	}
	if f.ErrorClass == "stuck" {
		fmt.Fprintf(&b, "\n> Tip: the liveness watchdog declared this call stuck because the OpenCode session stopped emitting parts AND no tool was running AND no permission was pending. The aborted prompt's response is in the response files above — inspect it to see what the model was doing right before it froze. If this objective genuinely needs more thinking time (very large file), raise `runtime.idle_timeout_seconds`.\n")
	}
	if f.ErrorClass == "http_4xx" && f.HTTPStatus == 401 {
		fmt.Fprintf(&b, "\n> Tip: 401 Unauthorized. Verify `DIFFMIND_OPENCODE_USERNAME`/`PASSWORD` and that `opencode auth login` for the configured provider is current.\n")
	}
	if f.ErrorClass == "auth" {
		fmt.Fprintf(&b, "\n> Tip: the provider rejected this call due to authentication. Run `opencode auth login` for the configured provider and then retry from the dashboard (or `diffmind retry --run <id>`). The retry will skip every stage that already succeeded.\n")
	}
	if f.ErrorClass == "quota" {
		fmt.Fprintf(&b, "\n> Tip: the provider's quota or credit limit is exhausted. Top up your account (or switch to a provider with available credit), then retry. Stages that already completed will not be re-billed because they're loaded from `state/*.json`.\n")
	}
	return b.String()
}
