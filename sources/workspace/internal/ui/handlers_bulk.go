package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/store"
)

type importReposRequest struct {
	Provider        string `json:"provider"`
	Org             string `json:"org"`
	APIBase         string `json:"api_base"`
	Include         string `json:"include"`
	Exclude         string `json:"exclude"`
	Team            string `json:"team"`
	DefaultBranch   string `json:"default_branch"`
	DryRun          bool   `json:"dry_run"`
	Clone           bool   `json:"clone"`
	IncludeForks    bool   `json:"include_forks"`
	IncludeArchived bool   `json:"include_archived"`
	Limit           int    `json:"limit"`
	Concurrency     int    `json:"concurrency"`
}

type importedRepoResult struct {
	Name   string `json:"name"`
	GitURL string `json:"git_url"`
	Status string `json:"status"`
	RepoID string `json:"repo_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s *Server) handleImportRepos(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	if _, err := s.store.GetProject(pid); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	var req importReposRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Provider) == "" {
		req.Provider = "github"
	}
	if req.Provider != "github" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("provider %q is not supported yet", req.Provider))
		return
	}
	if strings.TrimSpace(req.Org) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("org is required"))
		return
	}
	repos, err := githubOrgRepos(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	results := s.importGitHubRepos(pid, req, repos)
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "count": len(results)})
}

type githubRepo struct {
	Name          string `json:"name"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
}

func githubOrgRepos(ctx context.Context, req importReposRequest) ([]githubRepo, error) {
	base := strings.TrimRight(firstNonEmpty(req.APIBase, "https://api.github.com"), "/")
	token := os.Getenv("GITHUB_TOKEN")
	client := &http.Client{Timeout: 30 * time.Second}
	var out []githubRepo
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&page=%d&type=all", base, req.Org, page)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Accept", "application/vnd.github+json")
		if token != "" {
			httpReq.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, err
		}
		var pageRepos []githubRepo
		if err := json.NewDecoder(resp.Body).Decode(&pageRepos); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("github repos request failed: %s", resp.Status)
		}
		if len(pageRepos) == 0 {
			break
		}
		for _, repo := range pageRepos {
			if !req.IncludeArchived && repo.Archived {
				continue
			}
			if !req.IncludeForks && repo.Fork {
				continue
			}
			out = append(out, repo)
			if req.Limit > 0 && len(out) >= req.Limit {
				return out, nil
			}
		}
		if len(pageRepos) < 100 {
			break
		}
	}
	return out, nil
}

func (s *Server) importGitHubRepos(pid string, req importReposRequest, repos []githubRepo) []importedRepoResult {
	include := compileOptionalRegexp(req.Include)
	exclude := compileOptionalRegexp(req.Exclude)
	existing := map[string]store.Repo{}
	if current, err := s.store.ListRepos(pid); err == nil {
		for _, repo := range current {
			existing[strings.TrimSpace(repo.GitURL)] = repo
			existing[strings.TrimSpace(repo.Name)] = repo
		}
	}
	var results []importedRepoResult
	for _, gh := range repos {
		if include != nil && !include.MatchString(gh.Name) {
			continue
		}
		if exclude != nil && exclude.MatchString(gh.Name) {
			continue
		}
		gitURL := firstNonEmpty(gh.SSHURL, gh.CloneURL, gh.HTMLURL)
		result := importedRepoResult{Name: gh.Name, GitURL: gitURL, Status: "candidate"}
		if existing[gitURL].ID != "" || existing[gh.Name].ID != "" {
			result.Status = "skipped_existing"
			results = append(results, result)
			continue
		}
		if req.DryRun {
			results = append(results, result)
			continue
		}
		created, err := s.store.CreateRepo(pid, store.Repo{
			Name:          gh.Name,
			Kind:          "service_repo",
			SourceType:    "git",
			GitURL:        gitURL,
			GitProvider:   "github",
			DefaultBranch: firstNonEmpty(req.DefaultBranch, gh.DefaultBranch),
			Team:          firstNonEmpty(req.Team, "default"),
		})
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		result.Status = "imported"
		result.RepoID = created.ID
		results = append(results, result)
		if req.Clone {
			go func(repo store.Repo) {
				_, _ = s.syncGitRepo(context.Background(), pid, repo)
			}(*created)
		}
	}
	return results
}

func compileOptionalRegexp(pattern string) *regexp.Regexp {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

type batchDiffMindRunRequest struct {
	RepoIDs     []string                        `json:"repo_ids"`
	All         bool                            `json:"all"`
	SkipFresh   bool                            `json:"skip_fresh"`
	Concurrency int                             `json:"concurrency"`
	Options     orchestrator.DiffMindRunOptions `json:"options"`
}

func (s *Server) handleStartDiffMindBatchRun(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	var req batchDiffMindRunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repos, err := s.store.ListRepos(pid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	selected := selectBatchRepos(repos, req)
	if len(selected) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("no repositories selected"))
		return
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 4
	}
	if req.Concurrency > 16 {
		req.Concurrency = 16
	}
	for _, repo := range selected {
		repo := repo
		_, _ = s.store.UpdateRepo(pid, repo.ID, func(rp *store.Repo) {
			rp.SyncStatus = "diffmind_running"
			rp.SyncError = ""
		})
	}
	go s.runDiffMindBatch(pid, selected, req.Options, req.Concurrency)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "diffmind_running", "count": len(selected), "concurrency": req.Concurrency})
}

func selectBatchRepos(repos []store.Repo, req batchDiffMindRunRequest) []store.Repo {
	want := map[string]bool{}
	for _, id := range req.RepoIDs {
		want[id] = true
	}
	var out []store.Repo
	for _, repo := range repos {
		if repo.Kind != "" && repo.Kind != "service_repo" {
			continue
		}
		if !req.All && !want[repo.ID] {
			continue
		}
		if req.SkipFresh && repo.DiffMindFreshness == "fresh" {
			continue
		}
		out = append(out, repo)
	}
	return out
}

func (s *Server) runDiffMindBatch(pid string, repos []store.Repo, opts orchestrator.DiffMindRunOptions, concurrency int) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, repo := range repos {
		repo := repo
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s.runDiffMindForRepo(pid, repo.ID, repo, opts)
		}()
	}
	wg.Wait()
}
