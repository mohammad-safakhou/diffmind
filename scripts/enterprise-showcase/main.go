// Command enterprise-showcase creates a deterministic, synthetic DiffMind
// workspace for exercising navigation and rendering at company scale.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

const defaultSeed int64 = 20260910

var teamNames = []string{
	"catalog", "checkout", "customer", "data", "edge",
	"fulfillment", "growth", "identity", "payments", "platform",
}

var serviceRoles = []string{
	"api", "worker", "events", "reader", "writer",
	"orchestrator", "scheduler", "gateway", "sync", "search",
	"rules", "history", "admin", "exporter", "monitor",
}

var languages = []string{"go", "java", "python", "typescript", "kotlin", "csharp"}

type fixtureConfig struct {
	Home            string
	Teams           int
	ServicesPerTeam int
	Seed            int64
}

type fixtureSummary struct {
	ProjectID       string         `json:"project_id"`
	Seed            int64          `json:"seed"`
	Teams           int            `json:"teams"`
	Services        int            `json:"services"`
	Relationships   int            `json:"relationships"`
	Resources       int            `json:"resources"`
	EdgesByType     map[string]int `json:"edges_by_type"`
	GeneratedSource string         `json:"generated_source"`
}

func main() {
	home := flag.String("home", "", "new or empty DiffMind home directory")
	teams := flag.Int("teams", 10, "number of teams")
	services := flag.Int("services-per-team", 15, "services generated per team")
	seed := flag.Int64("seed", defaultSeed, "deterministic random seed")
	flag.Parse()

	if strings.TrimSpace(*home) == "" {
		fmt.Fprintln(os.Stderr, "--home is required")
		os.Exit(2)
	}
	summary, err := createFixture(fixtureConfig{Home: *home, Teams: *teams, ServicesPerTeam: *services, Seed: *seed})
	if err != nil {
		fmt.Fprintln(os.Stderr, "enterprise showcase:", err)
		os.Exit(1)
	}
	fmt.Printf("Synthetic enterprise workspace created at %s\n", *home)
	fmt.Printf("Project %s: %d teams, %d services, %d relationships, %d resources\n", summary.ProjectID, summary.Teams, summary.Services, summary.Relationships, summary.Resources)
	fmt.Printf("Start it with: DIFFMIND_HOME=%q ./bin/diffmind ui --no-spa-rebuild\n", *home)
}

func createFixture(cfg fixtureConfig) (*fixtureSummary, error) {
	if cfg.Teams < 1 || cfg.Teams > len(teamNames) {
		return nil, fmt.Errorf("teams must be between 1 and %d", len(teamNames))
	}
	if cfg.ServicesPerTeam < 2 || cfg.ServicesPerTeam > len(serviceRoles) {
		return nil, fmt.Errorf("services-per-team must be between 2 and %d", len(serviceRoles))
	}
	if err := ensureEmptyWorkspace(cfg.Home); err != nil {
		return nil, err
	}

	st, err := store.New(cfg.Home)
	if err != nil {
		return nil, err
	}
	project, err := st.CreateProject(store.Project{
		Name:        fmt.Sprintf("Northstar Commerce — %d-service synthetic company", cfg.Teams*cfg.ServicesPerTeam),
		Instruction: "Synthetic scale fixture. Metrics and topology are generated; no production data is present.",
	})
	if err != nil {
		return nil, err
	}

	generatedAt := time.Now().UTC()
	rng := rand.New(rand.NewSource(cfg.Seed))
	graph := &archgraph.ArchGraph{RunID: "pending", Connectivity: &archgraph.ConnectivityStats{EdgesByType: map[string]int{}}}
	refs := make([]store.RunRepoRef, 0, cfg.Teams*cfg.ServicesPerTeam)
	sourceRoot := filepath.Join(cfg.Home, "synthetic-repositories")

	for teamIndex := 0; teamIndex < cfg.Teams; teamIndex++ {
		team := teamNames[teamIndex]
		for serviceIndex := 0; serviceIndex < cfg.ServicesPerTeam; serviceIndex++ {
			name := team + "-" + serviceRoles[serviceIndex]
			lang := languages[rng.Intn(len(languages))]
			loc := 1800 + rng.Intn(78201)
			files := 18 + rng.Intn(483)
			repoPath := filepath.Join(sourceRoot, team, name)
			if err := writeSyntheticSource(repoPath, name, team, lang, loc, files, cfg.Seed); err != nil {
				return nil, err
			}
			sha := syntheticSHA(cfg.Seed, teamIndex, serviceIndex)
			repo, err := st.CreateRepo(project.ID, store.Repo{
				Name: name, Path: repoPath, Kind: "service_repo", SourceType: "local",
				Team: team, HeadSHA: sha, DefaultBranch: "main",
				SyncStatus: "diffmind_completed", LastDiffMindRunID: syntheticRunID(teamIndex, serviceIndex),
				DiffMindFreshness: "fresh",
			})
			if err != nil {
				return nil, err
			}
			runID := syntheticRunID(teamIndex, serviceIndex)
			if err := writeAnalyzerManifest(cfg.Home, runID, repoPath, name, team, sha, lang, loc, files, generatedAt); err != nil {
				return nil, err
			}
			refs = append(refs, store.RunRepoRef{RepoID: repo.ID, DiffMindRunID: runID})
			graph.Services = append(graph.Services, &archgraph.ServiceNode{
				Name: name, Known: true, RepoID: repo.ID, RepoPath: repoPath, Team: team,
				ComponentKind: "service", ComponentType: componentType(serviceIndex), DiffMindFreshness: "fresh",
				AnalysisStatus:  &archgraph.RepositoryAnalysisStatus{State: "analyzed_clean", AnalyzedRevision: sha, Branch: "main", AnalyzedAt: generatedAt.Format(time.RFC3339), SchemaVersion: "diffmind.service.v1"},
				RepoMetrics:     &model.RepoMetrics{TotalLOC: loc, FileCount: files, Languages: []model.LanguageMetric{{Language: lang, Files: files, LOC: loc}}},
				EntrypointCount: 1 + rng.Intn(9), DownstreamCount: 1 + rng.Intn(6), TraceCount: 1 + rng.Intn(12),
			})
		}
	}

	buildTopology(graph, cfg.Teams, cfg.ServicesPerTeam)
	graph.Connectivity.Services = len(graph.Services)
	graph.Connectivity.ServiceToServiceEdges = countServiceEdges(graph)
	graph.Connectivity.AsyncChains = cfg.Teams

	run, err := st.CreateRun(project.ID, store.RunManifest{Status: store.RunCompleted, Repos: refs, StartedAt: generatedAt})
	if err != nil {
		return nil, err
	}
	graph.RunID = run.ID
	run.ServiceCount = len(graph.Services)
	run.EdgeCount = len(graph.Edges)
	run.FinishedAt = generatedAt.Add(14 * time.Second)
	run.GraphQuality = &store.GraphQuality{}
	if err := st.SaveRun(project.ID, *run); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(st.RunDir(project.ID, run.ID), "graph.json"), graph); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(st.RunDir(project.ID, run.ID), "graph-overview.json"), archgraph.Overview(graph)); err != nil {
		return nil, err
	}
	ingestion, err := st.CreateIngestion(project.ID, store.Ingestion{
		Status: store.IngestionCompleted, Phase: "building_graph", Provider: "synthetic",
		Source: "deterministic enterprise fixture", Discovered: len(graph.Services), Imported: len(graph.Services),
		Repositories: len(graph.Services), Analyzed: len(graph.Services), GraphRunID: run.ID,
	})
	if err != nil {
		return nil, err
	}
	ingestion.FinishedAt = generatedAt.Add(14 * time.Second)
	if err := st.SaveIngestion(project.ID, *ingestion); err != nil {
		return nil, err
	}

	summary := &fixtureSummary{
		ProjectID: project.ID, Seed: cfg.Seed, Teams: cfg.Teams, Services: len(graph.Services),
		Relationships: len(graph.Edges), Resources: len(graph.ResourceNodes),
		EdgesByType: graph.Connectivity.EdgesByType, GeneratedSource: sourceRoot,
	}
	if err := writeJSON(filepath.Join(cfg.Home, "enterprise-summary.json"), summary); err != nil {
		return nil, err
	}
	return summary, nil
}

func ensureEmptyWorkspace(home string) error {
	entries, err := os.ReadDir(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.MkdirAll(home, 0o700)
		}
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("destination must be empty: %s", home)
	}
	return nil
}

func buildTopology(graph *archgraph.ArchGraph, teams, servicesPerTeam int) {
	addEdge := func(from, to, kind, label string) {
		graph.Edges = append(graph.Edges, &archgraph.GraphEdge{From: from, To: to, Type: kind, Label: label, Confidence: 1})
		graph.Connectivity.EdgesByType[kind]++
	}
	for teamIndex := 0; teamIndex < teams; teamIndex++ {
		team := teamNames[teamIndex]
		service := func(index int) string { return team + "-" + serviceRoles[index%servicesPerTeam] }
		for i := 0; i < servicesPerTeam-1; i++ {
			kind := "http"
			if i%5 == 2 {
				kind = "rpc"
			}
			addEdge(service(i), service(i+1), kind, strings.ToUpper(kind))
		}
		for _, pair := range [][2]int{{0, 4}, {3, 8}, {7, 12}} {
			if pair[1] < servicesPerTeam {
				addEdge(service(pair[0]), service(pair[1]), "http", "HTTP")
			}
		}

		dbID := "resource:db:" + team
		cacheID := "resource:cache:" + team
		queueID := "resource:queue:" + team
		graph.ResourceNodes = append(graph.ResourceNodes,
			&archgraph.ResourceNode{ID: "db:" + team, GraphID: dbID, Name: team + " database", Kind: "database", Platform: "postgresql", OwnerTeam: team, OperationCount: 12},
			&archgraph.ResourceNode{ID: "cache:" + team, GraphID: cacheID, Name: team + " cache", Kind: "cache", Platform: "redis", OwnerTeam: team, OperationCount: 8},
			&archgraph.ResourceNode{ID: "queue:" + team, GraphID: queueID, Name: team + ".events", Kind: "queue_topic_stream", Platform: "kafka", OwnerTeam: team, OperationCount: 6},
		)
		addEdge(service(4), dbID, "database", "read/write")
		addEdge(service(3), cacheID, "cache", "read/write")
		addEdge(service(2), queueID, "queue_publish", "publish")
		addEdge(queueID, service(1), "queue_consume", "consume")

		nextTeam := teamNames[(teamIndex+1)%teams]
		addEdge(service(5), nextTeam+"-"+serviceRoles[0], "rpc", "cross-team RPC")
	}
	graph.ExternalNodes = append(graph.ExternalNodes, &archgraph.ExternalNode{Name: "public-payment-network", Kind: "saas"})
	for teamIndex := 0; teamIndex < teams; teamIndex++ {
		team := teamNames[teamIndex]
		addEdge(team+"-"+serviceRoles[servicesPerTeam-1], "public-payment-network", "http", "HTTPS")
	}
}

func countServiceEdges(graph *archgraph.ArchGraph) int {
	services := make(map[string]bool, len(graph.Services))
	for _, service := range graph.Services {
		services[service.Name] = true
	}
	count := 0
	for _, edge := range graph.Edges {
		if services[edge.From] && services[edge.To] {
			count++
		}
	}
	return count
}

func componentType(index int) string {
	switch serviceRoles[index] {
	case "api", "gateway", "admin":
		return "http-api"
	case "events", "worker", "sync", "exporter":
		return "event-worker"
	case "scheduler", "monitor":
		return "scheduled-worker"
	default:
		return "application-service"
	}
}

func syntheticRunID(teamIndex, serviceIndex int) string {
	return fmt.Sprintf("synthetic-%02d-%02d", teamIndex+1, serviceIndex+1)
}

func syntheticSHA(seed int64, teamIndex, serviceIndex int) string {
	return fmt.Sprintf("%040x", uint64(seed)+uint64(teamIndex*1000+serviceIndex))
}

func writeSyntheticSource(dir, name, team, language string, loc, files int, seed int64) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := map[string]any{
		"schema": "diffmind.synthetic-service.v1", "name": name, "team": team,
		"language": language, "generated_metrics": map[string]int{"lines_of_code": loc, "files": files},
		"seed": seed, "notice": "Synthetic scale fixture; values are generated and are not runtime telemetry.",
	}
	return writeJSON(filepath.Join(dir, "service.json"), body)
}

func writeAnalyzerManifest(home, runID, repoPath, name, team, sha, language string, loc, files int, generatedAt time.Time) error {
	body := map[string]any{
		"run_id": runID, "started_at": generatedAt, "finished_at": generatedAt.Add(time.Second),
		"repo_path": repoPath, "service_id": name, "service_name": name, "team": team,
		"repo_git_sha": sha, "repo_git_branch": "main", "diffmind_version": "synthetic-fixture",
		"schema_version": "diffmind.service.v1", "pipeline": "synthetic-scale",
		"counts":       map[string]int{"connections": 0, "dependencies": 0, "exposures": 0, "unresolved": 0},
		"repo_metrics": map[string]any{"total_loc": loc, "file_count": files, "languages": []map[string]any{{"language": language, "files": files, "loc": loc}}},
	}
	return writeJSON(filepath.Join(home, "runs", runID, "run_manifest.json"), body)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
