package pipeline

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/runstate"
	detailstage "github.com/mohammad-safakhou/diffmind/internal/stage/detail"
)

func (o *orchestrator) runDetailBatch(
	ctx context.Context,
	jobs []detailJob,
	repoFacts *repoFacts,
	onResult func(),
) []detailResult {
	out := (detailstage.Runner{
		Workers:       o.cfg.Runtime.Workers,
		RunDir:        o.runDir,
		RepoPath:      o.repoPath,
		SubDir:        o.subDir,
		MinConfidence: o.cfg.Quality.MinConfidence,
		Store:         o.store,
		Prompt:        o.promptAgent,
		Hints: func(objective objectives.Objective) objectiveHints {
			return o.hintsFor(objective, nil)
		},
		Emit:       o.emit,
		PathMapper: o.PathMapper(),
	}).Run(ctx, detailstage.Input{
		Jobs: jobs, RepoFacts: repoFacts, Progress: onResult,
	})
	return out.Results
}

func (o *orchestrator) detailCheckpointForSeed(job detailJob) (runstate.DetailCheckpointEntry, bool) {
	return (detailstage.Runner{
		RepoPath: o.repoPath, MinConfidence: o.cfg.Quality.MinConfidence,
	}).CheckpointForSeed(job)
}
