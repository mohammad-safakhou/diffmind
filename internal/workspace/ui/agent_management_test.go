package ui

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/agentapi"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// A newly added browser mutation cannot silently remain unavailable to agents.
func TestManagementCatalogCoversMutations(t *testing.T) {
	source, e := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if e != nil {
		t.Fatal(e)
	}
	catalog := map[string]bool{}
	for _, op := range agentapi.Operations {
		catalog[op.Method+" "+op.Path] = true
	}
	ast.Inspect(source, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "HandleFunc" && selector.Sel.Name != "Handle") {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			return true
		}
		route, _ := strconv.Unquote(literal.Value)
		if strings.HasPrefix(route, "GET ") || !strings.Contains(route, " /api/") || strings.HasSuffix(route, "/webhooks/github") {
			return true
		}
		if !catalog[route] {
			t.Errorf("mutation missing from agent catalog: %s", route)
		}
		return true
	})
}
func TestAgentManagementHTTPPermissionsAndAudit(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(strconv.FormatBool(scoped), func(t *testing.T) {
			s := newAuthTestServer(t)
			s.SetAuthToken("admin-secret")
			s.SetTrustedProxySecret("proxy")
			if scoped {
				if err := s.ConfigureProjectAccess("scoped"); err != nil {
					t.Fatal(err)
				}
			}
			hs := httptest.NewServer(s.Handler())
			defer hs.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			rt := &scopedIdentityTransport{}
			rt.identity.Store(Identity{User: "admin", Role: RoleAdmin})
			client := mcp.NewClient(&mcp.Implementation{Name: "management-test", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: hs.URL + "/mcp", HTTPClient: &http.Client{Transport: rt}, DisableStandaloneSSE: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			call := func(tool string, in agentapi.Input, want int) map[string]any {
				t.Helper()
				result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: in})
				if e != nil {
					t.Fatal(e)
				}
				b, _ := json.Marshal(result.StructuredContent)
				var out struct {
					Status int
					Data   map[string]any
				}
				if e = json.Unmarshal(b, &out); e != nil {
					t.Fatalf("%s %v", b, e)
				}
				if out.Status != want || result.IsError != (want >= 400) {
					t.Fatalf("result=%+v body=%s want %d", result, b, want)
				}
				return out.Data
			}
			p := call("manage_workspace", agentapi.Input{Operation: "create_project", Body: map[string]any{"name": "Agent created"}}, 201)
			pid := p["id"].(string)
			packOp, _ := agentapi.Find("create_project_pack")
			createdPack := call("manage_workspace", agentapi.Input{Operation: "create_project_pack", Selectors: map[string]string{"pid": pid}, Body: packOp.BodyExample.(map[string]any)}, 201)
			packSelectors := map[string]string{"pid": pid, "pack_id": createdPack["id"].(string)}
			packBody := call("inspect_workspace", agentapi.Input{Operation: "get_project_pack", Selectors: packSelectors}, 200)
			packBody["name"] = "Updated conventions"
			call("manage_workspace", agentapi.Input{Operation: "update_project_pack", Selectors: packSelectors, Body: packBody}, 200)
			updatedPack := call("inspect_workspace", agentapi.Input{Operation: "get_project_pack", Selectors: packSelectors}, 200)
			if updatedPack["id"] != "example.conventions" || updatedPack["name"] != "Updated conventions" {
				t.Fatalf("pack identity or update lost: %+v", updatedPack)
			}
			packBody["id"] = "different.identity"
			call("manage_workspace", agentapi.Input{Operation: "update_project_pack", Selectors: packSelectors, Body: packBody}, 422)
			packBody["id"] = "example.conventions"
			call("manage_workspace", agentapi.Input{Operation: "delete_project_pack", Selectors: packSelectors, Confirm: "delete_project_pack"}, 200)
			call("manage_workspace", agentapi.Input{Operation: "update_project_pack", Selectors: packSelectors, Body: packBody}, 404)
			call("inspect_workspace", agentapi.Input{Operation: "get_project_pack", Selectors: packSelectors}, 404)
			call("inspect_workspace", agentapi.Input{Operation: "get_project", Selectors: map[string]string{"pid": pid}}, 200)
			// Same stateful MCP session loses admin authority on its NEXT request.
			rt.identity.Store(Identity{User: "viewer", Role: RoleViewer})
			result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "manage_workspace", Arguments: agentapi.Input{Operation: "create_project", Body: map[string]any{"name": "Forbidden"}}})
			if e == nil && !result.IsError {
				t.Fatal("viewer inherited admin MCP mutation")
			}
			rt.identity.Store(Identity{User: "editor", Role: RoleEditor})
			want := 200
			if scoped {
				want = 404
			}
			call("manage_workspace", agentapi.Input{Operation: "update_project", Selectors: map[string]string{"pid": pid}, Body: map[string]any{"instruction": "editor mutation"}}, want)
			rt.identity.Store(Identity{User: "admin", Role: RoleAdmin})
			if scoped {
				call("manage_workspace", agentapi.Input{Operation: "set_access", Selectors: map[string]string{"pid": pid}, Body: map[string]any{"revision": 0, "members": map[string]string{"editor": "editor"}}}, 200)
				rt.identity.Store(Identity{User: "editor", Role: RoleEditor})
				call("manage_workspace", agentapi.Input{Operation: "update_project", Selectors: map[string]string{"pid": pid}, Body: map[string]any{"instruction": "no host config"}}, 403)
				rt.identity.Store(Identity{User: "admin", Role: RoleAdmin})
			}
			call("manage_workspace", agentapi.Input{Operation: "delete_project", Selectors: map[string]string{"pid": pid}, Confirm: "delete_project"}, 200)
			audit, e := os.ReadFile(s.auditLogPath)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(audit), pid) || strings.Contains(string(audit), "admin-secret") || strings.Contains(string(audit), "proxy-secret") {
				t.Fatalf("bad audit: %s", audit)
			}
			ps, _ := s.store.ListProjects()
			if len(ps) != 0 {
				t.Fatal("unexpected project left by rejected mutation")
			}
		})
	}
}
func TestManagementAdapterRejectsMissingTransportContext(t *testing.T) {
	s := newAuthTestServer(t)
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader("{}"))
	if _, e := s.invokeAgentOperation(context.Background(), nil, req); e == nil {
		t.Fatal("missing context accepted")
	}
}

// Every finite management mutation passes the same write authorization gate,
// including entries added later. Tool annotations are not the security boundary.
func TestAllAgentMutationsRejectViewer(t *testing.T) {
	s := newAuthTestServer(t)
	s.SetTrustedProxySecret("proxy")
	for _, op := range agentapi.Operations {
		if op.Method == "GET" {
			continue
		}
		in := agentapi.Input{Operation: op.Name, Confirm: op.Name, Selectors: map[string]string{}, Body: map[string]any{}}
		for _, part := range strings.Split(op.Path, "/") {
			if strings.HasPrefix(part, "{") {
				in.Selectors[strings.Trim(part, "{}")] = "target"
			}
		}
		req, e := agentapi.Request(context.Background(), in, false)
		if e != nil {
			t.Fatal(e)
		}
		headers := make(http.Header)
		headers.Set(proxySecretHeader, "proxy")
		headers.Set(proxyUserHeader, "viewer")
		headers.Set(proxyRoleHeader, "viewer")
		result, e := s.invokeAgentOperation(context.Background(), &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: headers}}, req)
		if e != nil || result.Status != 403 {
			t.Fatalf("%s: %+v %v", op.Name, result, e)
		}
	}
}

func TestAgentShutdownDrainsActiveIngestion(t *testing.T) {
	s := newAuthTestServer(t)
	tmp := t.TempDir()
	ready := filepath.Join(tmp, "ready")
	binary := filepath.Join(tmp, "analyzer")
	script := "#!/bin/sh\ntrap 'exit 0' TERM INT\nprintf ready > '" + ready + "'\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIFFMIND_BINARY", binary)
	p, err := s.store.CreateProject(store.Project{Name: "Shutdown"})
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "source")
	os.Mkdir(repo, 0700)
	os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0600)
	if _, err = s.store.CreateRepo(p.ID, store.Repo{Name: "service", Path: repo, Kind: "service_repo", SourceType: "local"}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.StartListener(ctx, listener) }()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, e := client.Get("http://" + listener.Addr().String() + "/healthz")
		if e == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	req, _ := http.NewRequest("POST", "http://"+listener.Addr().String()+"/api/projects/"+p.ID+"/ingestion", strings.NewReader("{}"))
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 202 {
		t.Fatal(res.StatusCode)
	}
	for {
		if _, e := os.Stat(ready); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("analyzer did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown failed to drain ingestion")
	}
	record, err := s.store.GetIngestion(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status == "running" {
		t.Fatalf("shutdown returned before durable ingestion terminal state: %+v", record)
	}
	s.repositoryMu.Lock()
	active := s.repositoryTotal
	s.repositoryMu.Unlock()
	if active != 0 {
		t.Fatal("repository subprocess still active")
	}
	if _, err = s.runs.Start(p.ID, []store.RunRepoRef{{RepoID: "anything"}}, nil); err == nil {
		t.Fatal("graph admission remained open")
	}
}
