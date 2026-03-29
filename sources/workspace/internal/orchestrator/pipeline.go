// Package orchestrator implements the 3-phase DiffMind pipeline:
// Phase 1: Collection (DiffMind artifacts + Blueprint extraction)
// Phase 2: Resolution (LLM-driven identity matching)
// Phase 3: Graph construction
package orchestrator

import (
	"fmt"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/blueprints"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/graph"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/registry"
	"github.com/mohammad-safakhou/diffmind/internal/resolver"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Pipeline orchestrates the full DiffMind run.
type Pipeline struct {
	cfg    *config.Config
	client *opencode.Client
	log    *util.Logger
	reg    *registry.Registry
	bps    []*blueprints.Blueprint
}

// NewPipeline creates a new pipeline.
func NewPipeline(cfg *config.Config, client *opencode.Client, log *util.Logger) *Pipeline {
	return &Pipeline{
		cfg:    cfg,
		client: client,
		log:    log,
		reg:    registry.New(),
	}
}

// RunResult holds the output of a full pipeline run.
type RunResult struct {
	Graph        *model.CrossServiceGraph
	OutputDir    string
	Duration     time.Duration
	ServiceCount int
	EdgeCount    int
}

// Run executes the full 3-phase pipeline.
func (p *Pipeline) Run() (*RunResult, error) {
	start := time.Now()

	// Load blueprints.
	var err error
	p.bps, err = blueprints.LoadBlueprintsFromDirs(p.cfg.Blueprints.Dirs)
	if err != nil {
		p.log.Warn("failed to load some blueprints", "error", err.Error())
	}
	p.log.Info("loaded blueprints", "count", fmt.Sprintf("%d", len(p.bps)))

	// Phase 1: Collection.
	p.log.Info("=== Phase 1: Collection ===")
	if err := p.phaseCollection(); err != nil {
		return nil, fmt.Errorf("phase 1 (collection) failed: %w", err)
	}
	p.log.Info("collection complete", "registry", p.reg.Summary())

	// Phase 2: Resolution.
	p.log.Info("=== Phase 2: Resolution ===")
	resolution, err := p.phaseResolution()
	if err != nil {
		return nil, fmt.Errorf("phase 2 (resolution) failed: %w", err)
	}
	p.log.Info("resolution complete",
		"matches", fmt.Sprintf("%d", len(resolution.Matches)),
		"unresolved", fmt.Sprintf("%d", len(resolution.Unresolved)))

	// Phase 3: Graph construction.
	p.log.Info("=== Phase 3: Graph Construction ===")
	builder := graph.NewBuilder(p.reg, p.log)
	g := builder.Build(resolution)
	p.log.Info("graph built",
		"services", fmt.Sprintf("%d", len(g.Services)),
		"edges", fmt.Sprintf("%d", len(g.Edges)),
		"shared_resources", fmt.Sprintf("%d", len(g.SharedResources)))

	// Write artifacts.
	outputDir, err := artifacts.WriteGraph(p.cfg.Artifacts.BaseDir, g)
	if err != nil {
		return nil, fmt.Errorf("write artifacts: %w", err)
	}

	duration := time.Since(start)
	p.log.Info("run complete", "duration", duration.String(), "output", outputDir)

	return &RunResult{
		Graph:        g,
		OutputDir:    outputDir,
		Duration:     duration,
		ServiceCount: len(g.Services),
		EdgeCount:    len(g.Edges),
	}, nil
}

// phaseCollection reads DiffMind artifacts and runs blueprints on all repos.
func (p *Pipeline) phaseCollection() error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []error

	// Process service repos.
	for _, repo := range p.cfg.Repos.ServiceRepos {
		wg.Add(1)
		go func(repo config.RepoEntry) {
			defer wg.Done()

			// Read DiffMind artifacts.
			var arch *model.ServiceArchitecture
			var err error
			if repo.DiffMindArtifacts != "" {
				arch, err = artifacts.ReadDiffMindArtifacts(repo.DiffMindArtifacts)
			} else {
				arch, err = artifacts.ReadDiffMindRun(repo.Path)
			}
			if err != nil {
				p.log.Warn("no DiffMind data for service", "name", repo.Name, "error", err.Error())
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
				engine := blueprints.NewEngine(p.client, p.log)
				var allResults []blueprints.ExtractionResult
				for _, bp := range matchedBPs {
					results := engine.Run(bp, repo.Path, "")
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
			matchedBPs := blueprints.FindMatchingBlueprints(p.bps, repo.Path, "infra_repo")
			if len(matchedBPs) == 0 {
				p.log.Debug("no blueprints matched", "infra_repo", repo.Name)
				return
			}
			engine := blueprints.NewEngine(p.client, p.log)
			for _, bp := range matchedBPs {
				results := engine.Run(bp, repo.Path, "")
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

// phaseResolution runs identity resolution.
func (p *Pipeline) phaseResolution() (*resolver.Resolution, error) {
	res := resolver.New(p.client, p.reg, p.log)

	// Create a session for LLM resolution if client is available.
	var sessionID string
	if p.client != nil {
		session, err := p.client.CreateSession(".")
		if err != nil {
			p.log.Warn("could not create OpenCode session for resolution; using deterministic-only mode", "error", err.Error())
		} else {
			sessionID = session.ID
			defer p.client.DeleteSession(sessionID)
		}
	}

	return res.Resolve(sessionID)
}
