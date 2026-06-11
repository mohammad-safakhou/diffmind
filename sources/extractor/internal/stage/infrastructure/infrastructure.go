// Package infrastructure derives a project-level external-system inventory
// from configuration entries collected by the AST index.
package infrastructure

import (
	"context"
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
)

type PromptFunc func(context.Context, string, string, map[string]any) (map[string]any, error)

type Runner struct {
	Prompt PromptFunc
}

type Input struct {
	Index *ast.ProjectIndex
	Facts *extraction.RepoFacts
}

type Inventory struct {
	Databases []System `json:"databases"`
	Topics    []System `json:"topics"`
	Queues    []System `json:"queues"`
	Services  []System `json:"services"`
	Caches    []System `json:"caches"`
}

type System struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	System       string   `json:"system"`
	ConfigKeys   []string `json:"config_keys,omitempty"`
	EndpointHint string   `json:"endpoint_hint,omitempty"`
}

func (r Runner) Run(ctx context.Context, input Input) (*Inventory, error) {
	if input.Index == nil || len(input.Index.Configs) == 0 {
		return &Inventory{}, nil
	}
	payload, err := r.Prompt(ctx, "infrastructure", buildPrompt(configEntries(input.Index), input.Facts), schema())
	if err != nil {
		return nil, err
	}
	return parse(payload), nil
}

func configEntries(index *ast.ProjectIndex) string {
	var sb strings.Builder
	sb.WriteString("Configuration entries found in this project:\n\n")
	for path, file := range index.Configs {
		if len(file.Entries) == 0 {
			continue
		}
		sb.WriteString("--- " + path + " ---\n")
		for _, entry := range file.Entries {
			sb.WriteString(fmt.Sprintf("  %s = %s\n", entry.Key, entry.Value))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func buildPrompt(entries string, facts *extraction.RepoFacts) string {
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: infrastructure-analyst\n\n")
	sb.WriteString("You are analysing a software project's configuration files to identify every external infrastructure system the project interacts with.\n\n")
	if facts != nil && facts.ServiceName != "" {
		sb.WriteString("Service name: " + facts.ServiceName + "\n\n")
	}
	sb.WriteString(entries)
	sb.WriteString("\nBased on the configuration entries above, identify every external infrastructure system.\n")
	sb.WriteString("For each system provide: name (human-readable identifier), kind (database/topic/queue/http_service/cache/object_store/secret_store/other), system (postgres/mysql/mongodb/redis/kafka/sns/sqs/rabbitmq/elasticsearch/s3/http/grpc/ldap/other), the config keys that reference it, and a sample endpoint/connection string hint.\n")
	sb.WriteString("\nReturn ONLY a JSON object matching the schema. Do not include systems that are purely for metrics/logs/tracing (observability) unless they are the primary data store.\n")
	return sb.String()
}

func schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"databases": systemListSchema(),
			"topics":    systemListSchema(),
			"queues":    systemListSchema(),
			"services":  systemListSchema(),
			"caches":    systemListSchema(),
		},
	}
}

func systemListSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":          map[string]any{"type": "string"},
				"kind":          map[string]any{"type": "string"},
				"system":        map[string]any{"type": "string"},
				"config_keys":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"endpoint_hint": map[string]any{"type": "string"},
			},
		},
	}
}

func parse(payload map[string]any) *Inventory {
	inventory := &Inventory{}
	parseList := func(key string) []System {
		values, _ := payload[key].([]any)
		out := make([]System, 0, len(values))
		for _, value := range values {
			item, ok := value.(map[string]any)
			if !ok {
				continue
			}
			system := System{
				Name: stringValue(item, "name"), Kind: stringValue(item, "kind"),
				System: stringValue(item, "system"), EndpointHint: stringValue(item, "endpoint_hint"),
			}
			if keys, ok := item["config_keys"].([]any); ok {
				for _, key := range keys {
					if text, ok := key.(string); ok {
						system.ConfigKeys = append(system.ConfigKeys, text)
					}
				}
			}
			if system.Name != "" {
				out = append(out, system)
			}
		}
		return out
	}
	inventory.Databases = parseList("databases")
	inventory.Topics = parseList("topics")
	inventory.Queues = parseList("queues")
	inventory.Services = parseList("services")
	inventory.Caches = parseList("caches")
	return inventory
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}
