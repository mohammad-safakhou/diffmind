package orchestrator

import (
	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

// CollectService reads DiffMind artifacts for a single service.
func CollectService(repo config.RepoEntry, log *util.Logger) (*model.ServiceArchitecture, error) {
	if repo.DiffMindArtifacts != "" {
		log.Info("reading pre-existing artifacts", "service", repo.Name, "path", repo.DiffMindArtifacts)
		return artifacts.ReadDiffMindArtifacts(repo.DiffMindArtifacts)
	}
	log.Info("reading DiffMind run from repo", "service", repo.Name, "path", repo.Path)
	return artifacts.ReadDiffMindRun(repo.Path)
}
