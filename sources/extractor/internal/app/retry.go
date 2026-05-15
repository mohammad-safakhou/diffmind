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
	runDir := filepath.Join(in.BaseDir, runID)
	if info, err := os.Stat(runDir); err != nil || !info.IsDir() {
		return RunOutput{}, fmt.Errorf("retry: run dir %q does not exist", runDir)
	}
	util.Info("app.retry", "resuming failed run", map[string]any{"run_id": runID, "run_dir": runDir})

	// Read the failure report to confirm there is something to resume
	// AND to log a clear summary of what we're about to redo.
	failurePath := filepath.Join(runDir, "run_failure.json")
	failureBytes, err := os.ReadFile(failurePath)
	if err != nil {
		return RunOutput{}, fmt.Errorf("retry: %w (no run_failure.json — run did not fail or has been cleaned up)", err)
	}
	var prior agents.Failure
	if err := json.Unmarshal(failureBytes, &prior); err != nil {
		return RunOutput{}, fmt.Errorf("retry: failed to parse run_failure.json: %w", err)
	}
	util.Info("app.retry", "prior failure context", map[string]any{
		"stage":        prior.Stage,
		"job_id":       prior.JobID,
		"objective_id": prior.ObjectiveID,
		"error_class":  prior.ErrorClass,
		"http_status":  prior.HTTPStatus,
		"occurred_at":  prior.OccurredAt,
	})

	// Read the original manifest for repoPath / minConfidence / opencodeURL.
	manifestPath := filepath.Join(runDir, "run_manifest.json")
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		return RunOutput{}, fmt.Errorf("retry: read manifest: %w", err)
	}
	var manifest model.RunManifest
	if err := json.Unmarshal(mb, &manifest); err != nil {
		return RunOutput{}, fmt.Errorf("retry: parse manifest: %w", err)
	}
	if strings.TrimSpace(manifest.RepoPath) == "" {
		return RunOutput{}, fmt.Errorf("retry: manifest is missing repo_path")
	}

	// Locate the retained snapshot. The failure report carries it
	// explicitly; older runs (before SnapshotPath was added) fall back
	// to the path embedded in the events log.
	snapshotPath := strings.TrimSpace(prior.SnapshotPath)
	if snapshotPath == "" {
		snapshotPath = scanSnapshotPathFromEvents(filepath.Join(runDir, "events.jsonl"))
	}
	if snapshotPath == "" {
		return RunOutput{}, fmt.Errorf("retry: could not determine retained snapshot path; was the run created with a recent diffmind version?")
	}
	if info, err := os.Stat(snapshotPath); err != nil || !info.IsDir() {
		return RunOutput{}, fmt.Errorf("retry: retained snapshot %q is gone; please run from scratch", snapshotPath)
	}

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
		return RunOutput{}, fmt.Errorf("retry: opencode-url is required")
	}
	if err := oc.Health(ctx); err != nil {
		return RunOutput{}, fmt.Errorf("retry: opencode health check failed at %s: %w", in.Config.OpenCode.BaseURL, err)
	}

	result, err := agents.RunWith(ctx, in.Config, manifest.RepoPath, oc, agents.RunOptions{
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
		RepoPath:      manifest.RepoPath,
		OpenCodeURL:   in.Config.OpenCode.BaseURL,
		MinConfidence: in.Config.Quality.MinConfidence,
		Exposures:     result.Exposures,
		Dependencies:  result.Dependencies,
		Connections:   result.Connections,
		Unresolved:    result.Unresolved,
		Warnings:      result.Warnings,
		StartedAt:     started, // start of THIS retry; keeps things consistent
		FinishedAt:    time.Now().UTC(),
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

// scanSnapshotPathFromEvents pulls the snapshot path out of the
// run_started event when the failure report doesn't carry it
// explicitly (older run dirs). The events log is JSONL so we can
// stream-decode without loading the entire file at once.
func scanSnapshotPathFromEvents(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var ev struct {
			Kind    string         `json:"kind"`
			Payload map[string]any `json:"payload"`
		}
		if err := dec.Decode(&ev); err != nil {
			return ""
		}
		if ev.Kind != "run_started" {
			continue
		}
		if v, ok := ev.Payload["snapshot"].(string); ok {
			return v
		}
	}
}
