package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestDirectSyncAndAnalysisRespectProjectBudget(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "direct budget"})
	if _, err := s.putProjectLimits(p.ID, 0, 1, 1); err != nil {
		t.Fatal(err)
	}
	repo, err := s.store.CreateRepo(p.ID, store.Repo{Name: "repo", Path: t.TempDir(), GitURL: "https://example.invalid/never-clone.git"})
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.acquireRepository(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.syncGitRepo(ctx, p.ID, *repo); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sync bypassed limiter: %v", err)
	}
	if _, err := os.Stat(s.store.WorktreeDir(p.ID, repo.ID)); !os.IsNotExist(err) {
		t.Fatalf("waiting sync changed checkout: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	s.runDiffMindForRepoContext(ctx2, p.ID, repo.ID, *repo, orchestrator.DiffMindRunOptions{})
	after, err := s.store.GetRepo(p.ID, repo.ID)
	if err != nil || after.SyncStatus != "diffmind_failed" || after.SyncError != context.DeadlineExceeded.Error() {
		t.Fatalf("analysis bypassed limiter: %+v %v", after, err)
	}
}

func TestFleetRefreshReportsProjectQuota(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "fleet budget"})
	if _, err := s.putProjectLimits(p.ID, 0, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.enqueueRefresh(p.ID, "manual", "", ""); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	s.refreshProject = func(ctx context.Context, pid string) ProjectRefreshResult {
		close(started)
		<-ctx.Done()
		return ProjectRefreshResult{ProjectID: pid, Error: ctx.Err().Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.StartOperations(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.StopOperations()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("worker did not start")
	}
	results, err := s.refreshAllProjects(ctx)
	if err == nil || !strings.Contains(err.Error(), store.ErrProjectQueueFull.Error()) || len(results) != 0 {
		t.Fatalf("fleet quota report %+v %v", results, err)
	}
	jobs, err := s.store.ListJobs(p.ID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("fleet bypassed quota %+v %v", jobs, err)
	}
}

func TestRepositoryProjectBudgetIsolationAndDynamicChanges(t *testing.T) {
	s := newAuthTestServer(t)
	if err := s.ConfigureOperations(OperationsConfig{RepositoryWorkers: 3}); err != nil {
		t.Fatal(err)
	}
	a, _ := s.store.CreateProject(store.Project{Name: "A"})
	b, _ := s.store.CreateProject(store.Project{Name: "B"})
	if _, err := s.putProjectLimits(a.ID, 0, 1, 1); err != nil {
		t.Fatal(err)
	}
	first, err := s.acquireRepository(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waiting := make(chan func(), 1)
	go func() {
		release, err := s.acquireRepository(ctx, a.ID)
		if err != nil {
			t.Error(err)
		}
		waiting <- release
	}()
	select {
	case release := <-waiting:
		if release != nil {
			release()
		}
		t.Fatal("project cap bypassed")
	case <-time.After(30 * time.Millisecond):
	}
	// A saturated project's waiter must not reserve a global slot.
	b1, err := s.acquireRepository(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer b1()
	b2, err := s.acquireRepository(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer b2()
	fullCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	if release, err := s.acquireRepository(fullCtx, b.ID); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("global budget %v", err)
	}
	stop()
	b1()
	b2()
	// Raising a project cap wakes its waiter without a running operation ending.
	if _, err := s.putProjectLimits(a.ID, 1, 1, 2); err != nil {
		t.Fatal(err)
	}
	var second func()
	select {
	case second = <-waiting:
		if second == nil {
			t.Fatal("waiter failed")
		}
	case <-ctx.Done():
		t.Fatal("raising cap did not wake waiter")
	}
	defer second()
	if _, err := s.putProjectLimits(a.ID, 2, 1, 1); err != nil {
		t.Fatal(err)
	}
	s.repositoryMu.Lock()
	active := s.repositoryActive[a.ID]
	s.repositoryMu.Unlock()
	if active != 2 {
		t.Fatal("lowering killed active work")
	}
	first()
	first() // release is intentionally idempotent
	drainCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	if release, err := s.acquireRepository(drainCtx, a.ID); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("lowered cap %v", err)
	}
	stop()
	second()
	last, err := s.acquireRepository(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	last()
	s.repositoryMu.Lock()
	defer s.repositoryMu.Unlock()
	if s.repositoryTotal != 0 || len(s.repositoryActive) != 0 {
		t.Fatalf("leaked slots total=%d projects=%v", s.repositoryTotal, s.repositoryActive)
	}
}

func TestRepositoryLimitsConcurrentCancellationAndCorruption(t *testing.T) {
	s := newAuthTestServer(t)
	if err := s.ConfigureOperations(OperationsConfig{RepositoryWorkers: 4}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.store.CreateProject(store.Project{Name: "load"})
	if _, err := s.putProjectLimits(p.ID, 0, 2, 2); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 80 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if i%3 == 0 {
				cancel()
			}
			release, err := s.acquireRepository(ctx, p.ID)
			if err != nil {
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Error(err)
				}
				return
			}
			defer release()
			if i%3 == 0 {
				t.Error("already cancelled work admitted")
			}
			s.repositoryMu.Lock()
			if s.repositoryActive[p.ID] > 2 || s.repositoryTotal > 4 {
				t.Error("concurrent budget exceeded")
			}
			s.repositoryMu.Unlock()
			time.Sleep(time.Millisecond)
		}(i)
	}
	wg.Wait()
	s.repositoryMu.Lock()
	total := s.repositoryTotal
	s.repositoryMu.Unlock()
	if total != 0 {
		t.Fatalf("leaked %d slots", total)
	}
	path := filepath.Join(s.store.HomeDir(), "projects", p.ID, "limits.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if release, err := s.acquireRepository(context.Background(), p.ID); !errors.Is(err, store.ErrLimitsUnavailable) {
		if release != nil {
			release()
		}
		t.Fatalf("corrupt budget %v", err)
	}
}

func TestProjectLimitsHTTPRolesValidationAndUsage(t *testing.T) {
	s, pid, hidden := accessFixture(t)
	h := s.Handler()
	path := "/api/v1/projects/" + pid + "/limits"
	body := `{"revision":0,"max_pending_jobs":1,"repository_workers":1}`
	for _, mode := range []string{"scoped", "legacy"} {
		if err := s.ConfigureProjectAccess(mode); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"viewer", "editor"} {
			if w := accessRequest(h, "alice", role, "GET", path, ""); w.Code != 200 {
				t.Fatalf("%s %s read %d %s", mode, role, w.Code, w.Body.String())
			}
			if w := accessRequest(h, "alice", role, "PUT", path, body); w.Code != 403 {
				t.Fatalf("%s %s write %d", mode, role, w.Code)
			}
		}
	}
	if err := s.ConfigureProjectAccess("scoped"); err != nil {
		t.Fatal(err)
	}
	if w := accessRequest(h, "alice", "editor", "GET", "/api/v1/projects/"+hidden+"/limits", ""); w.Code != 404 {
		t.Fatalf("hidden %d", w.Code)
	}
	admin := func(body string) *httptest.ResponseRecorder { return bearerRequest(h, "recovery", "PUT", path, body) }
	for _, invalid := range []string{"", "{}", "null", body + "{}", strings.Replace(body, `"revision":0,`, "", 1), strings.Replace(body, `"repository_workers":1`, `"repository_workers":null`, 1), strings.Replace(body, `"repository_workers":1`, `"repository_workers":33`, 1), strings.Replace(body, `"max_pending_jobs":1`, `"max_pending_jobs":-1`, 1), strings.Replace(body, `"revision":0`, `"revision":-1`, 1), strings.Replace(body, `"revision":0`, `"revision":0,"extra":1`, 1), strings.Repeat(" ", 4097)} {
		if w := admin(invalid); w.Code != 400 {
			t.Fatalf("invalid %q: %d %s", invalid, w.Code, w.Body.String())
		}
	}
	if w := admin(body); w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}
	if w := admin(body); w.Code != 409 {
		t.Fatalf("stale %d", w.Code)
	}
	release, err := s.acquireRepository(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	w := accessRequest(h, "alice", "viewer", "GET", path, "")
	var response projectLimitsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.PendingJobs != 1 || response.ActiveRepositoryWorkers != 1 || response.EffectivePendingJobs != 1 || response.EffectiveRepositoryWorkers != 1 {
		t.Fatalf("usage %+v %v", response, err)
	}
	for _, role := range []string{"viewer", "editor"} {
		_, secret := issueTokenHTTP(t, h, pid, role)
		if w := bearerRequest(h, secret, "GET", path, ""); w.Code != 200 {
			t.Fatalf("agent read %d", w.Code)
		}
		if w := bearerRequest(h, secret, "PUT", path, body); w.Code != 403 {
			t.Fatalf("agent write %d", w.Code)
		}
	}
	policyPath := filepath.Join(s.store.HomeDir(), "projects", pid, "limits.json")
	if err := os.WriteFile(policyPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PUT"} {
		w := bearerRequest(h, "recovery", method, path, body)
		if w.Code != 503 || strings.Contains(w.Body.String(), s.store.HomeDir()) {
			t.Fatalf("corrupt %s %d %s", method, w.Code, w.Body.String())
		}
	}
}

func TestProjectQuotaWebhookManualScheduleAndRetry(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			s := newAuthTestServer(t)
			s.SetAuthToken("admin")
			secret := strings.Repeat("w", 32)
			if err := s.ConfigureOperations(OperationsConfig{WebhookSecret: secret, Capacity: 10}); err != nil {
				t.Fatal(err)
			}
			p, _ := s.store.CreateProject(store.Project{Name: "quota webhook"})
			if _, err := s.store.CreateRepo(p.ID, store.Repo{Name: "api", GitURL: "git@github.com:example/api.git", DefaultBranch: "master"}); err != nil {
				t.Fatal(err)
			}
			if backend == "sqlite" {
				if _, err := store.MigrateQueue(s.store.HomeDir()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.putProjectLimits(p.ID, 0, 1, 1); err != nil {
				t.Fatal(err)
			}
			failed, _, err := s.enqueueRefresh(p.ID, "manual", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.store.CancelJob(failed.ID); err != nil {
				t.Fatal(err)
			}
			queued, _, err := s.enqueueRefresh(p.ID, "scheduled", "", "")
			if err != nil {
				t.Fatal(err)
			}
			h := s.Handler()
			for _, path := range []string{"/api/v1/projects/" + p.ID + "/refresh-jobs", "/api/v1/jobs/" + failed.ID + "/retry"} {
				w := bearerRequest(h, "admin", "POST", path, "")
				if w.Code != 429 || w.Header().Get("Retry-After") == "" {
					t.Fatalf("backpressure %d %s", w.Code, w.Body.String())
				}
			}
			body := `{"ref":"refs/heads/master","repository":{"html_url":"https://github.com/example/api","default_branch":"master"}}`
			if w := signedWebhook(s, p.ID, secret, "push", "retry-delivery", body); w.Code != 429 || w.Header().Get("Retry-After") == "" {
				t.Fatalf("webhook backpressure %d %s", w.Code, w.Body.String())
			}
			if jobs, err := s.store.ListJobs(p.ID); err != nil || len(jobs) != 2 {
				t.Fatalf("rejected delivery recorded %v %v", jobs, err)
			}
			if _, err := s.store.CancelJob(queued.ID); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if w := signedWebhook(s, p.ID, secret, "push", "retry-delivery", body); w.Code != 202 {
					t.Fatalf("redelivery %d %s", w.Code, w.Body.String())
				}
			}
			if _, _, err := s.enqueueRefresh(p.ID, "scheduled", "", ""); !errors.Is(err, store.ErrProjectQueueFull) {
				t.Fatalf("schedule bypass %v", err)
			}
			if jobs, err := s.store.ListJobs(p.ID); err != nil || len(jobs) != 3 {
				t.Fatalf("delivery history %v %v", jobs, err)
			}
		})
	}
}
