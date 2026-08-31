package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/events"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/stage/astindex"
	connectionstage "github.com/mohammad-safakhou/diffmind/internal/extractor/stage/connections"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/extractor/stage/discovery"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

const astIndexStateFile = "ast_index_done"

// runASTIndexStage builds the tree-sitter project index and stores it in the
// orchestrator for use by subsequent stages (connections, discovery context).
//
// The stage:
//  1. Checks for an existing index (fast-path on retry).
//  2. Walks the source tree, parses every source and config file.
//  3. Resolves cross-file symbols and detects framework bindings.
//  4. Writes a completion marker to state/ so retries skip this stage.
func (o *orchestrator) runASTIndexStage(ctx context.Context) error {
	// Fast-path: if a prior run already built the index, load the marker
	// and trust the in-memory index that was built from the same source tree.
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
			util.Info("agents.ast_index", "index marker found; rebuilding in-memory source index", nil)
			// We still need to build the in-memory index because we can't
			// serialise the full ProjectIndex efficiently. Build it now;
			// it's cheap (seconds) even for large repos.
		}
	}

	o.emit(events.Event{
		Kind: events.KindStageStarted, Stage: "ast_index",
		Status: events.StatusRunning,
		Payload: map[string]any{
			"source_root": o.sourceRoot,
			"tip":         "Building language-agnostic AST index of the project source.",
		},
	})

	// AST parsing is extension-driven and inherently multi-language: every
	// supported source file is parsed regardless of language, and the resulting
	// index records ALL languages present (out.Summary.Languages). The
	// configured Indexer.Languages no longer gates anything; we pass the first
	// entry only as a fallback LABEL for the rare repo with no parseable source.
	primaryLang := ""
	if len(o.cfg.Indexer.Languages) > 0 {
		primaryLang = o.cfg.Indexer.Languages[0]
	}
	out, err := (astindex.Runner{}).Run(ctx, astindex.Input{
		SourceRoot: o.sourceRoot, PrimaryLanguage: primaryLang,
		Workers: o.cfg.Runtime.Workers,
	})
	if err != nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "ast_index",
			Status: events.StatusFailed, Message: err.Error(),
		})
		return fmt.Errorf("ast_index: %w", err)
	}
	o.astIndex = out.Index

	// Write completion marker and summary.
	if o.runDir != "" {
		if err := os.MkdirAll(filepath.Join(o.runDir, stateDir), 0o755); err == nil {
			if b, err := json.Marshal(out.Summary); err == nil {
				_ = os.WriteFile(filepath.Join(o.runDir, stateDir, astIndexStateFile), b, 0o644)
			}
		}
	}

	util.Info("agents.ast_index", "index built", map[string]any{
		"files":       out.Summary.Files,
		"languages":   out.Summary.Languages,
		"symbols":     out.Summary.Symbols,
		"call_edges":  out.Summary.CallEdges,
		"configs":     out.Summary.Configs,
		"frameworks":  out.Summary.Frameworks,
		"duration_ms": out.Summary.DurationMs,
	})

	o.emit(events.Event{
		Kind: events.KindStageCompleted, Stage: "ast_index",
		Status: events.StatusSuccess,
		Payload: map[string]any{
			"files":       out.Summary.Files,
			"languages":   out.Summary.Languages,
			"symbols":     out.Summary.Symbols,
			"call_edges":  out.Summary.CallEdges,
			"configs":     out.Summary.Configs,
			"frameworks":  out.Summary.Frameworks,
			"duration_ms": out.Summary.DurationMs,
		},
	})
	return nil
}

func countCallEdges(idx *ast.ProjectIndex) int {
	return astindex.CountCallEdges(idx)
}

func (o *orchestrator) runDeterministicDiscovery(ctx context.Context, objectives []objectives.Objective) []discoveryResult {
	started := time.Now()
	o.emit(events.Event{
		Kind: events.KindStageStarted, Stage: "deterministic_discovery", Status: events.StatusRunning,
	})
	if err := ctx.Err(); err != nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "deterministic_discovery",
			Status: events.StatusFailed, Message: err.Error(),
		})
		return nil
	}
	if o.astIndex == nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "deterministic_discovery",
			Status: events.StatusSkipped, Message: "AST index unavailable",
		})
		return nil
	}

	out := (discoverystage.DeterministicRunner{}).Run(discoverystage.DeterministicInput{
		Index: o.astIndex, Objectives: objectives,
	})
	o.persistStageState("deterministic_frameworks.json", out.Report)
	for _, result := range out.Results {
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "deterministic_discovery",
			JobID: "deterministic." + result.Objective.ID, Status: events.StatusSuccess,
			Payload: map[string]any{
				"objective_id": result.Objective.ID,
				"kind":         string(result.Objective.Kind),
				"type":         result.Objective.Type,
				"items":        len(result.Items),
			},
		})
	}
	o.persistStageState("deterministic_discovery.json", out.Results)
	o.emitStageCompleted("deterministic_discovery", events.StatusSuccess, map[string]any{
		"items": out.Items, "objectives": len(out.Results),
		"duration_ms": time.Since(started).Milliseconds(),
	})
	util.Info("agents.deterministic_discovery", "deterministic discovery completed", map[string]any{
		"items": out.Items, "objectives": len(out.Results),
	})
	return out.Results
}

// runConnectionsBatch is the pipeline boundary for the deterministic
// connections stage. The stage owns connection derivation and fallback policy;
// the pipeline owns externally visible events.
func (o *orchestrator) runConnectionsBatch(
	ctx context.Context,
	exposures []model.Exposure,
	dependencies []model.Dependency,
	_ map[string]objectives.Objective,
	_ *repoFacts,
) ([]model.Connection, []model.UnresolvedItem, error, string) {
	out := (connectionstage.Runner{Report: o.emitConnectionsAggregate}).Run(ctx, connectionstage.Input{
		Index:         o.astIndex,
		Exposures:     exposures,
		Dependencies:  dependencies,
		MinConfidence: o.cfg.Quality.MinConfidence,
		Workers:       o.cfg.Runtime.Workers,
	})
	return out.Connections, out.Unresolved, nil, ""
}

func (o *orchestrator) emitConnectionsAggregate(
	exposures, connections, exposuresWithoutPaths int, source string,
) {
	o.emit(events.Event{
		Kind: events.KindLog, Stage: "connections", JobID: "connections.summary",
		Message: fmt.Sprintf("%d connections across %d exposures (%d with no paths)",
			connections, exposures, exposuresWithoutPaths),
		Payload: map[string]any{
			"connections":             connections,
			"exposures":               exposures,
			"exposures_without_paths": exposuresWithoutPaths,
			"source":                  source,
		},
	})
}
