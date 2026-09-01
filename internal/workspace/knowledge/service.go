package knowledge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"gopkg.in/yaml.v3"
)

// ServiceOverride is the highest-precedence, repository-owned identity file at
// .diffmind/service.yaml.
type ServiceOverride struct {
	APIVersion  string                `json:"api_version" yaml:"api_version"`
	Kind        string                `json:"kind" yaml:"kind"`
	ServiceName string                `json:"service_name" yaml:"service_name"`
	Aliases     []model.IdentityAlias `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Resources   []model.OwnedResource `json:"resources,omitempty" yaml:"resources,omitempty"`
	Metadata    map[string]string     `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

func LoadServiceOverride(repoPath string) (*ServiceOverride, error) {
	path := filepath.Join(repoPath, ".diffmind", "service.yaml")
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read service override: %w", err)
	}
	var override ServiceOverride
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&override); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if override.APIVersion != APIVersion {
		return nil, fmt.Errorf("%s api_version must be %s", path, APIVersion)
	}
	if override.Kind != "ServiceIdentity" {
		return nil, fmt.Errorf("%s kind must be ServiceIdentity", path)
	}
	if override.ServiceName == "" {
		return nil, fmt.Errorf("%s service_name is required", path)
	}
	return &override, nil
}

func ApplyServiceOverride(identity *model.ServiceIdentity, override *ServiceOverride) {
	if override == nil {
		return
	}
	identity.ServiceName = override.ServiceName
	if override.Aliases != nil {
		identity.Aliases = append([]model.IdentityAlias(nil), override.Aliases...)
	}
	if override.Resources != nil {
		identity.Resources = append([]model.OwnedResource(nil), override.Resources...)
	}
	if override.Metadata != nil {
		identity.Metadata = make(map[string]string, len(override.Metadata))
		for key, value := range override.Metadata {
			identity.Metadata[key] = value
		}
	}
}
