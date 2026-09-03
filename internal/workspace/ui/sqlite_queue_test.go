package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestSQLiteScopedJobAPI(t *testing.T) {
	s, a, b := accessFixture(t)
	if _, err := store.MigrateQueue(s.store.HomeDir()); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	w := accessRequest(h, "alice", "editor", "GET", "/api/v1/jobs?limit=1", "")
	var page store.JobPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || page.Total != 1 || len(page.Jobs) != 1 || page.Jobs[0].ProjectID != a || strings.Contains(w.Body.String(), b) {
		t.Fatalf("scoped SQL page %d %s", w.Code, w.Body.String())
	}
	hidden, err := s.store.ListJobs(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/jobs?project=" + b, "/api/v1/jobs/" + hidden[0].ID + "/cancel"} {
		method := "GET"
		if strings.HasSuffix(path, "cancel") {
			method = "POST"
		}
		w := accessRequest(h, "alice", "editor", method, path, "")
		if w.Code != 404 {
			t.Fatalf("foreign SQL job %d %s", w.Code, w.Body.String())
		}
	}
	w = accessRequest(h, "alice", "editor", "POST", "/api/v1/jobs/"+page.Jobs[0].ID+"/cancel", "")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	w = accessRequest(h, "alice", "editor", "POST", "/api/v1/jobs/"+page.Jobs[0].ID+"/retry", "")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	w = accessRequest(h, "admin", "admin", "GET", "/metrics", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `diffmind_refresh_jobs{status="queued"} 2`) {
		t.Fatalf("SQLite metrics %d %s", w.Code, w.Body.String())
	}
	if _, err := s.store.PutProjectAccess(a, 1, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	w = accessRequest(h, "alice", "editor", "GET", "/api/v1/jobs", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total": 0`) {
		t.Fatalf("revoked SQL page %d %s", w.Code, w.Body.String())
	}
}
