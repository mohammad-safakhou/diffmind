package eval

import (
	"context"
	"fmt"

	"github.com/mohammad-safakhou/diffmind/internal/agents"
	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// RunCheap scores the deterministic floor of one fixture against its label.
// It builds the tree-sitter index, runs agents.DeterministicFloor (no LLM,
// hermetic), and grades the result in ModeCheap. This is the CI guardrail: a
// regression in any deterministic stage drops a fixture's F1 below its checked-
// in threshold.
func RunCheap(ctx context.Context, fixtureDir string, cfg config.Config) (Report, error) {
	set, err := LoadExpected(fixtureDir)
	if err != nil {
		return Report{}, err
	}
	repo := set.ResolvedRepoPath()
	idx, err := astpkg.Build(ctx, repo, "", cfg.Runtime.Workers, nil)
	if err != nil {
		return Report{}, fmt.Errorf("ast build %s: %w", repo, err)
	}
	res := agents.DeterministicFloor(ctx, idx, repo, cfg)
	ext := Extracted{
		Exposures:    res.Exposures,
		Dependencies: res.Dependencies,
		Connections:  res.Connections,
	}
	return ScoreAll(ext, set, ModeCheap), nil
}

// ScoreRun grades an already-finished run directory (e.g. from `diffmind run`)
// against a fixture label, in ModeFull. No rebuild, no LLM — just reads the
// artifacts on disk.
func ScoreRun(runDir, fixtureDir string) (Report, error) {
	set, err := LoadExpected(fixtureDir)
	if err != nil {
		return Report{}, err
	}
	ext, err := LoadRunArtifacts(runDir)
	if err != nil {
		return Report{}, err
	}
	return ScoreAll(ext, set, ModeFull), nil
}
