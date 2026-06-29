// Package pipeline coordinates extraction lifecycle and stage sequencing.
package pipeline

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
)

// Engine owns one extraction configuration and its runtime dependencies.
type Engine struct {
	config config.Config
	sink   events.Sink
}

func New(cfg config.Config, sink events.Sink) *Engine {
	if sink == nil {
		sink = events.NoopSink{}
	}
	return &Engine{config: cfg, sink: sink}
}

func (e *Engine) Run(ctx context.Context, request extraction.Request) (extraction.Result, error) {
	return RunWith(ctx, e.config, request.RepoPath, RunOptions{
		Sink:          e.sink,
		CaptureDir:    request.CaptureDir,
		RunDir:        request.RunDir,
		RunID:         request.RunID,
		ResumeFromDir: request.ResumeFromDir,
	})
}
