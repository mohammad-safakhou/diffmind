package pipeline

import (
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
)

func (o *orchestrator) hintsFor(objective objectives.Objective, fileScope []string) objectiveHints {
	if !o.cfg.Runtime.DiscoveryASTHints {
		return objectiveHints{}
	}
	return discoverystage.BuildObjectiveHints(o.astIndex, objective, o.subDir, fileScope)
}
