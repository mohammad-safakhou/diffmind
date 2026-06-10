package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/ast/framework" // register all detectors
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

const astIndexStateFile = "ast_index_done"

// runASTIndexStage builds the tree-sitter project index and stores it in the
// orchestrator for use by subsequent stages (connections, discovery context).
//
// The stage:
//  1. Checks for an existing index (fast-path on retry).
//  2. Walks the snapshot, parses every source and config file.
//  3. Resolves cross-file symbols and detects framework bindings.
//  4. Writes a completion marker to state/ so retries skip this stage.
func (o *orchestrator) runASTIndexStage(ctx context.Context) error {
	// Fast-path: if a prior run already built the index, load the marker
	// and trust the in-memory index that was built from the same snapshot.
	// (On a fresh start the astIndex field is nil.)
	if o.astIndex != nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "ast_index",
			Status: events.StatusSkipped, Message: "loaded from prior run",
		})
		return nil
	}

	// Check for a saved completion marker (retry fast-path).
	if o.runDir != "" {
		markerPath := filepath.Join(o.runDir, stateDir, astIndexStateFile)
		if _, err := os.Stat(markerPath); err == nil {
			util.Info("agents.ast_index", "index marker found; reusing snapshot state", nil)
			// We still need to build the in-memory index because we can't
			// serialise the full ProjectIndex efficiently. Build it now;
			// it's cheap (seconds) even for large repos.
		}
	}

	started := time.Now()
	o.emit(events.Event{
		Kind: events.KindStageStarted, Stage: "ast_index",
		Status: events.StatusRunning,
		Payload: map[string]any{
			"snapshot": o.sessionDir,
			"tip":      "Building language-agnostic AST index of the project source.",
		},
	})

	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}

	progressFn := func(done, total int) {
		if done%50 == 0 || done == total {
			o.emit(events.Event{
				Kind: events.KindStageProgress, Stage: "ast_index",
				Payload: map[string]any{"done": done, "total": total},
			})
		}
	}

	// Determine primary language: use the first configured language if set,
	// otherwise pass empty string and let langdetect within ast.Build handle it.
	primaryLang := ""
	if len(o.cfg.Indexer.Languages) > 0 {
		primaryLang = o.cfg.Indexer.Languages[0]
	}
	idx, err := ast.Build(ctx, o.sessionDir, primaryLang, workers, progressFn)
	if err != nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "ast_index",
			Status: events.StatusFailed, Message: err.Error(),
		})
		return fmt.Errorf("ast_index: %w", err)
	}

	o.astIndex = idx

	// Write completion marker and summary.
	dur := time.Since(started)
	if o.runDir != "" {
		if err := os.MkdirAll(filepath.Join(o.runDir, stateDir), 0o755); err == nil {
			summary := astIndexSummary{
				Files:      len(idx.Files),
				Symbols:    len(idx.Symbols),
				CallEdges:  countCallEdges(idx),
				Configs:    len(idx.Configs),
				Frameworks: len(idx.Frameworks),
				DurationMs: dur.Milliseconds(),
			}
			if b, err := json.Marshal(summary); err == nil {
				_ = os.WriteFile(filepath.Join(o.runDir, stateDir, astIndexStateFile), b, 0o644)
			}
		}
	}

	util.Info("agents.ast_index", "index built", map[string]any{
		"files":       len(idx.Files),
		"symbols":     len(idx.Symbols),
		"call_edges":  countCallEdges(idx),
		"configs":     len(idx.Configs),
		"frameworks":  len(idx.Frameworks),
		"duration_ms": dur.Milliseconds(),
	})

	o.emit(events.Event{
		Kind: events.KindStageCompleted, Stage: "ast_index",
		Status: events.StatusSuccess,
		Payload: map[string]any{
			"files":       len(idx.Files),
			"symbols":     len(idx.Symbols),
			"call_edges":  countCallEdges(idx),
			"configs":     len(idx.Configs),
			"frameworks":  len(idx.Frameworks),
			"duration_ms": dur.Milliseconds(),
		},
	})
	return nil
}

type astIndexSummary struct {
	Files      int   `json:"files"`
	Symbols    int   `json:"symbols"`
	CallEdges  int   `json:"call_edges"`
	Configs    int   `json:"configs"`
	Frameworks int   `json:"frameworks"`
	DurationMs int64 `json:"duration_ms"`
}

func countCallEdges(idx *ast.ProjectIndex) int {
	total := 0
	for _, calls := range idx.CallGraph {
		total += len(calls)
	}
	return total
}

// runInfrastructureStage uses the config files already parsed by ast_index to
// build an infrastructure inventory (databases, topics, queues, external services).
// It sends the flat config entries to an LLM that names each system.
func (o *orchestrator) runInfrastructureStage(ctx context.Context, rf *repoFacts) (*InfrastructureInventory, error) {
	if o.astIndex == nil || len(o.astIndex.Configs) == 0 {
		util.Info("agents.infrastructure", "no config files; skipping inventory", nil)
		return &InfrastructureInventory{}, nil
	}

	o.emit(events.Event{
		Kind: events.KindStageStarted, Stage: "infrastructure",
		Status:  events.StatusRunning,
		Payload: map[string]any{"config_files": len(o.astIndex.Configs)},
	})

	// Flatten all config entries into a concise text representation for the LLM.
	var sb strings.Builder
	sb.WriteString("Configuration entries found in this project:\n\n")
	for path, cf := range o.astIndex.Configs {
		if len(cf.Entries) == 0 {
			continue
		}
		sb.WriteString("--- " + path + " ---\n")
		for _, e := range cf.Entries {
			sb.WriteString(fmt.Sprintf("  %s = %s\n", e.Key, e.Value))
		}
		sb.WriteString("\n")
	}

	prompt := buildInfrastructurePrompt(sb.String(), rf)
	schema := infrastructureSchema()

	payload, err := o.promptAgent(ctx, "infrastructure", prompt, schema)
	if err != nil {
		// Infrastructure inventory is best-effort; don't halt the run.
		util.Warn("agents.infrastructure", "inventory LLM call failed; continuing without inventory", map[string]any{"error": err.Error()})
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "infrastructure",
			Status: events.StatusSkipped, Message: "LLM call failed: " + err.Error(),
		})
		return &InfrastructureInventory{}, nil
	}

	inv := parseInfrastructureInventory(payload)

	// Persist to state/.
	if o.runDir != "" {
		o.persistStageState("infrastructure.json", inv)
	}

	o.emit(events.Event{
		Kind: events.KindStageCompleted, Stage: "infrastructure",
		Status: events.StatusSuccess,
		Payload: map[string]any{
			"databases": len(inv.Databases),
			"topics":    len(inv.Topics),
			"queues":    len(inv.Queues),
			"services":  len(inv.Services),
		},
	})
	return inv, nil
}

// Infrastructure types

// InfrastructureInventory is the project-level list of external systems.
type InfrastructureInventory struct {
	Databases []InfraSystem `json:"databases"`
	Topics    []InfraSystem `json:"topics"`
	Queues    []InfraSystem `json:"queues"`
	Services  []InfraSystem `json:"services"`
	Caches    []InfraSystem `json:"caches"`
}

// InfraSystem is one external infrastructure system.
type InfraSystem struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`   // "database" | "topic" | "queue" | "http_service" | "cache" | ...
	System       string   `json:"system"` // "postgres" | "mysql" | "mongodb" | "kafka" | "sns" | "redis" | ...
	ConfigKeys   []string `json:"config_keys,omitempty"`
	EndpointHint string   `json:"endpoint_hint,omitempty"`
}

func buildInfrastructurePrompt(configEntries string, rf *repoFacts) string {
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: infrastructure-analyst\n\n")
	sb.WriteString("You are analysing a software project's configuration files to identify every external infrastructure system the project interacts with.\n\n")
	if rf != nil && rf.ServiceName != "" {
		sb.WriteString("Service name: " + rf.ServiceName + "\n\n")
	}
	sb.WriteString(configEntries)
	sb.WriteString("\nBased on the configuration entries above, identify every external infrastructure system.\n")
	sb.WriteString("For each system provide: name (human-readable identifier), kind (database/topic/queue/http_service/cache/object_store/secret_store/other), system (postgres/mysql/mongodb/redis/kafka/sns/sqs/rabbitmq/elasticsearch/s3/http/grpc/ldap/other), the config keys that reference it, and a sample endpoint/connection string hint.\n")
	sb.WriteString("\nReturn ONLY a JSON object matching the schema. Do not include systems that are purely for metrics/logs/tracing (observability) unless they are the primary data store.\n")
	return sb.String()
}

func infrastructureSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"databases": listOfInfraSystems(),
			"topics":    listOfInfraSystems(),
			"queues":    listOfInfraSystems(),
			"services":  listOfInfraSystems(),
			"caches":    listOfInfraSystems(),
		},
	}
}

func listOfInfraSystems() map[string]any {
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

func parseInfrastructureInventory(payload map[string]any) *InfrastructureInventory {
	inv := &InfrastructureInventory{}
	parse := func(key string) []InfraSystem {
		arr, _ := payload[key].([]any)
		var out []InfraSystem
		for _, item := range arr {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			sys := InfraSystem{
				Name:         stringVal(m, "name"),
				Kind:         stringVal(m, "kind"),
				System:       stringVal(m, "system"),
				EndpointHint: stringVal(m, "endpoint_hint"),
			}
			if keys, ok := m["config_keys"].([]any); ok {
				for _, k := range keys {
					if s, ok := k.(string); ok {
						sys.ConfigKeys = append(sys.ConfigKeys, s)
					}
				}
			}
			if sys.Name != "" {
				out = append(out, sys)
			}
		}
		return out
	}
	inv.Databases = parse("databases")
	inv.Topics = parse("topics")
	inv.Queues = parse("queues")
	inv.Services = parse("services")
	inv.Caches = parse("caches")
	return inv
}

func stringVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
