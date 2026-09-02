package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestRefreshNowAPIRejectsOverlapAndReportsCompletion(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Platform"})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	s.refreshProject = func(_ context.Context, pid string) ProjectRefreshResult {
		close(started)
		<-release
		return ProjectRefreshResult{ProjectID: pid, Synced: 2, Analyzed: 2, GraphRunID: "graph-1"}
	}
	handler := s.Handler()

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first refresh status = %d: %s", first.Code, first.Body.String())
	}
	<-started

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil))
	if second.Code != http.StatusConflict {
		t.Fatalf("overlapping refresh status = %d, want 409", second.Code)
	}
	close(release)
	waitForRefresh(t, s, func(status RefreshStatus) bool { return !status.Running && status.LastFinished != nil })

	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/refresh/status", nil))
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d", statusRecorder.Code)
	}
	var status RefreshStatus
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.LastTrigger != "manual" || len(status.Projects) != 1 || status.Projects[0].ProjectID != project.ID || status.Projects[0].GraphRunID != "graph-1" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestScheduledRefreshRunsOnStartAndInterval(t *testing.T) {
	s := newAuthTestServer(t)
	if _, err := s.store.CreateProject(store.Project{Name: "Platform"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.refreshProject = func(_ context.Context, pid string) ProjectRefreshResult {
		calls.Add(1)
		return ProjectRefreshResult{ProjectID: pid}
	}
	if err := s.ConfigureRefresh(RefreshConfig{Interval: 15 * time.Millisecond, OnStart: true, Concurrency: 2}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.startRefreshLoop(ctx)
	t.Cleanup(cancel)

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatalf("scheduled refresh calls = %d, want at least 2", calls.Load())
	}
	status := s.currentRefreshStatus()
	if !status.Enabled || status.Interval != "15ms" || status.Concurrency != 2 || status.NextRun == nil {
		t.Fatalf("unexpected scheduled status: %+v", status)
	}
}

func TestConfigureRefreshValidation(t *testing.T) {
	s := newAuthTestServer(t)
	if err := s.ConfigureRefresh(RefreshConfig{Interval: -time.Second}); err == nil {
		t.Fatal("expected negative interval to fail")
	}
	if err := s.ConfigureRefresh(RefreshConfig{Concurrency: 17}); err == nil {
		t.Fatal("expected excessive concurrency to fail")
	}
	if err := s.ConfigureRefresh(RefreshConfig{}); err != nil {
		t.Fatal(err)
	}
	if got := s.currentRefreshStatus().Concurrency; got != 4 {
		t.Fatalf("default concurrency = %d, want 4", got)
	}
}

func TestRefreshAllProjectsHandlesEmptyProject(t *testing.T) {
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Empty"})
	if err != nil {
		t.Fatal(err)
	}
	results, err := s.refreshAllProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ProjectID != project.ID || results[0].Error != "" {
		t.Fatalf("unexpected refresh results: %+v", results)
	}
}

func TestRefreshReportsAnalysisFailure(t *testing.T) {
	t.Setenv("DIFFMIND_BINARY", "/bin/false")
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Platform"})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := s.store.CreateRepo(project.ID, store.Repo{Name: "broken-service", Kind: "service_repo", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	result := s.refreshOneProject(context.Background(), project.ID)
	if result.Analyzed != 0 {
		t.Fatalf("analyzed=%d, want 0", result.Analyzed)
	}
	if !strings.Contains(result.Error, "analyze "+repo.ID) {
		t.Fatalf("refresh error=%q", result.Error)
	}
}

func TestSyncGitRepoAdvancesManagedCheckout(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	s := newAuthTestServer(t)
	project, err := s.store.CreateProject(store.Project{Name: "Platform"})
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	runGitForTest(t, source, "init", "-b", "master")
	runGitForTest(t, source, "config", "user.email", "developer@example.test")
	runGitForTest(t, source, "config", "user.name", "Developer")
	tracked := filepath.Join(source, "version.txt")
	if err := os.WriteFile(tracked, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, source, "add", "version.txt")
	runGitForTest(t, source, "commit", "-m", "first")
	repo, err := s.store.CreateRepo(project.ID, store.Repo{Name: "service", Kind: "service_repo", GitURL: source, DefaultBranch: "master"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := s.syncGitRepo(context.Background(), project.ID, *repo)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, filepath.Join(first.ClonePath, "version.txt"), "one\n")

	if err := os.WriteFile(tracked, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, source, "add", "version.txt")
	runGitForTest(t, source, "commit", "-m", "second")
	second, err := s.syncGitRepo(context.Background(), project.ID, *first)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, filepath.Join(second.ClonePath, "version.txt"), "two\n")
	if second.HeadSHA == first.HeadSHA || second.HeadSHA != second.RemoteHeadSHA {
		t.Fatalf("managed checkout did not advance: first=%s second=%s remote=%s", first.HeadSHA, second.HeadSHA, second.RemoteHeadSHA)
	}
}

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func waitForRefresh(t *testing.T, s *Server, done func(RefreshStatus) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done(s.currentRefreshStatus()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("refresh did not finish: %+v", s.currentRefreshStatus())
}
