package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/agents"
	"github.com/mohammad-safakhou/diffmind/internal/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
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
}

func Run(ctx context.Context, in RunInput) (RunOutput, error) {
	started := time.Now().UTC()
	logProgress("bootstrap", 0, "Initializing run context and validating configuration.")
	util.Info("app.run", "starting run", map[string]any{
		"repo_input":       in.RepoPath,
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

	oc := opencode.New(
		in.Config.OpenCode.BaseURL,
		in.Config.OpenCode.ProviderID,
		in.Config.OpenCode.ModelID,
		in.Config.OpenCode.ModelVariant,
		in.Config.OpenCode.Username,
		in.Config.OpenCode.Password,
		time.Duration(in.Config.OpenCode.TimeoutSec)*time.Second,
	)
	warnings := make([]string, 0)
	if oc.Enabled() {
		util.Info("app.run", "checking opencode health", map[string]any{"base_url": in.Config.OpenCode.BaseURL})
		if err := oc.Health(ctx); err != nil {
			util.Warn("app.run", "opencode health failed", map[string]any{"error": err})
			warnings = append(warnings, "OpenCode health check failed: "+err.Error())
		} else {
			util.Info("app.run", "opencode health ok", map[string]any{"base_url": in.Config.OpenCode.BaseURL})
		}
		logProgress("bootstrap", 10, "OpenCode health check completed.")
	} else {
		return RunOutput{}, fmt.Errorf("opencode-url is required; static/regex extraction path has been removed")
	}

	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		runID = started.Format("20060102T150405Z")
	}
	captureDir := filepath.Join(in.Config.Artifacts.BaseDir, runID, "prompts")
	result, err := agents.RunWith(ctx, in.Config, repo, oc, agents.RunOptions{
		Sink:       in.Sink,
		CaptureDir: captureDir,
	})
	if err != nil {
		util.Error("app.run", "agent pipeline failed", map[string]any{"error": err})
		return RunOutput{}, err
	}
	util.Info("app.run", "agent pipeline completed", map[string]any{
		"exposures":    len(result.Exposures),
		"dependencies": len(result.Dependencies),
		"connections":  len(result.Connections),
		"unresolved":   len(result.Unresolved),
	})
	logProgress("pipeline", 90, "Extraction pipeline completed. Preparing artifacts.")
	warnings = append(warnings, result.Warnings...)

	runDir, err := artifacts.Write(artifacts.WriteInput{
		RunID:         runID,
		BaseDir:       in.Config.Artifacts.BaseDir,
		RepoPath:      repo,
		OpenCodeURL:   in.Config.OpenCode.BaseURL,
		MinConfidence: in.Config.Quality.MinConfidence,
		Exposures:     result.Exposures,
		Dependencies:  result.Dependencies,
		Connections:   result.Connections,
		Unresolved:    result.Unresolved,
		Warnings:      warnings,
		StartedAt:     started,
		FinishedAt:    time.Now().UTC(),
	})
	if err != nil {
		util.Error("app.run", "artifact write failed", map[string]any{"error": err})
		return RunOutput{}, err
	}
	logProgress("artifacts", 100, "Artifacts written successfully.")
	util.Info("app.run", "run completed", map[string]any{
		"run_id":      runID,
		"run_dir":     runDir,
		"duration_ms": time.Since(started).Milliseconds(),
		"warnings":    len(warnings),
	})
	return RunOutput{RunID: runID, RunDir: runDir, Warning: warnings}, nil
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
