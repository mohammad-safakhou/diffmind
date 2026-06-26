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
	"github.com/mohammad-safakhou/diffmind/internal/preflight"
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
	logProgress("bootstrap", 0, "Initializing run context and validating configuration.")
	util.Info("app.run", "starting run", map[string]any{
		"repo_input":       in.RepoPath,
		"pipeline":         in.Config.Pipeline(),
		"opencode_enabled": in.Config.OpenCode.BaseURL != "",
		"workers":          in.Config.Runtime.Workers,
		"min_confidence":   in.Config.Quality.MinConfidence,
	})
	repo, err := filepath.Abs(in.RepoPath)
	if err != nil {
		util.Error("app.run", "failed resolving repo path", map[string]any{"error": err})
		return RunOutput{}, err
	}
	util.Debug("app.run", "resolved repo path", map[string]any{"repo": repo})
	logProgress("bootstrap", 5, "Repository path resolved.")

	// Preflight gate: run the system-readiness checks before we
	// touch the snapshot or open any OpenCode connection. A single
	// SeverityFail aborts the run with a clear message. The UI
	// handler does the same on the HTTP edge; the CLI path covers
	// `diffmind run` invocations that bypass the dashboard.
	if !in.Config.IsDeterministicPipeline() {
		checks := preflight.DefaultChecks(preflight.OptionsFromConfig(in.Config))
		rep := preflight.NewRunner(checks).Run(ctx)
		if rep.HasFail() {
			failures := rep.Failures()
			var msg strings.Builder
			msg.WriteString("preflight rejected the run; ")
			for i, f := range failures {
				if i > 0 {
					msg.WriteString("; ")
				}
				msg.WriteString(f.Title + ": " + f.Message)
				if f.Remediation != "" {
					msg.WriteString(" (")
					msg.WriteString(f.Remediation)
					msg.WriteString(")")
				}
			}
			util.Error("app.run", "preflight failed", map[string]any{
				"failures": len(failures),
				"message":  msg.String(),
			})
			return RunOutput{}, fmt.Errorf("%s", msg.String())
		}
	}

	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		runID = started.Format("20060102T150405Z")
	}
	runDir := filepath.Join(in.Config.Artifacts.BaseDir, runID)
	out, err := execute(ctx, executionInput{
		Config: in.Config, Sink: in.Sink,
		RunID: runID, RunDir: runDir, BaseDir: in.Config.Artifacts.BaseDir,
		RepoPath: repo, StartedAt: started,
		Component: "app.run", RequireModel: true,
		FailureLogText: "agent pipeline failed; see failure report",
		AfterHealth: func() {
			logProgress("bootstrap", 10, "OpenCode health check completed.")
		},
		AfterPipeline: func(result extraction.Result) {
			util.Info("app.run", "agent pipeline completed", map[string]any{
				"exposures":    len(result.Exposures),
				"dependencies": len(result.Dependencies),
				"connections":  len(result.Connections),
				"unresolved":   len(result.Unresolved),
			})
			logProgress("pipeline", 90, "Extraction pipeline completed. Preparing artifacts.")
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "opencode-url is required") {
			return RunOutput{}, fmt.Errorf("opencode-url is required; static/regex extraction path has been removed")
		}
		return out, err
	}
	logProgress("artifacts", 100, "Artifacts written successfully.")
	util.Info("app.run", "run completed", map[string]any{
		"run_id":      runID,
		"run_dir":     out.RunDir,
		"duration_ms": time.Since(started).Milliseconds(),
		"warnings":    len(out.Warning),
	})
	return out, nil
}

func logProgress(phase string, percent int, tip string) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	width := 20
	filled := int(float64(width) * (float64(percent) / 100.0))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	bar := "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "] " + fmt.Sprintf("%d%%", percent)
	util.Info("progress", "run status", map[string]any{
		"phase":   phase,
		"percent": percent,
		"bar":     bar,
		"tip":     tip,
	})
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
