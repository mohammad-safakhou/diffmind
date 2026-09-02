package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	projects, err := s.store.ListProjects()
	if err != nil {
		return nil, err
	}
	results := make([]ProjectRefreshResult, 0, len(projects))
	var failures []string
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		var result ProjectRefreshResult
		if s.refreshProject != nil {
			result = s.refreshProject(ctx, project.ID)
		} else {
			result = s.refreshOneProject(ctx, project.ID)
		}
		results = append(results, result)
		if result.Error != "" {
			failures = append(failures, project.ID+": "+result.Error)
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
	return s.runProjectRefresh(ctx, pid, s.refreshConcurrency(), orchestrator.DiffMindRunOptions{}, "fleet_refresh")
}

func (s *Server) runProjectRefresh(ctx context.Context, pid string, concurrency int, opts orchestrator.DiffMindRunOptions, trigger string) ProjectRefreshResult {
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

	gitRepos := make([]store.Repo, 0, len(repos))
	for _, repo := range repos {
		if strings.TrimSpace(repo.GitURL) != "" && repo.SyncStatus != "diffmind_running" {
			gitRepos = append(gitRepos, repo)
		}
	}
	syncErrors := s.refreshSyncRepos(ctx, pid, gitRepos, concurrency)
	result.Synced = len(gitRepos) - len(syncErrors)
	for _, syncErr := range syncErrors {
		failures = append(failures, syncErr.Error())
	}

	repos, err = s.store.ListRepos(pid)
	if err != nil {
		failures = append(failures, err.Error())
		result.Error = strings.Join(failures, "; ")
		return result
	}
	serviceRepos := make([]store.Repo, 0, len(repos))
	attempted := make(map[string]struct{}, len(repos))
	for _, repo := range repos {
		if (repo.Kind == "" || repo.Kind == "service_repo") && repo.SyncStatus != "diffmind_running" {
			serviceRepos = append(serviceRepos, repo)
			attempted[repo.ID] = struct{}{}
		}
	}
	for _, repo := range serviceRepos {
		_, _ = s.store.UpdateRepo(pid, repo.ID, func(rp *store.Repo) {
			rp.SyncStatus = "diffmind_running"
			rp.SyncError = ""
		})
	}
	s.runDiffMindBatch(pid, serviceRepos, opts, concurrency)

	repos, err = s.store.ListRepos(pid)
	if err != nil {
		failures = append(failures, err.Error())
		result.Error = strings.Join(failures, "; ")
		return result
	}
	refs := make([]store.RunRepoRef, 0, len(repos))
	for _, repo := range repos {
		if _, ok := attempted[repo.ID]; ok {
			if repo.SyncStatus == "diffmind_completed" {
				result.Analyzed++
			} else {
				failure := firstNonEmpty(repo.SyncError, "analysis did not complete")
				failures = append(failures, fmt.Sprintf("analyze %s: %s", repo.ID, failure))
			}
		}
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

func (s *Server) refreshSyncRepos(ctx context.Context, pid string, repos []store.Repo, concurrency int) []error {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	for _, repo := range repos {
		repo := repo
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				failures = append(failures, ctx.Err())
				mu.Unlock()
				return
			}
			if _, err := s.syncGitRepo(ctx, pid, repo); err != nil {
				mu.Lock()
				failures = append(failures, fmt.Errorf("sync %s: %w", repo.ID, err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return failures
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
