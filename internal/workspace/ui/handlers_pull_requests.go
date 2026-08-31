package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// Pull requests are intentionally a live projection rather than persisted
// project state. The architecture graph is the stable company model; GitHub is
// queried for the changing review queue and a selected PR is overlaid on that
// graph on demand.
type pullRequestSummary struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Draft     bool      `json:"draft"`
	Author    string    `json:"author"`
	Head      string    `json:"head"`
	Base      string    `json:"base"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Labels    []string  `json:"labels,omitempty"`
	RepoID    string    `json:"repo_id"`
	RepoName  string    `json:"repo_name"`
	Team      string    `json:"team,omitempty"`
}

type pullRequestRepo struct {
	RepoID       string               `json:"repo_id"`
	RepoName     string               `json:"repo_name"`
	Team         string               `json:"team,omitempty"`
	Provider     string               `json:"provider"`
	Status       string               `json:"status"`
	Error        string               `json:"error,omitempty"`
	OpenCount    int                  `json:"open_count"`
	Truncated    bool                 `json:"truncated,omitempty"`
	PullRequests []pullRequestSummary `json:"pull_requests"`
}

type pullRequestsResponse struct {
	TotalOpen    int               `json:"total_open"`
	RepoCount    int               `json:"repo_count"`
	ErrorCount   int               `json:"error_count"`
	GeneratedAt  time.Time         `json:"generated_at"`
	Repositories []pullRequestRepo `json:"repositories"`
}

type githubPull struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	ChangedFiles   int    `json:"changed_files"`
	Commits        int    `json:"commits"`
	Comments       int    `json:"comments"`
	ReviewComments int    `json:"review_comments"`
	MergeableState string `json:"mergeable_state"`
}

type githubPullFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
	Patch     string `json:"patch"`
	BlobURL   string `json:"blob_url"`
}

type codeImpactFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Category  string `json:"category"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	URL       string `json:"url,omitempty"`
}

type changeCategory struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Files     int    `json:"files"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Risk      int    `json:"risk"`
}

type semanticSignal struct {
	Kind     string   `json:"kind"`
	Label    string   `json:"label"`
	Severity string   `json:"severity"`
	Files    []string `json:"files"`
}

type codebaseImpact struct {
	ChangedFiles int              `json:"changed_files"`
	Additions    int              `json:"additions"`
	Deletions    int              `json:"deletions"`
	Commits      int              `json:"commits"`
	RiskScore    int              `json:"risk_score"`
	RiskLevel    string           `json:"risk_level"`
	RiskReasons  []string         `json:"risk_reasons"`
	Categories   []changeCategory `json:"categories"`
	Signals      []semanticSignal `json:"signals,omitempty"`
	Areas        []string         `json:"areas,omitempty"`
	Files        []codeImpactFile `json:"files"`
	Truncated    bool             `json:"truncated,omitempty"`
}

type impactedService struct {
	Name   string `json:"name"`
	Team   string `json:"team,omitempty"`
	Depth  int    `json:"depth"`
	Reason string `json:"reason"`
}

type companyImpact struct {
	Available        bool                `json:"available"`
	RootService      string              `json:"root_service,omitempty"`
	RunID            string              `json:"run_id,omitempty"`
	DirectServices   int                 `json:"direct_services"`
	IndirectServices int                 `json:"indirect_services"`
	Teams            []string            `json:"teams,omitempty"`
	Resources        []string            `json:"resources,omitempty"`
	Services         []impactedService   `json:"services,omitempty"`
	Flow             *archgraph.FlowView `json:"flow,omitempty"`
	Confidence       string              `json:"confidence"`
	Notes            []string            `json:"notes,omitempty"`
}

type pullRequestImpactResponse struct {
	PullRequest pullRequestSummary `json:"pull_request"`
	Codebase    codebaseImpact     `json:"codebase"`
	Company     companyImpact      `json:"company"`
	RiskScore   int                `json:"risk_score"`
	RiskLevel   string             `json:"risk_level"`
	GeneratedAt time.Time          `json:"generated_at"`
}

func (s *Server) handlePullRequests(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	repos, err := s.workspaceRepos(pid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	results := make([]pullRequestRepo, len(repos))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i := range repos {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = githubOpenPullRequests(r.Context(), repos[i])
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool {
		if results[i].OpenCount != results[j].OpenCount {
			return results[i].OpenCount > results[j].OpenCount
		}
		return results[i].RepoName < results[j].RepoName
	})
	response := pullRequestsResponse{RepoCount: len(results), GeneratedAt: time.Now().UTC(), Repositories: results}
	for _, result := range results {
		response.TotalOpen += result.OpenCount
		if result.Status == "error" {
			response.ErrorCount++
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func githubOpenPullRequests(ctx context.Context, repo workspaceRepo) pullRequestRepo {
	result := pullRequestRepo{
		RepoID: repo.ID, RepoName: repo.Name, Team: repo.EffectiveTeam,
		Provider: firstNonEmpty(repo.GitProvider, repo.SourceType, "git"), Status: "unavailable",
		PullRequests: []pullRequestSummary{},
	}
	githubSource := githubSourceForRepo(ctx, repo.Repo)
	owner, name, ok := githubOwnerRepo(githubSource)
	if !ok {
		return result
	}
	result.Provider = "github"
	client := &http.Client{Timeout: 20 * time.Second}
	token := githubToken(ctx, githubSource)
	for page := 1; page <= 10; page++ {
		endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=open&per_page=100&page=%d&sort=updated&direction=desc", owner, name, page)
		var pulls []githubPull
		if err := githubJSON(ctx, client, token, endpoint, &pulls); err != nil {
			result.Status = "error"
			result.Error = err.Error()
			return result
		}
		for _, pull := range pulls {
			summary := pullSummary(pull, repo.Repo)
			summary.Team = repo.EffectiveTeam
			result.PullRequests = append(result.PullRequests, summary)
		}
		if len(pulls) < 100 {
			break
		}
		if page == 10 {
			result.Truncated = true
		}
	}
	result.OpenCount = len(result.PullRequests)
	result.Status = "ok"
	return result
}

func (s *Server) handlePullRequestImpact(w http.ResponseWriter, r *http.Request) {
	pid, repoID := r.PathValue("pid"), r.PathValue("repo_id")
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("invalid pull request number"))
		return
	}
	repo, err := s.store.GetRepo(pid, repoID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	githubSource := githubSourceForRepo(r.Context(), *repo)
	owner, name, ok := githubOwnerRepo(githubSource)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("repository is not connected to GitHub"))
		return
	}
	client := &http.Client{Timeout: 30 * time.Second}
	token := githubToken(r.Context(), githubSource)
	base := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, name, number)
	var pull githubPull
	if err := githubJSON(r.Context(), client, token, base, &pull); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	files, truncated, err := githubPullFiles(r.Context(), client, token, base)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	codebase := analyzeCodebaseImpact(pull, files, truncated)
	company := s.pullRequestCompanyImpact(pid, r.URL.Query().Get("run_id"), *repo)
	companyScore := companyImpactScore(company)
	overall := minInt(100, int(math.Round(float64(codebase.RiskScore)*0.62+float64(companyScore)*0.38)))
	if !company.Available {
		overall = codebase.RiskScore
	}
	writeJSON(w, http.StatusOK, pullRequestImpactResponse{
		PullRequest: pullSummary(pull, *repo), Codebase: codebase, Company: company,
		RiskScore: overall, RiskLevel: riskLevel(overall), GeneratedAt: time.Now().UTC(),
	})
}

func githubPullFiles(ctx context.Context, client *http.Client, token, base string) ([]githubPullFile, bool, error) {
	var out []githubPullFile
	for page := 1; page <= 30; page++ {
		var files []githubPullFile
		endpoint := fmt.Sprintf("%s/files?per_page=100&page=%d", base, page)
		if err := githubJSON(ctx, client, token, endpoint, &files); err != nil {
			return nil, false, err
		}
		out = append(out, files...)
		if len(files) < 100 {
			return out, false, nil
		}
	}
	return out, true, nil
}

func githubJSON(ctx context.Context, client *http.Client, token, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var ghErr githubErrorResponse
		if json.Unmarshal(body, &ghErr) == nil && ghErr.Message != "" {
			return fmt.Errorf("github returned %s: %s", resp.Status, ghErr.Message)
		}
		return fmt.Errorf("github returned %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func githubSourceForRepo(ctx context.Context, repo store.Repo) string {
	if source := strings.TrimSpace(repo.GitURL); source != "" {
		return source
	}
	path := firstNonEmpty(repo.Path, repo.ClonePath)
	if path != "" {
		if remote := gitOutput(ctx, path, "remote", "get-url", "origin"); remote != "" {
			return remote
		}
	}
	return path
}

func pullSummary(p githubPull, repo store.Repo) pullRequestSummary {
	labels := make([]string, 0, len(p.Labels))
	for _, label := range p.Labels {
		if strings.TrimSpace(label.Name) != "" {
			labels = append(labels, label.Name)
		}
	}
	sort.Strings(labels)
	return pullRequestSummary{
		Number: p.Number, Title: p.Title, URL: p.HTMLURL, Draft: p.Draft, Author: p.User.Login,
		Head: p.Head.Ref, Base: p.Base.Ref, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		Labels: labels, RepoID: repo.ID, RepoName: repo.Name, Team: repo.Team,
	}
}

var changeCategoryMeta = map[string]struct {
	label string
	risk  int
}{
	"api":            {"API & contracts", 20},
	"data":           {"Data & migrations", 25},
	"security":       {"Security & identity", 25},
	"infrastructure": {"Infrastructure", 18},
	"dependencies":   {"Dependencies", 15},
	"configuration":  {"Configuration", 10},
	"tests":          {"Tests", 0},
	"documentation":  {"Documentation", 0},
	"code":           {"Application code", 8},
}

func analyzeCodebaseImpact(pull githubPull, files []githubPullFile, truncated bool) codebaseImpact {
	impact := codebaseImpact{
		ChangedFiles: pull.ChangedFiles, Additions: pull.Additions, Deletions: pull.Deletions, Commits: pull.Commits,
		Files: make([]codeImpactFile, 0, len(files)), Truncated: truncated,
	}
	if impact.ChangedFiles == 0 {
		impact.ChangedFiles = len(files)
	}
	if impact.Additions == 0 && impact.Deletions == 0 {
		for _, f := range files {
			impact.Additions += f.Additions
			impact.Deletions += f.Deletions
		}
	}
	byCategory := map[string]*changeCategory{}
	areaCounts := map[string]int{}
	signalFiles := map[string]map[string]bool{}
	hasSource, hasTests := false, false
	for _, file := range files {
		category := classifyChangedFile(file.Filename)
		meta := changeCategoryMeta[category]
		cat := byCategory[category]
		if cat == nil {
			cat = &changeCategory{ID: category, Label: meta.label, Risk: meta.risk}
			byCategory[category] = cat
		}
		cat.Files++
		cat.Additions += file.Additions
		cat.Deletions += file.Deletions
		impact.Files = append(impact.Files, codeImpactFile{Path: file.Filename, Status: file.Status, Category: category, Additions: file.Additions, Deletions: file.Deletions, URL: file.BlobURL})
		areaCounts[changeArea(file.Filename)]++
		if category == "tests" {
			hasTests = true
		}
		if category == "code" || category == "api" || category == "data" {
			hasSource = true
		}
		for _, signal := range fileSignals(file) {
			if signalFiles[signal] == nil {
				signalFiles[signal] = map[string]bool{}
			}
			signalFiles[signal][file.Filename] = true
		}
	}
	for _, cat := range byCategory {
		impact.Categories = append(impact.Categories, *cat)
	}
	sort.Slice(impact.Categories, func(i, j int) bool {
		if impact.Categories[i].Risk != impact.Categories[j].Risk {
			return impact.Categories[i].Risk > impact.Categories[j].Risk
		}
		return impact.Categories[i].Files > impact.Categories[j].Files
	})
	for area := range areaCounts {
		impact.Areas = append(impact.Areas, area)
	}
	sort.Slice(impact.Areas, func(i, j int) bool {
		if areaCounts[impact.Areas[i]] != areaCounts[impact.Areas[j]] {
			return areaCounts[impact.Areas[i]] > areaCounts[impact.Areas[j]]
		}
		return impact.Areas[i] < impact.Areas[j]
	})
	if len(impact.Areas) > 8 {
		impact.Areas = impact.Areas[:8]
	}
	for kind, paths := range signalFiles {
		files := sortedKeys(paths)
		label, severity := signalLabel(kind)
		impact.Signals = append(impact.Signals, semanticSignal{Kind: kind, Label: label, Severity: severity, Files: files})
	}
	sort.Slice(impact.Signals, func(i, j int) bool {
		return signalRank(impact.Signals[i].Severity) > signalRank(impact.Signals[j].Severity)
	})

	score := 5
	lines := impact.Additions + impact.Deletions
	switch {
	case lines > 1500:
		score += 24
	case lines > 500:
		score += 16
	case lines > 100:
		score += 8
	}
	switch {
	case impact.ChangedFiles > 50:
		score += 20
	case impact.ChangedFiles > 20:
		score += 12
	case impact.ChangedFiles > 8:
		score += 5
	}
	for _, cat := range impact.Categories {
		if cat.Risk > 0 {
			score += minInt(cat.Risk, 8+cat.Files*3)
		}
	}
	if hasSource && !hasTests {
		score += 10
		impact.RiskReasons = append(impact.RiskReasons, "production code changed without test-file changes")
	}
	if impact.Deletions > impact.Additions && impact.Deletions > 100 {
		score += 6
		impact.RiskReasons = append(impact.RiskReasons, "large removal surface")
	}
	for _, signal := range impact.Signals {
		if signal.Severity == "high" {
			score += 8
		}
		impact.RiskReasons = append(impact.RiskReasons, signal.Label)
	}
	if lines > 500 {
		impact.RiskReasons = append(impact.RiskReasons, fmt.Sprintf("large diff: %d changed lines", lines))
	}
	if impact.ChangedFiles > 20 {
		impact.RiskReasons = append(impact.RiskReasons, fmt.Sprintf("broad change across %d files", impact.ChangedFiles))
	}
	impact.RiskScore = minInt(100, score)
	impact.RiskLevel = riskLevel(impact.RiskScore)
	if len(impact.RiskReasons) == 0 {
		impact.RiskReasons = []string{"localized change with no high-risk file patterns detected"}
	}
	sort.Slice(impact.Files, func(i, j int) bool {
		return impact.Files[i].Additions+impact.Files[i].Deletions > impact.Files[j].Additions+impact.Files[j].Deletions
	})
	return impact
}

func classifyChangedFile(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	ext := strings.ToLower(filepath.Ext(base))
	contains := func(parts ...string) bool {
		for _, p := range parts {
			if strings.Contains(lower, p) {
				return true
			}
		}
		return false
	}
	if contains("/test/", "/tests/", "_test.", ".spec.", ".test.", "__tests__") {
		return "tests"
	}
	if ext == ".md" || ext == ".rst" || contains("/docs/") {
		return "documentation"
	}
	if contains("migration", "schema.sql", "liquibase", "flyway", "prisma/schema", "db/schema") {
		return "data"
	}
	if contains("openapi", "swagger", ".proto", "graphql", "/routes/", "/controller", "/api/") {
		return "api"
	}
	if contains("auth", "security", "permission", "policy", "iam", "rbac", "oauth", "secret", "credential", "certificate", "tls") {
		return "security"
	}
	if ext == ".tf" || contains("terraform", "helm/", "charts/", "k8s/", "kubernetes", "dockerfile", "docker-compose", ".github/workflows") {
		return "infrastructure"
	}
	if base == "go.mod" || base == "go.sum" || base == "package.json" || base == "package-lock.json" || base == "pnpm-lock.yaml" || base == "yarn.lock" || base == "pom.xml" || base == "build.gradle" || base == "requirements.txt" || base == "poetry.lock" || base == "cargo.toml" || base == "cargo.lock" {
		return "dependencies"
	}
	if ext == ".yaml" || ext == ".yml" || ext == ".json" || ext == ".toml" || ext == ".ini" || ext == ".conf" || ext == ".properties" || strings.HasPrefix(base, ".env") {
		return "configuration"
	}
	return "code"
}

func fileSignals(file githubPullFile) []string {
	category := classifyChangedFile(file.Filename)
	set := map[string]bool{}
	if category == "api" {
		set["contract_change"] = true
	}
	if category == "data" {
		set["data_migration"] = true
	}
	if category == "security" {
		set["security_boundary"] = true
	}
	if category == "infrastructure" {
		set["deployment_change"] = true
	}
	if category == "dependencies" {
		set["dependency_change"] = true
	}
	patch := strings.ToLower(file.Patch)
	if strings.Contains(patch, "-func ") || strings.Contains(patch, "-public ") || strings.Contains(patch, "-export ") || strings.Contains(patch, "-interface ") {
		set["public_surface_removal"] = true
	}
	if strings.Contains(patch, "drop table") || strings.Contains(patch, "drop column") || strings.Contains(patch, "alter table") {
		set["destructive_schema"] = true
	}
	return sortedKeys(set)
}

func signalLabel(kind string) (string, string) {
	switch kind {
	case "contract_change":
		return "API or contract surface changed", "high"
	case "data_migration":
		return "Database schema or migration changed", "high"
	case "destructive_schema":
		return "Potentially destructive schema operation", "high"
	case "security_boundary":
		return "Security or authorization boundary changed", "high"
	case "public_surface_removal":
		return "Public symbol removal candidate", "high"
	case "deployment_change":
		return "Deployment or infrastructure changed", "medium"
	case "dependency_change":
		return "Dependency graph changed", "medium"
	default:
		return kind, "low"
	}
}

func (s *Server) pullRequestCompanyImpact(pid, requestedRun string, repo store.Repo) companyImpact {
	result := companyImpact{Confidence: "unavailable", Notes: []string{}}
	runID := strings.TrimSpace(requestedRun)
	if runID == "" {
		if run := s.latestCompletedWorkspaceRun(pid); run != nil {
			runID = run.ID
		}
	}
	if runID == "" {
		result.Notes = append(result.Notes, "build a company graph to calculate cross-service impact")
		return result
	}
	graph, err := s.fullArchGraphForRun(pid, runID)
	if err != nil {
		result.Notes = append(result.Notes, "company graph is unavailable: "+err.Error())
		return result
	}
	root := graphServiceForRepo(graph, repo)
	if root == "" {
		result.RunID = runID
		result.Notes = append(result.Notes, "repository is not represented as a service in this graph run")
		return result
	}
	flow, ok := archgraph.BuildImpactView(graph, root, archgraph.FlowOptions{Depth: 8, MaxNodes: 750})
	if !ok {
		return result
	}
	result.Available, result.RootService, result.RunID, result.Flow, result.Confidence = true, root, runID, flow, "graph_estimate"
	teamSet, resourceSet := map[string]bool{}, map[string]bool{}
	for _, service := range flow.Services {
		if service.Team != "" {
			teamSet[service.Team] = true
		}
		if service.Name == root {
			continue
		}
		reason := "indirect dependency path"
		if service.Depth == 1 {
			reason = "direct dependency"
			result.DirectServices++
		} else {
			result.IndirectServices++
		}
		result.Services = append(result.Services, impactedService{Name: service.Name, Team: service.Team, Depth: service.Depth, Reason: reason})
	}
	for _, node := range flow.Nodes {
		if node.Kind != "service" && node.Kind != "external" && node.Label != "" {
			resourceSet[node.Kind+": "+node.Label] = true
		}
	}
	result.Teams, result.Resources = sortedKeys(teamSet), sortedKeys(resourceSet)
	if len(result.Services) == 0 {
		result.Notes = append(result.Notes, "no downstream service dependency is currently proven by the graph")
	}
	result.Notes = append(result.Notes, "blast radius is estimated from repository ownership and current graph dependency direction")
	return result
}

func graphServiceForRepo(graph *ArchGraph, repo store.Repo) string {
	wantPath := cleanPath(firstNonEmpty(repo.Path, repo.ClonePath))
	wantName := normalizedRepoName(repo.Name)
	for _, service := range graph.Services {
		if service == nil {
			continue
		}
		if repo.ID != "" && service.RepoID == repo.ID {
			return service.Name
		}
		if wantPath != "" && cleanPath(service.RepoPath) == wantPath {
			return service.Name
		}
	}
	for _, service := range graph.Services {
		if service != nil && normalizedRepoName(service.Name) == wantName {
			return service.Name
		}
	}
	return ""
}

func cleanPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Clean(path)
}
func normalizedRepoName(name string) string {
	return strings.Trim(strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(name)), "-")
}
func changeArea(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "root"
}
func riskLevel(score int) string {
	switch {
	case score >= 80:
		return "critical"
	case score >= 55:
		return "high"
	case score >= 30:
		return "moderate"
	default:
		return "low"
	}
}
func companyImpactScore(c companyImpact) int {
	if !c.Available {
		return 0
	}
	return minInt(100, 8+c.DirectServices*12+c.IndirectServices*5+len(c.Teams)*8+len(c.Resources)*2)
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func sortedKeys[T ~bool](in map[string]T) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
func signalRank(severity string) int {
	switch severity {
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}
