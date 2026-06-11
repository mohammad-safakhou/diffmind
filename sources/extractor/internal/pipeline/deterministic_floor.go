package pipeline

import (
	"context"
	"sort"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	connectionstage "github.com/mohammad-safakhou/diffmind/internal/stage/connections"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	reconcile "github.com/mohammad-safakhou/diffmind/internal/stage/reconcile"
)

// DeterministicFloor runs ONLY the LLM-free stages of the pipeline over a
// prebuilt AST index and returns the same reconciled Result the full pipeline
// would produce minus anything that needs the model. It exists so the eval
// harness can score the deterministic recall floor cheaply, hermetically and
// without an OpenCode server — and so a regression in any deterministic stage
// (framework binding → db deriver → reconcile → AST connections) shows up as a
// CI failure rather than a silent loss inside a full LLM run.
//
// It is deliberately NOT a pipeline "mode": the canonical run always layers the
// LLM on top of this floor. This is a read-only projection of the floor, used
// only by eval and its own test. It mirrors RunWith's deterministic path
// exactly — keep the two in sync when that path changes.
func DeterministicFloor(ctx context.Context, idx *astpkg.ProjectIndex, repoPath string, cfg config.Config) Result {
	if idx == nil {
		return Result{}
	}
	objs := objectives.Default()
	minConf := cfg.Quality.MinConfidence
	workers := cfg.Runtime.Workers

	// Deterministic discovery (no events/persistence/path-mapping)
	// This is the pure core of runDeterministicDiscovery: framework bindings →
	// entities, plus call-graph-derived db operations. The orchestrator method
	// adds event emission and snapshot→source path mapping on top; neither is
	// needed here because the floor runs against the real repo, not a snapshot.
	deterministic := (discoverystage.DeterministicRunner{}).Run(discoverystage.DeterministicInput{
		Index: idx, Objectives: objs,
	})

	// Convert to model entities (same confidence/location gate as detail)
	var exposures []model.Exposure
	var dependencies []model.Dependency
	for _, result := range deterministic.Results {
		obj := result.Objective
		for _, e := range result.Items {
			base, ur := ToBase(repoPath, obj, e, minConf)
			if ur != nil {
				continue
			}
			if obj.Kind == model.KindExposure {
				exposures = append(exposures, model.Exposure{BaseEntity: base})
			} else {
				dependencies = append(dependencies, model.Dependency{BaseEntity: base})
			}
		}
	}
	exposures = reconcile.DedupeExposures(exposures)
	dependencies = reconcile.DedupeDependencies(dependencies)

	// AST-derived db augmentation (the same step the pipeline runs)
	dependencies = connectionstage.AugmentDependencies(idx, exposures, dependencies, minConf)
	stampInferredDBPlatform(idx, dependencies) // P7: give deterministic db ops the configured platform
	dependencies = reconcile.DedupeDependencies(dependencies)

	// Connections (AST walk only; no shallow fallback, no LLM repair)
	var conns []model.Connection
	var unresolved []model.UnresolvedItem
	if len(exposures) > 0 && len(dependencies) > 0 && (len(idx.Symbols) > 0 || len(idx.Frameworks) > 0) {
		out := (connectionstage.Runner{}).Run(ctx, connectionstage.Input{
			Index: idx, Exposures: exposures, Dependencies: dependencies,
			MinConfidence: minConf, Workers: workers,
		})
		conns, unresolved = out.Connections, out.Unresolved
	}
	conns, orphan := reconcile.FilterConnections(conns, exposures, dependencies)
	unresolved = append(unresolved, orphan...)

	// Deterministic ordering, matching RunWith's final sort
	sort.Slice(exposures, func(i, j int) bool { return exposures[i].ID < exposures[j].ID })
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })

	return Result{
		Exposures:    exposures,
		Dependencies: dependencies,
		Connections:  conns,
		Unresolved:   reconcile.DedupeUnresolved(unresolved),
	}
}
