package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

type OperationsConfig struct {
	Workers, Capacity, RepositoryWorkers int
	WebhookSecret                        string
}

// ConfigureOperations must run before the server starts accepting work.
func (s *Server) ConfigureOperations(cfg OperationsConfig) error {
	if cfg.Workers == 0 {
		cfg.Workers = 2
	}
	if cfg.Capacity == 0 {
		cfg.Capacity = 256
	}
	if cfg.RepositoryWorkers == 0 {
		cfg.RepositoryWorkers = 4
	}
	if cfg.Workers < 1 || cfg.Workers > 16 || cfg.Capacity < 1 || cfg.Capacity > 10000 || cfg.RepositoryWorkers < 1 || cfg.RepositoryWorkers > 32 {
		return errors.New("job workers must be 1..16, queue capacity 1..10000, repository workers 1..32")
	}
	if cfg.WebhookSecret != "" && len(cfg.WebhookSecret) < 32 {
		return errors.New("webhook secret must be at least 32 bytes")
	}
	s.operationsMu.Lock()
	defer s.operationsMu.Unlock()
	if s.operationsStarted {
		return errors.New("operations already started")
	}
	s.operationsConfig = cfg
	s.repositorySlots = make(chan struct{}, cfg.RepositoryWorkers)
	return nil
}

func (s *Server) StartOperations(parent context.Context) error {
	s.operationsMu.Lock()
	defer s.operationsMu.Unlock()
	if s.operationsStarted {
		return s.operationsError
	}
	if err := s.store.RecoverJobs(); err != nil {
		return fmt.Errorf("recover refresh jobs: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	s.operationsStop = cancel
	s.operationsStarted = true
	for i := 0; i < s.operationsConfig.Workers; i++ {
		s.operationsWG.Add(1)
		go s.operationsWorker(ctx)
	}
	return nil
}
func (s *Server) StopOperations() {
	s.operationsMu.Lock()
	cancel := s.operationsStop
	s.operationsMu.Unlock()
	if cancel != nil {
		cancel()
		s.operationsWG.Wait()
	}
}
func (s *Server) operationsWorker(ctx context.Context) {
	defer s.operationsWG.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := s.store.ClaimJob(time.Now().UTC())
		if err != nil {
			s.failOperations(err)
			return
		}
		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}
		attemptCtx, cancel := context.WithCancelCause(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			poll := time.NewTicker(50 * time.Millisecond)
			defer poll.Stop()
			for {
				select {
				case <-attemptCtx.Done():
					return
				case <-poll.C:
					latest, err := s.store.GetJob(job.ID)
					if err != nil {
						cancel(err)
						return
					}
					if latest.CancelRequested {
						cancel(errIngestionCancelled)
						return
					}
				}
			}
		}()
		result, busy := s.runQueuedRefresh(attemptCtx, *job)
		if ctx.Err() != nil {
			result.Status = "interrupted"
			result.Error = "server stopped during refresh"
		}
		cancel(nil)
		<-done
		delay := time.Duration(1<<min(len(job.Attempts), 6)) * time.Second
		if busy {
			delay = time.Second
		}
		if err := s.store.FinishJob(job.ID, result, busy, delay); err != nil {
			s.failOperations(err)
			return
		}
	}
}
func (s *Server) failOperations(err error) {
	s.operationsMu.Lock()
	s.operationsError = err
	cancel := s.operationsStop
	s.operationsMu.Unlock()
	s.log.Error("refresh scheduler stopped", "error", err.Error())
	if cancel != nil {
		cancel()
	}
}
func (s *Server) runQueuedRefresh(ctx context.Context, job store.RefreshJob) (store.JobAttempt, bool) {
	out := store.JobAttempt{Status: "failed"}
	if _, err := s.store.GetProject(job.ProjectID); err != nil {
		out.Error = err.Error()
		return out, false
	}
	if s.refreshProject != nil {
		r := s.refreshProject(ctx, job.ProjectID)
		out.GraphRunID = r.GraphRunID
		out.Synced = r.Synced
		out.Analyzed = r.Analyzed
		out.Reused = r.Reused
		out.Error = r.Error
		if r.Error == "" {
			out.Status = "succeeded"
		}
		return out, false
	}
	repos, err := s.store.ListRepos(job.ProjectID)
	if err != nil {
		out.Error = err.Error()
		return out, false
	}
	if len(repos) == 0 {
		out.Status = "succeeded"
		return out, false
	}
	req := ingestionRequest{Concurrency: s.refreshConcurrency()}
	in := store.Ingestion{JobID: job.ID, Status: store.IngestionRunning, Phase: "queued_refresh", Attempt: 1, Provider: job.Trigger}
	in.Request, _ = json.Marshal(req)
	resume := false
	if old, err := s.store.GetIngestion(job.ProjectID); err == nil && old.JobID == job.ID && old.Status != store.IngestionRunning && old.Status != store.IngestionCompleted {
		in = *old
		resume = true
	}
	created, err := s.launchIngestion(ctx, job.ProjectID, req, in, resume)
	if err != nil {
		out.Error = err.Error()
		return out, errors.Is(err, store.ErrConflict)
	}
	out.IngestionID = created.ID
	// Drain the ingestion on cancellation too. Releasing a worker before its
	// analyzer has stopped would violate the concurrency bound.
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		s.ingestionMu.Lock()
		active := s.ingestionActive[job.ProjectID]
		s.ingestionMu.Unlock()
		if !active {
			break
		}
		<-tick.C
	}
	final, err := s.store.GetIngestionAttempt(job.ProjectID, created.ID, created.Attempt)
	if err != nil {
		out.Error = err.Error()
		return out, false
	}
	if final.ID != created.ID {
		out.Error = "ingestion projection changed unexpectedly"
		return out, false
	}
	out.GraphRunID = final.GraphRunID
	out.Synced = final.Synced
	out.Analyzed = final.Analyzed
	out.Reused = final.Reused
	out.Error = strings.Join(final.Errors, "; ")
	if final.Status == store.IngestionCompleted {
		out.Status = "succeeded"
	} else if final.Status == store.IngestionCancelled {
		out.Status = "cancelled"
	}
	return out, false
}
func (s *Server) enqueueRefresh(pid, trigger, delivery, digest string) (*store.RefreshJob, bool, error) {
	s.operationsMu.Lock()
	err := s.operationsError
	capacity := s.operationsConfig.Capacity
	s.operationsMu.Unlock()
	if err != nil {
		return nil, false, fmt.Errorf("scheduler unavailable: %w", err)
	}
	return s.store.EnqueueJob(pid, trigger, delivery, digest, capacity)
}
func (s *Server) acquireRepository(ctx context.Context) (func(), error) {
	s.operationsMu.Lock()
	slots := s.repositorySlots
	s.operationsMu.Unlock()
	select {
	case slots <- struct{}{}:
		if ctx.Err() != nil {
			<-slots
			return nil, ctx.Err()
		}
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *Server) jobError(w http.ResponseWriter, err error) {
	status := 500
	switch {
	case errors.Is(err, store.ErrNotFound):
		status = 404
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrDeliveryConflict):
		status = 409
	case errors.Is(err, store.ErrQueueFull):
		status = 503
		w.Header().Set("Retry-After", "10")
	}
	writeErr(w, status, err)
}
func operationPage(r *http.Request, total int) (int, int, error) {
	offset, err := queryInteger(r, "offset", 0)
	if err != nil {
		return 0, 0, err
	}
	limit, err := queryInteger(r, "limit", 50)
	if err != nil {
		return 0, 0, err
	}
	if offset < 0 || limit < 1 || limit > 500 {
		return 0, 0, errors.New("offset must be nonnegative and limit 1..500")
	}
	start := min(offset, total)
	return start, start + min(limit, total-start), nil
}
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	offset, err := queryInteger(r, "offset", 0)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	limit, err := queryInteger(r, "limit", 50)
	if err != nil || offset < 0 || limit < 1 || limit > 500 {
		writeErr(w, 400, errors.New("offset must be nonnegative and limit 1..500"))
		return
	}
	var projects []string // nil means trusted global access, empty means denied
	identity := identityFromContext(r.Context())
	if pid := r.URL.Query().Get("project"); pid != "" {
		if _, err := s.store.GetProject(pid); err != nil {
			s.jobError(w, err)
			return
		}
		if _, err := s.projectRole(identity, pid); err != nil {
			s.writeAccessError(w, err)
			return
		}
		projects = []string{pid}
	} else if s.projectAccessScoped && identity.Role != RoleAdmin {
		projects = []string{}
		all, err := s.store.ListProjects()
		if err != nil {
			s.jobError(w, err)
			return
		}
		for _, p := range all {
			if _, err := s.projectRole(identity, p.ID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				s.writeAccessError(w, err)
				return
			}
			projects = append(projects, p.ID)
		}
	}
	page, err := s.store.JobsPage(projects, offset, limit)
	if err != nil {
		s.jobError(w, err)
		return
	}
	s.operationsMu.Lock()
	cfg := s.operationsConfig
	health := s.operationsError == nil
	s.operationsMu.Unlock()
	writeJSON(w, 200, map[string]any{"jobs": page.Jobs, "total": page.Total, "next_offset": page.NextOffset, "workers": cfg.Workers, "capacity": cfg.Capacity, "repository_workers": cfg.RepositoryWorkers, "healthy": health})
}
func (s *Server) handleEnqueueJob(w http.ResponseWriter, r *http.Request) {
	j, dup, err := s.enqueueRefresh(r.PathValue("pid"), "manual", "", "")
	if err != nil {
		s.jobError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"job": j, "duplicate": dup})
}
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.store.CancelJob(r.PathValue("jid"))
	if err != nil {
		s.jobError(w, err)
		return
	}
	writeJSON(w, 202, j)
}
func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	s.operationsMu.Lock()
	capacity := s.operationsConfig.Capacity
	s.operationsMu.Unlock()
	j, err := s.store.RetryJob(r.PathValue("jid"), capacity)
	if err != nil {
		s.jobError(w, err)
		return
	}
	writeJSON(w, 202, j)
}
func (s *Server) handleIngestionHistory(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.IngestionHistory(r.PathValue("pid"))
	if err != nil {
		s.jobError(w, err)
		return
	}
	start, end, err := operationPage(r, len(all))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var next *int
	if end < len(all) {
		next = &end
	}
	writeJSON(w, 200, map[string]any{"attempts": all[start:end], "total": len(all), "next_offset": next})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.JobStatistics()
	if err != nil {
		s.jobError(w, err)
		return
	}
	states := []string{"queued", "running", "succeeded", "failed", "cancelled", "interrupted"}
	counts, attempts, duration := stats.Jobs, stats.Attempts, stats.Duration
	s.operationsMu.Lock()
	cfg := s.operationsConfig
	active := len(s.repositorySlots)
	healthy := s.operationsError == nil
	s.operationsMu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# HELP diffmind_refresh_jobs Current retained jobs by status.\n# TYPE diffmind_refresh_jobs gauge")
	sort.Strings(states)
	for _, state := range states {
		fmt.Fprintf(w, "diffmind_refresh_jobs{status=%q} %d\n", state, counts[state])
	}
	fmt.Fprintln(w, "# HELP diffmind_refresh_attempts Retained attempts by status.\n# TYPE diffmind_refresh_attempts gauge")
	for _, state := range states {
		fmt.Fprintf(w, "diffmind_refresh_attempts{status=%q} %d\n", state, attempts[state])
	}
	fmt.Fprintf(w, "# TYPE diffmind_refresh_attempt_duration_seconds gauge\ndiffmind_refresh_attempt_duration_seconds %g\n# TYPE diffmind_repository_operations_active gauge\ndiffmind_repository_operations_active %d\n# TYPE diffmind_repository_operations_limit gauge\ndiffmind_repository_operations_limit %d\n# TYPE diffmind_refresh_workers gauge\ndiffmind_refresh_workers %d\n", duration, active, cfg.RepositoryWorkers, cfg.Workers)
	health := 0
	if healthy {
		health = 1
	}
	fmt.Fprintf(w, "# TYPE diffmind_scheduler_healthy gauge\ndiffmind_scheduler_healthy %d\n", health)
}
