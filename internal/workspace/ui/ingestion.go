package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

type ingestionRequest struct {
	Import      *importReposRequest             `json:"import,omitempty"`
	Concurrency int                             `json:"concurrency,omitempty"`
	Options     orchestrator.DiffMindRunOptions `json:"options,omitempty"`
	Force       bool                            `json:"force,omitempty"`
}

var errIngestionCancelled = errors.New("ingestion cancelled by user")

func (s *Server) handleGetIngestion(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	ingestion, err := s.store.GetIngestion(pid)
	if errors.Is(err, store.ErrNotFound) {
		if _, projectErr := s.store.GetProject(pid); projectErr != nil {
			s.writeStoreErr(w, projectErr)
			return
		}
		writeJSON(w, http.StatusOK, store.Ingestion{ProjectID: pid, Status: "not_started", Phase: "idle"})
		return
	}
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ingestion)
}

func (s *Server) handleStartIngestion(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	if _, err := s.store.GetProject(pid); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	var req ingestionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Import != nil {
		if req.Import.DryRun {
			writeErr(w, http.StatusBadRequest, errors.New("dry_run is not supported for ingestion; use repository import preview"))
			return
		}
		if err := validateImportReposRequest(req.Import); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if req.Concurrency < 0 || req.Concurrency > 16 {
		writeErr(w, http.StatusBadRequest, errors.New("concurrency must be between 0 and 16"))
		return
	}
	req.Concurrency = importCloneConcurrency(req.Concurrency)
	ingestion := store.Ingestion{Status: store.IngestionRunning, Phase: "starting", Attempt: 1}
	ingestion.Request, _ = json.Marshal(req)
	if req.Import != nil {
		ingestion.Provider = req.Import.Provider
		if req.Import.Provider == "github" {
			ingestion.Source = strings.TrimSpace(req.Import.Org)
		} else {
			ingestion.Source = strings.TrimSpace(req.Import.Root)
		}
	}
	created, err := s.launchIngestion(s.serverContext(), pid, req, ingestion, false)
	if err != nil {
		s.writeIngestionStartError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, created)
}

func (s *Server) launchIngestion(parent context.Context, pid string, req ingestionRequest, ingestion store.Ingestion, resume bool) (*store.Ingestion, error) {
	s.ingestionMu.Lock()
	defer s.ingestionMu.Unlock()
	if s.ingestionActive[pid] || !s.beginProjectOperation(pid) {
		return nil, store.ErrConflict
	}
	active, err := s.projectHasActiveWork(pid)
	if err != nil || active {
		s.endProjectOperation(pid)
		if err != nil {
			return nil, err
		}
		return nil, store.ErrConflict
	}
	var created *store.Ingestion
	if resume {
		// Read while holding the operation lock, so an old request cannot
		// overwrite the status of a newer ingestion.
		current, readErr := s.store.GetIngestion(pid)
		if readErr != nil || current.ID != ingestion.ID || current.Status != ingestion.Status || current.Attempt != ingestion.Attempt || current.Status == store.IngestionRunning {
			s.endProjectOperation(pid)
			return nil, store.ErrConflict
		}
		ingestion.Attempt = max(1, ingestion.Attempt) + 1
		ingestion.AttemptStartedAt = time.Now().UTC()
		ingestion.CancelRequested = false
		ingestion.Status = store.IngestionRunning
		ingestion.Phase = "resuming"
		ingestion.Errors = nil
		ingestion.FinishedAt = time.Time{}
		ingestion.GraphRunID = ""
		ingestion.Analyzed, ingestion.Reused, ingestion.Synced = 0, 0, 0
		ingestion.RepoProgress = nil
		err = s.store.SaveIngestion(pid, ingestion)
		created = &ingestion
	} else {
		created, err = s.store.CreateIngestion(pid, ingestion)
	}
	if err != nil {
		s.endProjectOperation(pid)
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	s.ingestionActive[pid] = true
	s.ingestionCancel[pid] = cancel
	go s.executeIngestion(ctx, pid, req, *created)
	return created, nil
}

func (s *Server) writeIngestionStartError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrConflict) {
		writeErr(w, http.StatusConflict, errors.New("project is already being processed"))
		return
	}
	s.writeStoreErr(w, err)
}

func (s *Server) handleCancelIngestion(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	if _, err := s.store.GetProject(pid); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.ingestionMu.Lock()
	cancel := s.ingestionCancel[pid]
	if cancel != nil {
		if err := s.store.RequestIngestionCancellation(pid); err != nil {
			s.ingestionMu.Unlock()
			s.writeIngestionStartError(w, err)
			return
		}
		cancel(errIngestionCancelled)
	}
	s.ingestionMu.Unlock()
	if cancel == nil {
		writeErr(w, http.StatusConflict, errors.New("no active ingestion"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}

func (s *Server) handleResumeIngestion(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	ingestion, err := s.store.GetIngestion(pid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if ingestion.JobID != "" {
		writeErr(w, http.StatusConflict, errors.New("this ingestion belongs to a refresh job; retry it from Operations"))
		return
	}
	if ingestion.Status == store.IngestionRunning || ingestion.Status == store.IngestionCompleted {
		writeErr(w, http.StatusConflict, errors.New("only interrupted, failed, partial, or cancelled ingestions can be resumed"))
		return
	}
	var req ingestionRequest
	if len(ingestion.Request) == 0 || json.Unmarshal(ingestion.Request, &req) != nil {
		writeErr(w, http.StatusConflict, errors.New("this ingestion predates resumable jobs; start a new import and build"))
		return
	}
	req.Force = false
	created, err := s.launchIngestion(s.serverContext(), pid, req, *ingestion, true)
	if err != nil {
		s.writeIngestionStartError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, created)
}

func (s *Server) executeIngestion(ctx context.Context, pid string, req ingestionRequest, ingestion store.Ingestion) {
	defer s.finishIngestionOperation(pid)
	ctx, cancelWork := context.WithCancelCause(ctx)
	defer cancelWork(nil)
	save := func() {
		if err := s.store.SaveIngestion(pid, ingestion); err != nil {
			s.log.Error("save project ingestion", "project", pid, "error", err.Error())
			cancelWork(fmt.Errorf("persist ingestion checkpoint: %w", err))
		}
	}
	fail := func(err error) {
		ingestion.Status = store.IngestionFailed
		ingestion.Phase = "failed"
		if ctx.Err() != nil {
			err = context.Cause(ctx)
			if ingestion.GraphRunID != "" {
				s.runs.Cancel(pid, ingestion.GraphRunID)
				s.runs.WaitFor(pid, ingestion.GraphRunID)
			}
			ingestion.Status = store.IngestionInterrupted
			if errors.Is(context.Cause(ctx), errIngestionCancelled) {
				ingestion.Status = store.IngestionCancelled
			}
			ingestion.Phase = ingestion.Status
		}
		ingestion.Errors = append(ingestion.Errors, err.Error())
		ingestion.FinishedAt = time.Now().UTC()
		save()
	}

	if req.Import != nil && !ingestion.ImportComplete {
		ingestion.Discovered, ingestion.Imported, ingestion.Skipped = 0, 0, 0
		ingestion.Phase = "discovering"
		save()
		importReq := *req.Import
		importReq.Clone = false // the project pipeline performs one coordinated sync
		var results []importedRepoResult
		switch importReq.Provider {
		case "github":
			repos, err := githubOrgRepos(ctx, importReq)
			if err != nil {
				fail(err)
				return
			}
			results = s.importGitHubRepos(pid, importReq, repos)
		case "local":
			repos, err := localRepos(importReq)
			if err != nil {
				fail(err)
				return
			}
			results = s.importLocalRepos(pid, importReq, repos)
		}
		ingestion.Phase = "importing"
		ingestion.Discovered = len(results)
		for _, result := range results {
			switch result.Status {
			case "imported":
				ingestion.Imported++
			case "skipped_existing":
				ingestion.Skipped++
			case "failed":
				ingestion.Errors = append(ingestion.Errors, fmt.Sprintf("import %s: %s", result.Name, result.Error))
			}
		}
		ingestion.ImportComplete = len(ingestion.Errors) == 0
		save()
	}

	if err := ctx.Err(); err != nil {
		fail(err)
		return
	}
	repos, err := s.store.ListRepos(pid)
	if err != nil {
		fail(err)
		return
	}
	ingestion.Repositories = len(repos)
	if len(repos) == 0 {
		fail(errors.New("no repositories were found; adjust the import source or filters"))
		return
	}
	ingestion.Phase = "processing_repositories"
	for _, repo := range repos {
		ingestion.RepoProgress = append(ingestion.RepoProgress, store.IngestionRepo{RepoID: repo.ID, Status: "pending"})
	}
	save()

	var result ProjectRefreshResult
	if s.refreshProject != nil {
		result = s.refreshProject(ctx, pid)
	} else {
		var progressMu sync.Mutex
		result = s.runProjectRefreshWithControl(ctx, pid, req.Concurrency, req.Options, "ingestion", req.Force, func(progress store.IngestionRepo) {
			progressMu.Lock()
			defer progressMu.Unlock()
			for i := range ingestion.RepoProgress {
				if ingestion.RepoProgress[i].RepoID == progress.RepoID {
					ingestion.RepoProgress[i] = progress
					break
				}
			}
			ingestion.Analyzed, ingestion.Reused = 0, 0
			for _, p := range ingestion.RepoProgress {
				if p.Status == "completed" {
					ingestion.Analyzed++
				}
				if p.Status == "reused" {
					ingestion.Reused++
				}
			}
			save()
		})
	}
	ingestion.Synced = result.Synced
	ingestion.Analyzed = result.Analyzed
	ingestion.Reused = result.Reused
	ingestion.GraphRunID = result.GraphRunID
	if result.Error != "" {
		ingestion.Errors = append(ingestion.Errors, result.Error)
	}
	if ctx.Err() != nil {
		fail(ctx.Err())
		return
	}
	if result.GraphRunID == "" {
		fail(errors.New("no graph was started because no repository produced usable analysis artifacts"))
		return
	}

	ingestion.Phase = "building_graph"
	save()
	if err := s.waitForGraph(ctx, pid, result.GraphRunID); err != nil {
		fail(err)
		return
	}
	graphRun, err := s.store.GetRun(pid, result.GraphRunID)
	if err != nil {
		fail(fmt.Errorf("read graph run: %w", err))
		return
	}
	if graphRun.Status != store.RunCompleted {
		failure := firstNonEmpty(graphRun.Error, "graph build ended with status "+graphRun.Status)
		fail(errors.New(failure))
		return
	}

	ingestion.Phase = "complete"
	ingestion.Status = store.IngestionCompleted
	if len(ingestion.Errors) > 0 {
		ingestion.Status = store.IngestionPartial
	}
	ingestion.FinishedAt = time.Now().UTC()
	save()
}

func (s *Server) finishIngestionOperation(pid string) {
	s.ingestionMu.Lock()
	if cancel := s.ingestionCancel[pid]; cancel != nil {
		cancel(nil)
	}
	delete(s.ingestionCancel, pid)
	delete(s.ingestionActive, pid)
	s.endProjectOperation(pid)
	s.ingestionMu.Unlock()
}

func (s *Server) serverContext() context.Context {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshContext != nil {
		return s.refreshContext
	}
	return context.Background()
}

func (s *Server) recoverInterruptedIngestions() {
	projects, err := s.store.ListProjects()
	if err != nil {
		s.log.Warn("recover project ingestions", "error", err.Error())
		return
	}
	for _, project := range projects {
		runs, _ := s.store.ListRuns(project.ID)
		for _, run := range runs {
			if (run.Status == store.RunRunning || run.Status == store.RunCancelling) && !s.runs.IsActive(project.ID, run.ID) {
				run.Status = store.RunFailed
				run.Error = "graph build interrupted by server restart"
				run.FinishedAt = time.Now().UTC()
				_ = s.store.SaveRun(project.ID, run)
			}
		}
		repos, reposErr := s.store.ListRepos(project.ID)
		if reposErr != nil {
			s.log.Warn("recover repository operations", "project", project.ID, "error", reposErr.Error())
		} else {
			for _, repo := range repos {
				if repo.SyncStatus != "diffmind_running" && repo.SyncStatus != "syncing" {
					continue
				}
				_, _ = s.store.UpdateRepo(project.ID, repo.ID, func(current *store.Repo) {
					current.SyncStatus = "diffmind_failed"
					current.SyncError = "operation was interrupted by a server restart; run ingestion or analysis again"
				})
			}
		}
		ingestion, getErr := s.store.GetIngestion(project.ID)
		if errors.Is(getErr, store.ErrNotFound) {
			continue
		}
		if getErr != nil {
			s.log.Warn("recover project ingestion", "project", project.ID, "error", getErr.Error())
			continue
		}
		if ingestion.Status != store.IngestionRunning {
			continue
		}
		ingestion.Status = store.IngestionInterrupted
		if ingestion.CancelRequested {
			ingestion.Status = store.IngestionCancelled
		}
		ingestion.Phase = ingestion.Status
		ingestion.Errors = append(ingestion.Errors, "ingestion was interrupted by a server restart; completed repository checkpoints will be reused on resume")
		ingestion.FinishedAt = time.Now().UTC()
		if saveErr := s.store.SaveIngestion(project.ID, *ingestion); saveErr != nil {
			s.log.Warn("save recovered project ingestion", "project", project.ID, "error", saveErr.Error())
		}
	}
}

// Automatic recovery starts only when the server starts, after its lifetime
// context and configuration are installed. Explicit user cancellations stay stopped.
func (s *Server) resumeInterruptedIngestions(ctx context.Context) {
	projects, err := s.store.ListProjects()
	if err != nil {
		s.log.Warn("list interrupted ingestions", "error", err.Error())
		return
	}
	for _, project := range projects {
		ingestion, err := s.store.GetIngestion(project.ID)
		if err != nil || ingestion.JobID != "" || ingestion.Status != store.IngestionInterrupted || len(ingestion.Request) == 0 {
			continue
		}
		var req ingestionRequest
		if err := json.Unmarshal(ingestion.Request, &req); err != nil {
			s.log.Warn("decode interrupted ingestion", "error", err.Error())
			continue
		}
		req.Force = false
		if _, err := s.launchIngestion(ctx, project.ID, req, *ingestion, true); err != nil {
			s.log.Warn("resume ingestion", "project", project.ID, "error", err.Error())
		}
	}
}

func (s *Server) waitForGraph(ctx context.Context, pid, runID string) error {
	done := make(chan struct{})
	go func() { s.runs.WaitFor(pid, runID); close(done) }()
	select {
	case <-ctx.Done():
		s.runs.Cancel(pid, runID)
		<-done
		return ctx.Err()
	case <-done:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	run, err := s.store.GetRun(pid, runID)
	if err != nil {
		return err
	}
	if run.Status != store.RunCompleted {
		return errors.New(firstNonEmpty(run.Error, "graph build ended with status "+run.Status))
	}
	return nil
}

func (s *Server) requireProjectIdle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pid := r.PathValue("pid")
		if !s.beginProjectOperation(pid) {
			writeErr(w, http.StatusConflict, errors.New("project is being processed; wait for ingestion or refresh to finish"))
			return
		}
		defer s.endProjectOperation(pid)
		next(w, r)
	}
}

func (s *Server) projectHasActiveWork(pid string) (bool, error) {
	repos, err := s.store.ListRepos(pid)
	if err != nil {
		return false, err
	}
	for _, repo := range repos {
		if repo.SyncStatus == "diffmind_running" || repo.SyncStatus == "syncing" {
			return true, nil
		}
	}
	runs, err := s.store.ListRuns(pid)
	if err != nil {
		return false, err
	}
	for _, run := range runs {
		if s.runs.IsActive(pid, run.ID) {
			return true, nil
		}
	}
	return false, nil
}
