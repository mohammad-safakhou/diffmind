package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/stage/astindex"
	infrastructurestage "github.com/mohammad-safakhou/diffmind/internal/stage/infrastructure"
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

	o.emit(events.Event{
		Kind: events.KindStageStarted, Stage: "ast_index",
		Status: events.StatusRunning,
		Payload: map[string]any{
			"snapshot": o.sessionDir,
			"tip":      "Building language-agnostic AST index of the project source.",
		},
	})

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
	out, err := (astindex.Runner{}).Run(ctx, astindex.Input{
		SnapshotPath: o.sessionDir, PrimaryLanguage: primaryLang,
		Workers: o.cfg.Runtime.Workers, Progress: progressFn,
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

	inv, err := (infrastructurestage.Runner{Prompt: o.promptAgent}).Run(ctx, infrastructurestage.Input{
		Index: o.astIndex, Facts: rf,
	})
	if err != nil {
		// Infrastructure inventory is best-effort; don't halt the run.
		util.Warn("agents.infrastructure", "inventory LLM call failed; continuing without inventory", map[string]any{"error": err.Error()})
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "infrastructure",
			Status: events.StatusSkipped, Message: "LLM call failed: " + err.Error(),
		})
		return &InfrastructureInventory{}, nil
	}

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
type InfrastructureInventory = infrastructurestage.Inventory

// InfraSystem is one external infrastructure system.
type InfraSystem = infrastructurestage.System
