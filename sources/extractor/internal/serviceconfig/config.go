// Package serviceconfig reads manual, repo-local hints for deterministic
// DiffMind extraction. It is deliberately separate from generated DiffMind protocol output.
package serviceconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/detectors"
	"gopkg.in/yaml.v3"
)

const FileName = "diffmind-configuration.yaml"
const Schema = "diffmind.config.v1"

type Config struct {
	Schema           string                  `yaml:"schema" json:"schema"`
	Service          ServiceConfig           `yaml:"service" json:"service"`
	Paths            PathConfig              `yaml:"paths" json:"paths"`
	Aliases          AliasConfig             `yaml:"aliases" json:"aliases"`
	HTTPTargets      []HTTPTargetConfig      `yaml:"http_targets" json:"http_targets"`
	ResourcePatterns []ResourcePatternConfig `yaml:"resource_patterns" json:"resource_patterns"`
	Config           ConfigPathConfig        `yaml:"config" json:"config"`
	Conventions      ConventionConfig        `yaml:"conventions" json:"conventions"`
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

type ConventionConfig struct {
	DependencyInjection []DependencyInjectionConvention `yaml:"dependency_injection" json:"dependency_injection"`
}

type DependencyInjectionConvention struct {
	ID              string            `yaml:"id" json:"id"`
	Kind            string            `yaml:"kind" json:"kind"`
	Roots           []string          `yaml:"roots" json:"roots"`
	Sets            map[string]string `yaml:"sets" json:"sets"`
	Entrypoints     map[string]string `yaml:"entrypoints" json:"entrypoints"`
	Classifications []Classification  `yaml:"classifications" json:"classifications"`
}

type Classification struct {
	Match      map[string]string `yaml:"match" json:"match"`
	TargetRef  string            `yaml:"target_ref" json:"target_ref"`
	Kind       string            `yaml:"kind" json:"kind"`
	ConfigKeys []string          `yaml:"config_keys" json:"config_keys"`
	Metadata   map[string]string `yaml:"metadata" json:"metadata"`
}

type DetectorConfig struct {
	Enabled  []string          `yaml:"enabled" json:"enabled"` // deprecated: discovery runs all detectors by default.
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
	paths := inheritedPaths(repoPath)
	cfg := Config{}
	for _, path := range paths {
		next, err := loadFile(path)
		if err != nil {
			return nil, err
		}
		mergeConfig(&cfg, next)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func inheritedPaths(repoPath string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			seen[path] = true
			out = append(out, path)
		}
	}
	for _, path := range filepath.SplitList(os.Getenv("DIFFMIND_CONFIGURATION_PATHS")) {
		add(path)
	}
	repoAbs, err := filepath.Abs(repoPath)
	if err != nil {
		repoAbs = repoPath
	}
	var dirs []string
	for dir := filepath.Clean(repoAbs); ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		add(filepath.Join(dirs[i], FileName))
	}
	return out
}

func mergeConfig(dst *Config, src Config) {
	if strings.TrimSpace(src.Schema) != "" {
		dst.Schema = src.Schema
	}
	mergeService(&dst.Service, src.Service)
	dst.Paths.Include = append(dst.Paths.Include, src.Paths.Include...)
	dst.Paths.Exclude = append(dst.Paths.Exclude, src.Paths.Exclude...)
	mergeStringSliceMap(&dst.Aliases.Services, src.Aliases.Services)
	mergeStringSliceMap(&dst.Aliases.Resources, src.Aliases.Resources)
	dst.HTTPTargets = append(dst.HTTPTargets, src.HTTPTargets...)
	dst.ResourcePatterns = append(dst.ResourcePatterns, src.ResourcePatterns...)
	dst.Config.Paths = append(dst.Config.Paths, src.Config.Paths...)
	mergeStringMap(&dst.Config.Profiles, src.Config.Profiles)
	mergeStringMap(&dst.Config.Env, src.Config.Env)
	dst.Conventions.DependencyInjection = append(dst.Conventions.DependencyInjection, src.Conventions.DependencyInjection...)
	dst.Detectors.Enabled = append(dst.Detectors.Enabled, src.Detectors.Enabled...)
	dst.Detectors.Disabled = append(dst.Detectors.Disabled, src.Detectors.Disabled...)
	mergeStringMap(&dst.Detectors.Options, src.Detectors.Options)
	dst.Patterns = append(dst.Patterns, src.Patterns...)
}

func mergeService(dst *ServiceConfig, src ServiceConfig) {
	if strings.TrimSpace(src.ID) != "" {
		dst.ID = src.ID
	}
	if strings.TrimSpace(src.Name) != "" {
		dst.Name = src.Name
	}
	if strings.TrimSpace(src.Team) != "" {
		dst.Team = src.Team
	}
	if strings.TrimSpace(src.Domain) != "" {
		dst.Domain = src.Domain
	}
	if strings.TrimSpace(src.Criticality) != "" {
		dst.Criticality = src.Criticality
	}
}

func mergeStringMap(dst *map[string]string, src map[string]string) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = map[string]string{}
	}
	for k, v := range src {
		(*dst)[k] = v
	}
}

func mergeStringSliceMap(dst *map[string][]string, src map[string][]string) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = map[string][]string{}
	}
	for k, v := range src {
		(*dst)[k] = append((*dst)[k], v...)
	}
}

func (c Config) Validate() error {
	if schema := strings.TrimSpace(c.Schema); schema != "" && schema != Schema {
		return fmt.Errorf("schema %q is unsupported; expected %s", c.Schema, Schema)
	}
	for _, id := range c.Detectors.Disabled {
		if err := detectors.ValidateID(strings.TrimSpace(id)); err != nil {
			return err
		}
	}
	for i, convention := range c.Conventions.DependencyInjection {
		if strings.TrimSpace(convention.Kind) == "" {
			return fmt.Errorf("conventions.dependency_injection[%d].kind is required", i)
		}
		switch strings.TrimSpace(convention.Kind) {
		case "go_wire", "wire":
		default:
			return fmt.Errorf("conventions.dependency_injection[%d].kind %q is unsupported", i, convention.Kind)
		}
	}
	for i, p := range c.Patterns {
		if strings.TrimSpace(p.ID) == "" {
			return fmt.Errorf("patterns[%d].id is required", i)
		}
		if strings.TrimSpace(p.Kind) == "" {
			return fmt.Errorf("patterns[%d].kind is required", i)
		}
		if strings.TrimSpace(p.Regex) == "" {
			return fmt.Errorf("patterns[%d].regex is required", i)
		}
		if _, err := regexp.Compile(p.Regex); err != nil {
			return fmt.Errorf("patterns[%d].regex: %w", i, err)
		}
	}
	for i, target := range c.HTTPTargets {
		if strings.TrimSpace(target.ID) == "" {
			return fmt.Errorf("http_targets[%d].id is required", i)
		}
		if strings.TrimSpace(target.ServiceRef) == "" {
			return fmt.Errorf("http_targets[%d].service_ref is required", i)
		}
	}
	for i, resource := range c.ResourcePatterns {
		if strings.TrimSpace(resource.ID) == "" {
			return fmt.Errorf("resource_patterns[%d].id is required", i)
		}
		if strings.TrimSpace(resource.Kind) == "" {
			return fmt.Errorf("resource_patterns[%d].kind is required", i)
		}
	}
	return nil
}
