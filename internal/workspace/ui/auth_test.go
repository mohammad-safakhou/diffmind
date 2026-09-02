package ui

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
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
	return New(st, mgr, t.TempDir(), "127.0.0.1", 8090, util.NewLogger(util.LevelInfo))
}

func TestHTTPAuthenticationDisabledByDefault(t *testing.T) {
	handler := newAuthTestServer(t).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
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
