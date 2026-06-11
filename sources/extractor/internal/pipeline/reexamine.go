package pipeline

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	reexaminestage "github.com/mohammad-safakhou/diffmind/internal/stage/reexamine"
)

type reexamineTrigger = reexaminestage.Trigger

func (o *orchestrator) runReexamination(
	ctx context.Context,
	seeds []detailJob,
	repoFacts *repoFacts,
	onResult func(),
) ([]detailJob, []model.UnresolvedItem, error, reexamineTrigger) {
	out := (reexaminestage.Runner{
		Workers:       o.cfg.Runtime.Workers,
		RunDir:        o.runDir,
		SubDir:        o.subDir,
		MinConfidence: o.cfg.Quality.MinConfidence,
		Store:         o.store,
		Prompt:        o.promptAgent,
		Hints: func(objective objectives.Objective) objectiveHints {
			return o.hintsFor(objective, nil)
		},
		Emit:       o.emit,
		PathMapper: o.PathMapper(),
	}).Run(ctx, reexaminestage.RunInput{
		Seeds: seeds, RepoFacts: repoFacts, Progress: onResult,
	})
	return out.Jobs, out.Unresolved, out.Err, out.FailedTrigger
}
