package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// RefreshConfig controls continuous company-wide repository refreshes.
type RefreshConfig struct {
	Interval    time.Duration
	OnStart     bool
	Concurrency int
}

// ProjectRefreshResult records one project's work in a fleet refresh.
type ProjectRefreshResult struct {
	ProjectID  string `json:"project_id"`
	Synced     int    `json:"synced"`
	Analyzed   int    `json:"analyzed"`
	Reused     int    `json:"reused"`
	GraphRunID string `json:"graph_run_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// RefreshStatus is the concurrency-safe status projection returned by the API.
type RefreshStatus struct {
	Enabled      bool                   `json:"enabled"`
	Running      bool                   `json:"running"`
	Interval     string                 `json:"interval,omitempty"`
	Concurrency  int                    `json:"concurrency"`
	LastTrigger  string                 `json:"last_trigger,omitempty"`
	LastStarted  *time.Time             `json:"last_started_at,omitempty"`
	LastFinished *time.Time             `json:"last_finished_at,omitempty"`
	NextRun      *time.Time             `json:"next_run_at,omitempty"`
	LastError    string                 `json:"last_error,omitempty"`
	Projects     []ProjectRefreshResult `json:"projects,omitempty"`
}

// ConfigureRefresh configures the background fleet refresh loop. An interval
// of zero disables periodic execution; OnStart can still request one refresh.
func (s *Server) ConfigureRefresh(cfg RefreshConfig) error {
	if cfg.Interval < 0 {
		return fmt.Errorf("refresh interval cannot be negative")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.Concurrency > 16 {
		return fmt.Errorf("refresh concurrency cannot exceed 16")
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.refreshConfig = cfg
	s.refreshStatus.Enabled = cfg.Interval > 0 || cfg.OnStart
	s.refreshStatus.Concurrency = cfg.Concurrency
	if cfg.Interval > 0 {
		s.refreshStatus.Interval = cfg.Interval.String()
	} else {
		s.refreshStatus.Interval = ""
	}
	return nil
}

func (s *Server) startRefreshLoop(ctx context.Context) {
	s.refreshMu.Lock()
	cfg := s.refreshConfig
	if cfg.Interval > 0 {
		next := time.Now().UTC().Add(cfg.Interval)
		s.refreshStatus.NextRun = &next
	}
	s.refreshMu.Unlock()

	if cfg.OnStart {
		s.triggerRefresh(ctx, "startup")
	}
	if cfg.Interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				s.refreshMu.Lock()
				next := at.UTC().Add(cfg.Interval)
				s.refreshStatus.NextRun = &next
				s.refreshMu.Unlock()
				s.triggerRefresh(ctx, "schedule")
			}
		}
	}()
}

func (s *Server) triggerRefresh(ctx context.Context, trigger string) bool {
	s.refreshMu.Lock()
	if s.refreshStatus.Running {
		s.refreshMu.Unlock()
		return false
	}
	s.refreshStatus.Running = true
	s.refreshStatus.LastTrigger = trigger
	started := time.Now().UTC()
	s.refreshStatus.LastStarted = &started
	s.refreshStatus.LastError = ""
	s.refreshMu.Unlock()

	go func() {
		projects, err := s.refreshAllProjects(ctx)
		s.refreshMu.Lock()
		s.refreshStatus.Running = false
		finished := time.Now().UTC()
		s.refreshStatus.LastFinished = &finished
		s.refreshStatus.Projects = append([]ProjectRefreshResult(nil), projects...)
		if err != nil {
			s.refreshStatus.LastError = err.Error()
		}
		s.refreshMu.Unlock()
	}()
	return true
}

func (s *Server) refreshAllProjects(ctx context.Context) ([]ProjectRefreshResult, error) {
	if err := s.StartOperations(ctx); err != nil {
		return nil, err
	}
	projects, err := s.store.ListProjects()
	if err != nil {
		return nil, err
	}
	results := make([]ProjectRefreshResult, 0, len(projects))
	var failures []string
	var jobs []*store.RefreshJob
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		job, _, err := s.enqueueRefresh(project.ID, "fleet_refresh", "", "")
		if err != nil {
			failures = append(failures, project.ID+": "+err.Error())
			continue
		}
		jobs = append(jobs, job)
	}
	for _, job := range jobs {
		for {
			s.operationsMu.Lock()
			schedulerErr := s.operationsError
			s.operationsMu.Unlock()
			if schedulerErr != nil {
				return results, schedulerErr
			}
			if ctx.Err() != nil {
				return results, ctx.Err()
			}
			current, err := s.store.GetJob(job.ID)
			if err != nil {
				return results, err
			}
			if current.Status != "queued" && current.Status != "running" {
				result := ProjectRefreshResult{ProjectID: job.ProjectID}
				if len(current.Attempts) > 0 {
					last := current.Attempts[len(current.Attempts)-1]
					result.GraphRunID = last.GraphRunID
					result.Synced = last.Synced
					result.Analyzed = last.Analyzed
					result.Reused = last.Reused
					result.Error = last.Error
				}
				if current.Status != "succeeded" {
					result.Error = firstNonEmpty(result.Error, current.Status)
					failures = append(failures, job.ProjectID+": "+result.Error)
				}
				results = append(results, result)
				break
			}
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	if len(failures) > 0 {
		return results, errors.New(strings.Join(failures, "; "))
	}
	return results, nil
}

func (s *Server) refreshOneProject(ctx context.Context, pid string) ProjectRefreshResult {
	if !s.beginProjectOperation(pid) {
		return ProjectRefreshResult{ProjectID: pid, Error: "project is already being processed"}
	}
	defer s.endProjectOperation(pid)
	if active, err := s.projectHasActiveWork(pid); err != nil || active {
		return ProjectRefreshResult{ProjectID: pid, Error: "project has active repository or graph work"}
	}
	result := s.runProjectRefresh(ctx, pid, s.refreshConcurrency(), orchestrator.DiffMindRunOptions{}, "fleet_refresh")
	if result.GraphRunID != "" {
		if err := s.waitForGraph(ctx, pid, result.GraphRunID); err != nil {
			result.Error = strings.TrimSpace(result.Error + " " + err.Error())
		}
	}
	return result
}

func (s *Server) runProjectRefresh(ctx context.Context, pid string, concurrency int, opts orchestrator.DiffMindRunOptions, trigger string) ProjectRefreshResult {
	return s.runProjectRefreshWithControl(ctx, pid, concurrency, opts, trigger, false, nil)
}

func (s *Server) runProjectRefreshWithControl(ctx context.Context, pid string, concurrency int, opts orchestrator.DiffMindRunOptions, trigger string, force bool, progress func(store.IngestionRepo)) ProjectRefreshResult {
	result := ProjectRefreshResult{ProjectID: pid}
	var failures []string
	repos, err := s.store.ListRepos(pid)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if len(repos) == 0 {
		return result
	}

	analyzer, _ := orchestrator.AnalyzerIdentity(firstNonEmpty(os.Getenv("DIFFMIND_BINARY"), "diffmind"))
	var wg sync.WaitGroup
	var mu sync.Mutex
	process := func(repo store.Repo) {
		report := func(status, message, runID string) {
			if progress != nil {
				progress(store.IngestionRepo{RepoID: repo.ID, Status: status, Error: message, RunID: runID})
			}
		}
		fail := func(err error) {
			_, _ = s.store.UpdateRepo(pid, repo.ID, func(r *store.Repo) {
				r.SyncStatus = "diffmind_failed"
				r.SyncError = err.Error()
				r.DiffMindFreshness = "unknown"
			})
			mu.Lock()
			failures = append(failures, fmt.Sprintf("analyze %s: %s", repo.ID, err))
			mu.Unlock()
			status := "failed"
			if ctx.Err() != nil {
				status = "cancelled"
			}
			report(status, err.Error(), "")
		}
		if ctx.Err() != nil {
			report("cancelled", ctx.Err().Error(), "")
			return
		}
		if strings.TrimSpace(repo.GitURL) != "" {
			report("syncing", "", "")
			updated, err := s.syncGitRepo(ctx, pid, repo)
			if err != nil {
				fail(fmt.Errorf("sync: %w", err))
				return
			}
			repo = *updated
			mu.Lock()
			result.Synced++
			mu.Unlock()
		}
		if repo.Kind == "infra_repo" {
			report("skipped", "infrastructure-only repository", "")
			return
		}
		fingerprint, _ := s.analysisFingerprint(ctx, pid, repo, opts, analyzer)
		if !force && s.reusableAnalysis(repo, fingerprint) {
			_, err := s.store.UpdateRepo(pid, repo.ID, func(r *store.Repo) { r.SyncStatus = "diffmind_completed"; r.SyncError = "" })
			if err != nil {
				fail(err)
				return
			}
			mu.Lock()
			result.Reused++
			mu.Unlock()
			report("reused", "", repo.LastDiffMindRunID)
			return
		}
		_, err := s.store.UpdateRepo(pid, repo.ID, func(r *store.Repo) {
			r.SyncStatus = "diffmind_running"
			r.SyncError = ""
			r.AnalysisFingerprint = ""
			r.AnalysisArtifactDigest = ""
		})
		if err != nil {
			fail(err)
			return
		}
		report("analyzing", "", "")
		s.runDiffMindForRepoContext(ctx, pid, repo.ID, repo, opts)
		updated, err := s.store.GetRepo(pid, repo.ID)
		if err != nil {
			fail(err)
			return
		}
		if updated.SyncStatus != "diffmind_completed" {
			fail(errors.New(firstNonEmpty(updated.SyncError, "analysis did not complete")))
			return
		}
		digest, err := s.analysisArtifactDigest(*updated)
		if err != nil {
			fail(fmt.Errorf("analysis artifacts: %w", err))
			return
		}
		after, _ := s.analysisFingerprint(ctx, pid, *updated, opts, analyzer)
		if after != fingerprint {
			fingerprint = ""
		}
		_, err = s.store.UpdateRepo(pid, repo.ID, func(r *store.Repo) { r.AnalysisFingerprint = fingerprint; r.AnalysisArtifactDigest = digest })
		if err != nil {
			fail(err)
			return
		}
		mu.Lock()
		result.Analyzed++
		mu.Unlock()
		report("completed", "", updated.LastDiffMindRunID)
	}
	work := make(chan store.Repo)
	for i := 0; i < min(importCloneConcurrency(concurrency), len(repos)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range work {
				process(repo)
			}
		}()
	}
dispatch:
	for _, repo := range repos {
		select {
		case work <- repo:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(work)
	wg.Wait()
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
		return result
	}

	repos, err = s.store.ListRepos(pid)
	if err != nil {
		failures = append(failures, err.Error())
		result.Error = strings.Join(failures, "; ")
		return result
	}
	refs := make([]store.RunRepoRef, 0, len(repos))
	for _, repo := range repos {
		if repo.LastDiffMindRunID != "" {
			refs = append(refs, store.RunRepoRef{RepoID: repo.ID, DiffMindRunID: repo.LastDiffMindRunID})
		}
	}
	refs = s.validGraphRunRepos(pid, refs)
	if len(refs) > 0 {
		run, startErr := s.runs.Start(pid, refs, map[string]any{"trigger": trigger})
		if startErr != nil {
			failures = append(failures, "start graph: "+startErr.Error())
		} else {
			result.GraphRunID = run.ID
		}
	}
	if len(failures) > 0 {
		result.Error = strings.Join(failures, "; ")
	}
	return result
}

func (s *Server) beginProjectOperation(pid string) bool {
	s.projectOpsMu.Lock()
	defer s.projectOpsMu.Unlock()
	if s.projectOps[pid] {
		return false
	}
	s.projectOps[pid] = true
	return true
}

func (s *Server) endProjectOperation(pid string) {
	s.projectOpsMu.Lock()
	delete(s.projectOps, pid)
	s.projectOpsMu.Unlock()
}

func (s *Server) refreshConcurrency() int {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshConfig.Concurrency <= 0 {
		return 4
	}
	return s.refreshConfig.Concurrency
}

func (s *Server) currentRefreshStatus() RefreshStatus {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	status := s.refreshStatus
	status.Projects = append([]ProjectRefreshResult(nil), status.Projects...)
	return status
}

func (s *Server) handleRefreshStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.currentRefreshStatus())
}

func (s *Server) handleRefreshNow(w http.ResponseWriter, _ *http.Request) {
	s.refreshMu.Lock()
	ctx := s.refreshContext
	s.refreshMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.triggerRefresh(ctx, "manual") {
		writeErr(w, http.StatusConflict, errors.New("a fleet refresh is already running"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}
