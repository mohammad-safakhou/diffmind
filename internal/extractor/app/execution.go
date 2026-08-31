package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/config"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/events"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/pipeline"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

type executionInput struct {
	Config         config.Config
	Sink           events.Sink
	RunID          string
	RunDir         string
	BaseDir        string
	RepoPath       string
	ResumeFromDir  string
	StartedAt      time.Time
	Component      string
	ClearFailure   bool
	FailureLogText string
	AfterPipeline  func(extraction.Result)
}

type executionError struct {
	phase string
	err   error
}

func (e *executionError) Error() string { return e.err.Error() }
func (e *executionError) Unwrap() error { return e.err }

func execute(ctx context.Context, in executionInput) (RunOutput, error) {
	util.Info(in.Component, "deterministic pipeline selected", nil)

	result, err := pipeline.New(in.Config, in.Sink).Run(ctx, extraction.Request{
		RepoPath: in.RepoPath,
		RunDir:   in.RunDir,
		RunID:    in.RunID,
	})
	if err != nil {
		util.Error(in.Component, in.FailureLogText, map[string]any{
			"error":       err,
			"run_id":      in.RunID,
			"run_dir":     in.RunDir,
			"failure_md":  filepath.Join(in.RunDir, "run_failure.md"),
			"source_root": result.SourceRoot,
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
		RunID:         in.RunID,
		BaseDir:       in.BaseDir,
		RepoPath:      in.RepoPath,
		MinConfidence: in.Config.Quality.MinConfidence,
		Exposures:     result.Exposures,
		Dependencies:  result.Dependencies,
		Connections:   result.Connections,
		Unresolved:    result.Unresolved,
		Warnings:      result.Warnings,
		Pipeline:      in.Config.Pipeline(),
		StartedAt:     in.StartedAt,
		FinishedAt:    time.Now().UTC(),
		RepoFacts:     result.Intermediate.RepoFacts,
	})
	if err != nil {
		util.Error(in.Component, "artifact write failed", map[string]any{"error": err})
		return RunOutput{}, &executionError{phase: "artifacts", err: err}
	}
	return RunOutput{RunID: in.RunID, RunDir: writtenRunDir, Warning: result.Warnings}, nil
}
