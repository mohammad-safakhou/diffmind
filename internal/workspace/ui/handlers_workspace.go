package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

type workspaceResponse struct {
	Project      *store.Project                         `json:"project"`
	Repos        []workspaceRepo                        `json:"repos"`
	Teams        []workspaceTeam                        `json:"teams"`
	CurrentRun   *store.RunManifest                     `json:"current_run,omitempty"`
	LatestRun    *store.RunManifest                     `json:"latest_run,omitempty"`
	Graph        *ArchGraph                             `json:"graph,omitempty"`
	LiveStatus   map[string]repoLive                    `json:"live_status"`
	DiffMindRuns map[string][]artifacts.DiffMindRunInfo `json:"diffmind_runs"`
	GeneratedAt  time.Time                              `json:"generated_at"`
}

type workspaceRepo struct {
	store.Repo
	LatestDiffMindRun *artifacts.DiffMindRunInfo `json:"latest_diffmind_run,omitempty"`
	RepoMetrics       *model.RepoMetrics         `json:"repo_metrics,omitempty"`
	EffectiveTeam     string                     `json:"effective_team"`
	Freshness         string                     `json:"freshness"`
}

type workspaceTeam struct {
	Name         string   `json:"name"`
	RepoIDs      []string `json:"repo_ids"`
	ServiceNames []string `json:"service_names"`
}

type repoLive struct {
	Provider     string    `json:"provider"`
	PullRequests int       `json:"pull_requests,omitempty"`
	Issues       int       `json:"issues,omitempty"`
	ActionsState string    `json:"actions_state,omitempty"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
}

const liveStatusTTL = 60 * time.Second

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	project, err := s.store.GetProject(pid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	runGroups, err := artifacts.DiscoverDiffMindRunsByRepo(s.diffmindRunsDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	repos, err := s.workspaceReposWithRuns(pid, runGroups)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	current := s.latestWorkspaceRun(pid)
	s.enrichRunGraphCounts(pid, current)
	var latest *store.RunManifest
	var graph *ArchGraph
	if workspaceIncludesGraph(r) {
		latest, graph = s.latestWorkspaceGraph(pid, repos)
	} else {
		latest = s.latestCompletedWorkspaceRun(pid)
		s.enrichRunGraphCounts(pid, latest)
	}
	live := s.cachedLiveStatusForRepos(repos)
	teams := workspaceTeams(repos, graph)
	runs := map[string][]artifacts.DiffMindRunInfo{}
	for _, wr := range repos {
		rr := runGroups[wr.Path]
		if len(rr) == 0 && wr.LastDiffMindRunID != "" {
			if info, ok := artifacts.DiffMindRunByID(s.diffmindRunsDir, wr.LastDiffMindRunID); ok {
				if artifacts.RunMatchesRepo(info, wr.Name, wr.ID, wr.Path) {
					rr = []artifacts.DiffMindRunInfo{info}
				}
			}
		}
		runs[wr.ID] = rr
	}
	writeJSON(w, http.StatusOK, workspaceResponse{
		Project: project, Repos: repos, Teams: teams, CurrentRun: current, LatestRun: latest, Graph: graph,
		LiveStatus: live, DiffMindRuns: runs, GeneratedAt: time.Now().UTC(),
	})
}

func workspaceIncludesGraph(r *http.Request) bool {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("graph")))
	return raw != "0" && raw != "false" && raw != "metadata"
}

func (s *Server) workspaceRepos(pid string) ([]workspaceRepo, error) {
	groups, err := artifacts.DiscoverDiffMindRunsByRepo(s.diffmindRunsDir)
	if err != nil {
		return nil, err
	}
	return s.workspaceReposWithRuns(pid, groups)
}

func (s *Server) workspaceReposWithRuns(pid string, runGroups map[string][]artifacts.DiffMindRunInfo) ([]workspaceRepo, error) {
	repos, err := s.store.ListRepos(pid)
	if err != nil {
		return nil, err
	}
	out := make([]workspaceRepo, 0, len(repos))
	for _, repo := range repos {
		runs := runGroups[repo.Path]
		var latest *artifacts.DiffMindRunInfo
		if len(runs) > 0 {
			copy := runs[0]
			latest = &copy
		} else if repo.LastDiffMindRunID != "" {
			if info, ok := artifacts.DiffMindRunByID(s.diffmindRunsDir, repo.LastDiffMindRunID); ok {
				if artifacts.RunMatchesRepo(info, repo.Name, repo.ID, repo.Path) {
					latest = &info
					runs = []artifacts.DiffMindRunInfo{info}
				}
			}
		}
		team := firstNonEmpty(repo.Team, "default")
		var metrics *model.RepoMetrics
		if latest != nil {
			team = firstNonEmpty(latest.Team, team)
			metrics = latest.RepoMetrics
		}
		freshness := diffmindFreshness(repo, latest)
		out = append(out, workspaceRepo{Repo: repo, LatestDiffMindRun: latest, RepoMetrics: metrics, EffectiveTeam: team, Freshness: freshness})
	}
	return out, nil
}

func (s *Server) diffmindRunsForRepo(repoPath string) ([]artifacts.DiffMindRunInfo, error) {
	groups, err := artifacts.DiscoverDiffMindRunsByRepo(s.diffmindRunsDir)
	if err != nil {
		return nil, err
	}
	return groups[repoPath], nil
}

func diffmindFreshness(repo store.Repo, latest *artifacts.DiffMindRunInfo) string {
	if latest == nil || latest.RepoGitSHA == "" {
		return "unknown"
	}
	remote := firstNonEmpty(repo.RemoteHeadSHA, repo.HeadSHA)
	if remote == "" {
		return "unknown"
	}
	if latest.RepoGitSHA == remote {
		return "fresh"
	}
	return "stale"
}

func (s *Server) latestWorkspaceGraph(pid string, repos []workspaceRepo) (*store.RunManifest, *ArchGraph) {
	runs, err := s.store.ListRuns(pid)
	if err != nil {
		return nil, nil
	}
	for _, run := range runs {
		if run.Status != store.RunCompleted {
			continue
		}
		graph, err := s.persistedArchGraphForRun(pid, run.ID)
		if err != nil {
			graph, err = s.archGraphForRun(pid, &run)
		}
		if err != nil {
			continue
		}
		run.ServiceCount = len(graph.Services)
		run.EdgeCount = len(graph.Edges)
		if serviceCount, edgeCount, quality := s.runs.ArchitectureStats(pid, run); quality != nil {
			run.ServiceCount = serviceCount
			run.EdgeCount = edgeCount
			run.GraphQuality = quality
		}
		annotateGraphServices(graph, repos)
		return &run, graph
	}
	return nil, nil
}

func (s *Server) persistedArchGraphForRun(pid, rid string) (*ArchGraph, error) {
	path := filepath.Join(s.store.RunDir(pid, rid), "graph.json")
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	cacheKey := pid + "\x00" + rid
	s.archGraphMu.Lock()
	if entry, ok := s.archGraphCache[cacheKey]; ok && entry.modTime.Equal(info.ModTime()) && entry.size == info.Size() {
		s.archGraphMu.Unlock()
		return entry.graph, nil
	}
	s.archGraphMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var graph ArchGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, err
	}
	s.archGraphMu.Lock()
	s.archGraphCache[cacheKey] = archGraphCacheEntry{graph: &graph, modTime: info.ModTime(), size: info.Size()}
	s.archGraphMu.Unlock()
	return &graph, nil
}

func (s *Server) persistedArchGraphForRunFast(pid, rid string, r *http.Request) (*ArchGraph, error) {
	if archGraphRequestOverview(r) {
		if data, err := os.ReadFile(filepath.Join(s.store.RunDir(pid, rid), "graph-overview.json")); err == nil {
			var graph ArchGraph
			if err := json.Unmarshal(data, &graph); err == nil {
				return &graph, nil
			}
		}
	}
	graph, err := s.persistedArchGraphForRun(pid, rid)
	if err != nil {
		return nil, err
	}
	return graph, nil
}

func (s *Server) persistArchGraphFiles(pid, rid string, graph *ArchGraph) error {
	if graph == nil {
		return nil
	}
	data, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.store.RunDir(pid, rid), "graph.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	overviewData, err := json.MarshalIndent(archGraphView(graph, &http.Request{URL: &url.URL{RawQuery: "view=overview"}}), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.store.RunDir(pid, rid), "graph-overview.json"), append(overviewData, '\n'), 0o644)
}

func (s *Server) latestWorkspaceRun(pid string) *store.RunManifest {
	runs, err := s.store.ListRuns(pid)
	if err != nil || len(runs) == 0 {
		return nil
	}
	run := runs[0]
	return &run
}

func (s *Server) latestCompletedWorkspaceRun(pid string) *store.RunManifest {
	runs, err := s.store.ListRuns(pid)
	if err != nil {
		return nil
	}
	for _, run := range runs {
		if run.Status == store.RunCompleted {
			copy := run
			return &copy
		}
	}
	return nil
}

func (s *Server) archGraphForRun(pid string, mft *store.RunManifest) (*ArchGraph, error) {
	serviceRepoDirs := map[string]string{}
	for _, ref := range mft.Repos {
		repo, err := s.store.GetRepo(pid, ref.RepoID)
		if err != nil || repo.Kind == "infra_repo" || ref.DiffMindRunID == "" {
			continue
		}
		if info, ok := artifacts.DiffMindRunByID(s.diffmindRunsDir, ref.DiffMindRunID); !ok || !artifacts.RunMatchesRepo(info, repo.Name, repo.ID, repo.Path) {
			continue
		}
		serviceRepoDirs[repo.Name] = filepath.Join(s.diffmindRunsDir, ref.DiffMindRunID)
	}
	if len(serviceRepoDirs) == 0 {
		return nil, errors.New("no service repos with diffmind artifacts in this run")
	}
	return buildArchitectureGraph(mft.ID, serviceRepoDirs), nil
}

func annotateGraphServices(graph *ArchGraph, repos []workspaceRepo) {
	if graph == nil {
		return
	}
	byName := map[string]workspaceRepo{}
	for _, r := range repos {
		byName[r.Name] = r
	}
	for _, svc := range graph.Services {
		if r, ok := byName[svc.Name]; ok {
			svc.RepoID = r.ID
			svc.RepoPath = r.Path
			svc.Team = r.EffectiveTeam
			svc.DiffMindFreshness = r.Freshness
			svc.RepoMetrics = r.RepoMetrics
		}
	}
}

func workspaceTeams(repos []workspaceRepo, graph *ArchGraph) []workspaceTeam {
	teams := map[string]*workspaceTeam{}
	for _, repo := range repos {
		name := firstNonEmpty(repo.EffectiveTeam, "default")
		t := teams[name]
		if t == nil {
			t = &workspaceTeam{Name: name}
			teams[name] = t
		}
		t.RepoIDs = append(t.RepoIDs, repo.ID)
	}
	if graph != nil {
		for _, svc := range graph.Services {
			name := firstNonEmpty(svc.Team, "default")
			t := teams[name]
			if t == nil {
				t = &workspaceTeam{Name: name}
				teams[name] = t
			}
			t.ServiceNames = append(t.ServiceNames, svc.Name)
		}
	}
	out := make([]workspaceTeam, 0, len(teams))
	for _, t := range teams {
		sort.Strings(t.RepoIDs)
		sort.Strings(t.ServiceNames)
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) handleSyncRepo(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	repo, err := s.store.GetRepo(pid, rid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if repo.SourceType != "git" && repo.GitURL == "" {
		info := inspectLocalGit(r.Context(), repo.Path, repo.DefaultBranch)
		updated, err := s.store.UpdateRepo(pid, rid, func(rp *store.Repo) {
			applyGitInfo(rp, info)
			rp.SyncStatus = "synced"
			rp.SyncError = ""
			rp.LastSyncedAt = time.Now().UTC()
		})
		if err != nil {
			s.writeStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
		return
	}
	updated, err := s.syncGitRepo(r.Context(), pid, *repo)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) syncGitRepo(ctx context.Context, pid string, repo store.Repo) (*store.Repo, error) {
	_, _ = s.store.UpdateRepo(pid, repo.ID, func(rp *store.Repo) {
		rp.SyncStatus = "syncing"
		rp.SyncError = ""
	})
	clonePath := firstNonEmpty(repo.ClonePath, s.store.WorktreeDir(pid, repo.ID))
	if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(clonePath, ".git")); os.IsNotExist(err) {
		if err := gitCommand(ctx, "", repo.GitURL, "clone", repo.GitURL, clonePath); err != nil {
			return s.markRepoSyncFailed(pid, repo.ID, err)
		}
	} else {
		if err := gitCommand(ctx, clonePath, repo.GitURL, "fetch", "--prune", "origin"); err != nil {
			return s.markRepoSyncFailed(pid, repo.ID, err)
		}
		info := inspectLocalGit(ctx, clonePath, repo.DefaultBranch)
		if info.Dirty {
			return s.markRepoSyncFailed(pid, repo.ID, fmt.Errorf("managed checkout has local changes; refusing to overwrite them"))
		}
		if info.RemoteHead != "" && info.Head != info.RemoteHead {
			if err := gitCommand(ctx, clonePath, repo.GitURL, "checkout", "--detach", "origin/"+info.Branch); err != nil {
				return s.markRepoSyncFailed(pid, repo.ID, err)
			}
		}
	}
	info := inspectLocalGit(ctx, clonePath, repo.DefaultBranch)
	return s.store.UpdateRepo(pid, repo.ID, func(rp *store.Repo) {
		rp.SourceType = "git"
		rp.GitURL = repo.GitURL
		rp.GitProvider = inferGitProvider(repo.GitURL)
		rp.ClonePath = clonePath
		rp.Path = clonePath
		rp.SyncStatus = "synced"
		rp.SyncError = ""
		rp.LastSyncedAt = time.Now().UTC()
		applyGitInfo(rp, info)
	})
}

func (s *Server) markRepoSyncFailed(pid, rid string, err error) (*store.Repo, error) {
	updated, _ := s.store.UpdateRepo(pid, rid, func(rp *store.Repo) {
		rp.SyncStatus = "failed"
		rp.SyncError = err.Error()
		rp.LastSyncedAt = time.Now().UTC()
	})
	return updated, err
}

type gitInfo struct {
	Branch     string
	Head       string
	RemoteHead string
	Dirty      bool
}

func inspectLocalGit(ctx context.Context, path, branch string) gitInfo {
	if branch == "" {
		branch = firstNonEmpty(gitOutput(ctx, path, "rev-parse", "--abbrev-ref", "HEAD"), "main")
	}
	return gitInfo{
		Branch:     branch,
		Head:       gitOutput(ctx, path, "rev-parse", "HEAD"),
		RemoteHead: gitOutput(ctx, path, "rev-parse", "origin/"+branch),
		Dirty:      strings.TrimSpace(gitOutput(ctx, path, "status", "--porcelain")) != "",
	}
}

func applyGitInfo(repo *store.Repo, info gitInfo) {
	if info.Branch != "" {
		repo.DefaultBranch = info.Branch
	}
	if info.Head != "" {
		repo.HeadSHA = info.Head
	}
	if info.RemoteHead != "" {
		repo.RemoteHeadSHA = info.RemoteHead
	}
	repo.SyncStatus = firstNonEmpty(repo.SyncStatus, "synced")
}

func gitOutput(ctx context.Context, dir string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitCommand(ctx context.Context, dir, gitURL string, args ...string) error {
	token := githubToken(ctx, gitURL)
	if host := githubAuthHost(gitURL); token != "" && host != "" {
		args = append([]string{"-c", "http.https://" + host + "/.extraheader=AUTHORIZATION: bearer " + token}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

func githubAuthHost(gitURL string) string {
	u, err := url.Parse(gitURL)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(u.Host))
	if !strings.Contains(host, "github") {
		return ""
	}
	return host
}

func githubToken(ctx context.Context, raw string) string {
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token
	}
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
		return token
	}
	host := githubTokenHost(raw)
	if host == "" {
		host = "github.com"
	}
	tokenCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"auth", "token"}
	if host != "github.com" {
		args = append(args, "--hostname", host)
	}
	out, err := exec.CommandContext(tokenCtx, "gh", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func githubTokenHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	host := ""
	if err == nil {
		host = strings.ToLower(strings.TrimSpace(u.Host))
	}
	if host == "" {
		host = strings.ToLower(strings.TrimSpace(raw))
	}
	if at := strings.LastIndex(host, "@"); at >= 0 && at+1 < len(host) {
		host = host[at+1:]
	}
	if colon := strings.Index(host, ":"); colon > 0 && !strings.Contains(host[:colon], "/") {
		host = host[:colon]
	}
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.Trim(host, "/")
	if host == "api.github.com" {
		return "github.com"
	}
	if strings.Contains(host, "/") {
		host = strings.Split(host, "/")[0]
	}
	if !strings.Contains(host, "github") {
		return ""
	}
	return host
}

func (s *Server) handleStartDiffMindRepoRun(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	repo, err := s.store.GetRepo(pid, rid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	opts, err := decodeDiffMindRunOptions(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	_, _ = s.store.UpdateRepo(pid, rid, func(rp *store.Repo) { rp.SyncStatus = "diffmind_running"; rp.SyncError = "" })
	go s.runDiffMindForRepo(pid, rid, *repo, opts)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "diffmind_running", "options": opts})
}

func decodeDiffMindRunOptions(r *http.Request) (orchestrator.DiffMindRunOptions, error) {
	var opts orchestrator.DiffMindRunOptions
	if r.Body == nil {
		return opts, nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return opts, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return opts, nil
	}
	var req struct {
		Options orchestrator.DiffMindRunOptions `json:"options"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return opts, err
	}
	opts = req.Options
	return opts, nil
}

func (s *Server) runDiffMindForRepo(pid, rid string, repo store.Repo, opts orchestrator.DiffMindRunOptions) {
	repoPath := repo.Path
	if repoPath == "" {
		repoPath = repo.ClonePath
	}
	if _, statErr := os.Stat(repoPath); statErr != nil && repo.GitURL != "" {
		if updated, syncErr := s.syncGitRepo(context.Background(), pid, repo); syncErr == nil && updated != nil {
			repo = *updated
			repoPath = repo.Path
		} else if syncErr != nil {
			_, _ = s.store.UpdateRepo(pid, rid, func(rp *store.Repo) {
				rp.SyncStatus = "diffmind_failed"
				rp.SyncError = "sync before DiffMind failed: " + syncErr.Error()
				rp.DiffMindFreshness = "unknown"
			})
			return
		}
	}
	binary := firstNonEmpty(os.Getenv("DIFFMIND_BINARY"), config.NewDefault().DiffMind.BinaryPath)
	err := orchestrator.RunDiffMind(binary, repoPath, opts, s.log)
	runs, _ := s.diffmindRunsForRepo(repoPath)
	var latest string
	var team string
	var freshness string
	if len(runs) > 0 {
		if artifacts.RunMatchesRepo(runs[0], repo.Name, repo.ID, repoPath) {
			latest = runs[0].RunID
			team = firstNonEmpty(runs[0].Team, "default")
		}
	}
	_, _ = s.store.UpdateRepo(pid, rid, func(rp *store.Repo) {
		if err != nil {
			rp.SyncStatus = "diffmind_failed"
			rp.SyncError = err.Error()
			rp.DiffMindFreshness = "unknown"
			return
		}
		rp.SyncStatus = "diffmind_completed"
		rp.SyncError = ""
		rp.LastDiffMindRunID = latest
		if team != "" {
			rp.Team = team
		}
		if len(runs) > 0 && latest != "" {
			freshness = diffmindFreshness(*rp, &runs[0])
			rp.DiffMindFreshness = freshness
		} else {
			rp.DiffMindFreshness = "unknown"
		}
	})
}

func (s *Server) handleGetDiffMindConfigurationYAML(w http.ResponseWriter, r *http.Request) {
	repo, err := s.store.GetRepo(r.PathValue("pid"), r.PathValue("rid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	path := artifacts.RepoConfigurationPath(repo.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"path": path, "body": ""})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "body": string(data)})
}

func (s *Server) handlePutDiffMindConfigurationYAML(w http.ResponseWriter, r *http.Request) {
	repo, err := s.store.GetRepo(r.PathValue("pid"), r.PathValue("rid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path := artifacts.RepoConfigurationPath(repo.Path)
	if err := os.WriteFile(path, []byte(req.Body), 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	team := artifacts.RepoConfigurationTeam(repo.Path)
	updated, _ := s.store.UpdateRepo(r.PathValue("pid"), r.PathValue("rid"), func(rp *store.Repo) { rp.Team = team })
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "repo": updated})
}

func (s *Server) handleLiveStatus(w http.ResponseWriter, r *http.Request) {
	repos, err := s.workspaceRepos(r.PathValue("pid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": s.liveStatusForRepos(r.Context(), repos)})
}

func (s *Server) liveStatusForRepos(ctx context.Context, repos []workspaceRepo) map[string]repoLive {
	out := map[string]repoLive{}
	for _, repo := range repos {
		out[repo.ID] = s.cachedLiveStatus(ctx, repo.Repo)
	}
	return out
}

func (s *Server) cachedLiveStatusForRepos(repos []workspaceRepo) map[string]repoLive {
	out := map[string]repoLive{}
	now := time.Now().UTC()
	s.liveStatusMu.Lock()
	defer s.liveStatusMu.Unlock()
	for _, repo := range repos {
		key := repo.ID + "|" + repo.GitURL + "|" + repo.Path
		if cached, ok := s.liveStatusCache[key]; ok && now.Before(cached.expiresAt) {
			out[repo.ID] = cached.value
			continue
		}
		out[repo.ID] = repoLive{
			Provider:  firstNonEmpty(repo.GitProvider, repo.SourceType, "local"),
			Status:    "not_checked",
			CheckedAt: now,
		}
	}
	return out
}

func (s *Server) cachedLiveStatus(ctx context.Context, repo store.Repo) repoLive {
	key := repo.ID + "|" + repo.GitURL + "|" + repo.Path
	now := time.Now().UTC()
	s.liveStatusMu.Lock()
	if cached, ok := s.liveStatusCache[key]; ok && now.Before(cached.expiresAt) {
		s.liveStatusMu.Unlock()
		return cached.value
	}
	s.liveStatusMu.Unlock()

	value := githubLiveStatus(ctx, repo)
	s.liveStatusMu.Lock()
	s.liveStatusCache[key] = liveStatusCacheEntry{value: value, expiresAt: time.Now().UTC().Add(liveStatusTTL)}
	s.liveStatusMu.Unlock()
	return value
}

func githubLiveStatus(ctx context.Context, repo store.Repo) repoLive {
	now := time.Now().UTC()
	githubSource := githubSourceForRepo(ctx, repo)
	owner, name, ok := githubOwnerRepo(githubSource)
	if !ok {
		return repoLive{Provider: firstNonEmpty(repo.GitProvider, "git"), Status: "unavailable", CheckedAt: now}
	}
	token := githubToken(ctx, githubSource)
	client := &http.Client{Timeout: 8 * time.Second}
	prs, prErr := githubCount(ctx, client, token, fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=open&per_page=1", owner, name))
	issues, issueErr := githubCount(ctx, client, token, fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?state=open&per_page=1", owner, name))
	actions := "unknown"
	if state, err := githubActionsState(ctx, client, token, owner, name); err == nil {
		actions = state
	}
	status := "ok"
	errText := ""
	if prErr != nil || issueErr != nil {
		status = "error"
		errText = firstNonEmpty(errorString(prErr), errorString(issueErr))
	}
	return repoLive{Provider: "github", PullRequests: prs, Issues: issues, ActionsState: actions, Status: status, Error: errText, CheckedAt: now}
}

func githubOwnerRepo(raw string) (string, string, bool) {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	raw = strings.TrimSuffix(raw, "/")
	if strings.Contains(raw, "github.com:") {
		parts := strings.Split(raw, "github.com:")
		raw = "github.com/" + parts[len(parts)-1]
	}
	idx := strings.Index(strings.ToLower(raw), "github.com/")
	if idx < 0 {
		return "", "", false
	}
	rest := raw[idx+len("github.com/"):]
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func githubCount(ctx context.Context, client *http.Client, token, url string) (int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return 0, fmt.Errorf("github returned %s", resp.Status)
	}
	link := resp.Header.Get("Link")
	if n, ok := lastPageFromLink(link); ok {
		return n, nil
	}
	var arr []json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&arr)
	return len(arr), nil
}

func githubActionsState(ctx context.Context, client *http.Client, token, owner, repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs?per_page=1", owner, repo)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("github returned %s", resp.Status)
	}
	var body struct {
		WorkflowRuns []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if len(body.WorkflowRuns) == 0 {
		return "none", nil
	}
	if body.WorkflowRuns[0].Conclusion != "" {
		return body.WorkflowRuns[0].Conclusion, nil
	}
	return body.WorkflowRuns[0].Status, nil
}

func lastPageFromLink(link string) (int, bool) {
	for _, part := range strings.Split(link, ",") {
		if !strings.Contains(part, `rel="last"`) {
			continue
		}
		start := strings.Index(part, "page=")
		if start < 0 {
			continue
		}
		start += len("page=")
		end := start
		for end < len(part) && part[end] >= '0' && part[end] <= '9' {
			end++
		}
		var n int
		if _, err := fmt.Sscanf(part[start:end], "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
