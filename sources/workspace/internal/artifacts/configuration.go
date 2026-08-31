package artifacts

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const RepoConfigurationFile = "diffmind-configuration.yaml"

type rawRepoConfiguration struct {
	Team    string `yaml:"team,omitempty"`
	Service struct {
		Team string `yaml:"team,omitempty"`
	} `yaml:"service,omitempty"`
}

// RepoConfigurationPath returns the manual deterministic-pipeline hint file.
func RepoConfigurationPath(repoPath string) string {
	return filepath.Join(repoPath, RepoConfigurationFile)
}

func RepoConfigurationTeam(repoPath string) string {
	data, err := os.ReadFile(RepoConfigurationPath(repoPath))
	if err != nil {
		return "default"
	}
	var cfg rawRepoConfiguration
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "default"
	}
	return firstString(cfg.Service.Team, cfg.Team, "default")
}

func firstString(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}
