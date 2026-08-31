// Package astindex builds the deterministic project index consumed by later
// extraction stages.
package astindex

import (
	"context"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/register"
)

type Runner struct{}

type Input struct {
	SourceRoot      string
	PrimaryLanguage string
	Workers         int
}

type Summary struct {
	Files      int      `json:"files"`
	Languages  []string `json:"languages,omitempty"`
	Symbols    int      `json:"symbols"`
	CallEdges  int      `json:"call_edges"`
	Configs    int      `json:"configs"`
	Frameworks int      `json:"frameworks"`
	DurationMs int64    `json:"duration_ms"`
}

type Output struct {
	Index   *ast.ProjectIndex
	Summary Summary
}

func (Runner) Run(ctx context.Context, input Input) (Output, error) {
	workers := input.Workers
	if workers <= 0 {
		workers = 8
	}
	started := time.Now()
	index, err := ast.Build(ctx, input.SourceRoot, input.PrimaryLanguage, workers)
	if err != nil {
		return Output{}, err
	}
	return Output{
		Index: index,
		Summary: Summary{
			Files:      len(index.Files),
			Languages:  index.Languages,
			Symbols:    len(index.Symbols),
			CallEdges:  CountCallEdges(index),
			Configs:    len(index.Configs),
			Frameworks: len(index.Frameworks),
			DurationMs: time.Since(started).Milliseconds(),
		},
	}, nil
}

func CountCallEdges(index *ast.ProjectIndex) int {
	total := 0
	for _, calls := range index.CallGraph {
		total += len(calls)
	}
	return total
}
