package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestQueuedIngestionCannotResumeOutsideJobLifecycle(t *testing.T) {
	s := newAuthTestServer(t)
	p, err := s.store.CreateProject(store.Project{Name: "owned-job"})
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.store.CreateIngestion(p.ID, store.Ingestion{JobID: "refresh-owned", Status: store.IngestionFailed, Request: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/projects/"+p.ID+"/ingestion/resume", nil))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "retry it from Operations") {
		t.Fatalf("resume owned ingestion: %d %s", w.Code, w.Body.String())
	}
	after, err := s.store.GetIngestion(p.ID)
	if err != nil || after.ID != in.ID || after.Attempt != in.Attempt || after.Status != store.IngestionFailed {
		t.Fatalf("owned ingestion changed: %+v %v", after, err)
	}
}

func TestQueuedRefreshFailsForMissingProject(t *testing.T) {
	s := newAuthTestServer(t)
	result, busy := s.runQueuedRefresh(context.Background(), store.RefreshJob{ProjectID: "missing", ID: "refresh-missing"})
	if busy || result.Status != "failed" || result.Error == "" {
		t.Fatalf("missing project succeeded: %+v busy=%v", result, busy)
	}
}

func awaitRefreshJob(t *testing.T, s *Server, id string) *store.RefreshJob {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, err := s.store.GetJob(id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != "queued" && j.Status != "running" {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("refresh job did not finish")
	return nil
}
func TestOperationsWorkersCancellationAndProjectExclusion(t *testing.T) {
	s := newAuthTestServer(t)
	if err := s.ConfigureOperations(OperationsConfig{Workers: 2, Capacity: 10, RepositoryWorkers: 1}); err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int32
	started := make(chan string, 10)
	s.refreshProject = func(ctx context.Context, pid string) ProjectRefreshResult {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		started <- pid
		<-ctx.Done()
		return ProjectRefreshResult{ProjectID: pid, Error: ctx.Err().Error()}
	}
	var queued []*store.RefreshJob
	for i := 0; i < 3; i++ {
		p, err := s.store.CreateProject(store.Project{Name: fmt.Sprintf("project-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		j, _, err := s.enqueueRefresh(p.ID, "manual", "", "")
		if err != nil {
			t.Fatal(err)
		}
		queued = append(queued, j)
	}
	if err := s.StartOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	all, _ := s.store.ListJobs("")
	running := 0
	for _, j := range all {
		if j.Status == "running" {
			running++
		}
	}
	if running != 2 || peak.Load() != 2 {
		t.Fatalf("bound running=%d peak=%d", running, peak.Load())
	}
	for _, j := range queued {
		if _, err := s.store.CancelJob(j.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, j := range queued {
		if out := awaitRefreshJob(t, s, j.ID); out.Status != "cancelled" {
			t.Fatalf("cancel: %+v", out)
		}
	}
	if active.Load() != 0 {
		t.Fatal("worker finished before callback drained")
	}
}
func TestOperationsRetryAndShutdownRecovery(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "retry"})
	var calls atomic.Int32
	s.refreshProject = func(ctx context.Context, pid string) ProjectRefreshResult {
		if calls.Add(1) == 1 {
			return ProjectRefreshResult{ProjectID: pid, Error: "temporary failure"}
		}
		return ProjectRefreshResult{ProjectID: pid, GraphRunID: "graph-2"}
	}
	j, _, err := s.enqueueRefresh(p.ID, "manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := awaitRefreshJob(t, s, j.ID)
	if result.Status != "succeeded" || len(result.Attempts) != 2 || result.Attempts[0].Error != "temporary failure" || result.Attempts[1].GraphRunID != "graph-2" {
		t.Fatalf("retry history %+v", result)
	}
	s.StopOperations()
	// A separate server instance is required to restart the worker lifecycle.
	restarted := New(s.store, s.runs, s.diffmindRunsDir, "", 0, s.log)
	t.Cleanup(restarted.StopOperations)
	started := make(chan struct{})
	restarted.refreshProject = func(ctx context.Context, pid string) ProjectRefreshResult {
		close(started)
		<-ctx.Done()
		return ProjectRefreshResult{ProjectID: pid, Error: ctx.Err().Error()}
	}
	j, _, err = restarted.enqueueRefresh(p.ID, "manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.StartOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	restarted.StopOperations()
	result, err = s.store.GetJob(j.ID)
	if err != nil || result.Status != "queued" || len(result.Attempts) != 1 || result.Attempts[0].Status != "interrupted" {
		t.Fatalf("shutdown %+v %v", result, err)
	}
}
func TestRepositoryBudgetCancellationAndBusyDeferral(t *testing.T) {
	s := newAuthTestServer(t)
	if err := s.ConfigureOperations(OperationsConfig{RepositoryWorkers: 1}); err != nil {
		t.Fatal(err)
	}
	release, err := s.acquireRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.acquireRepository(ctx); err == nil {
		t.Fatal("budget bypassed")
	}
	p, _ := s.store.CreateProject(store.Project{Name: "busy"})
	_, err = s.store.CreateRepo(p.ID, store.Repo{Name: "repo", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !s.beginProjectOperation(p.ID) {
		t.Fatal("project lock")
	}
	defer s.endProjectOperation(p.ID)
	_, busy := s.runQueuedRefresh(context.Background(), store.RefreshJob{ProjectID: p.ID, ID: "job"})
	if !busy {
		t.Fatal("active work was not deferred")
	}
}
func TestOperationRolesPaginationAndMetrics(t *testing.T) {
	s := newAuthTestServer(t)
	s.SetTrustedProxySecret("proxy")
	p, _ := s.store.CreateProject(store.Project{Name: "ops"})
	j, _, err := s.enqueueRefresh(p.ID, "manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	for _, tc := range []struct {
		role, method, path string
		status             int
	}{
		{"viewer", "GET", "/api/v1/jobs?project=" + p.ID, 200}, {"viewer", "GET", "/metrics", 200},
		{"viewer", "GET", "/api/v1/projects/" + p.ID + "/ingestion-history", 200},
		{"viewer", "POST", "/api/v1/jobs/" + j.ID + "/cancel", 403}, {"viewer", "POST", "/api/v1/projects/" + p.ID + "/refresh-jobs", 403},
		{"viewer", "GET", "/api/v1/jobs?offset=-1", 400}, {"viewer", "GET", "/api/v1/jobs?limit=501", 400},
		{"editor", "POST", "/api/v1/jobs/" + j.ID + "/cancel", 202}, {"editor", "POST", "/api/v1/jobs/" + j.ID + "/retry", 202},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set(proxySecretHeader, "proxy")
		r.Header.Set(proxyUserHeader, "user@example.test")
		r.Header.Set(proxyRoleHeader, tc.role)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.path == "/metrics" && (!strings.Contains(w.Body.String(), `diffmind_refresh_jobs{status="queued"} 1`) || strings.Contains(w.Body.String(), p.ID)) {
			t.Fatalf("metrics missing or high-cardinality: %s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != 401 {
		t.Fatalf("metrics auth %d", w.Code)
	}
}
