package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// Called with repositoryMu held. No waiter holds a global slot while waiting
// for its project: both budgets are admitted atomically, avoiding head-of-line
// blocking of other projects. Caps are not throughput reservations or FIFO.
func (s *Server) notifyRepositoryWaiters() {
	close(s.repositoryChanged)
	s.repositoryChanged = make(chan struct{})
}

func (s *Server) acquireRepository(ctx context.Context, pid string) (func(), error) {
	for {
		// Lock order: operationsMu -> repositoryMu -> store.mu.
		s.operationsMu.Lock()
		s.repositoryMu.Lock()
		global := s.operationsConfig.RepositoryWorkers
		s.operationsMu.Unlock()
		if err := ctx.Err(); err != nil {
			s.repositoryMu.Unlock()
			return nil, err
		}
		policy, err := s.store.GetProjectLimits(pid)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			s.repositoryMu.Unlock()
			return nil, err
		}
		if s.repositoryTotal < global && s.repositoryActive[pid] < store.EffectiveLimit(policy.RepositoryWorkers, global) {
			s.repositoryTotal++
			s.repositoryActive[pid]++
			s.repositoryMu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					s.repositoryMu.Lock()
					defer s.repositoryMu.Unlock()
					s.repositoryTotal--
					s.repositoryActive[pid]--
					if s.repositoryActive[pid] == 0 {
						delete(s.repositoryActive, pid)
					}
					s.notifyRepositoryWaiters()
				})
			}, nil
		}
		changed := s.repositoryChanged
		s.repositoryMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (s *Server) putProjectLimits(pid string, revision, pending, workers int) (*store.ProjectLimits, error) {
	s.repositoryMu.Lock()
	defer s.repositoryMu.Unlock()
	policy, err := s.store.PutProjectLimits(pid, revision, pending, workers)
	// Wake even on an uncertain write result: re-read durable state, never keep
	// admitting under an old value after an acknowledged policy change.
	s.notifyRepositoryWaiters()
	return policy, err
}

type projectLimitsResponse struct {
	Limits                     *store.ProjectLimits `json:"limits"`
	EffectivePendingJobs       int                  `json:"effective_pending_jobs"`
	EffectiveRepositoryWorkers int                  `json:"effective_repository_workers"`
	PendingJobs                int                  `json:"pending_jobs"`
	ActiveRepositoryWorkers    int                  `json:"active_repository_workers"`
}

func (s *Server) handleGetLimits(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	policy, err := s.store.GetProjectLimits(pid)
	if err != nil {
		s.limitsError(w, err)
		return
	}
	pending, err := s.store.PendingJobCount(pid)
	if err != nil {
		s.limitsError(w, err)
		return
	}
	s.operationsMu.Lock()
	cfg := s.operationsConfig
	s.operationsMu.Unlock()
	s.repositoryMu.Lock()
	active := s.repositoryActive[pid]
	s.repositoryMu.Unlock()
	// Observational snapshot; admission itself uses locks/transactions.
	writeJSON(w, 200, projectLimitsResponse{policy, store.EffectiveLimit(policy.MaxPendingJobs, cfg.Capacity), store.EffectiveLimit(policy.RepositoryWorkers, cfg.RepositoryWorkers), pending, active})
}

func (s *Server) handlePutLimits(w http.ResponseWriter, r *http.Request) {
	if identityFromContext(r.Context()).Role != RoleAdmin {
		writeErr(w, 403, errors.New("administrator required"))
		return
	}
	var req struct {
		Revision          *int `json:"revision"`
		MaxPendingJobs    *int `json:"max_pending_jobs"`
		RepositoryWorkers *int `json:"repository_workers"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		writeErr(w, 400, store.ErrInvalidLimits)
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF || req.Revision == nil || req.MaxPendingJobs == nil || req.RepositoryWorkers == nil || *req.Revision < 0 {
		writeErr(w, 400, store.ErrInvalidLimits)
		return
	}
	policy, err := s.putProjectLimits(r.PathValue("pid"), *req.Revision, *req.MaxPendingJobs, *req.RepositoryWorkers)
	if err != nil {
		s.limitsError(w, err)
		return
	}
	writeJSON(w, 200, policy)
}

func (s *Server) limitsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, 404, store.ErrNotFound)
	case errors.Is(err, store.ErrConflict):
		writeErr(w, 409, err)
	case errors.Is(err, store.ErrInvalidLimits):
		writeErr(w, 400, store.ErrInvalidLimits)
	default:
		writeErr(w, 503, store.ErrLimitsUnavailable)
	}
}
