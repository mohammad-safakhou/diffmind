package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/pipeline"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type executionInput struct {
	Config         config.Config
	Sink           events.Sink
	RunID          string
	RunDir         string
	BaseDir        string
	RepoPath       string
	SnapshotPath   string
	ResumeFromDir  string
	StartedAt      time.Time
	Component      string
	ErrorPrefix    string
	RequireModel   bool
	ClearFailure   bool
	FailureLogText string
	AfterHealth    func()
	AfterPipeline  func(extraction.Result)
}

type executionError struct {
	phase string
	err   error
}

func (e *executionError) Error() string { return e.err.Error() }
func (e *executionError) Unwrap() error { return e.err }

func execute(ctx context.Context, in executionInput) (RunOutput, error) {
	var oc *opencode.Client
	if in.Config.IsDeterministicPipeline() {
		util.Info(in.Component, "deterministic pipeline selected; skipping opencode bootstrap", nil)
	} else {
		oc = opencode.New(
			in.Config.OpenCode.BaseURL,
			in.Config.OpenCode.ProviderID,
			in.Config.OpenCode.ModelID,
			in.Config.OpenCode.ModelVariant,
			in.Config.OpenCode.Username,
			in.Config.OpenCode.Password,
			time.Duration(in.Config.OpenCode.TimeoutSec)*time.Second,
		)
		if !oc.Enabled() {
			return RunOutput{}, &executionError{phase: "bootstrap", err: fmt.Errorf("%sopencode-url is required", in.ErrorPrefix)}
		}
		util.Info(in.Component, "checking opencode health", map[string]any{"base_url": in.Config.OpenCode.BaseURL})
		if err := oc.Health(ctx); err != nil {
			return RunOutput{}, &executionError{phase: "bootstrap", err: fmt.Errorf("%sopencode health check failed at %s: %w", in.ErrorPrefix, in.Config.OpenCode.BaseURL, err)}
		}
		if in.RequireModel && (strings.TrimSpace(in.Config.OpenCode.ProviderID) == "" || strings.TrimSpace(in.Config.OpenCode.ModelID) == "") {
			return RunOutput{}, &executionError{phase: "bootstrap", err: fmt.Errorf("opencode provider_id and model_id are required (got provider=%q model=%q); did you run `opencode auth login`?", in.Config.OpenCode.ProviderID, in.Config.OpenCode.ModelID)}
		}
		util.Info(in.Component, "opencode health ok", map[string]any{"base_url": in.Config.OpenCode.BaseURL})
		if in.AfterHealth != nil {
			in.AfterHealth()
		}
	}

	result, err := pipeline.New(in.Config, oc, in.Sink).Run(ctx, extraction.Request{
		RepoPath:      in.RepoPath,
		CaptureDir:    filepath.Join(in.RunDir, "prompts"),
		RunDir:        in.RunDir,
		RunID:         in.RunID,
		ResumeFromDir: in.ResumeFromDir,
		SnapshotPath:  in.SnapshotPath,
	})
	if err != nil {
		util.Error(in.Component, in.FailureLogText, map[string]any{
			"error":         err,
			"run_id":        in.RunID,
			"run_dir":       in.RunDir,
			"failure_md":    filepath.Join(in.RunDir, "run_failure.md"),
			"snapshot_path": result.SnapshotPath,
		})
		return RunOutput{
			RunID: in.RunID, RunDir: in.RunDir,
			Warning: result.Warnings, Failure: result.Failure,
		}, err
	}
	if in.AfterPipeline != nil {
		in.AfterPipeline(result)
	}

	if in.ClearFailure {
		_ = os.Remove(filepath.Join(in.RunDir, "run_failure.json"))
		_ = os.Remove(filepath.Join(in.RunDir, "run_failure.md"))
	}
	writtenRunDir, err := artifacts.Write(artifacts.WriteInput{
		RunID:                in.RunID,
		BaseDir:              in.BaseDir,
		RepoPath:             in.RepoPath,
		OpenCodeURL:          in.Config.OpenCode.BaseURL,
		MinConfidence:        in.Config.Quality.MinConfidence,
		Exposures:            result.Exposures,
		Dependencies:         result.Dependencies,
		Connections:          result.Connections,
		Unresolved:           result.Unresolved,
		Warnings:             result.Warnings,
		Pipeline:             in.Config.Pipeline(),
		ImportLegacyArchfile: in.Config.Runtime.ImportLegacyArchfile,
		StartedAt:            in.StartedAt,
		FinishedAt:           time.Now().UTC(),
		TokenTotals:          result.Tokens,
		RepoFacts:            result.Intermediate.RepoFacts,
	})
	if err != nil {
		util.Error(in.Component, "artifact write failed", map[string]any{"error": err})
		return RunOutput{}, &executionError{phase: "artifacts", err: err}
	}
	return RunOutput{RunID: in.RunID, RunDir: writtenRunDir, Warning: result.Warnings}, nil
}
