package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

// Exercises pack installation, the production graph job, persisted UI/HTTP/MCP
// queries, disable/rebuild, and historical isolation. The prior company fixture
// separately covers the real analyzer binary; here empty analyzer artifacts
// ensure every observed relationship actually comes from the installed pack.
func TestPackGraphAcceptance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIFFMIND_HOME", home)
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	packRoot := filepath.Join(root, "packs", "service-manifest")
	locked, err := knowledge.Install(knowledge.InstallOptions{Home: home, Source: packRoot})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(home)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(store.Project{Name: "Pack acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	runsDir := filepath.Join(home, "runs")
	log := util.NewLogger(util.LevelInfo)
	manager := runmgr.New(st, log, runsDir)
	s := New(st, manager, runsDir, "", 0, log)
	var refs []store.RunRepoRef
	sources := t.TempDir()
	for _, name := range []string{"gateway", "catalog", "billing"} {
		repoPath := filepath.Join(sources, name)
		if err := os.CopyFS(repoPath, os.DirFS(filepath.Join(packRoot, "testdata", name))); err != nil {
			t.Fatal(err)
		}
		repo, err := st.CreateRepo(p.ID, store.Repo{Name: name, Path: repoPath, Kind: "service_repo"})
		if err != nil {
			t.Fatal(err)
		}
		runID := name + "-run"
		dir := filepath.Join(runsDir, runID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(model.RunManifest{RunID: runID, RepoPath: repoPath, StartedAt: time.Now().UTC()})
		if err := os.WriteFile(filepath.Join(dir, "run_manifest.json"), body, 0o644); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, store.RunRepoRef{RepoID: repo.ID, DiffMindRunID: runID})
	}
	start := func(status string) *store.RunManifest {
		t.Helper()
		run, err := manager.Start(p.ID, refs, nil)
		if err != nil {
			t.Fatal(err)
		}
		manager.WaitFor(p.ID, run.ID)
		got, err := st.GetRun(p.ID, run.ID)
		if err != nil || got.Status != status {
			t.Fatalf("run: %+v %v", got, err)
		}
		return got
	}
	first := start(store.RunCompleted)
	if first.ServiceCount != 3 || first.EdgeCount != 5 || first.Options["knowledge_pack_set_digest"] == "" {
		t.Fatalf("wrong persisted counts: %+v", first)
	}
	graphPath := filepath.Join(st.RunDir(p.ID, first.ID), "graph.json")
	before, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "knowledge_pack") || !strings.Contains(string(before), "source_locations") {
		t.Fatalf("pack evidence missing: %s", before)
	}

	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/api/v1/projects/" + p.ID + "/dependencies?service=gateway&direction=outbound")
	if err != nil {
		t.Fatal(err)
	}
	var deps query.DependencyResult
	err = json.NewDecoder(response.Body).Decode(&deps)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || len(deps.Edges) != 4 {
		t.Fatalf("HTTP dependencies: %+v %v", deps, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "pack-acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_dependencies", Arguments: map[string]any{"project": p.ID, "service": "gateway", "direction": "outbound"}})
	if err != nil || result.IsError {
		t.Fatalf("MCP: %+v %v", result, err)
	}
	body, _ := json.Marshal(result.StructuredContent)
	var mcpDeps query.DependencyResult
	if err := json.Unmarshal(body, &mcpDeps); err != nil {
		t.Fatal(err)
	}
	if len(mcpDeps.Edges) != len(deps.Edges) || !strings.Contains(string(body), "RPC service mesh names") {
		t.Fatalf("MCP lost pack resolution: %s", body)
	}
	for _, edge := range mcpDeps.Edges {
		if len(edge.Details) == 0 || edge.Details[0].Details["evidence"] == nil {
			t.Fatalf("MCP lost evidence: %+v", edge)
		}
		if edge.Confidence != 0.9 {
			t.Fatalf("configured relationship confidence lost: %+v", edge)
		}
	}

	// A lock mismatch fails the new run; the last usable graph stays available.
	if err := os.WriteFile(filepath.Join(locked.Path, "tampered.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	failed := start(store.RunFailed)
	if !strings.Contains(failed.Error, "digest") {
		t.Fatalf("missing integrity error: %s", failed.Error)
	}
	latest, _, err := s.query.Load(p.ID, "")
	if err != nil || latest.ID != first.ID {
		t.Fatalf("failed job replaced valid graph: %+v %v", latest, err)
	}
	if err := knowledge.SetEnabled(home, locked.ID, false); err != nil {
		t.Fatal(err)
	}
	second := start(store.RunCompleted)
	if second.EdgeCount != 0 {
		t.Fatalf("disabled pack still produces edges: %+v", second)
	}
	comparison, err := s.query.CompareGraphs(ctx, p.ID, first.ID, second.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	removedRelationships := 0
	for _, change := range comparison.Changes {
		if change.Kind == "relationship" && change.Change == "removed" {
			removedRelationships++
		}
	}
	if removedRelationships != 5 || comparison.From.PackSetDigest == comparison.To.PackSetDigest {
		t.Fatalf("pack graph changes lost: %+v", comparison)
	}
	_, old, err := s.query.Load(p.ID, first.ID)
	if err != nil || len(old.Edges) != 5 {
		t.Fatalf("history changed: %+v %v", old, err)
	}
	after, _ := os.ReadFile(graphPath)
	if string(after) != string(before) {
		t.Fatal("historical graph was rewritten")
	}

	// Restore by reinstalling from the immutable source, then prove malformed
	// selected input fails rather than publishing an incomplete graph.
	if _, err := knowledge.Install(knowledge.InstallOptions{Home: home, Source: packRoot}); err != nil {
		t.Fatal(err)
	}
	if err := knowledge.SetEnabled(home, locked.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sources, "gateway", "service-architecture.yaml"), []byte("dependencies: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	failed = start(store.RunFailed)
	if !strings.Contains(failed.Error, "invalid YAML/JSON") {
		t.Fatalf("unexpected error: %s", failed.Error)
	}
}
