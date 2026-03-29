package orchestrator

import (
	"fmt"
	"os/exec"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// RunDiffMind triggers a DiffMind run against a repository.
func RunDiffMind(binaryPath, repoPath, configPath string, log *util.Logger) error {
	args := []string{"run", "--repo", repoPath}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	log.Info("running DiffMind", "binary", binaryPath, "repo", repoPath)
	cmd := exec.Command(binaryPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("diffmind run failed: %w\noutput: %s", err, string(output))
	}
	log.Info("DiffMind run complete", "repo", repoPath)
	return nil
}
