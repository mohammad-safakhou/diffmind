// Package orchestrator implements the 3-phase DiffMind pipeline:
// Phase 1: Collection (DiffMind Protocol artifacts + deterministic blueprint extraction)
// Phase 2: Deterministic resolution
// Phase 3: Graph construction
package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/blueprints"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/graph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/registry"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/resolver"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

// ProgressEvent reports a phase transition or notable event during a run. The
// run manager turns these into SSE messages; the CLI ignores them.
type ProgressEvent struct {
	Stage   string
	Status  string // started | completed | failed | info
	Message string
}

// Pipeline orchestrates the full DiffMind run.
type Pipeline struct {
	cfg *config.Config
	log *util.Logger
	reg *registry.Registry
	bps []*blueprints.Blueprint

	// Progress, when set, receives phase transitions for live streaming.
	Progress func(ProgressEvent)

	mu       sync.Mutex
	warnings []string
}

// NewPipeline creates a new pipeline.
func NewPipeline(cfg *config.Config, log *util.Logger) *Pipeline {
	return &Pipeline{
		cfg: cfg,
		log: log,
		reg: registry.New(),
	}
}

// RunResult holds the output of a full pipeline run.
type RunResult struct {
	Graph        *model.CrossServiceGraph
	OutputDir    string
	Duration     time.Duration
	ServiceCount int
	EdgeCount    int
	Warnings     []string
}

// Run executes the full pipeline with a background context (CLI entry point).
func (p *Pipeline) Run() (*RunResult, error) { return p.RunCtx(context.Background()) }

// emit publishes a progress event if a sink is registered.
func (p *Pipeline) emit(stage, status, message string) {
	if p.Progress != nil {
		p.Progress(ProgressEvent{Stage: stage, Status: status, Message: message})
	}
}

// warnf records a warning that is both logged and surfaced in RunResult.
func (p *Pipeline) warnf(msg string, kv ...string) {
	p.mu.Lock()
	p.warnings = append(p.warnings, msg)
	p.mu.Unlock()
	p.log.Warn(msg, kv...)
}

// RunCtx executes the full 3-phase pipeline, honouring context cancellation at
// phase boundaries and writing graph output into cfg.Artifacts.BaseDir
// (treated as the exact output directory, no extra subdir).
func (p *Pipeline) RunCtx(ctx context.Context) (*RunResult, error) {
	start := time.Now()

	// Load blueprints.
	var err error
	p.bps, err = blueprints.LoadBlueprintsFromDirs(p.cfg.Blueprints.Dirs)
	if err != nil {
		p.warnf("failed to load some blueprints", "error", err.Error())
	}
	p.log.Info("loaded blueprints", "count", fmt.Sprintf("%d", len(p.bps)))

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 1: Collection.
	p.emit("collection", "started", "Reading DiffMind artifacts and extracting identities")
	p.log.Info("=== Phase 1: Collection ===")
	if err := p.phaseCollection(ctx); err != nil {
		p.emit("collection", "failed", err.Error())
		return nil, fmt.Errorf("phase 1 (collection) failed: %w", err)
	}
	p.emit("collection", "completed", p.reg.Summary())
	p.log.Info("collection complete", "registry", p.reg.Summary())

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 2: Resolution (deterministic only).
	p.emit("resolution", "started", "Matching dependencies to service identities")
	p.log.Info("=== Phase 2: Resolution ===")
	resolution, err := resolver.New(p.reg, p.log).Resolve()
	if err != nil {
		p.emit("resolution", "failed", err.Error())
		return nil, fmt.Errorf("phase 2 (resolution) failed: %w", err)
	}
	p.emit("resolution", "completed", fmt.Sprintf("%d matches, %d unresolved", len(resolution.Matches), len(resolution.Unresolved)))
	p.log.Info("resolution complete",
		"matches", fmt.Sprintf("%d", len(resolution.Matches)),
		"unresolved", fmt.Sprintf("%d", len(resolution.Unresolved)))

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 3: Graph construction.
	p.emit("graph", "started", "Building cross-service graph")
	p.log.Info("=== Phase 3: Graph Construction ===")
	builder := graph.NewBuilder(p.reg, p.log)
	g := builder.Build(resolution)
	p.log.Info("graph built",
		"services", fmt.Sprintf("%d", len(g.Services)),
		"edges", fmt.Sprintf("%d", len(g.Edges)),
		"shared_resources", fmt.Sprintf("%d", len(g.SharedResources)))

	// Write artifacts directly into the configured output directory.
	outputDir := p.cfg.Artifacts.BaseDir
	if err := artifacts.WriteGraphTo(outputDir, g); err != nil {
		p.emit("graph", "failed", err.Error())
		return nil, fmt.Errorf("write artifacts: %w", err)
	}
	p.emit("graph", "completed", fmt.Sprintf("%d services, %d edges", len(g.Services), len(g.Edges)))

	duration := time.Since(start)
	p.log.Info("run complete", "duration", duration.String(), "output", outputDir)

	p.mu.Lock()
	warnings := append([]string(nil), p.warnings...)
	p.mu.Unlock()

	return &RunResult{
		Graph:        g,
		OutputDir:    outputDir,
		Duration:     duration,
		ServiceCount: len(g.Services),
		EdgeCount:    len(g.Edges),
		Warnings:     warnings,
	}, nil
}

// phaseCollection reads DiffMind artifacts and runs blueprints on all repos.
func (p *Pipeline) phaseCollection(ctx context.Context) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []error

	// Process service repos.
	for _, repo := range p.cfg.Repos.ServiceRepos {
		wg.Add(1)
		go func(repo config.RepoEntry) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}

			// Read DiffMind artifacts.
			var arch *model.ServiceArchitecture
			var err error
			if repo.DiffMindArtifacts != "" {
				arch, err = artifacts.ReadDiffMindArtifacts(repo.DiffMindArtifacts)
			} else {
				arch, err = artifacts.ReadDiffMindRun(repo.Path)
			}
			if err != nil {
				p.warnf("no DiffMind data for service", "name", repo.Name, "error", err.Error())
				// Continue without architecture data — we still want identity.
				arch = &model.ServiceArchitecture{ServiceName: repo.Name, RepoPath: repo.Path}
			}

			p.reg.AddArchitecture(repo.Name, arch)
			p.log.Info("collected architecture",
				"service", repo.Name,
				"exposures", fmt.Sprintf("%d", len(arch.Exposures)),
				"dependencies", fmt.Sprintf("%d", len(arch.Dependencies)))

			// Run blueprints for identity extraction.
			matchedBPs := blueprints.FindMatchingBlueprints(p.bps, repo.Path, "service_repo")
			if len(matchedBPs) > 0 {
				engine := blueprints.NewEngine(p.log)
				var allResults []blueprints.ExtractionResult
				for _, bp := range matchedBPs {
					results := engine.Run(bp, repo.Path)
					allResults = append(allResults, results...)
				}
				if len(allResults) > 0 {
					identity := blueprints.ToIdentity(repo.Name, repo.Path, allResults)
					p.reg.AddIdentity(repo.Name, &identity)
					p.log.Info("extracted identity",
						"service", repo.Name,
						"aliases", fmt.Sprintf("%d", len(identity.Aliases)),
						"resources", fmt.Sprintf("%d", len(identity.Resources)))
				}
			}
		}(repo)
	}

	// Process infra repos.
	for _, repo := range p.cfg.Repos.InfraRepos {
		wg.Add(1)
		go func(repo config.RepoEntry) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			matchedBPs := blueprints.FindMatchingBlueprints(p.bps, repo.Path, "infra_repo")
			if len(matchedBPs) == 0 {
				p.log.Debug("no blueprints matched", "infra_repo", repo.Name)
				return
			}
			engine := blueprints.NewEngine(p.log)
			for _, bp := range matchedBPs {
				results := engine.Run(bp, repo.Path)
				p.log.Info("infra blueprint executed",
					"repo", repo.Name,
					"blueprint", bp.Name,
					"results", fmt.Sprintf("%d", len(results)))
				// Infra results may contain cross-service mappings.
				// These need special handling based on the maps_to type.
				for _, r := range results {
					for mapsTo, val := range r.Values {
						if mapsTo == "queue_ownership" {
							// Expected: map of queue_name → service_name.
							if m, ok := val.(map[string]any); ok {
								for _, svcName := range m {
									if svc, ok := svcName.(string); ok {
										entry := p.reg.Get(svc)
										if entry != nil && entry.Identity != nil {
											// Already handled.
										}
									}
								}
							}
						}
					}
				}
			}
		}(repo)
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(errors) > 0 {
		return errors[0]
	}
	return nil
}
