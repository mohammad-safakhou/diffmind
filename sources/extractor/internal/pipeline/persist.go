package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/runstate"
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
// without re-running everything that already worked. The canonical definition
// lives in runstate; this alias keeps orchestration call sites terse.
const stateDir = runstate.StateDir

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
	DetailCheckpoint map[string]runstate.DetailCheckpointEntry
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
