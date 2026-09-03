package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestProjectIngestionImportsAnalyzesBuildsAndPersists(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Company graph"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mkdirGit(t, filepath.Join(root, "catalog"))
	mkdirGit(t, filepath.Join(root, "orders"))
	s.refreshProject = completedIngestionPipeline(t, s, 2)

	body, _ := json.Marshal(ingestionRequest{
		Import:      &importReposRequest{Provider: "local", Root: root},
		Concurrency: 2,
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/ingestion", bytes.NewReader(body))
	req.SetPathValue("pid", project.ID)
	s.handleStartIngestion(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("start status = %d: %s", recorder.Code, recorder.Body.String())
	}

	ingestion := waitForIngestion(t, s.store, project.ID)
	if ingestion.Status != store.IngestionCompleted || ingestion.Phase != "complete" {
		t.Fatalf("ingestion = %+v", ingestion)
	}
	if ingestion.Discovered != 2 || ingestion.Imported != 2 || ingestion.Repositories != 2 || ingestion.Analyzed != 2 || ingestion.GraphRunID == "" {
		t.Fatalf("ingestion counters = %+v", ingestion)
	}

	restarted := New(s.store, s.runs, s.diffmindRunsDir, "127.0.0.1", 8090, s.log)
	getRecorder := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+project.ID+"/ingestion", nil)
	getReq.SetPathValue("pid", project.ID)
	restarted.handleGetIngestion(getRecorder, getReq)
	if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), ingestion.ID) {
		t.Fatalf("persisted status = %d: %s", getRecorder.Code, getRecorder.Body.String())
	}
}

func TestProjectIngestionRejectsOverlap(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Company graph"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateRepo(project.ID, store.Repo{Name: "catalog", Path: t.TempDir(), Kind: "service_repo"}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	s.refreshProject = func(context.Context, string) ProjectRefreshResult {
		<-release
		return createCompletedGraphResult(t, s, project.ID, 1)
	}

	start := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/ingestion", strings.NewReader(`{}`))
		req.SetPathValue("pid", project.ID)
		s.handleStartIngestion(recorder, req)
		return recorder
	}
	if first := start(); first.Code != http.StatusAccepted {
		t.Fatalf("first start = %d: %s", first.Code, first.Body.String())
	}
	if second := start(); second.Code != http.StatusConflict {
		t.Fatalf("overlapping start = %d, want 409: %s", second.Code, second.Body.String())
	}
	mutation := httptest.NewRecorder()
	mutationRequest := httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/repos", strings.NewReader(`{"name":"late-repo","path":"/tmp/late-repo"}`))
	s.Handler().ServeHTTP(mutation, mutationRequest)
	if mutation.Code != http.StatusConflict {
		t.Fatalf("mutation during ingestion = %d, want 409: %s", mutation.Code, mutation.Body.String())
	}
	close(release)
	if got := waitForIngestion(t, s.store, project.ID); got.Status != store.IngestionCompleted {
		t.Fatalf("final ingestion = %+v", got)
	}
}

func TestProjectIngestionHTTPReportsNotStartedAndFailure(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Empty graph"})
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()

	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/projects/"+project.ID+"/ingestion", nil))
	var initial store.Ingestion
	if err := json.Unmarshal(get.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if get.Code != http.StatusOK || initial.Status != "not_started" || initial.Phase != "idle" {
		t.Fatalf("initial status = %d: %s", get.Code, get.Body.String())
	}

	start := httptest.NewRecorder()
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/ingestion", strings.NewReader(`{}`)))
	if start.Code != http.StatusAccepted {
		t.Fatalf("start status = %d: %s", start.Code, start.Body.String())
	}
	failed := waitForIngestion(t, s.store, project.ID)
	if failed.Status != store.IngestionFailed || !strings.Contains(strings.Join(failed.Errors, " "), "no repositories") {
		t.Fatalf("failed ingestion = %+v", failed)
	}

	get = httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/projects/"+project.ID+"/ingestion", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), failed.ID) {
		t.Fatalf("failed status = %d: %s", get.Code, get.Body.String())
	}
}

func TestProjectIngestionCompletesPartiallyWhenGraphIsUsable(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Partial graph"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateRepo(project.ID, store.Repo{Name: "catalog", Path: t.TempDir(), Kind: "service_repo"}); err != nil {
		t.Fatal(err)
	}
	s.refreshProject = func(context.Context, string) ProjectRefreshResult {
		result := createCompletedGraphResult(t, s, project.ID, 1)
		result.Error = "one optional repository could not be analyzed"
		return result
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/ingestion", strings.NewReader(`{}`))
	req.SetPathValue("pid", project.ID)
	s.handleStartIngestion(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("start status = %d: %s", recorder.Code, recorder.Body.String())
	}
	got := waitForIngestion(t, s.store, project.ID)
	if got.Status != store.IngestionPartial || got.GraphRunID == "" || len(got.Errors) != 1 {
		t.Fatalf("partial ingestion = %+v", got)
	}
}

func TestProjectIngestionFailsWhenGraphBuildFails(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Broken graph"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateRepo(project.ID, store.Repo{Name: "catalog", Path: t.TempDir(), Kind: "service_repo"}); err != nil {
		t.Fatal(err)
	}
	s.refreshProject = func(context.Context, string) ProjectRefreshResult {
		run, createErr := s.store.CreateRun(project.ID, store.RunManifest{Status: store.RunFailed, Error: "graph compiler failed"})
		if createErr != nil {
			t.Error(createErr)
			return ProjectRefreshResult{ProjectID: project.ID, Error: createErr.Error()}
		}
		return ProjectRefreshResult{ProjectID: project.ID, Analyzed: 1, GraphRunID: run.ID}
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/ingestion", strings.NewReader(`{}`))
	req.SetPathValue("pid", project.ID)
	s.handleStartIngestion(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("start status = %d: %s", recorder.Code, recorder.Body.String())
	}
	got := waitForIngestion(t, s.store, project.ID)
	if got.Status != store.IngestionFailed || !strings.Contains(strings.Join(got.Errors, " "), "graph compiler failed") {
		t.Fatalf("failed graph ingestion = %+v", got)
	}
}

func TestServerMarksInterruptedIngestionRecoverable(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Interrupted graph"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateIngestion(project.ID, store.Ingestion{Phase: "processing_repositories"}); err != nil {
		t.Fatal(err)
	}
	repo, err := s.store.CreateRepo(project.ID, store.Repo{Name: "catalog", Path: t.TempDir(), Kind: "service_repo", SyncStatus: "diffmind_running"})
	if err != nil {
		t.Fatal(err)
	}

	New(s.store, s.runs, s.diffmindRunsDir, "127.0.0.1", 8090, s.log)
	got, err := s.store.GetIngestion(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.IngestionInterrupted || got.FinishedAt.IsZero() || !strings.Contains(strings.Join(got.Errors, " "), "server restart") {
		t.Fatalf("recovered ingestion = %+v", got)
	}
	recoveredRepo, err := s.store.GetRepo(project.ID, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredRepo.SyncStatus != "diffmind_failed" || !strings.Contains(recoveredRepo.SyncError, "server restart") {
		t.Fatalf("recovered repository = %+v", recoveredRepo)
	}
}

func TestValidateImportReposRequestRejectsInvalidInput(t *testing.T) {
	tests := []importReposRequest{
		{Provider: "gitlab", Org: "example"},
		{Provider: "github"},
		{Provider: "local"},
		{Provider: "github", Org: "example", Include: "["},
		{Provider: "github", Org: "example", Concurrency: 17},
		{Provider: "local", Root: "/tmp", Limit: -1},
	}
	for _, test := range tests {
		req := test
		if err := validateImportReposRequest(&req); err == nil {
			t.Errorf("expected validation error for %+v", test)
		}
	}
	valid := importReposRequest{Org: "example", Include: "^service-", Exclude: "archive"}
	if err := validateImportReposRequest(&valid); err != nil || valid.Provider != "github" {
		t.Fatalf("valid request = %+v, err=%v", valid, err)
	}
}

func TestProjectIngestionRejectsInvalidConcurrency(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Invalid concurrency"})
	if err != nil {
		t.Fatal(err)
	}
	for _, concurrency := range []int{-1, 17} {
		recorder := httptest.NewRecorder()
		body := strings.NewReader(fmt.Sprintf(`{"concurrency":%d}`, concurrency))
		req := httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/ingestion", body)
		req.SetPathValue("pid", project.ID)
		s.handleStartIngestion(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("concurrency %d status = %d, want 400: %s", concurrency, recorder.Code, recorder.Body.String())
		}
	}
}

func completedIngestionPipeline(t *testing.T, s *Server, analyzed int) func(context.Context, string) ProjectRefreshResult {
	t.Helper()
	return func(_ context.Context, pid string) ProjectRefreshResult {
		repos, err := s.store.ListRepos(pid)
		if err != nil {
			t.Error(err)
			return ProjectRefreshResult{ProjectID: pid, Error: err.Error()}
		}
		if len(repos) != analyzed {
			t.Errorf("pipeline repositories = %d, want %d", len(repos), analyzed)
		}
		return createCompletedGraphResult(t, s, pid, analyzed)
	}
}

func createCompletedGraphResult(t *testing.T, s *Server, pid string, analyzed int) ProjectRefreshResult {
	t.Helper()
	run, err := s.store.CreateRun(pid, store.RunManifest{Status: store.RunCompleted})
	if err != nil {
		t.Error(err)
		return ProjectRefreshResult{ProjectID: pid, Error: err.Error()}
	}
	return ProjectRefreshResult{ProjectID: pid, Analyzed: analyzed, GraphRunID: run.ID}
}

func waitForIngestion(t *testing.T, st *store.Store, pid string) *store.Ingestion {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ingestion, err := st.GetIngestion(pid)
		if err == nil && ingestion.Status != store.IngestionRunning {
			return ingestion
		}
		time.Sleep(10 * time.Millisecond)
	}
	ingestion, err := st.GetIngestion(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("ingestion did not finish: %+v", ingestion)
	return nil
}
