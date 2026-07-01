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
schema: diffmind.config.v1
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
conventions:
  dependency_injection:
    - id: app-wire
      kind: go_wire
      roots: ["cmd/app/*_wire.go", "cmd/app/wire_set.go", "cmd/app/wire_gen.go"]
      sets:
        infra: infraSet
        outbound: outboundSet
detectors:
  disabled: ["java.http.spring"]
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
	if len(cfg.Conventions.DependencyInjection) != 1 || cfg.Conventions.DependencyInjection[0].Kind != "go_wire" {
		t.Fatalf("unexpected conventions: %+v", cfg.Conventions)
	}
}

func TestLoadRejectsUnsupportedSchema(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`schema: diffmind.config.v2`)
	if err := os.WriteFile(filepath.Join(dir, FileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected unsupported schema error")
	}
}

func TestLoadIgnoresDeprecatedUnknownEnabledDetector(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`
schema: diffmind.config.v1
detectors:
  enabled: ["java.http.unknown"]
`)
	if err := os.WriteFile(filepath.Join(dir, FileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadRejectsUnknownDisabledDetector(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`
schema: diffmind.config.v1
detectors:
  disabled: ["java.http.unknown"]
`)
	if err := os.WriteFile(filepath.Join(dir, FileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected unknown disabled detector error")
	}
}

func TestLoadRejectsBadCustomPatternRegex(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`
schema: diffmind.config.v1
patterns:
  - id: company.route
    kind: route
    regex: "["
`)
	if err := os.WriteFile(filepath.Join(dir, FileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected bad regex error")
	}
}
