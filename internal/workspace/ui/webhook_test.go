package ui

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func signedWebhook(s *Server, pid, secret, event, delivery, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/projects/"+pid+"/webhooks/github", strings.NewReader(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}
func TestWebhookSignatureOfficialVector(t *testing.T) {
	if !validWebhookSignature("It's a Secret to Everybody", "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17", []byte("Hello, World!")) {
		t.Fatal("official signature vector failed")
	}
	for _, sig := range []string{"", "sha1=bad", "sha256=00", "sha256=nothex"} {
		if validWebhookSignature("secret", sig, []byte("hello")) {
			t.Fatal("invalid signature accepted")
		}
	}
}
func TestWebhookAuthenticationFilteringDedupAndExecution(t *testing.T) {
	s := newAuthTestServer(t)
	secret := strings.Repeat("x", 32)
	s.SetAuthToken("admin-token")
	if err := s.ConfigureOperations(OperationsConfig{WebhookSecret: secret, Capacity: 1}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.store.CreateProject(store.Project{Name: "webhooks"})
	_, err := s.store.CreateRepo(p.ID, store.Repo{Name: "api", GitURL: "git@github.com:example/api.git", DefaultBranch: "master"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"ref":"refs/heads/master","deleted":false,"repository":{"html_url":"https://github.com/example/api","default_branch":"master"}}`
	for _, tc := range []struct {
		secret, event, id, body string
		status                  int
	}{
		{"wrong", "push", "one", body, 401}, {secret, "push", "", body, 400}, {secret, "push", "one", "{", 400},
		{secret, "ping", "", `{}`, 200}, {secret, "issues", "one", `{}`, 200},
		{secret, "push", "tag", strings.Replace(body, "refs/heads/master", "refs/tags/v1", 1), 200},
		{secret, "push", "branch", strings.Replace(body, "refs/heads/master", "refs/heads/main", 1), 200},
		{secret, "push", "host", strings.Replace(body, "github.com", "untrusted.test", 1), 200},
		{secret, "push", "other", strings.Replace(body, "example/api", "example/other", 1), 200},
		{secret, "push", "deleted", strings.Replace(body, `"deleted":false`, `"deleted":true`, 1), 200},
	} {
		w := signedWebhook(s, p.ID, tc.secret, tc.event, tc.id, tc.body)
		if w.Code != tc.status {
			t.Fatalf("case %s: %d %s", tc.id, w.Code, w.Body.String())
		}
	}
	jobs, _ := s.store.ListJobs("")
	if len(jobs) != 0 {
		t.Fatal("ignored delivery queued work")
	}
	accepted := signedWebhook(s, p.ID, secret, "push", "one", body)
	if accepted.Code != 202 {
		t.Fatal(accepted.Body.String())
	}
	var data struct {
		ID        string `json:"job_id"`
		Duplicate bool   `json:"duplicate"`
	}
	if err := json.Unmarshal(accepted.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	duplicate := signedWebhook(s, p.ID, secret, "push", "one", body)
	if duplicate.Code != 202 || !bytes.Contains(duplicate.Body.Bytes(), []byte(`"duplicate": true`)) {
		t.Fatalf("duplicate: %s", duplicate.Body.String())
	}
	conflict := signedWebhook(s, p.ID, secret, "push", "one", body+" ")
	if conflict.Code != 409 {
		t.Fatalf("conflicting delivery %d", conflict.Code)
	}
	full := signedWebhook(s, p.ID, secret, "push", "two", body)
	if full.Code != 503 || full.Header().Get("Retry-After") == "" {
		t.Fatalf("backpressure %d", full.Code)
	}
	var calls atomic.Int32
	s.refreshProject = func(ctx context.Context, pid string) ProjectRefreshResult {
		calls.Add(1)
		return ProjectRefreshResult{ProjectID: pid, GraphRunID: "saved-graph"}
	}
	if err := s.StartOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if done := awaitRefreshJob(t, s, data.ID); done.Status != "succeeded" {
		t.Fatalf("webhook execution %+v", done)
	}
	signedWebhook(s, p.ID, secret, "push", "one", body)
	if calls.Load() != 1 {
		t.Fatalf("duplicate executed %d times", calls.Load())
	}
	oversized := signedWebhook(s, p.ID, secret, "push", "large", strings.Repeat(" ", (2<<20)+1))
	if oversized.Code != 413 {
		t.Fatalf("body limit %d", oversized.Code)
	}
}
func TestWebhookDisabledAndConfiguration(t *testing.T) {
	s := newAuthTestServer(t)
	if got := signedWebhook(s, "unknown", "secret", "ping", "", `{}`); got.Code != 404 {
		t.Fatalf("disabled %d", got.Code)
	}
	for _, cfg := range []OperationsConfig{{Workers: 17}, {Capacity: -1}, {RepositoryWorkers: 33}, {WebhookSecret: "short"}} {
		if err := s.ConfigureOperations(cfg); err == nil {
			t.Fatalf("invalid config %+v", cfg)
		}
	}
}
