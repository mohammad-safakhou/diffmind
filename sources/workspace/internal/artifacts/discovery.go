package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// DiffMindRunInfo is the discovery projection of a single central DiffMind run.
type DiffMindRunInfo struct {
	RunID            string             `json:"run_id"`
	RepoPath         string             `json:"repo_path"`
	ServiceID        string             `json:"service_id,omitempty"`
	ServiceName      string             `json:"service_name,omitempty"`
	StartedAt        time.Time          `json:"started_at"`
	FinishedAt       time.Time          `json:"finished_at"`
	Team             string             `json:"team,omitempty"`
	RepoGitSHA       string             `json:"repo_git_sha,omitempty"`
	RepoGitBranch    string             `json:"repo_git_branch,omitempty"`
	RepoGitRemoteURL string             `json:"repo_git_remote_url,omitempty"`
	RepoGitDirty     bool               `json:"repo_git_dirty,omitempty"`
	RepoMetrics      *model.RepoMetrics `json:"repo_metrics,omitempty"`
	Dir              string             `json:"-"`
	Source           string             `json:"source,omitempty"`
}

// DiscoverDiffMindRuns scans the central DiffMind runs directory for completed
// runs, reading each run_manifest.json. Runs whose manifest is missing or
// malformed are skipped (never fatal). The result is sorted newest-first
// (deterministically: by StartedAt desc, then RunID desc as a tiebreaker).
func DiscoverDiffMindRuns(runsDir string) ([]DiffMindRunInfo, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []DiffMindRunInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(runsDir, e.Name())
		info, ok := readRunManifest(dir)
		if !ok {
			continue
		}
		info.RunID = e.Name()
		info.Dir = dir
		out = append(out, info)
	}
	sortRuns(out)
	return out, nil
}

// DiscoverDiffMindRunsByRepo groups discovered runs by repo_path, each group
// sorted newest-first.
func DiscoverDiffMindRunsByRepo(runsDir string) (map[string][]DiffMindRunInfo, error) {
	all, err := DiscoverDiffMindRuns(runsDir)
	if err != nil {
		return nil, err
	}
	groups := map[string][]DiffMindRunInfo{}
	for _, r := range all {
		groups[r.RepoPath] = append(groups[r.RepoPath], r)
	}
	for k := range groups {
		sortRuns(groups[k])
	}
	return groups, nil
}

// LatestDiffMindRunForRepo returns the newest run for a given repo path, or
// false if there are none.
func LatestDiffMindRunForRepo(runsDir, repoPath string) (DiffMindRunInfo, bool, error) {
	groups, err := DiscoverDiffMindRunsByRepo(runsDir)
	if err != nil {
		return DiffMindRunInfo{}, false, err
	}
	runs := groups[repoPath]
	if len(runs) == 0 {
		return DiffMindRunInfo{}, false, nil
	}
	return runs[0], true, nil
}

// DiffMindRunByID reads one central DiffMind run by id. This is used as a
// stable fallback when a project repo stores last_diffmind_run_id but the run
// manifest's repo_path no longer matches the current local checkout path.
func DiffMindRunByID(runsDir, runID string) (DiffMindRunInfo, bool) {
	runID = filepath.Base(runID)
	if runID == "." || runID == string(filepath.Separator) {
		return DiffMindRunInfo{}, false
	}
	dir := filepath.Join(runsDir, runID)
	info, ok := readRunManifest(dir)
	if !ok {
		return DiffMindRunInfo{}, false
	}
	info.RunID = runID
	info.Dir = dir
	return info, true
}

func RunMatchesRepo(info DiffMindRunInfo, repoName, repoID, repoPath string) bool {
	if samePath(info.RepoPath, repoPath) {
		return true
	}
	runNames := []string{info.ServiceName, info.ServiceID, filepath.Base(info.RepoPath)}
	repoNames := []string{repoName, repoID, filepath.Base(repoPath)}
	for _, a := range runNames {
		for _, b := range repoNames {
			if sameIdentity(a, b) {
				return true
			}
		}
	}
	return false
}

func samePath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if aa, err := filepath.Abs(a); err == nil {
		a = aa
	}
	if bb, err := filepath.Abs(b); err == nil {
		b = bb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func sameIdentity(a, b string) bool {
	aa := normalizeIdentity(a)
	bb := normalizeIdentity(b)
	return aa != "" && aa == bb
}

func normalizeIdentity(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimPrefix(raw, "service.")
	raw = strings.TrimPrefix(raw, "external.")
	raw = strings.TrimSuffix(raw, ".git")
	var out strings.Builder
	lastDash := false
	for _, r := range raw {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

// ReadRunDir reads a specific DiffMind run directory into a ServiceArchitecture.
// Used by the run manager once a user has selected a run per repo.
func ReadRunDir(runDir string) (*model.ServiceArchitecture, error) {
	return readRunDir("", runDir)
}

func readRunManifest(dir string) (DiffMindRunInfo, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "run_manifest.json"))
	if err != nil {
		return DiffMindRunInfo{}, false
	}
	var m model.RunManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return DiffMindRunInfo{}, false
	}
	if m.RepoPath == "" {
		return DiffMindRunInfo{}, false
	}
	team := m.Team
	if team == "" {
		team = "default"
	}
	serviceID, serviceName := protocolServiceIdentity(dir)
	return DiffMindRunInfo{
		RepoPath:         m.RepoPath,
		ServiceID:        serviceID,
		ServiceName:      serviceName,
		StartedAt:        m.StartedAt,
		FinishedAt:       m.FinishedAt,
		Team:             team,
		RepoGitSHA:       m.RepoGitSHA,
		RepoGitBranch:    m.RepoGitBranch,
		RepoGitRemoteURL: m.RepoGitRemoteURL,
		RepoGitDirty:     m.RepoGitDirty,
		RepoMetrics:      m.RepoMetrics,
	}, true
}

func protocolServiceIdentity(runDir string) (string, string) {
	doc, err := ReadDiffMind protocol(runDir)
	if err != nil || doc == nil {
		return "", ""
	}
	return doc.Service.ID, doc.Service.Name
}

func sortRuns(runs []DiffMindRunInfo) {
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].StartedAt.After(runs[j].StartedAt)
		}
		return runs[i].RunID > runs[j].RunID
	})
}
