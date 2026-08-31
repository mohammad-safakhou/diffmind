package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

// Env-var queue identities: real queue names are injected at deploy time via
// helm values (configMaps.<name>.env.VAR), invisible to full-key ConfigValue
// lookups. These tests model the external-tracking-translator shape observed
// in the real corpus.
func TestHelmStaticName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"orders-events-sqs", "orders-events-sqs"},
		{"{{.Values.stageConfig.account}}-{{.Values.stageConfig.uuid}}-external-tracking-catalogue-campaign-sqs", "external-tracking-catalogue-campaign-sqs"},
		{"cdp-{{.Values.stageConfig.uuid}}-content-publication-stream", "cdp-content-publication-stream"},
		{"{{.Values.stageConfig.account}}", ""}, // all template
		{"{{.Values.a}}-{{.Values.b}}", ""},     // separators only
		{"{{.Values.a}-broken", ""},             // unbalanced: don't guess
		{"external_tracking_catalogue_campaign_events_sqs", "external_tracking_catalogue_campaign_events_sqs"},
	}
	for _, c := range cases {
		if got := HelmStaticName(c.in); got != c.want {
			t.Errorf("HelmStaticName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLooksLikeEnvVar(t *testing.T) {
	for in, want := range map[string]bool{
		"CATALOGUE_CAMPAIGN_SQS_QUEUE_URL": true,
		"SQS_QUEUE_URL":                    true,
		"QUEUE1":                           true,
		"input.sqs.queue-name":             false, // dotted property key
		"queueUrl":                         false, // camelCase identifier
		"queue-url":                        false, // kebab
		"":                                 false,
	} {
		if got := looksLikeEnvVar(in); got != want {
			t.Errorf("looksLikeEnvVar(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEnvConfigValueProductionWins(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		".example/config/stage/values.yaml": cfg(".example/config/stage/values.yaml",
			"configMaps.configmap.env.CATALOGUE_CAMPAIGN_SQS_QUEUE_URL",
			"{{.Values.stageConfig.account}}-{{.Values.stageConfig.uuid}}-external-tracking-catalogue-campaign-sqs"),
		".example/config/production/values.yaml": cfg(".example/config/production/values.yaml",
			"configMaps.configmap.env.CATALOGUE_CAMPAIGN_SQS_QUEUE_URL",
			"external_tracking_catalogue_campaign_events_sqs"),
	}}

	got, ok := EnvConfigValue(idx, "CATALOGUE_CAMPAIGN_SQS_QUEUE_URL")
	if !ok || got != "external_tracking_catalogue_campaign_events_sqs" {
		t.Fatalf("EnvConfigValue = %q, %v; want production value", got, ok)
	}
}

func TestEnvConfigValueAgreementAndDisagreement(t *testing.T) {
	// All environments agree (after template stripping) -> resolved.
	agree := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		".example/config/stage/values.yaml": cfg(".example/config/stage/values.yaml",
			"configMaps.c.env.EVENTS_QUEUE", "{{.Values.uuid}}-orders-events-sqs"),
		".example/config/dev/values.yaml": cfg(".example/config/dev/values.yaml",
			"configMaps.c.env.EVENTS_QUEUE", "{{.Values.uuid}}-orders-events-sqs"),
	}}
	if got, ok := EnvConfigValue(agree, "EVENTS_QUEUE"); !ok || got != "orders-events-sqs" {
		t.Fatalf("agreeing overlays = %q, %v; want orders-events-sqs", got, ok)
	}

	// Non-production environments disagree, no production overlay -> unresolved.
	disagree := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		".example/config/stage/values.yaml": cfg(".example/config/stage/values.yaml",
			"configMaps.c.env.EVENTS_QUEUE", "stage-orders-sqs"),
		".example/config/dev/values.yaml": cfg(".example/config/dev/values.yaml",
			"configMaps.c.env.EVENTS_QUEUE", "dev-orders-sqs"),
	}}
	if got, ok := EnvConfigValue(disagree, "EVENTS_QUEUE"); ok {
		t.Fatalf("disagreeing overlays resolved to %q; want unresolved", got)
	}
}

// The full chain observed in external-tracking-translator:
// @SqsListener("${input.sqs.catalogue-campaign-events-queue-name}")
//
//	-> application-stage.yml: input.sqs...queue-name: ${CATALOGUE_CAMPAIGN_SQS_QUEUE_URL}
//	-> .example/config/production/values.yaml env: the real queue name.
func TestResolveResourceNameThroughEnvChain(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"src/main/resources/application.yml": cfg("src/main/resources/application.yml",
			"input.sqs.catalogue-campaign-events-queue-name", "${CATALOGUE_CAMPAIGN_SQS_QUEUE_URL}"),
		".example/config/production/values.yaml": cfg(".example/config/production/values.yaml",
			"configMaps.configmap.env.CATALOGUE_CAMPAIGN_SQS_QUEUE_URL",
			"external_tracking_catalogue_campaign_events_sqs"),
	}}

	res := ResolveResourceNameDetailed(idx, "${input.sqs.catalogue-campaign-events-queue-name}")
	if res.Name != "external_tracking_catalogue_campaign_events_sqs" {
		t.Fatalf("Name = %q; want the helm-injected queue name", res.Name)
	}
	if res.Source != "env_value" {
		t.Fatalf("Source = %q; want env_value", res.Source)
	}
	if res.EnvVar != "CATALOGUE_CAMPAIGN_SQS_QUEUE_URL" {
		t.Fatalf("EnvVar = %q; want the env-var breadcrumb", res.EnvVar)
	}
	if res.ConfigKey != "input.sqs.catalogue-campaign-events-queue-name" {
		t.Fatalf("ConfigKey = %q; want the placeholder body", res.ConfigKey)
	}
}

// When the env var is nowhere in deployment config, the fallback name keeps
// working but carries config_key provenance so DiffMind won't join on it.
func TestResolveResourceNameFallbackProvenance(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml",
			"input.sqs.queue-url", "${SOME_UNKNOWN_QUEUE_URL}"),
	}}

	res := ResolveResourceNameDetailed(idx, "${input.sqs.queue-url}")
	if res.Source != "config_key" {
		t.Fatalf("Source = %q; want config_key fallback", res.Source)
	}
	if res.EnvVar != "SOME_UNKNOWN_QUEUE_URL" {
		t.Fatalf("EnvVar = %q; want breadcrumb even when unresolved", res.EnvVar)
	}
	if res.Name != "queue-url" {
		t.Fatalf("Name = %q; want key-segment fallback", res.Name)
	}
}

// URL-shaped env values still reduce to the trailing queue name.
func TestResolveResourceNameEnvURLValue(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		".example/config/production/values.yaml": cfg(".example/config/production/values.yaml",
			"configMaps.c.env.ORDERS_QUEUE_URL", "https://sqs.eu-west-1.amazonaws.com/123456789/orders-events-sqs"),
	}}

	res := ResolveResourceNameDetailed(idx, "${ORDERS_QUEUE_URL}")
	if res.Name != "orders-events-sqs" || res.Source != "env_value" {
		t.Fatalf("got %+v; want trailing segment orders-events-sqs via env_value", res)
	}
}
