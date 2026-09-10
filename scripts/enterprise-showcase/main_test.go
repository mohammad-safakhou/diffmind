package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
)

func TestCreateFixtureBuildsTenTeamCompany(t *testing.T) {
	home := t.TempDir()
	summary, err := createFixture(fixtureConfig{Home: home, Teams: 10, ServicesPerTeam: 15, Seed: defaultSeed})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Services != 150 || summary.Teams != 10 {
		t.Fatalf("unexpected scale: %+v", summary)
	}
	if summary.Resources != 30 || summary.Relationships != 230 {
		t.Fatalf("unexpected topology totals: %+v", summary)
	}
	if summary.EdgesByType["queue_publish"] != 10 || summary.EdgesByType["queue_consume"] != 10 {
		t.Fatalf("unexpected async edges: %#v", summary.EdgesByType)
	}

	projectRoot := filepath.Join(home, "projects", summary.ProjectID)
	entries, err := os.ReadDir(filepath.Join(projectRoot, "repos"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 150 {
		t.Fatalf("repo count = %d, want 150", len(entries))
	}
	runs, err := os.ReadDir(filepath.Join(projectRoot, "runs"))
	if err != nil || len(runs) != 1 {
		t.Fatalf("project runs = %d, err = %v", len(runs), err)
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, "runs", runs[0].Name(), "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var graph archgraph.ArchGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Services) != 150 || len(graph.ResourceNodes) != 30 || len(graph.Edges) != 230 {
		t.Fatalf("graph totals = %d services, %d resources, %d edges", len(graph.Services), len(graph.ResourceNodes), len(graph.Edges))
	}
	teamCounts := map[string]int{}
	for _, service := range graph.Services {
		teamCounts[service.Team]++
		if service.RepoMetrics == nil || service.RepoMetrics.TotalLOC == 0 {
			t.Fatalf("missing generated metrics for %s", service.Name)
		}
	}
	if len(teamCounts) != 10 {
		t.Fatalf("team count = %d, want 10", len(teamCounts))
	}
	for team, count := range teamCounts {
		if count != 15 {
			t.Fatalf("team %s has %d services, want 15", team, count)
		}
	}
}

func TestCreateFixtureRejectsNonEmptyDestination(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := createFixture(fixtureConfig{Home: home, Teams: 2, ServicesPerTeam: 2, Seed: 1}); err == nil {
		t.Fatal("expected non-empty destination to be rejected")
	}
}
