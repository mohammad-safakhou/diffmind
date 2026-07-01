package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	Root            string `json:"root"`
	APIBase         string `json:"api_base"`
	Include         string `json:"include"`
	Exclude         string `json:"exclude"`
	Team            string `json:"team"`
	DefaultBranch   string `json:"default_branch"`
	DryRun          bool   `json:"dry_run"`
	Clone           bool   `json:"clone"`
	CloneTransport  string `json:"clone_transport"`
	IncludeForks    bool   `json:"include_forks"`
	IncludeArchived bool   `json:"include_archived"`
	Limit           int    `json:"limit"`
	Concurrency     int    `json:"concurrency"`
	Recursive       bool   `json:"recursive"`
	MaxDepth        int    `json:"max_depth"`
}

type importedRepoResult struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
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
	switch req.Provider {
	case "github":
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
	case "local":
		if strings.TrimSpace(req.Root) == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("root is required"))
			return
		}
		repos, err := localRepos(req)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		results := s.importLocalRepos(pid, req, repos)
		writeJSON(w, http.StatusOK, map[string]any{"results": results, "count": len(results)})
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("provider %q is not supported yet", req.Provider))
	}
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
	token := githubToken(ctx, base)
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
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		pageRepos, err := parseGitHubReposResponse(resp.StatusCode, resp.Status, body)
		if err != nil {
			return nil, err
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

type githubErrorResponse struct {
	Message          string `json:"message"`
	DocumentationURL string `json:"documentation_url"`
}

func parseGitHubReposResponse(statusCode int, status string, body []byte) ([]githubRepo, error) {
	if statusCode < 200 || statusCode >= 300 {
		var ghErr githubErrorResponse
		if err := json.Unmarshal(body, &ghErr); err == nil && strings.TrimSpace(ghErr.Message) != "" {
			msg := ghErr.Message
			if ghErr.DocumentationURL != "" {
				msg += " (" + ghErr.DocumentationURL + ")"
			}
			return nil, fmt.Errorf("github repos request failed: %s: %s", status, msg)
		}
		return nil, fmt.Errorf("github repos request failed: %s: %s", status, strings.TrimSpace(string(body)))
	}
	var repos []githubRepo
	if err := json.Unmarshal(body, &repos); err != nil {
		var ghErr githubErrorResponse
		if json.Unmarshal(body, &ghErr) == nil && strings.TrimSpace(ghErr.Message) != "" {
			return nil, fmt.Errorf("github repos response was an error object: %s", ghErr.Message)
		}
		return nil, fmt.Errorf("github repos response was not a repository list: %w", err)
	}
	return repos, nil
}

type localRepo struct {
	Name string
	Path string
}

func localRepos(req importReposRequest) ([]localRepo, error) {
	root, err := filepath.Abs(strings.TrimSpace(req.Root))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root is not a directory: %s", root)
	}
	limit := req.Limit
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	var out []localRepo
	addRepo := func(path string) bool {
		out = append(out, localRepo{Name: filepath.Base(path), Path: path})
		return limit > 0 && len(out) >= limit
	}
	if looksLikeRepo(root) {
		addRepo(root)
		return out, nil
	}
	if !req.Recursive {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(root, e.Name())
			if looksLikeRepo(path) && addRepo(path) {
				break
			}
		}
		return out, nil
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if name == ".git" || name == "node_modules" || name == "vendor" || name == ".idea" || name == ".vscode" {
			return filepath.SkipDir
		}
		depth := relDepth(root, path)
		if depth > maxDepth {
			return filepath.SkipDir
		}
		if looksLikeRepo(path) {
			if addRepo(path) {
				return filepath.SkipAll
			}
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func relDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}

func (s *Server) importLocalRepos(pid string, req importReposRequest, repos []localRepo) []importedRepoResult {
	include := compileOptionalRegexp(req.Include)
	exclude := compileOptionalRegexp(req.Exclude)
	existing := map[string]store.Repo{}
	if current, err := s.store.ListRepos(pid); err == nil {
		for _, repo := range current {
			existing[filepath.Clean(repo.Path)] = repo
			existing[strings.TrimSpace(repo.Name)] = repo
		}
	}
	var results []importedRepoResult
	for _, local := range repos {
		if include != nil && !include.MatchString(local.Name) {
			continue
		}
		if exclude != nil && exclude.MatchString(local.Name) {
			continue
		}
		clean := filepath.Clean(local.Path)
		result := importedRepoResult{Name: local.Name, Path: clean, Status: "candidate"}
		if existing[clean].ID != "" || existing[local.Name].ID != "" {
			result.Status = "skipped_existing"
			results = append(results, result)
			continue
		}
		if req.DryRun {
			results = append(results, result)
			continue
		}
		created, err := s.store.CreateRepo(pid, store.Repo{
			Name:       local.Name,
			Path:       clean,
			Kind:       "service_repo",
			SourceType: "local",
			Team:       firstNonEmpty(req.Team, "default"),
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
	}
	return results
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
	var toClone []store.Repo
	for _, gh := range repos {
		if include != nil && !include.MatchString(gh.Name) {
			continue
		}
		if exclude != nil && exclude.MatchString(gh.Name) {
			continue
		}
		gitURL := githubCloneURL(gh, req)
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
			SyncStatus:    map[bool]string{true: "sync_queued", false: "unknown"}[req.Clone],
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
			toClone = append(toClone, *created)
		}
	}
	if len(toClone) > 0 {
		go s.syncGitReposBatch(pid, toClone, importCloneConcurrency(req.Concurrency))
	}
	return results
}

func githubCloneURL(repo githubRepo, req importReposRequest) string {
	switch strings.ToLower(strings.TrimSpace(req.CloneTransport)) {
	case "https":
		return firstNonEmpty(repo.CloneURL, repo.HTMLURL, repo.SSHURL)
	case "ssh":
		return firstNonEmpty(repo.SSHURL, repo.CloneURL, repo.HTMLURL)
	default:
		if githubToken(context.Background(), req.APIBase) != "" {
			return firstNonEmpty(repo.CloneURL, repo.HTMLURL, repo.SSHURL)
		}
		return firstNonEmpty(repo.SSHURL, repo.CloneURL, repo.HTMLURL)
	}
}

func importCloneConcurrency(n int) int {
	if n <= 0 {
		return 4
	}
	if n > 16 {
		return 16
	}
	return n
}

func (s *Server) syncGitReposBatch(pid string, repos []store.Repo, concurrency int) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, repo := range repos {
		repo := repo
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			_, _ = s.syncGitRepo(context.Background(), pid, repo)
		}()
	}
	wg.Wait()
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
