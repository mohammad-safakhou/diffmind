package pipeline

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	"github.com/mohammad-safakhou/diffmind/internal/stage/repofacts"
)

func (o *orchestrator) runRepoFacts(ctx context.Context) (*repoFacts, error) {
	out, err := (repofacts.Runner{Prompt: o.promptAgent}).Run(ctx, repofacts.Input{
		SubDir: o.subDir, SessionDir: o.sessionDir,
	})
	if err != nil {
		return nil, err
	}
	return out.Facts, nil
}

func (o *orchestrator) runDiscovery(
	ctx context.Context,
	objectives []objectives.Objective,
	repoFacts *repoFacts,
	onResult func(),
) []discoveryResult {
	out := o.discoveryRunner().Run(ctx, discoverystage.RunInput{
		Objectives: objectives, RepoFacts: repoFacts, Progress: onResult,
	})
	return out.Results
}

func (o *orchestrator) runDiscoveryOne(
	ctx context.Context,
	objective objectives.Objective,
	repoFacts *repoFacts,
) ([]llmEntity, error) {
	return o.discoveryRunner().RunObjective(ctx, objective, repoFacts)
}

func (o *orchestrator) discoveryRunner() discoverystage.Runner {
	return discoverystage.Runner{
		Workers:         o.cfg.Runtime.Workers,
		RunDir:          o.runDir,
		SubDir:          o.subDir,
		ASTHintsEnabled: o.cfg.Runtime.DiscoveryASTHints,
		Index:           o.astIndex,
		Store:           o.store,
		Prompt:          o.promptAgent,
		Emit:            o.emit,
		PathMapper:      o.PathMapper(),
		Confirmed:       o.discoveryConfirmed,
	}
}
