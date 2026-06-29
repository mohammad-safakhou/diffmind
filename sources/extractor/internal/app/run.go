package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type RunInput struct {
	RepoPath string
	Config   config.Config
	// Sink is the optional live event sink consumed by the dashboard. nil
	// means "no live streaming"; the run still produces artifacts as usual.
	Sink events.Sink
	// RunID lets the caller pre-allocate a stable id (e.g. so the UI can
	// open the SSE stream before Run actually starts). Empty falls back to
	// a timestamp-based id.
	RunID string
}

type RunOutput struct {
	RunID   string
	RunDir  string
	Warning []string
	// Failure, when non-nil, captures why the pipeline halted. It is
	// only set when Run returns an error and the orchestrator made it
	// far enough to identify a single root cause.
	Failure *extraction.Failure
}

func Run(ctx context.Context, in RunInput) (RunOutput, error) {
	started := time.Now().UTC()
	util.Info("app.run", "starting run", map[string]any{
		"repo_input":     in.RepoPath,
		"pipeline":       in.Config.Pipeline(),
		"workers":        in.Config.Runtime.Workers,
		"min_confidence": in.Config.Quality.MinConfidence,
	})
	repo, err := filepath.Abs(in.RepoPath)
	if err != nil {
		util.Error("app.run", "failed resolving repo path", map[string]any{"error": err})
		return RunOutput{}, err
	}
	util.Debug("app.run", "resolved repo path", map[string]any{"repo": repo})

	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		runID = started.Format("20060102T150405Z")
	}
	runDir := filepath.Join(in.Config.Artifacts.BaseDir, runID)
	out, err := execute(ctx, executionInput{
		Config: in.Config, Sink: in.Sink,
		RunID: runID, RunDir: runDir, BaseDir: in.Config.Artifacts.BaseDir,
		RepoPath: repo, StartedAt: started,
		Component:      "app.run",
		FailureLogText: "deterministic pipeline failed; see failure report",
		AfterPipeline: func(result extraction.Result) {
			util.Info("app.run", "deterministic pipeline completed", map[string]any{
				"exposures":    len(result.Exposures),
				"dependencies": len(result.Dependencies),
				"connections":  len(result.Connections),
				"unresolved":   len(result.Unresolved),
			})
		},
	})
	if err != nil {
		return out, err
	}
	util.Info("app.run", "run completed", map[string]any{
		"run_id":      runID,
		"run_dir":     out.RunDir,
		"duration_ms": time.Since(started).Milliseconds(),
		"warnings":    len(out.Warning),
	})
	return out, nil
}

func PrintSummary(out RunOutput) string {
	txt := fmt.Sprintf("DiffMind run complete\nRun ID: %s\nArtifacts: %s\n", out.RunID, out.RunDir)
	if len(out.Warning) > 0 {
		txt += "Warnings:\n"
		for _, w := range out.Warning {
			txt += "- " + w + "\n"
		}
	}
	return txt
}
