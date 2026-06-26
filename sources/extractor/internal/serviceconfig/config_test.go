package serviceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmptyConfig(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg is nil")
	}
}

func TestLoadConfiguration(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`
service:
  id: gateway-service
  team: cfp
paths:
  include: ["gateway/**"]
aliases:
  services:
    routing-service: ["ats"]
  resources:
    traffic-info-redis: ["redis_de", "redis_fr"]
http_targets:
  - id: ats
    service_ref: routing-service
    client_class: RoutingApi
    config_key: client.routing-service.baseUrl
resource_patterns:
  - id: redis
    kind: cache
    platform: redis
    resource_ref: traffic-info-redis
    config_key: DE_REDIS_URL
detectors:
  disabled: ["llm"]
patterns:
  - id: example.cronjob
    kind: activation
    file_glob: ".example/config/**/values.yaml"
    regex: "cronjob"
`)
	if err := os.WriteFile(filepath.Join(dir, FileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Service.Team != "cfp" || len(cfg.Patterns) != 1 || cfg.Aliases.Services["routing-service"][0] != "ats" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if len(cfg.HTTPTargets) != 1 || cfg.HTTPTargets[0].ClientClass != "RoutingApi" {
		t.Fatalf("unexpected http targets: %+v", cfg.HTTPTargets)
	}
	if len(cfg.ResourcePatterns) != 1 || cfg.ResourcePatterns[0].Platform != "redis" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
