package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/agents"
	"github.com/mohammad-safakhou/diffmind/internal/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// RetryInput collects the parameters for resuming a previously-failed
// run. RunID identifies which run to resume; everything else is
// reconstructed from the run's manifest + failure report on disk.
type RetryInput struct {
	BaseDir string        // root of artifact storage (matches in.Config.Artifacts.BaseDir)
	RunID   string        // the directory under BaseDir to resume
	Config  config.Config // current config; OpenCode credentials/timeout/etc. are taken from here
	Sink    events.Sink   // optional live event sink
}

// RetryRun resumes a failed run by re-using its retained snapshot and
// per-stage state. Only the failed stage and the stages downstream of
// it are re-executed; stages that already completed are skipped on the
// authority of <runDir>/state/*.json.
//
// The function is read-mostly: it does NOT delete the run's existing
// artifacts. On success it overwrites run_manifest.json (and the
// per-type entity files). On another failure it rewrites the failure
// report so the operator sees the latest state.
func RetryRun(ctx context.Context, in RetryInput) (RunOutput, error) {
	started := time.Now().UTC()
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		return RunOutput{}, fmt.Errorf("retry: run id is required")
	}

	// fail emits a synthetic run_failed event on the sink (when one
	// is wired) before returning the error. This guarantees the
	// dashboard sees a proper terminal event with the actual error
	// message instead of bouncing through the _eof-then-reconcile
	// path — which has lower information density and previously
	// confused users with a near-instant "failed" status and no
	// explanation. Pre-flight failures (missing manifest, no
	// snapshot, opencode unreachable) all flow through here.
	fail := func(err error) (RunOutput, error) {
		if in.Sink != nil {
			in.Sink.Emit(events.Event{
				Kind: events.KindRunFailed, Status: events.StatusFailed,
				Message: err.Error(),
				Payload: map[string]any{
					"stage":       "retry_preflight",
					"error_class": "retry_preflight",
					"error":       err.Error(),
					"elapsed_ms":  time.Since(started).Milliseconds(),
				},
			})
		}
		return RunOutput{RunID: runID}, err
	}

	runDir := filepath.Join(in.BaseDir, runID)
	if info, err := os.Stat(runDir); err != nil || !info.IsDir() {
		return fail(fmt.Errorf("retry: run dir %q does not exist", runDir))
	}
	util.Info("app.retry", "resuming failed run", map[string]any{"run_id": runID, "run_dir": runDir})

	// Read the failure report to confirm there is something to resume
	// AND to log a clear summary of what we're about to redo.
	failurePath := filepath.Join(runDir, "run_failure.json")
	failureBytes, err := os.ReadFile(failurePath)
	if err != nil {
		return fail(fmt.Errorf("retry: %w (no run_failure.json — run did not fail or has been cleaned up)", err))
	}
	var prior agents.Failure
	if err := json.Unmarshal(failureBytes, &prior); err != nil {
		return fail(fmt.Errorf("retry: failed to parse run_failure.json: %w", err))
	}
	util.Info("app.retry", "prior failure context", map[string]any{
		"stage":        prior.Stage,
		"job_id":       prior.JobID,
		"objective_id": prior.ObjectiveID,
		"error_class":  prior.ErrorClass,
		"http_status":  prior.HTTPStatus,
		"occurred_at":  prior.OccurredAt,
	})

	// The retry needs repo_path + snapshot_path. We look in three
	// places, in priority order:
	//
	//   1. run_manifest.json  — only present after a successful run
	//      (or a successful retry). Carries everything.
	//   2. run_failure.json   — present after every failed run.
	//      Has snapshot_path but NOT repo_path historically; we
	//      filled it more recently but older runs lack it.
	//   3. events.jsonl::run_started.payload — fallback for older
	//      runs that have neither of the above filled in.
	//
	// Failing all three is an unrecoverable retry — surface that
	// clearly rather than silently dying with "manifest missing".
	var repoPath, snapshotPath string

	manifestPath := filepath.Join(runDir, "run_manifest.json")
	if mb, err := os.ReadFile(manifestPath); err == nil {
		var manifest model.RunManifest
		if err := json.Unmarshal(mb, &manifest); err == nil {
			repoPath = strings.TrimSpace(manifest.RepoPath)
		}
	}
	if snapshotPath == "" {
		snapshotPath = strings.TrimSpace(prior.SnapshotPath)
	}
	// Final fallback: walk events.jsonl looking for run_started.
	if repoPath == "" || snapshotPath == "" {
		gotRepo, gotSnap := scanRunStartedFromEvents(filepath.Join(runDir, "events.jsonl"))
		if repoPath == "" {
			repoPath = gotRepo
		}
		if snapshotPath == "" {
			snapshotPath = gotSnap
		}
	}
	if repoPath == "" {
		return fail(fmt.Errorf("retry: could not determine repo path; the run dir %q has no manifest and no run_started event with a repo field — run from scratch", runDir))
	}
	if snapshotPath == "" {
		return fail(fmt.Errorf("retry: could not determine retained snapshot path; was the run created with a recent diffmind version?"))
	}
	if info, err := os.Stat(snapshotPath); err != nil || !info.IsDir() {
		return fail(fmt.Errorf("retry: retained snapshot %q is gone; please run from scratch", snapshotPath))
	}
	util.Info("app.retry", "resolved retry inputs", map[string]any{
		"repo_path":     repoPath,
		"snapshot_path": snapshotPath,
	})

	// Reuse the run's existing prompts/state/events directory layout.
	captureDir := filepath.Join(runDir, "prompts")
	stateDirPath := filepath.Join(runDir, "state")

	// Re-build OpenCode client with the CURRENT config. Credentials
	// can change between runs — that's the whole point of "retry".
	oc := opencode.New(
		in.Config.OpenCode.BaseURL,
		in.Config.OpenCode.ProviderID,
		in.Config.OpenCode.ModelID,
		in.Config.OpenCode.ModelVariant,
		in.Config.OpenCode.Username,
		in.Config.OpenCode.Password,
		time.Duration(in.Config.OpenCode.TimeoutSec)*time.Second,
	)
	if !oc.Enabled() {
		return fail(fmt.Errorf("retry: opencode-url is required"))
	}
	if err := oc.Health(ctx); err != nil {
		return fail(fmt.Errorf("retry: opencode health check failed at %s: %w", in.Config.OpenCode.BaseURL, err))
	}

	result, err := agents.RunWith(ctx, in.Config, repoPath, oc, agents.RunOptions{
		Sink:          in.Sink,
		CaptureDir:    captureDir,
		RunDir:        runDir,
		ResumeFromDir: stateDirPath,
		SnapshotPath:  snapshotPath,
	})
	if err != nil {
		util.Error("app.retry", "retry failed; new failure report written", map[string]any{
			"error":         err,
			"run_id":        runID,
			"run_dir":       runDir,
			"snapshot_path": result.SnapshotPath,
		})
		return RunOutput{RunID: runID, RunDir: runDir, Failure: result.Failure}, err
	}

	// Success: clear the old failure report and overwrite the manifest.
	_ = os.Remove(filepath.Join(runDir, "run_failure.json"))
	_ = os.Remove(filepath.Join(runDir, "run_failure.md"))

	if _, err := artifacts.Write(artifacts.WriteInput{
		RunID:         runID,
		BaseDir:       in.BaseDir,
		RepoPath:      repoPath,
		OpenCodeURL:   in.Config.OpenCode.BaseURL,
		MinConfidence: in.Config.Quality.MinConfidence,
		Exposures:     result.Exposures,
		Dependencies:  result.Dependencies,
		Connections:   result.Connections,
		Unresolved:    result.Unresolved,
		Warnings:      result.Warnings,
		StartedAt:     started, // start of THIS retry; keeps things consistent
		FinishedAt:    time.Now().UTC(),
		TokenTotals:   result.Tokens,
	}); err != nil {
		util.Error("app.retry", "artifact write failed", map[string]any{"error": err})
		return RunOutput{}, err
	}
	util.Info("app.retry", "retry succeeded", map[string]any{
		"run_id": runID, "run_dir": runDir,
		"duration_ms": time.Since(started).Milliseconds(),
	})
	return RunOutput{RunID: runID, RunDir: runDir, Warning: result.Warnings}, nil
}

// scanRunStartedFromEvents pulls the repo path and snapshot path out
// of the run_started event recorded in the run's events.jsonl. It is
// the fallback used when the manifest is missing (failed runs) and
// the failure report doesn't have snapshot_path filled in (older
// runs). Returns ("", "") on any error so the caller can decide
// whether to keep trying or surface a clear "no info" message.
//
// We stream-decode rather than load the whole file because
// events.jsonl can be tens of MB for a long-running pipeline. The
// run_started event is always the FIRST line of the file, so we
// always hit it on the very first decode.
func scanRunStartedFromEvents(path string) (repoPath, snapshotPath string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var ev struct {
			Kind    string         `json:"kind"`
			Payload map[string]any `json:"payload"`
		}
		if err := dec.Decode(&ev); err != nil {
			return "", ""
		}
		if ev.Kind != "run_started" {
			continue
		}
		if v, ok := ev.Payload["repo"].(string); ok {
			repoPath = v
		}
		if v, ok := ev.Payload["snapshot"].(string); ok {
			snapshotPath = v
		}
		return repoPath, snapshotPath
	}
}
