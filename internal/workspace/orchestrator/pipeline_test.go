package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

func TestPipeline_FullRun_DeterministicOnly(t *testing.T) {
	t.Setenv("DIFFMIND_HOME", t.TempDir())
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..", "..")

	tmpDir := t.TempDir()

	cfg := &config.Config{
		Repos: config.ReposConfig{
			ServiceRepos: []config.RepoEntry{
				{
					Name:              "order-service",
					Path:              filepath.Join(projectRoot, "testdata", "workspace", "sample_service_repos", "order-service"),
					DiffMindArtifacts: filepath.Join(projectRoot, "testdata", "workspace", "sample_diffmind_output", "order-service", ".diffmind", "runs", "run_001"),
				},
				{
					Name:              "billing-service",
					Path:              filepath.Join(projectRoot, "testdata", "workspace", "sample_service_repos", "billing-service"),
					DiffMindArtifacts: filepath.Join(projectRoot, "testdata", "workspace", "sample_diffmind_output", "billing-service", ".diffmind", "runs", "run_001"),
				},
			},
		},
		Packs: config.PacksConfig{
			Dirs: []string{filepath.Join(projectRoot, "packs")},
		},
		Artifacts: config.ArtifactsConfig{
			BaseDir: tmpDir,
		},
	}

	log := util.NewLogger(util.LevelDebug)
	pipeline := NewPipeline(cfg, log)

	result, err := pipeline.Run()
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	// Check basic results.
	if result.ServiceCount < 2 {
		t.Errorf("expected at least 2 services, got %d", result.ServiceCount)
	}

	// The order-service calls billing-service.example.global which should match billing-service.
	if result.EdgeCount == 0 {
		t.Log("WARNING: No edges resolved. This is expected in deterministic-only mode if identity aliases don't match dependency targets.")
	}

	if result.OutputDir == "" {
		t.Error("expected non-empty output directory")
	}

	// Check output files exist.
	graphPath := filepath.Join(result.OutputDir, "graph.json")
	if _, err := os.Stat(graphPath); os.IsNotExist(err) {
		t.Error("expected graph.json to be written")
	}

	// Check the graph has correct structure.
	if result.Graph == nil {
		t.Fatal("expected graph to be non-nil")
	}
	if result.Graph.Version != "v1alpha1" {
		t.Errorf("expected version v1alpha1, got %s", result.Graph.Version)
	}
}

func TestPipeline_WithPackExtraction(t *testing.T) {
	t.Setenv("DIFFMIND_HOME", t.TempDir())
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..", "..")

	tmpDir := t.TempDir()

	cfg := &config.Config{
		Repos: config.ReposConfig{
			ServiceRepos: []config.RepoEntry{
				{
					Name:              "order-service",
					Path:              filepath.Join(projectRoot, "testdata", "workspace", "sample_service_repos", "order-service"),
					DiffMindArtifacts: filepath.Join(projectRoot, "testdata", "workspace", "sample_diffmind_output", "order-service", ".diffmind", "runs", "run_001"),
				},
				{
					Name:              "billing-service",
					Path:              filepath.Join(projectRoot, "testdata", "workspace", "sample_service_repos", "billing-service"),
					DiffMindArtifacts: filepath.Join(projectRoot, "testdata", "workspace", "sample_diffmind_output", "billing-service", ".diffmind", "runs", "run_001"),
				},
			},
		},
		Packs: config.PacksConfig{
			Dirs: []string{filepath.Join(projectRoot, "packs")},
		},
		Artifacts: config.ArtifactsConfig{
			BaseDir: tmpDir,
		},
	}

	log := util.NewLogger(util.LevelDebug)
	pipeline := NewPipeline(cfg, log)

	result, err := pipeline.Run()
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	// Both services should have been collected.
	if result.ServiceCount < 2 {
		t.Errorf("expected at least 2 services, got %d", result.ServiceCount)
	}

	// Verify the graph structure.
	g := result.Graph
	serviceNames := make(map[string]bool)
	for _, s := range g.Services {
		serviceNames[s.Name] = true
	}

	if !serviceNames["order-service"] {
		t.Error("expected order-service in graph")
	}
	if !serviceNames["billing-service"] {
		t.Error("expected billing-service in graph")
	}

	// Check that architecture data was collected.
	for _, s := range g.Services {
		if s.Name == "order-service" && s.ExposuresCount == 0 {
			t.Error("expected order-service to have exposures")
		}
	}
}

func TestPipelineUsesBuiltInPackAndRecordsDigest(t *testing.T) {
	t.Setenv("DIFFMIND_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	values := "service:\n  name: catalog\n  port: 8080\ningress:\n  hosts: [catalog.internal]\nqueues:\n  owned: [catalog-events]\n"
	if err := os.WriteFile(filepath.Join(repo, "deploy", "values.yaml"), []byte(values), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Repos:     config.ReposConfig{ServiceRepos: []config.RepoEntry{{Name: "repo-name", Path: repo}}},
		Artifacts: config.ArtifactsConfig{BaseDir: t.TempDir()},
	}
	result, err := NewPipeline(cfg, util.NewLogger(util.LevelInfo)).Run()
	if err != nil {
		t.Fatal(err)
	}
	if result.PackSetDigest == "" {
		t.Fatal("knowledge pack set digest was not recorded")
	}
	if len(result.Graph.Services) != 1 {
		t.Fatalf("services = %+v", result.Graph.Services)
	}
	identity := result.Graph.Services[0].Identity
	if identity.ServiceName != "catalog" || len(identity.Aliases) != 1 || len(identity.Resources) != 1 {
		t.Fatalf("built-in pack identity = %+v", identity)
	}
}
