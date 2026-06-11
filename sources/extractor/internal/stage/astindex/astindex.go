// Package astindex builds the deterministic project index consumed by later
// extraction stages.
package astindex

import (
	"context"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/ast/framework"
)

type Runner struct{}

type Input struct {
	SnapshotPath    string
	PrimaryLanguage string
	Workers         int
	Progress        func(done, total int)
}

type Summary struct {
	Files      int   `json:"files"`
	Symbols    int   `json:"symbols"`
	CallEdges  int   `json:"call_edges"`
	Configs    int   `json:"configs"`
	Frameworks int   `json:"frameworks"`
	DurationMs int64 `json:"duration_ms"`
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
	index, err := ast.Build(ctx, input.SnapshotPath, input.PrimaryLanguage, workers, input.Progress)
	if err != nil {
		return Output{}, err
	}
	return Output{
		Index: index,
		Summary: Summary{
			Files:      len(index.Files),
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
