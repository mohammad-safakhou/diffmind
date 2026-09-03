package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/agentapi"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/agenthost"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// No browser, API helper, store mutation or pre-created project is used to
// operate the product. Every workspace action below travels through stdio MCP.
func TestAgentAcceptance(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	binary := os.Getenv("DIFFMIND_ACCEPTANCE_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "diffmind")
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/diffmind")
		build.Dir = root
		if b, e := build.CombinedOutput(); e != nil {
			t.Fatalf("build: %v %s", e, b)
		}
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("acceptance binary must be absolute")
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "work home")
	demo := filepath.Join(tmp, "demo")
	prepare := exec.CommandContext(ctx, "sh", filepath.Join(root, "scripts/prepare-demo.sh"), demo)
	if b, e := prepare.CombinedOutput(); e != nil {
		t.Fatalf("demo: %v %s", e, b)
	}
	env := []string{}
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if !strings.HasPrefix(k, "DIFFMIND_") && k != "GITHUB_TOKEN" && k != "GH_TOKEN" {
			env = append(env, e)
		}
	}
	env = append(env, "DIFFMIND_HOME="+home)
	var ownerCommand *exec.Cmd
	connect := func() *mcp.ClientSession {
		t.Helper()
		cmd := exec.Command(binary, "agent")
		ownerCommand = cmd
		cmd.Dir = tmp
		cmd.Env = env
		cmd.Stderr = os.Stderr
		client := mcp.NewClient(&mcp.Implementation{Name: "agent-acceptance", Version: "1"}, nil)
		session, e := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 25 * time.Second}, nil)
		if e != nil {
			t.Fatal(e)
		}
		return session
	}
	session := connect()
	defer func() {
		if session != nil {
			session.Close()
		}
	}()
	call := func(name string, args any, out any) {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil || result.IsError {
			t.Fatalf("%s: %v %+v", name, e, result)
		}
		if out != nil {
			b, _ := json.Marshal(result.StructuredContent)
			if e = json.Unmarshal(b, out); e != nil {
				t.Fatalf("%s: %s %v", name, b, e)
			}
		}
	}
	op := func(name string, selectors map[string]string, body map[string]any, out any) {
		t.Helper()
		info, _ := agentapi.Find(name)
		tool := "manage_workspace"
		if info.Method == "GET" {
			tool = "inspect_workspace"
		}
		var result struct {
			Status int
			Data   json.RawMessage
		}
		call(tool, agentapi.Input{Operation: name, Selectors: selectors, Body: body}, &result)
		if result.Status < 200 || result.Status > 299 {
			t.Fatalf("%s HTTP %d: %s", name, result.Status, result.Data)
		}
		if out != nil {
			if e := json.Unmarshal(result.Data, out); e != nil {
				t.Fatalf("%s decode: %v %s", name, e, result.Data)
			}
		}
	}
	listed, e := session.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(listed.Tools) != 16 {
		t.Fatalf("tools=%d want 16", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if tool.Name == "manage_workspace" && tool.Annotations.ReadOnlyHint {
			t.Fatal("mutations advertised as read-only")
		}
	}
	var initial struct{ Projects []any }
	call("list_projects", map[string]any{}, &initial)
	if len(initial.Projects) != 0 {
		t.Fatal("home not empty")
	}
	var runtime agenthost.Status
	call("agent_runtime", map[string]any{"action": "status"}, &runtime)
	if !runtime.Running || runtime.DashboardURL == "" {
		t.Fatal(runtime)
	}
	var p store.Project
	op("create_project", nil, map[string]any{"name": "Agent managed work"}, &p)
	sel := map[string]string{"pid": p.ID}
	var preview struct{ Count int }
	op("import_repositories", sel, map[string]any{"provider": "local", "root": filepath.Join(demo, "repositories"), "dry_run": true}, &preview)
	if preview.Count != 3 {
		t.Fatalf("preview: %+v", preview)
	}
	var repos struct{ Repos []store.Repo }
	op("list_repositories", sel, nil, &repos)
	if len(repos.Repos) != 0 {
		t.Fatal("preview mutated")
	}
	var ingestion store.Ingestion
	op("start_ingestion", sel, map[string]any{"import": map[string]any{"provider": "local", "root": filepath.Join(demo, "repositories")}, "concurrency": 2}, &ingestion)
	await := func(analyzed, reused int) string {
		t.Helper()
		deadline := time.Now().Add(50 * time.Second)
		for time.Now().Before(deadline) {
			op("get_ingestion", sel, nil, &ingestion)
			if ingestion.Status != "running" {
				if ingestion.Status != "completed" || ingestion.Analyzed != analyzed || ingestion.Reused != reused {
					t.Fatalf("ingestion %+v", ingestion)
				}
				return ingestion.GraphRunID
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("ingestion timeout")
		return ""
	}
	first := await(3, 0)
	var deps query.DependencyResult
	call("get_dependencies", map[string]any{"project": p.ID, "service": "gateway", "direction": "outbound"}, &deps)
	edges := []string{}
	for _, edge := range deps.Edges {
		if edge.From == "gateway" && (edge.To == "catalog" || edge.To == "billing") {
			edges = append(edges, edge.From+" -> "+edge.To)
		}
	}
	sort.Strings(edges)
	if !reflect.DeepEqual(edges, []string{"gateway -> billing", "gateway -> catalog"}) {
		t.Fatalf("wrong edges %+v", deps.Edges)
	}
	var summary map[string]any
	call("get_graph_summary", map[string]any{"project": p.ID}, &summary)
	op("start_ingestion", sel, map[string]any{}, nil)
	second := await(0, 3)
	if first == second {
		t.Fatal("refresh did not version graph")
	}
	call("compare_graphs", map[string]any{"project": p.ID, "from": first, "to": second}, nil)
	// Agent authors/validates/installs a tested pack; no shell is offered as MCP.
	pack := filepath.Join(tmp, "agent-pack")
	for _, args := range [][]string{{"pack", "init", pack, "--id", "example.agent"}, {"pack", "lint", pack}, {"pack", "test", pack}, {"pack", "install", pack}} {
		var result agenthost.CommandResult
		call("agent_command", map[string]any{"args": args}, &result)
		if result.ExitCode != 0 || !result.BackendRestarted {
			t.Fatalf("pack %v: %+v", args, result)
		}
	}
	// A failed maintenance command still restores service availability.
	failed, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "agent_command", Arguments: map[string]any{"args": []string{"backup", "verify", "--archive", filepath.Join(tmp, "missing.tar.gz")}}})
	if e != nil || !failed.IsError {
		t.Fatalf("expected handled command failure %+v %v", failed, e)
	}
	call("agent_runtime", map[string]any{"action": "status"}, &runtime)
	if !runtime.Running {
		t.Fatal("failed command left backend stopped")
	}
	// Offline backup and SQLite migration are orchestrated by the agent itself.
	archive := filepath.Join(tmp, "workspace.tar.gz")
	call("agent_command", map[string]any{"args": []string{"backup", "create", "--offline", "--output", archive, "--json"}}, nil)
	call("agent_command", map[string]any{"args": []string{"backup", "verify", "--archive", archive, "--json"}}, nil)
	call("agent_command", map[string]any{"args": []string{"storage", "migrate", "--offline", "--json"}, "confirm": "storage migrate"}, nil)
	call("agent_command", map[string]any{"args": []string{"storage", "verify", "--offline", "--json"}}, nil)
	// Agent configures a persistent schedule without editing configuration files.
	settings := agenthost.DefaultSettings()
	settings.RefreshInterval = "1h"
	settings.ProjectAccess = "scoped"
	call("agent_runtime", map[string]any{"action": "configure", "settings": settings}, &runtime)
	if runtime.Settings.RefreshInterval != "1h" {
		t.Fatal(runtime)
	}
	// Full local authority can enable scoped mode and administer an agent grant
	// without a settings-file edit or browser interaction.
	var issued struct {
		Secret string
		Token  struct{ ID string }
	}
	op("issue_token", sel, map[string]any{"name": "Synthetic viewer", "role": "viewer", "expires_in_seconds": 3600}, &issued)
	if issued.Secret == "" || issued.Token.ID == "" {
		t.Fatal("token issuance returned no grant")
	}
	request, _ := http.NewRequest("GET", runtime.DashboardURL+"/api/v1/projects", nil)
	request.Header.Set("Authorization", "Bearer "+issued.Secret)
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("issued viewer token cannot query")
	}
	call("manage_workspace", agentapi.Input{Operation: "revoke_token", Selectors: map[string]string{"pid": p.ID, "tid": issued.Token.ID}, Confirm: "revoke_token"}, nil)
	response, err = (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("revoked token still accepted")
	}
	oldURL := runtime.DashboardURL
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	session = nil
	httpClient := &http.Client{Timeout: time.Second}
	if res, e := httpClient.Get(oldURL + "/healthz"); e == nil {
		res.Body.Close()
		t.Fatal("backend survived normal owner disconnect")
	}
	session = connect()
	call("agent_runtime", map[string]any{"action": "status"}, &runtime)
	if runtime.Settings.RefreshInterval != "1h" {
		t.Fatal("schedule not persisted")
	}
	call("get_dependencies", map[string]any{"project": p.ID, "service": "gateway", "run": first}, &deps)
	if len(deps.Edges) < 2 {
		t.Fatal("historical graph lost")
	}
	// Requests to the wrong home cannot be hidden by a PATH-resolved old binary.
	if runtime.Home != home {
		t.Fatalf("wrong home: %s", runtime.Home)
	}
	// SIGKILL cannot run a parent defer. The inherited lifetime pipe must still
	// stop its backend and release the singleton lock without manual cleanup.
	if err := ownerCommand.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	session = nil
	deadline := time.Now().Add(20 * time.Second)
	released := false
	for time.Now().Before(deadline) {
		if release, e := homelock.AcquireServer(home); e == nil {
			release()
			released = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !released {
		t.Fatal("backend orphaned after controller crash")
	}
	session = connect()
	op("get_project", sel, nil, &p)
	t.Log(fmt.Sprintf("agent-only onboarding, real graph, incremental reuse, pack install, recovery, backup, SQLite, schedule and reconnect verified for %s", p.ID))
}
