package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func accessFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := newAuthTestServer(t)
	s.SetTrustedProxySecret("proxy")
	s.SetAuthToken("recovery")
	if err := s.ConfigureProjectAccess("scoped"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"visible", "private"} {
		p, err := s.store.CreateProject(store.Project{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		user := "alice"
		if name == "private" {
			user = "bob"
		}
		if _, err := s.store.PutProjectAccess(p.ID, 0, map[string]string{user: "editor"}); err != nil {
			t.Fatal(err)
		}
		run, err := s.store.CreateRun(p.ID, store.RunManifest{Status: store.RunCompleted})
		if err != nil {
			t.Fatal(err)
		}
		graph := archgraph.ArchGraph{RunID: run.ID, Services: []*archgraph.ServiceNode{{Name: name + "-service", Known: true}}}
		body, _ := json.Marshal(graph)
		if err := os.WriteFile(filepath.Join(s.store.RunDir(p.ID, run.ID), "graph.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.enqueueRefresh(p.ID, "manual", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	return s, "visible", "private"
}
func accessRequest(h http.Handler, user, role, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(proxySecretHeader, "proxy")
	r.Header.Set(proxyUserHeader, user)
	r.Header.Set(proxyRoleHeader, role)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestScopedHTTPDiscoveryRolesAndPolicyChanges(t *testing.T) {
	s, a, b := accessFixture(t)
	h := s.Handler()
	for _, path := range []string{"/api/projects", "/api/v1/projects", "/api/v1/jobs?limit=1"} {
		w := accessRequest(h, "alice", "editor", "GET", path, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), b) || !strings.Contains(w.Body.String(), a) {
			t.Fatalf("discovery %s: %d %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(path, "jobs") && !strings.Contains(w.Body.String(), `"total": 1`) {
			t.Fatal(w.Body.String())
		}
	}
	for _, tc := range []struct {
		role, method, path, body string
		status                   int
	}{
		{"viewer", "GET", "/api/projects/" + a, "", 200},
		{"editor", "GET", "/api/projects/" + b, "", 404},
		{"editor", "GET", "/api/v1/jobs?project=" + b, "", 404},
		{"editor", "GET", "/api/v1/projects/" + a + "/access", "", 403},
		{"editor", "GET", "/api/projects/" + a + "/repo-suggestions", "", 403},
		{"editor", "GET", "/api/diffmind-runs?repo_path=/private", "", 403},
		{"editor", "GET", "/api/v1/refresh/status", "", 403},
		{"editor", "GET", "/metrics", "", 403},
		{"viewer", "POST", "/api/v1/projects/" + a + "/refresh-jobs", "{}", 403},
		{"editor", "POST", "/api/v1/projects/" + a + "/refresh-jobs", "{}", 202},
		{"editor", "POST", "/api/projects", "{\"name\":\"attack\"}", 403},
		{"editor", "PATCH", "/api/projects/" + a, `{"search_roots":["/private"]}`, 403},
		{"editor", "POST", "/api/projects/" + a + "/repos", `{"path":"/private","name":"secret"}`, 403},
		{"editor", "POST", "/api/projects/" + a + "/ingestion", `{"import":{"provider":"local","root":"/private"}}`, 403},
		{"editor", "POST", "/api/projects/" + a + "/runs", `{"repos":[{"repo_id":"../../private","diffmind_run_id":"private"}]}`, 403},
		{"editor", "PUT", "/api/v1/projects/" + a + "/access", `{"revision":1,"members":{"alice":"editor","mallory":"editor"}}`, 403},
		{"admin", "PUT", "/api/v1/projects/" + a + "/access", `{"revision":1,"members":{"alice":"viewer"}}`, 200},
		{"editor", "POST", "/api/v1/projects/" + a + "/refresh-jobs", "{}", 403},
		{"admin", "PUT", "/api/v1/projects/" + a + "/access", `{"revision":1,"members":{}}`, 409},
		{"admin", "PUT", "/api/v1/projects/" + a + "/access", `{"revision":2,"members":{}}`, 200},
		{"editor", "GET", "/api/projects/" + a, "", 404},
		{"admin", "GET", "/api/projects/" + a, "", 200},
	} {
		w := accessRequest(h, "alice", tc.role, tc.method, tc.path, tc.body)
		if w.Code != tc.status {
			t.Errorf("%s %s (%s): %d %s", tc.method, tc.path, tc.role, w.Code, w.Body.String())
		}
	}
	// A revoked subject has no jobs or projects, not a forced create prompt.
	w := accessRequest(h, "alice", "editor", "GET", "/api/v1/jobs", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total": 0`) {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest("GET", "/api/projects/"+b, nil)
	r.Header.Set("Authorization", "Bearer recovery")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("recovery admin denied")
	}
	audit, err := os.ReadFile(s.auditLogPath)
	if err != nil || !strings.Contains(string(audit), `/access`) || !strings.Contains(string(audit), `"status":403`) || !strings.Contains(string(audit), `"status":200`) {
		t.Fatalf("policy audit %v %s", err, audit)
	}
}

func TestAllProjectRoutesDenyNonmembers(t *testing.T) {
	s, _, hidden := accessFixture(t)
	h := s.Handler()
	assertAllProjectRoutesDenyNonmembers(t, s, hidden, h)
}

func assertAllProjectRoutesDenyNonmembers(t *testing.T, s *Server, hidden string, h http.Handler) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.Contains(pattern, "{pid}") {
			return true
		}
		method, path, _ := strings.Cut(pattern, " ")
		path = strings.ReplaceAll(path, "{pid}", hidden)
		for strings.Contains(path, "{") {
			start := strings.Index(path, "{")
			end := strings.Index(path, "}")
			path = path[:start] + "record" + path[end+1:]
		}
		w := accessRequest(h, "alice", "editor", method, path, "{}")
		if w.Code != 403 && w.Code != 404 {
			t.Errorf("route leaked %s: %d %s", pattern, w.Code, w.Body.String())
		}
		count++
		return true
	})
	if count < 40 {
		t.Fatalf("incomplete route coverage: %d", count)
	}
	jobs, _ := s.store.ListJobs(hidden)
	for _, action := range []string{"cancel", "retry"} {
		w := accessRequest(h, "alice", "editor", "POST", "/api/v1/jobs/"+jobs[0].ID+"/"+action, "")
		if w.Code != 404 {
			t.Fatalf("foreign job %d %s", w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/projects/%2e%2e%2fprivate", "/api/projects/visible/runs/%2e%2e%2fprivate", "/api/v1/projects/visible/services?run=../../private", "/api/v1/projects/visible/graph/compare?from=../../private&to=valid"} {
		w := accessRequest(h, "alice", "editor", "GET", path, "")
		if w.Code != 404 {
			t.Errorf("traversal %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestPolicyCorruptionAndLegacyCompatibility(t *testing.T) {
	s, a, b := accessFixture(t)
	if err := os.WriteFile(filepath.Join(s.store.HomeDir(), "projects", a, "access.json"), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	w := accessRequest(s.Handler(), "alice", "editor", "GET", "/api/projects/"+a, "")
	if w.Code != 503 || strings.Contains(w.Body.String(), s.store.HomeDir()) {
		t.Fatalf("corruption %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/projects", "/api/v1/projects", "/api/v1/jobs"} {
		w := accessRequest(s.Handler(), "alice", "editor", "GET", path, "")
		if w.Code != 503 || strings.Contains(w.Body.String(), s.store.HomeDir()) {
			t.Fatalf("corrupt policy discovery %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w = accessRequest(s.Handler(), "admin", "admin", "PUT", "/api/v1/projects/"+a+"/access", `{"revision":0,"members":{}}`)
	if w.Code != 503 || strings.Contains(w.Body.String(), s.store.HomeDir()) {
		t.Fatalf("corrupt policy update %d %s", w.Code, w.Body.String())
	}
	if err := s.ConfigureProjectAccess("typo"); err == nil {
		t.Fatal("invalid mode allowed")
	}
	if err := s.ConfigureProjectAccess("legacy"); err != nil {
		t.Fatal(err)
	}
	w = accessRequest(s.Handler(), "alice", "viewer", "GET", "/api/projects/"+b, "")
	if w.Code != 200 {
		t.Fatal("legacy read changed")
	}
	w = accessRequest(s.Handler(), "alice", "editor", "PUT", "/api/v1/projects/"+b+"/access", `{"revision":1,"members":{}}`)
	if w.Code != 403 {
		t.Fatal("legacy editor changed policy")
	}
}

func TestAccessCapabilitiesAndMalformedUpdates(t *testing.T) {
	s, a, _ := accessFixture(t)
	h := s.Handler()
	for _, tc := range []struct {
		role                       string
		refresh, configure, manage bool
	}{
		{"viewer", false, false, false}, {"editor", true, false, false}, {"admin", true, true, true},
	} {
		w := accessRequest(h, "alice", tc.role, "GET", "/api/v1/projects/"+a+"/capabilities", "")
		var got capabilities
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || got.Mode != "scoped" || got.Role != Role(tc.role) || got.CanRefresh != tc.refresh || got.CanConfigure != tc.configure || got.CanManageAccess != tc.manage || got.CanDelete != tc.manage {
			t.Fatalf("capabilities for %s: %d %+v", tc.role, w.Code, got)
		}
	}
	for _, body := range []string{
		`{`, `{}`, `{"revision":1}`, `{"revision":1,"members":null}`, `{"revision":1,"members":{},"extra":true}`,
		`{"revision":1,"members":{}} {}`, `{"revision":1,"members":{"alice":"admin"}}`,
		`{"revision":1,"members":{" alice":"viewer"}}`, `{"revision":1,"members":{"a\nb":"viewer"}}`,
		`{"revision":1,"members":{"` + strings.Repeat("x", 257) + `":"viewer"}}`,
		`{"revision":1,"members":{"` + strings.Repeat("x", 1<<20) + `":"viewer"}}`,
	} {
		w := accessRequest(h, "admin", "admin", "PUT", "/api/v1/projects/"+a+"/access", body)
		if w.Code != 400 {
			t.Fatalf("invalid update status %d: %s", w.Code, w.Body.String())
		}
	}
	policy, err := s.store.GetProjectAccess(a)
	if err != nil || policy.Revision != 1 || policy.Members["alice"] != "editor" {
		t.Fatalf("bad updates changed grants: %+v %v", policy, err)
	}
	p, err := s.store.CreateProject(store.Project{Name: "no-grants"})
	if err != nil {
		t.Fatal(err)
	}
	w := accessRequest(h, "alice", "editor", "GET", "/api/projects/"+p.ID, "")
	if w.Code != 404 {
		t.Fatalf("new project without grants: %d", w.Code)
	}
}

type scopedIdentityTransport struct{ identity atomic.Value }

func (rt *scopedIdentityTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	identity := rt.identity.Load().(Identity)
	clone := req.Clone(req.Context())
	clone.Header.Set(proxySecretHeader, "proxy")
	clone.Header.Set(proxyUserHeader, identity.User)
	clone.Header.Set(proxyRoleHeader, string(identity.Role))
	return http.DefaultTransport.RoundTrip(clone)
}
func TestScopedMCPAllToolsIdentitySwitchAndRevocation(t *testing.T) {
	s, a, b := accessFixture(t)
	hs := httptest.NewServer(s.Handler())
	defer hs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rt := &scopedIdentityTransport{}
	rt.identity.Store(Identity{User: "alice", Role: RoleViewer})
	client := mcp.NewClient(&mcp.Implementation{Name: "scoped-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: hs.URL + "/mcp", HTTPClient: &http.Client{Transport: rt}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) (*mcp.CallToolResult, string) {
		t.Helper()
		out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(out)
		return out, string(encoded)
	}
	out, encoded := call("list_projects", map[string]any{})
	if out.IsError || !strings.Contains(encoded, a) || strings.Contains(encoded, b) {
		t.Fatalf("MCP discovery %s", encoded)
	}
	out, encoded = call("get_graph_summary", map[string]any{})
	if out.IsError || !strings.Contains(encoded, a) {
		t.Fatalf("MCP sole-visible %s", encoded)
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "list_projects" {
			continue
		}
		args := map[string]any{"project": b}
		switch tool.Name {
		case "get_graph_summary", "list_services", "list_graph_runs":
		case "get_service", "get_dependencies":
			args["service"] = "private-service"
		case "search_architecture":
			args["query"] = "private"
		case "get_impact":
			args["target"] = "private-service"
		case "compare_graphs", "find_dependency_path":
			args["from"], args["to"] = "before", "after"
		case "get_object_trace":
			args["service"], args["object_id"] = "private-service", "object"
		default:
			t.Fatalf("add valid isolation arguments for new tool %s", tool.Name)
		}
		out, encoded := call(tool.Name, args)
		if !out.IsError || !strings.Contains(encoded, "not found") || strings.Contains(encoded, "private-service") {
			t.Errorf("MCP foreign %s: %s", tool.Name, encoded)
		}
	}
	// Same logical client/transport now carries another user's identity. No
	// cached session privilege may survive this switch or subsequent revocation.
	rt.identity.Store(Identity{User: "bob", Role: RoleViewer})
	out, encoded = call("get_graph_summary", map[string]any{"project": a})
	if !out.IsError {
		t.Fatalf("identity reuse %s", encoded)
	}
	out, encoded = call("get_graph_summary", map[string]any{"project": b})
	if out.IsError {
		t.Fatalf("new identity denied %s", encoded)
	}
	if _, err := s.store.PutProjectAccess(b, 1, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	out, encoded = call("get_graph_summary", map[string]any{"project": b})
	if !out.IsError {
		t.Fatalf("revoked MCP %s", encoded)
	}
	out, encoded = call("list_projects", map[string]any{})
	if out.IsError || strings.Contains(encoded, b) || strings.Contains(encoded, a) {
		t.Fatalf("revoked listing %s", encoded)
	}
}

func TestSSEStopsAfterMembershipRevocation(t *testing.T) {
	s, a, _ := accessFixture(t)
	ch := make(chan runmgr.Event, 1) // deliberately never closed: not a finished-run replay
	mux := http.NewServeMux()
	mux.Handle("GET /api/projects/{pid}/events", s.scopeControlled(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		s.streamRunEvents(w, r, r.PathValue("pid"), ch, w.(http.Flusher))
	})))
	hs := httptest.NewServer(s.accessControlled(mux))
	defer hs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/projects/%s/events", hs.URL, a), nil)
	req.Header.Set(proxySecretHeader, "proxy")
	req.Header.Set(proxyUserHeader, "alice")
	req.Header.Set(proxyRoleHeader, "viewer")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("SSE status %d", resp.StatusCode)
	}
	if _, err := s.store.PutProjectAccess(a, 1, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("SSE did not close after revocation: %v", err)
	}
}
