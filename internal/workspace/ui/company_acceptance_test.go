package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/backup"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

// This deliberately builds and invokes the real CLI. No analyzer or graph
// implementation is mocked, and the fixtures need no network or framework installs.
func TestCompanyAcceptance(t *testing.T) {
	testCompanyAcceptance(t, false)
}
func TestCompanyAcceptanceSQLite(t *testing.T) {
	testCompanyAcceptance(t, true)
}
func testCompanyAcceptance(t *testing.T, sqlite bool) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "diffmind")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if supplied := os.Getenv("DIFFMIND_ACCEPTANCE_BINARY"); supplied != "" {
		info, err := os.Stat(supplied)
		if !filepath.IsAbs(supplied) || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			t.Fatal("DIFFMIND_ACCEPTANCE_BINARY must be an absolute executable file")
		}
		binary = supplied
	} else {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/diffmind")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build CLI: %v\n%s", err, out)
		}
	}
	home := t.TempDir()
	t.Setenv("DIFFMIND_HOME", home)
	t.Setenv("DIFFMIND_BINARY", binary)
	if sqlite {
		release, err := homelock.AcquireServer(home)
		if err != nil {
			t.Fatal(err)
		}
		probeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		probe := exec.CommandContext(probeCtx, binary, "ui", "--no-spa-rebuild")
		out, probeErr := probe.CombinedOutput()
		stop()
		release()
		if probeErr == nil || !strings.Contains(string(out), "another Diffmind server owns") {
			t.Fatalf("CLI single-server guard: %v %s", probeErr, out)
		}
	}
	st, err := store.New(home)
	if err != nil {
		t.Fatal(err)
	}
	if sqlite {
		migration := exec.CommandContext(ctx, binary, "storage", "migrate", "--offline", "--json")
		if out, err := migration.CombinedOutput(); err != nil || !strings.Contains(string(out), `"backend":"sqlite"`) {
			t.Fatalf("migration CLI: %v %s", err, out)
		}
	}
	runsDir := filepath.Join(home, "runs")
	log := util.NewLogger(util.LevelInfo)
	s := New(st, runmgr.New(st, log, runsDir), runsDir, "", 0, log)
	if err := s.ConfigureProjectAccess("scoped"); err != nil {
		t.Fatal(err)
	}
	s.refreshContext = ctx
	p, _ := st.CreateProject(store.Project{Name: "Synthetic company"})
	limitsBefore, err := s.putProjectLimits(p.ID, 0, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	sources := t.TempDir()
	fixture := filepath.Join(root, "testdata", "company")
	var expected struct {
		Services []string `json:"services"`
		Edges    []string `json:"service_edges"`
		External []string `json:"external_services"`
	}
	body, err := os.ReadFile(filepath.Join(fixture, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &expected); err != nil {
		t.Fatal(err)
	}
	for _, name := range expected.Services {
		dir := filepath.Join(sources, name)
		if err := os.CopyFS(dir, os.DirFS(filepath.Join(fixture, name))); err != nil {
			t.Fatal(err)
		}
		runGitForTest(t, dir, "init", "-b", "master")
		runGitForTest(t, dir, "config", "user.email", "developer@example.test")
		runGitForTest(t, dir, "config", "user.name", "Developer")
		runGitForTest(t, dir, "add", ".")
		runGitForTest(t, dir, "commit", "-m", "synthetic fixture")
	}
	request, _ := json.Marshal(ingestionRequest{Import: &importReposRequest{Provider: "local", Root: sources}, Concurrency: 2})
	start := func(body string, analyzed, reused int) *store.Ingestion {
		t.Helper()
		if w := ingestionPost(s, p.ID, "", body); w.Code != 202 {
			t.Fatalf("start: %d %s", w.Code, w.Body.String())
		}
		i := awaitIngestionIdle(t, s, p.ID)
		if i.Status != store.IngestionCompleted || i.Analyzed != analyzed || i.Reused != reused {
			t.Fatalf("ingestion: %+v", i)
		}
		return i
	}
	first := start(string(request), 3, 0)
	_, graph, err := s.query.Load(p.ID, first.GraphRunID)
	if err != nil {
		t.Fatal(err)
	}
	assertCompanyGraph(t, graph, expected.Services, expected.Edges, expected.External)
	deps, err := s.query.Dependencies(p.ID, first.GraphRunID, "gateway", "outbound")
	if err != nil || len(deps.Edges) < 2 {
		t.Fatalf("agent dependencies: %+v %v", deps, err)
	}
	firstRepos, _ := st.ListRepos(p.ID)
	for _, repo := range firstRepos {
		if repo.AnalysisFingerprint == "" || repo.AnalysisArtifactDigest == "" {
			t.Fatalf("missing checkpoint: %+v", repo)
		}
		doc, err := artifacts.ReadProtocol(filepath.Join(runsDir, repo.LastDiffMindRunID))
		if err != nil {
			t.Fatal(err)
		}
		evidence := map[string]bool{}
		for _, ev := range doc.Evidence {
			source, err := os.ReadFile(filepath.Join(repo.Path, ev.File))
			if err != nil || ev.StartLine < 1 || ev.EndLine < ev.StartLine || ev.EndLine > len(strings.Split(string(source), "\n")) {
				t.Fatalf("invalid source evidence: %+v %v", ev, err)
			}
			evidence[ev.ID] = true
		}
		for _, endpoint := range doc.Objects.HTTPEndpoints {
			if len(endpoint.EvidenceRefs) == 0 {
				t.Fatal("endpoint lacks evidence")
			}
			for _, ref := range endpoint.EvidenceRefs {
				if !evidence[ref] {
					t.Fatalf("dangling evidence %s", ref)
				}
			}
		}
		for _, call := range doc.Objects.HTTPCalls {
			if len(call.EvidenceRefs) == 0 {
				t.Fatal("HTTP call lacks evidence")
			}
			for _, ref := range call.EvidenceRefs {
				if !evidence[ref] {
					t.Fatalf("dangling evidence %s", ref)
				}
			}
		}
	}
	// Exercise the actual agent transport and HTTP query surface on this graph.
	agentToken, agentSecret, err := st.IssueProjectToken(p.ID, "company acceptance agent", "viewer", "local", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	agentHTTP := &http.Client{Transport: headerRoundTripper{headers: map[string]string{"Authorization": "Bearer " + agentSecret}, base: http.DefaultTransport}}
	response, err := agentHTTP.Get(httpServer.URL + "/api/v1/projects/" + p.ID + "/dependencies?service=gateway&direction=outbound")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("query HTTP status: %d", response.StatusCode)
	}
	response.Body.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "company-acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: agentHTTP, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	toolResult, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_dependencies", Arguments: map[string]any{"project": p.ID, "service": "gateway", "direction": "outbound"}})
	if err != nil || toolResult.IsError {
		t.Fatalf("MCP result: %+v %v", toolResult, err)
	}
	encoded, _ := json.Marshal(toolResult.StructuredContent)
	if !strings.Contains(string(encoded), "catalog") || !strings.Contains(string(encoded), "billing") {
		t.Fatalf("MCP lost relationships: %s", encoded)
	}
	second := start("{}", 0, 3)
	_, graph, err = s.query.Load(p.ID, second.GraphRunID)
	if err != nil {
		t.Fatal(err)
	}
	assertCompanyGraph(t, graph, expected.Services, expected.Edges, expected.External)
	if first.GraphRunID == second.GraphRunID {
		t.Fatal("graph refresh did not create a version")
	}
	reusedRepos, _ := st.ListRepos(p.ID)
	for i, repo := range firstRepos {
		if reusedRepos[i].LastDiffMindRunID != repo.LastDiffMindRunID {
			t.Fatal("unchanged repository was reanalyzed")
		}
	}
	// One new commit must invalidate only that repository.
	changed := filepath.Join(sources, "catalog", "app", "views.py")
	data, _ := os.ReadFile(changed)
	writeTestFile(t, changed, string(data)+"\n# a new revision\n")
	runGitForTest(t, filepath.Join(sources, "catalog"), "add", ".")
	runGitForTest(t, filepath.Join(sources, "catalog"), "commit", "-m", "change catalog")
	if err := s.StartOperations(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.StopOperations)
	job, _, err := s.enqueueRefresh(p.ID, "manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	finished := awaitRefreshJob(t, s, job.ID)
	if finished.Status != "succeeded" || len(finished.Attempts) != 1 || finished.Attempts[0].Analyzed != 1 || finished.Attempts[0].Reused != 2 {
		t.Fatalf("queued real-binary refresh: %+v", finished)
	}
	if _, _, err := s.query.Load(p.ID, finished.Attempts[0].GraphRunID); err != nil {
		t.Fatalf("queued graph: %v", err)
	}
	// Corrupted artifacts must never be a cache hit.
	current, _ := st.ListRepos(p.ID)
	writeTestFile(t, filepath.Join(runsDir, current[0].LastDiffMindRunID, "tampered.txt"), "changed")
	start("{}", 1, 2)
	// Force is an escape hatch and does not erase historical graph versions.
	start(`{"force":true}`, 3, 0)
	// One failed repository does not discard successful checkpoints. Retry only
	// reruns the failed work when all other inputs are still unchanged.
	brokenConfig := filepath.Join(sources, "billing", "diffmind-configuration.yaml")
	writeTestFile(t, brokenConfig, "service: [invalid yaml")
	if w := ingestionPost(s, p.ID, "", "{}"); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	partial := awaitIngestionIdle(t, s, p.ID)
	if partial.Status != store.IngestionPartial || partial.Reused != 2 || partial.Analyzed != 0 {
		t.Fatalf("partial retry checkpoint: %+v", partial)
	}
	if err := os.Remove(brokenConfig); err != nil {
		t.Fatal(err)
	}
	if w := ingestionPost(s, p.ID, "/resume", ""); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	retried := awaitIngestionIdle(t, s, p.ID)
	if retried.Status != store.IngestionCompleted || retried.Reused != 2 || retried.Analyzed != 1 {
		t.Fatalf("retry: %+v", retried)
	}
	if _, _, err := s.query.Load(p.ID, first.GraphRunID); err != nil {
		t.Fatalf("historical graph lost: %v", err)
	}
	// Disaster-recovery drill through the actual CLI, with every process stopped.
	// Restore to the original path so even absolute source/artifact paths retain
	// their meaning; the old directory remains untouched for comparison.
	session.Close()
	httpServer.Close()
	s.StopOperations()
	lease, err := homelock.Acquire(home, true)
	if err != nil {
		t.Fatal(err)
	}
	blocked := exec.CommandContext(ctx, binary, "doctor", "--json")
	blockedOutput, blockedErr := blocked.CombinedOutput()
	lease()
	if blockedErr == nil || !strings.Contains(string(blockedOutput), "workspace is in use") {
		t.Fatalf("CLI bypassed maintenance: %v %s", blockedErr, blockedOutput)
	}
	historyBefore, err := st.IngestionHistory(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	accessBefore, err := st.PutProjectAccess(p.ID, 0, map[string]string{"synthetic-reader": "viewer", "synthetic-operator": "editor"})
	if err != nil {
		t.Fatal(err)
	}
	revokedToken, revokedSecret, err := st.IssueProjectToken(p.ID, "former agent", "editor", "local", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RevokeProjectToken(p.ID, revokedToken.ID, "local"); err != nil {
		t.Fatal(err)
	}
	tokensBefore, err := st.ListProjectTokens(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "company-backup.tar.gz")
	backupCmd := exec.CommandContext(ctx, binary, "backup", "create", "--offline", "--output", archive, "--json")
	body, err = backupCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("backup CLI: %v %s", err, body)
	}
	var snapshot backup.Report
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal(err)
	}
	// Exercise actual CLI catalog retention with real graph/queue/token/limit
	// records. The standalone archive above is not owned by the catalog.
	catalog := filepath.Join(t.TempDir(), "managed")
	if err := os.Mkdir(catalog, 0700); err != nil {
		t.Fatal(err)
	}
	var rotation backup.RotationReport
	for iteration := 0; iteration < 2; iteration++ {
		cmd := exec.CommandContext(ctx, binary, "backup", "rotate", "--offline", "--directory", catalog, "--keep-last", "1", "--json")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rotate CLI: %v %s", err, out)
		}
		if err := json.Unmarshal(out, &rotation); err != nil || len(rotation.Retained) != 1 || len(rotation.Removed) != iteration {
			t.Fatalf("rotation %+v %v", rotation, err)
		}
	}
	if _, err := backup.Verify(archive, snapshot.SHA256, backup.DefaultMaxBytes); err != nil {
		t.Fatalf("standalone archive affected by retention: %v", err)
	}
	archive = rotation.Created.Archive
	snapshot = rotation.Created.Report
	if err := os.Rename(home, home+"-original"); err != nil {
		t.Fatal(err)
	}
	restoreCmd := exec.CommandContext(ctx, binary, "backup", "restore", "--offline", "--archive", archive, "--destination", home, "--sha256", snapshot.SHA256)
	if out, err := restoreCmd.CombinedOutput(); err != nil {
		t.Fatalf("restore CLI: %v %s", err, out)
	}
	restored, err := store.New(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.GraphRunID, finished.Attempts[0].GraphRunID, retried.GraphRunID} {
		_, g, err := query.New(restored).Load(p.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		assertCompanyGraph(t, g, expected.Services, expected.Edges, expected.External)
	}
	savedJob, err := restored.GetJob(job.ID)
	if err != nil || !reflect.DeepEqual(savedJob, finished) {
		t.Fatalf("restored attempts changed: %+v %v", savedJob, err)
	}
	historyAfter, err := restored.IngestionHistory(p.ID)
	if err != nil || !reflect.DeepEqual(historyBefore, historyAfter) {
		t.Fatalf("ingestion history changed: %v", err)
	}
	accessAfter, err := restored.GetProjectAccess(p.ID)
	if err != nil || !reflect.DeepEqual(accessBefore, accessAfter) {
		t.Fatalf("project grants, revision, or timestamp changed: %+v %v", accessAfter, err)
	}
	tokensAfter, err := restored.ListProjectTokens(p.ID)
	limitsAfter, limitsErr := restored.GetProjectLimits(p.ID)
	if limitsErr != nil || !reflect.DeepEqual(limitsBefore, limitsAfter) {
		t.Fatalf("project limits, revision, or timestamp changed: %+v %v", limitsAfter, limitsErr)
	}
	if err != nil || !reflect.DeepEqual(tokensBefore, tokensAfter) {
		t.Fatalf("token history/dates changed on restore: %v", err)
	}
	restoredToken, err := restored.AuthenticateProjectToken(agentSecret)
	if err != nil || !reflect.DeepEqual(agentToken, restoredToken) {
		t.Fatalf("active agent not restored: %v", err)
	}
	if _, err := restored.AuthenticateProjectToken(revokedSecret); err == nil {
		t.Fatal("backup resurrected a token revoked before the snapshot")
	}
	if sqlite {
		report, err := restored.VerifyQueue()
		if err != nil || report.Backend != "sqlite" || report.Jobs < 1 {
			t.Fatalf("restored SQLite queue: %+v %v", report, err)
		}
		verification := exec.CommandContext(ctx, binary, "storage", "verify", "--offline", "--json")
		if out, err := verification.CombinedOutput(); err != nil || !strings.Contains(string(out), `"backend":"sqlite"`) {
			t.Fatalf("restored queue verify CLI: %v %s", err, out)
		}
	}
}

func assertCompanyGraph(t *testing.T, graph *archgraph.ArchGraph, services, edges, external []string) {
	t.Helper()
	var gotServices, gotEdges, gotExternal []string
	known := map[string]bool{}
	for _, service := range graph.Services {
		gotServices = append(gotServices, service.Name)
		known[service.Name] = true
	}
	for _, edge := range graph.Edges {
		if known[edge.From] && known[edge.To] {
			gotEdges = append(gotEdges, edge.From+" -> "+edge.To)
		}
	}
	for _, node := range graph.ExternalNodes {
		gotExternal = append(gotExternal, strings.TrimPrefix(node.Name, "external."))
	}
	sort.Strings(gotServices)
	sort.Strings(gotEdges)
	sort.Strings(gotExternal)
	if !reflect.DeepEqual(gotServices, services) || !reflect.DeepEqual(gotEdges, edges) || !reflect.DeepEqual(gotExternal, external) {
		t.Fatalf("graph mismatch: services=%v edges=%v external=%v", gotServices, gotEdges, gotExternal)
	}
}
