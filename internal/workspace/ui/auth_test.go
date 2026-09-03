package ui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

func TestHTTPAuthentication(t *testing.T) {
	s := newAuthTestServer(t)
	s.SetAuthToken("company-secret")
	handler := s.Handler()

	assertStatus := func(path, authorization, tokenHeader string, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		if tokenHeader != "" {
			req.Header.Set("X-DiffMind-Token", tokenHeader)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		resp := recorder.Result()
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
		if want == http.StatusUnauthorized && resp.Header.Get("WWW-Authenticate") == "" {
			t.Fatal("unauthorized response did not advertise Basic authentication")
		}
	}

	assertStatus("/healthz", "", "", http.StatusOK)
	assertStatus("/api/v1/projects", "", "", http.StatusUnauthorized)
	assertStatus("/api/v1/projects", "Bearer wrong", "", http.StatusUnauthorized)
	assertStatus("/api/v1/projects", "Bearer company-secret", "", http.StatusOK)
	assertStatus("/api/v1/projects", "Basic "+base64.StdEncoding.EncodeToString([]byte("diffmind:company-secret")), "", http.StatusOK)
	assertStatus("/api/v1/projects", "", "company-secret", http.StatusOK)
}

func newAuthTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr := runmgr.New(st, util.NewLogger(util.LevelInfo), t.TempDir())
	s := New(st, mgr, t.TempDir(), "127.0.0.1", 8090, util.NewLogger(util.LevelInfo))
	t.Cleanup(s.StopOperations)
	return s
}

func TestHTTPAuthenticationDisabledByDefault(t *testing.T) {
	handler := newAuthTestServer(t).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestSharedTokenReceivesAdminIdentity(t *testing.T) {
	s := newAuthTestServer(t)
	s.SetAuthToken("company-secret")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	req.Header.Set("Authorization", "Bearer company-secret")
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var identity Identity
	if err := json.Unmarshal(recorder.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.User != "shared-token" || identity.Role != RoleAdmin || identity.AuthMethod != "token" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestTrustedProxyRBAC(t *testing.T) {
	s := newAuthTestServer(t)
	s.SetTrustedProxySecret("proxy-secret")
	handler := s.Handler()

	request := func(method, path, secret, user, role, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			req.Header.Set(proxySecretHeader, secret)
		}
		if user != "" {
			req.Header.Set(proxyUserHeader, user)
		}
		if role != "" {
			req.Header.Set(proxyRoleHeader, role)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	if got := request(http.MethodGet, "/api/v1/session", "", "mallory", "admin", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("spoofed identity status = %d, want 401", got)
	}
	if got := request(http.MethodGet, "/api/v1/session", "proxy-secret", "", "viewer", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("missing user status = %d, want 401", got)
	}
	if got := request(http.MethodGet, "/api/v1/session", "proxy-secret", "alice", "owner", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("invalid role status = %d, want 401", got)
	}

	viewer := request(http.MethodGet, "/api/v1/session", "proxy-secret", "alice@example.test", "", "")
	if viewer.Code != http.StatusOK {
		t.Fatalf("viewer session status = %d, want 200", viewer.Code)
	}
	var identity Identity
	if err := json.Unmarshal(viewer.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.User != "alice@example.test" || identity.Role != RoleViewer || identity.AuthMethod != "trusted_proxy" {
		t.Fatalf("viewer identity = %+v", identity)
	}
	if got := request(http.MethodPost, "/api/projects", "proxy-secret", "alice", "viewer", `{"name":"denied"}`).Code; got != http.StatusForbidden {
		t.Fatalf("viewer mutation status = %d, want 403", got)
	}

	created := request(http.MethodPost, "/api/projects", "proxy-secret", "bob", "editor", `{"name":"Company graph"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("editor create status = %d, want 201: %s", created.Code, created.Body.String())
	}
	if got := request(http.MethodPost, "/api/v1/refresh", "proxy-secret", "bob", "editor", "").Code; got != http.StatusForbidden {
		t.Fatalf("editor fleet refresh status = %d, want 403", got)
	}
	if got := request(http.MethodDelete, "/api/projects/company-graph", "proxy-secret", "bob", "editor", "").Code; got != http.StatusForbidden {
		t.Fatalf("editor delete status = %d, want 403", got)
	}
	if got := request(http.MethodDelete, "/api/projects/company-graph", "proxy-secret", "carol", "admin", "").Code; got != http.StatusOK {
		t.Fatalf("admin delete status = %d, want 200", got)
	}
	if !authorized(RoleViewer, http.MethodPost, "/mcp") {
		t.Fatal("viewer must be able to query the read-only MCP endpoint")
	}
}

func TestMutationAuditLogRecordsAllowedAndDeniedRequests(t *testing.T) {
	s := newAuthTestServer(t)
	s.SetTrustedProxySecret("proxy-secret")
	auditPath := filepath.Join(t.TempDir(), "nested", "audit.jsonl")
	s.SetAuditLogPath(auditPath)
	handler := s.Handler()

	request := func(secret, user, role, name string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/projects?token=must-not-leak", strings.NewReader(`{"name":"`+name+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(proxySecretHeader, secret)
		req.Header.Set(proxyUserHeader, user)
		req.Header.Set(proxyRoleHeader, role)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Header().Get("X-Request-ID") == "" {
			t.Fatal("response omitted X-Request-ID")
		}
		return recorder.Code
	}

	if got := request("wrong", "mallory", "admin", "unauthorized"); got != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", got)
	}
	if got := request("proxy-secret", "alice", "viewer", "forbidden"); got != http.StatusForbidden {
		t.Fatalf("forbidden status = %d", got)
	}
	if got := request("proxy-secret", "bob", "editor", "created"); got != http.StatusCreated {
		t.Fatalf("created status = %d", got)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"proxy-secret", "must-not-leak"} {
		if strings.Contains(text, secret) {
			t.Fatalf("audit log leaked secret %q: %s", secret, text)
		}
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 {
		t.Fatalf("audit event count = %d, want 3: %s", len(lines), text)
	}
	wantActors := []string{"anonymous", "alice", "bob"}
	wantStatuses := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusCreated}
	for i, line := range lines {
		var event auditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if event.Actor != wantActors[i] || event.Status != wantStatuses[i] || event.Path != "/api/projects" || event.RequestID == "" {
			t.Fatalf("event %d = %+v", i, event)
		}
	}
	info, err := os.Stat(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("audit permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestCrossOriginMutationIsRejected(t *testing.T) {
	s := newAuthTestServer(t)
	s.SetAuthToken("company-secret")
	req := httptest.NewRequest(http.MethodPost, "http://diffmind.example/api/v1/refresh", nil)
	req.Header.Set("Authorization", "Bearer company-secret")
	req.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin mutation status=%d, want 403", recorder.Code)
	}
}
