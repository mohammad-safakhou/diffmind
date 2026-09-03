package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func bearerRequest(h http.Handler, secret, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+secret)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func issueTokenHTTP(t *testing.T, h http.Handler, pid, role string) (*store.ProjectToken, string) {
	t.Helper()
	w := bearerRequest(h, "recovery", "POST", "/api/v1/projects/"+pid+"/tokens", `{"name":"test agent","role":"`+role+`","expires_in_seconds":3600}`)
	var response struct {
		Token  *store.ProjectToken `json:"token"`
		Secret string              `json:"secret"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Token == nil || response.Secret == "" {
		t.Fatalf("issue %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "sha256") {
		t.Fatal("unsafe credential response")
	}
	return response.Token, response.Secret
}

func TestProjectTokenHTTPIsolationLifecycleAndAuditing(t *testing.T) {
	s, a, b := accessFixture(t)
	h := s.Handler()
	meta, viewer := issueTokenHTTP(t, h, a, "viewer")
	_, editor := issueTokenHTTP(t, h, a, "editor")
	for _, path := range []string{"/api/projects", "/api/v1/projects", "/api/v1/jobs?limit=1"} {
		w := bearerRequest(h, viewer, "GET", path, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), b) || !strings.Contains(w.Body.String(), a) {
			t.Fatalf("discovery %s %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		secret, method, path string
		status               int
	}{
		{viewer, "GET", "/api/v1/projects/" + a + "/graph/summary", 200},
		{viewer, "GET", "/api/projects/" + b, 404},
		{viewer, "POST", "/api/v1/projects/" + a + "/refresh-jobs", 403},
		{editor, "POST", "/api/v1/projects/" + a + "/refresh-jobs", 202},
		{editor, "POST", "/api/projects/" + a + "/ingestion", 403},
		{editor, "GET", "/metrics", 403},
		{viewer, "GET", "/api/v1/projects/" + a + "/tokens", 403},
		{editor, "POST", "/api/v1/projects/" + a + "/tokens", 403},
		{editor, "POST", "/api/v1/projects/" + a + "/tokens/" + meta.ID + "/revoke", 403},
		{"recovery", "POST", "/api/v1/projects/" + b + "/tokens/" + meta.ID + "/revoke", 404},
	} {
		w := bearerRequest(h, tc.secret, tc.method, tc.path, "{}")
		if w.Code != tc.status {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	// Service tokens are independent of proxy grants, not an impersonated user.
	if _, err := s.store.PutProjectAccess(a, 1, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	w := bearerRequest(h, viewer, "GET", "/api/v1/session", "")
	var identity Identity
	if json.Unmarshal(w.Body.Bytes(), &identity) != nil || identity.Role != RoleViewer || identity.TokenID != meta.ID || identity.TokenProject != a || identity.AuthMethod != "project_token" {
		t.Fatalf("session %s", w.Body.String())
	}
	if _, err := s.projectRole(identity, a); err != nil {
		t.Fatalf("service grant depends on user: %v", err)
	}
	w = bearerRequest(h, "recovery", "GET", "/api/v1/projects/"+a+"/tokens", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "sha256") || strings.Contains(w.Body.String(), viewer) {
		t.Fatalf("listing %d %s", w.Code, w.Body.String())
	}
	path := "/api/v1/projects/" + a + "/tokens/" + meta.ID + "/revoke"
	w = bearerRequest(h, "recovery", "POST", path, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	firstRevocation := w.Body.String()
	if again := bearerRequest(h, "recovery", "POST", path, ""); again.Body.String() != firstRevocation {
		t.Fatal("idempotent HTTP revoke rewrote history")
	}
	if w := bearerRequest(h, viewer, "GET", "/api/v1/projects", ""); w.Code != 401 {
		t.Fatalf("revoked %d", w.Code)
	}
	if _, err := s.projectRole(identity, a); err == nil {
		t.Fatal("in-flight identity retained grant")
	}
	if w := bearerRequest(h, editor, "GET", "/api/v1/projects", ""); w.Code != 200 {
		t.Fatal("revocation affected another token")
	}
	audit, err := os.ReadFile(s.auditLogPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{viewer, editor, "recovery", "proxy"} {
		if strings.Contains(string(audit), secret) {
			t.Fatal("audit leaked a secret")
		}
	}
	if !strings.Contains(string(audit), `"auth_method":"project_token"`) || !strings.Contains(string(audit), path) || !strings.Contains(string(audit), `"status":201`) {
		t.Fatal("missing lifecycle/actor audit")
	}
}

func TestProjectTokenAllRoutesAndStrongerIdentityCannotBypassScope(t *testing.T) {
	s, a, b := accessFixture(t)
	base := s.Handler()
	meta, secret := issueTokenHTTP(t, base, a, "editor")
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+secret)
		base.ServeHTTP(w, r)
	})
	assertAllProjectRoutesDenyNonmembers(t, s, b, h)
	for _, value := range []string{secret, "dmt1.malformed"} {
		r := httptest.NewRequest("GET", "/api/projects/"+b, nil)
		r.Header.Set("Authorization", "Bearer "+value)
		r.Header.Set(proxySecretHeader, "proxy")
		r.Header.Set(proxyUserHeader, "admin")
		r.Header.Set(proxyRoleHeader, "admin")
		w := httptest.NewRecorder()
		base.ServeHTTP(w, r)
		if w.Code != 404 && w.Code != 401 {
			t.Fatalf("proxy upgraded token: %d", w.Code)
		}
	}
	if err := s.ConfigureProjectAccess("legacy"); err != nil {
		t.Fatal(err)
	}
	base = s.Handler()
	if w := bearerRequest(base, secret, "GET", "/api/projects/"+a, ""); w.Code != 401 {
		t.Fatal("legacy token not disabled")
	}
	if w := bearerRequest(base, "recovery", "POST", "/api/v1/projects/"+a+"/tokens", `{}`); w.Code != 409 {
		t.Fatal("issued unscoped token")
	}
	if w := bearerRequest(base, "recovery", "POST", "/api/v1/projects/"+a+"/tokens/"+meta.ID+"/revoke", ""); w.Code != 200 {
		t.Fatal("legacy admin cannot revoke")
	}
	s.SetAuthToken("")
	s.SetTrustedProxySecret("")
	if w := bearerRequest(s.Handler(), secret, "GET", "/api/projects", ""); w.Code != 401 {
		t.Fatal("local fallback upgraded invalid token")
	}
}

func TestProjectTokenBadRequestsAndCorruptionFailClosed(t *testing.T) {
	s, a, _ := accessFixture(t)
	h := s.Handler()
	meta, secret := issueTokenHTTP(t, h, a, "viewer")
	for _, body := range []string{`{`, `{}`, `null`, `{"name":"agent","role":"admin","expires_in_seconds":60}`, `{"name":"agent","role":"viewer","expires_in_seconds":59}`, `{"name":"agent","role":"viewer","expires_in_seconds":9223372036854775807}`, `{"name":"agent","role":"viewer","expires_in_seconds":60,"project_id":"private"}`, `{"name":"agent","role":"viewer","expires_in_seconds":60} {}`, strings.Repeat("x", 4097)} {
		w := bearerRequest(h, "recovery", "POST", "/api/v1/projects/"+a+"/tokens", body)
		if w.Code != 400 {
			t.Errorf("bad request %d %s", w.Code, w.Body.String())
		}
	}
	path := filepath.Join(s.store.HomeDir(), "projects", a, "tokens.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"tokens":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ secret, method, path, body string }{
		{secret, "GET", "/api/v1/projects", ""}, {"recovery", "GET", "/api/v1/projects/" + a + "/tokens", ""},
		{"recovery", "POST", "/api/v1/projects/" + a + "/tokens", `{"name":"agent","role":"viewer","expires_in_seconds":60}`},
		{"recovery", "POST", "/api/v1/projects/" + a + "/tokens/" + meta.ID + "/revoke", ""},
	} {
		w := bearerRequest(h, tc.secret, tc.method, tc.path, tc.body)
		if w.Code != 503 || strings.Contains(w.Body.String(), s.store.HomeDir()) {
			t.Fatalf("corrupt registry %d %s", w.Code, w.Body.String())
		}
	}
	if w := bearerRequest(h, "recovery", "GET", "/api/projects/"+a, ""); w.Code != 200 {
		t.Fatal("corrupt tokens blocked admin recovery")
	}
}

type tokenTransport struct{ token atomic.Value }

func (rt *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+rt.token.Load().(string))
	return http.DefaultTransport.RoundTrip(clone)
}

func TestProjectTokenMCPAllToolsIsolationIdentitySwitchAndRevocation(t *testing.T) {
	s, a, b := accessFixture(t)
	h := s.Handler()
	_, first := issueTokenHTTP(t, h, a, "viewer")
	secondMeta, second := issueTokenHTTP(t, h, b, "viewer")
	hs := httptest.NewServer(h)
	defer hs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rt := &tokenTransport{}
	rt.token.Store(first)
	client := mcp.NewClient(&mcp.Implementation{Name: "project-token-test", Version: "1"}, nil)
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
		t.Fatalf("MCP listing %s", encoded)
	}
	out, encoded = call("get_graph_summary", map[string]any{})
	if out.IsError || !strings.Contains(encoded, `"project_id":"`+a+`"`) {
		t.Fatalf("MCP default %s", encoded)
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
			t.Fatalf("add isolation coverage for new tool %s", tool.Name)
		}
		out, encoded = call(tool.Name, args)
		if !out.IsError || !strings.Contains(encoded, "not found") || strings.Contains(encoded, "private-service") {
			t.Errorf("MCP foreign %s: %s", tool.Name, encoded)
		}
	}
	rt.token.Store(second)
	out, encoded = call("get_graph_summary", map[string]any{"project": a})
	if !out.IsError {
		t.Fatalf("identity retained old access %s", encoded)
	}
	out, encoded = call("get_graph_summary", map[string]any{"project": b})
	if out.IsError || !strings.Contains(encoded, `"project_id":"`+b+`"`) {
		t.Fatalf("new identity denied %s", encoded)
	}
	if _, err := s.store.RevokeProjectToken(b, secondMeta.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_projects", Arguments: map[string]any{}}); err == nil && !out.IsError {
		t.Fatal("revoked MCP credential accepted")
	}
}

func TestProjectTokenIdleSSEStopsOnRevocation(t *testing.T) {
	for _, cause := range []string{"revocation", "expiry", "corruption"} {
		t.Run(cause, func(t *testing.T) { tokenStreamCloses(t, cause) })
	}
}

func tokenStreamCloses(t *testing.T, cause string) {
	t.Helper()
	s, a, _ := accessFixture(t)
	meta, secret := issueTokenHTTP(t, s.Handler(), a, "viewer")
	ch := make(chan runmgr.Event)
	mux := http.NewServeMux()
	mux.Handle("GET /api/projects/{pid}/events", s.scopeControlled(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		s.streamRunEvents(w, r, r.PathValue("pid"), ch, w.(http.Flusher))
	})))
	hs := httptest.NewServer(s.accessControlled(mux))
	defer hs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", hs.URL+"/api/projects/"+a+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("SSE %d", resp.StatusCode)
	}
	wantStatus := 401
	if cause == "revocation" {
		if _, err := s.store.RevokeProjectToken(a, meta.ID, "admin"); err != nil {
			t.Fatal(err)
		}
	} else {
		path := filepath.Join(s.store.HomeDir(), "projects", a, "tokens.json")
		body := []byte(`{"version":99}`)
		if cause == "expiry" {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var registry map[string]any
			if err := json.Unmarshal(source, &registry); err != nil {
				t.Fatal(err)
			}
			record := registry["tokens"].([]any)[0].(map[string]any)
			record["created_at"] = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
			record["expires_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			body, err = json.Marshal(registry)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			wantStatus = 503
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if w := bearerRequest(s.Handler(), secret, "GET", "/api/v1/projects", ""); w.Code != wantStatus {
		t.Fatalf("%s authentication status %d", cause, w.Code)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("SSE did not close: %v", err)
	}
}
