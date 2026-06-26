// Package serviceconfig reads manual, repo-local hints for deterministic
// DiffMind extraction. It is deliberately separate from generated DiffMind protocol output.
package serviceconfig

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const FileName = "diffmind-configuration.yaml"

type Config struct {
	Service          ServiceConfig           `yaml:"service" json:"service"`
	Paths            PathConfig              `yaml:"paths" json:"paths"`
	Aliases          AliasConfig             `yaml:"aliases" json:"aliases"`
	HTTPTargets      []HTTPTargetConfig      `yaml:"http_targets" json:"http_targets"`
	ResourcePatterns []ResourcePatternConfig `yaml:"resource_patterns" json:"resource_patterns"`
	Config           ConfigPathConfig        `yaml:"config" json:"config"`
	Detectors        DetectorConfig          `yaml:"detectors" json:"detectors"`
	Patterns         []CustomPattern         `yaml:"patterns" json:"patterns"`
}

type ServiceConfig struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Team        string `yaml:"team" json:"team"`
	Domain      string `yaml:"domain" json:"domain"`
	Criticality string `yaml:"criticality" json:"criticality"`
}

type PathConfig struct {
	Include []string `yaml:"include" json:"include"`
	Exclude []string `yaml:"exclude" json:"exclude"`
}

type AliasConfig struct {
	Services  map[string][]string `yaml:"services" json:"services"`
	Resources map[string][]string `yaml:"resources" json:"resources"`
}

type HTTPTargetConfig struct {
	ID          string            `yaml:"id" json:"id"`
	ServiceRef  string            `yaml:"service_ref" json:"service_ref"`
	External    bool              `yaml:"external" json:"external"`
	ClientClass string            `yaml:"client_class" json:"client_class"`
	ConfigKey   string            `yaml:"config_key" json:"config_key"`
	URLHost     string            `yaml:"url_host" json:"url_host"`
	PathPrefix  string            `yaml:"path_prefix" json:"path_prefix"`
	Aliases     []string          `yaml:"aliases" json:"aliases"`
	Metadata    map[string]string `yaml:"metadata" json:"metadata"`
}

type ResourcePatternConfig struct {
	ID          string            `yaml:"id" json:"id"`
	Kind        string            `yaml:"kind" json:"kind"`
	Platform    string            `yaml:"platform" json:"platform"`
	ResourceRef string            `yaml:"resource_ref" json:"resource_ref"`
	ConfigKey   string            `yaml:"config_key" json:"config_key"`
	URLHost     string            `yaml:"url_host" json:"url_host"`
	NamePattern string            `yaml:"name_pattern" json:"name_pattern"`
	Aliases     []string          `yaml:"aliases" json:"aliases"`
	Metadata    map[string]string `yaml:"metadata" json:"metadata"`
}

type ConfigPathConfig struct {
	Paths    []string          `yaml:"paths" json:"paths"`
	Profiles map[string]string `yaml:"profiles" json:"profiles"`
	Env      map[string]string `yaml:"env" json:"env"`
}

type DetectorConfig struct {
	Enabled  []string          `yaml:"enabled" json:"enabled"`
	Disabled []string          `yaml:"disabled" json:"disabled"`
	Options  map[string]string `yaml:"options" json:"options"`
}

type CustomPattern struct {
	ID          string            `yaml:"id" json:"id"`
	Kind        string            `yaml:"kind" json:"kind"`
	Language    string            `yaml:"language" json:"language"`
	FileGlob    string            `yaml:"file_glob" json:"file_glob"`
	Regex       string            `yaml:"regex" json:"regex"`
	Fields      map[string]string `yaml:"fields" json:"fields"`
	Description string            `yaml:"description" json:"description"`
}

func Path(repoPath string) string {
	return filepath.Join(repoPath, FileName)
}

func Load(repoPath string) (*Config, error) {
	data, err := os.ReadFile(Path(repoPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
