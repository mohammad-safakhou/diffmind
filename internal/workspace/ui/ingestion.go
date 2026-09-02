package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

type ingestionRequest struct {
	Import      *importReposRequest             `json:"import,omitempty"`
	Concurrency int                             `json:"concurrency,omitempty"`
	Options     orchestrator.DiffMindRunOptions `json:"options,omitempty"`
}

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
	active, err := s.projectHasActiveWork(pid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if active {
		writeErr(w, http.StatusConflict, errors.New("project already has repository analysis or a graph build running"))
		return
	}

	s.ingestionMu.Lock()
	if s.ingestionActive[pid] {
		s.ingestionMu.Unlock()
		writeErr(w, http.StatusConflict, errors.New("project ingestion is already running"))
		return
	}
	if !s.beginProjectOperation(pid) {
		s.ingestionMu.Unlock()
		writeErr(w, http.StatusConflict, errors.New("project is already being processed"))
		return
	}
	s.ingestionActive[pid] = true
	s.ingestionMu.Unlock()

	ingestion := store.Ingestion{Status: store.IngestionRunning, Phase: "starting"}
	if req.Import != nil {
		ingestion.Provider = req.Import.Provider
		if req.Import.Provider == "github" {
			ingestion.Source = strings.TrimSpace(req.Import.Org)
		} else {
			ingestion.Source = strings.TrimSpace(req.Import.Root)
		}
	}
	created, err := s.store.CreateIngestion(pid, ingestion)
	if err != nil {
		s.finishIngestionOperation(pid)
		s.writeStoreErr(w, err)
		return
	}
	ctx := s.serverContext()
	go s.executeIngestion(ctx, pid, req, *created)
	writeJSON(w, http.StatusAccepted, created)
}

func (s *Server) executeIngestion(ctx context.Context, pid string, req ingestionRequest, ingestion store.Ingestion) {
	defer s.finishIngestionOperation(pid)
	save := func() {
		if err := s.store.SaveIngestion(pid, ingestion); err != nil {
			s.log.Error("save project ingestion", "project", pid, "error", err.Error())
		}
	}
	fail := func(err error) {
		ingestion.Status = store.IngestionFailed
		ingestion.Phase = "failed"
		ingestion.Errors = append(ingestion.Errors, err.Error())
		ingestion.FinishedAt = time.Now().UTC()
		save()
	}

	if req.Import != nil {
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
	save()

	var result ProjectRefreshResult
	if s.refreshProject != nil {
		result = s.refreshProject(ctx, pid)
	} else {
		result = s.runProjectRefresh(ctx, pid, req.Concurrency, req.Options, "ingestion")
	}
	ingestion.Synced = result.Synced
	ingestion.Analyzed = result.Analyzed
	ingestion.GraphRunID = result.GraphRunID
	if result.Error != "" {
		ingestion.Errors = append(ingestion.Errors, result.Error)
	}
	if result.GraphRunID == "" {
		fail(errors.New("no graph was started because no repository produced usable analysis artifacts"))
		return
	}

	ingestion.Phase = "building_graph"
	save()
	s.runs.WaitFor(pid, result.GraphRunID)
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
	delete(s.ingestionActive, pid)
	s.ingestionMu.Unlock()
	s.endProjectOperation(pid)
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
		ingestion.Status = store.IngestionFailed
		ingestion.Phase = "failed"
		ingestion.Errors = append(ingestion.Errors, "ingestion was interrupted by a server restart; start it again")
		ingestion.FinishedAt = time.Now().UTC()
		if saveErr := s.store.SaveIngestion(project.ID, *ingestion); saveErr != nil {
			s.log.Warn("save recovered project ingestion", "project", project.ID, "error", saveErr.Error())
		}
	}
}

func (s *Server) requireProjectIdle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pid := r.PathValue("pid")
		s.projectOpsMu.Lock()
		active := s.projectOps[pid]
		s.projectOpsMu.Unlock()
		if active {
			writeErr(w, http.StatusConflict, errors.New("project is being processed; wait for ingestion or refresh to finish"))
			return
		}
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
