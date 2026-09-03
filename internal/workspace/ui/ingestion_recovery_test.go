package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func ingestionPost(s *Server, pid, action, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/projects/"+pid+"/ingestion"+action, strings.NewReader(body)))
	return w
}

func awaitIngestionIdle(t *testing.T, s *Server, pid string) *store.Ingestion {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		s.ingestionMu.Lock()
		active := s.ingestionActive[pid]
		s.ingestionMu.Unlock()
		if !active {
			i, err := s.store.GetIngestion(pid)
			if err != nil {
				t.Fatal(err)
			}
			return i
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("ingestion did not release its operation lock")
	return nil
}

func TestIngestionCancelThenResumeUsesSavedRequest(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Recovery"})
	s.store.CreateRepo(p.ID, store.Repo{Name: "service", Path: t.TempDir()})
	entered := make(chan struct{})
	s.refreshProject = func(ctx context.Context, _ string) ProjectRefreshResult {
		close(entered)
		<-ctx.Done()
		return ProjectRefreshResult{Error: ctx.Err().Error()}
	}
	if w := ingestionPost(s, p.ID, "", `{"concurrency":2}`); w.Code != 202 {
		t.Fatalf("start: %s", w.Body.String())
	}
	<-entered
	if w := ingestionPost(s, p.ID, "/cancel", ""); w.Code != 202 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	stopped := awaitIngestionIdle(t, s, p.ID)
	if stopped.Status != store.IngestionCancelled || stopped.FinishedAt.IsZero() {
		t.Fatalf("cancelled: %+v", stopped)
	}
	s.resumeInterruptedIngestions(context.Background())
	if got, _ := s.store.GetIngestion(p.ID); got.Status != store.IngestionCancelled {
		t.Fatal("user cancellation was automatically resumed")
	}
	s.refreshProject = completedIngestionPipeline(t, s, 1)
	if w := ingestionPost(s, p.ID, "/resume", ""); w.Code != 202 {
		t.Fatalf("resume: %s", w.Body.String())
	}
	resumed := awaitIngestionIdle(t, s, p.ID)
	if resumed.Status != store.IngestionCompleted || resumed.ID != stopped.ID || resumed.Attempt != 2 || len(resumed.Errors) != 0 {
		t.Fatalf("resumed: %+v", resumed)
	}
	if w := ingestionPost(s, p.ID, "/resume", ""); w.Code != 409 {
		t.Fatalf("completed resume: %d", w.Code)
	}
}

func TestIngestionAutomaticallyResumesInterruptedJob(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Restart"})
	s.store.CreateRepo(p.ID, store.Repo{Name: "service", Path: t.TempDir(), SyncStatus: "diffmind_running"})
	request, _ := json.Marshal(ingestionRequest{Concurrency: 2})
	before, _ := s.store.CreateIngestion(p.ID, store.Ingestion{Request: request, Attempt: 1, ImportComplete: true})
	s = New(s.store, s.runs, s.diffmindRunsDir, "", 0, s.log)
	s.refreshProject = completedIngestionPipeline(t, s, 1)
	s.resumeInterruptedIngestions(context.Background())
	after := awaitIngestionIdle(t, s, p.ID)
	if after.Status != store.IngestionCompleted || after.Attempt != 2 || after.ID != before.ID {
		t.Fatalf("recovered: %+v", after)
	}
}

func TestServerShutdownLeavesIngestionResumable(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Shutdown"})
	s.store.CreateRepo(p.ID, store.Repo{Name: "service", Path: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	s.refreshContext = ctx
	entered := make(chan struct{})
	s.refreshProject = func(ctx context.Context, _ string) ProjectRefreshResult {
		close(entered)
		<-ctx.Done()
		return ProjectRefreshResult{}
	}
	if w := ingestionPost(s, p.ID, "", "{}"); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	<-entered
	cancel()
	if got := awaitIngestionIdle(t, s, p.ID); got.Status != store.IngestionInterrupted {
		t.Fatalf("shutdown: %+v", got)
	}
}

func TestIngestionParallelStartsHaveOneWinner(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Concurrency"})
	s.store.CreateRepo(p.ID, store.Repo{Name: "service", Path: t.TempDir()})
	release := make(chan struct{})
	s.refreshProject = func(ctx context.Context, _ string) ProjectRefreshResult {
		<-release
		return createCompletedGraphResult(t, s, p.ID, 1)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- ingestionPost(s, p.ID, "", "{}").Code }()
	}
	wg.Wait()
	close(codes)
	winners := 0
	for code := range codes {
		if code == 202 {
			winners++
		} else if code != 409 {
			t.Errorf("unexpected status %d", code)
		}
	}
	close(release)
	awaitIngestionIdle(t, s, p.ID)
	if winners != 1 {
		t.Fatalf("%d accepted starts", winners)
	}
}

func TestIngestionLifecycleEndpointsRejectInvalidState(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Legacy"})
	for _, action := range []string{"/resume", "/cancel"} {
		if w := ingestionPost(s, "missing", action, ""); w.Code != 404 {
			t.Errorf("missing %s: %d", action, w.Code)
		}
	}
	s.store.CreateIngestion(p.ID, store.Ingestion{Status: store.IngestionFailed})
	if w := ingestionPost(s, p.ID, "/resume", ""); w.Code != 409 || !strings.Contains(w.Body.String(), "predates") {
		t.Fatalf("legacy resume: %d %s", w.Code, w.Body.String())
	}
	if w := ingestionPost(s, p.ID, "/cancel", ""); w.Code != 409 {
		t.Errorf("inactive cancel: %d", w.Code)
	}
}

func TestIngestionRecoveryRoutesRequireEditor(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Roles"})
	s.SetAuthToken("admin-secret")
	s.SetTrustedProxySecret("proxy-secret")
	for _, action := range []string{"", "/resume", "/cancel"} {
		r := httptest.NewRequest(http.MethodPost, "/api/projects/"+p.ID+"/ingestion"+action, strings.NewReader("{}"))
		r.Header.Set(proxySecretHeader, "proxy-secret")
		r.Header.Set(proxyUserHeader, "viewer@example.test")
		r.Header.Set(proxyRoleHeader, "viewer")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("viewer mutation %s: %d", action, w.Code)
		}
	}
}

func TestInterruptedGraphRunIsMarkedFailedOnRestart(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Graph restart"})
	run, _ := s.store.CreateRun(p.ID, store.RunManifest{Status: store.RunRunning})
	New(s.store, s.runs, s.diffmindRunsDir, "", 0, s.log)
	got, err := s.store.GetRun(p.ID, run.ID)
	if err != nil || got.Status != store.RunFailed || got.FinishedAt.IsZero() {
		t.Fatalf("stale graph: %+v %v", got, err)
	}
}

func TestProjectMutationLeasePreventsIngestionStart(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Mutation"})
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	mutation := s.requireProjectIdle(func(http.ResponseWriter, *http.Request) { close(entered); <-release })
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.SetPathValue("pid", p.ID)
		mutation(httptest.NewRecorder(), r)
		close(done)
	}()
	<-entered
	w := ingestionPost(s, p.ID, "", "{}")
	close(release)
	<-done
	if w.Code != 409 {
		t.Fatalf("ingestion during mutation: %d", w.Code)
	}
}

func TestCancellationIntentSurvivesProgressSaveAndCrash(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Durable cancellation"})
	s.store.CreateRepo(p.ID, store.Repo{Name: "service", Path: t.TempDir()})
	req, _ := json.Marshal(ingestionRequest{})
	i, err := s.store.CreateIngestion(p.ID, store.Ingestion{Request: req, Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.RequestIngestionCancellation(p.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate a worker writing an older copy, then a crash before termination.
	i.Phase = "processing_repositories"
	if err := s.store.SaveIngestion(p.ID, *i); err != nil {
		t.Fatal(err)
	}
	s = New(s.store, s.runs, s.diffmindRunsDir, "", 0, s.log)
	s.resumeInterruptedIngestions(context.Background())
	got, _ := s.store.GetIngestion(p.ID)
	if got.Status != store.IngestionCancelled || !got.CancelRequested {
		t.Fatalf("cancel intent lost: %+v", got)
	}
	s.refreshProject = completedIngestionPipeline(t, s, 1)
	if w := ingestionPost(s, p.ID, "/resume", ""); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	got = awaitIngestionIdle(t, s, p.ID)
	if got.Status != store.IngestionCompleted || got.CancelRequested {
		t.Fatalf("new attempt inherited cancellation: %+v", got)
	}
}
