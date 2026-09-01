package ui

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.store.ListRepos(r.PathValue("pid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

type createRepoRequest struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Kind          string   `json:"kind"`
	SourceType    string   `json:"source_type"`
	GitURL        string   `json:"git_url"`
	DefaultBranch string   `json:"default_branch"`
	Team          string   `json:"team"`
	PackIDs       []string `json:"pack_ids"`
	Instruction   string   `json:"instruction"`
}

func (s *Server) handleCreateRepo(w http.ResponseWriter, r *http.Request) {
	var req createRepoRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" && strings.TrimSpace(req.GitURL) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("path or git_url is required"))
		return
	}
	repo, err := s.store.CreateRepo(r.PathValue("pid"), store.Repo{
		Name:          req.Name,
		Path:          req.Path,
		Kind:          req.Kind,
		SourceType:    req.SourceType,
		GitURL:        req.GitURL,
		GitProvider:   inferGitProvider(req.GitURL),
		DefaultBranch: req.DefaultBranch,
		Team:          req.Team,
		PackIDs:       req.PackIDs,
		Instruction:   req.Instruction,
	})
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

func (s *Server) handleGetRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := s.store.GetRepo(r.PathValue("pid"), r.PathValue("rid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

type patchRepoRequest struct {
	Name          *string   `json:"name"`
	Path          *string   `json:"path"`
	Kind          *string   `json:"kind"`
	SourceType    *string   `json:"source_type"`
	GitURL        *string   `json:"git_url"`
	DefaultBranch *string   `json:"default_branch"`
	Team          *string   `json:"team"`
	PackIDs       *[]string `json:"pack_ids"`
	Instruction   *string   `json:"instruction"`
}

func (s *Server) handlePatchRepo(w http.ResponseWriter, r *http.Request) {
	var req patchRepoRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repo, err := s.store.UpdateRepo(r.PathValue("pid"), r.PathValue("rid"), func(rp *store.Repo) {
		if req.Name != nil {
			rp.Name = *req.Name
		}
		if req.Path != nil {
			rp.Path = *req.Path
		}
		if req.Kind != nil {
			rp.Kind = *req.Kind
		}
		if req.SourceType != nil {
			rp.SourceType = *req.SourceType
		}
		if req.GitURL != nil {
			rp.GitURL = *req.GitURL
			rp.GitProvider = inferGitProvider(*req.GitURL)
		}
		if req.DefaultBranch != nil {
			rp.DefaultBranch = *req.DefaultBranch
		}
		if req.Team != nil {
			rp.Team = *req.Team
		}
		if req.PackIDs != nil {
			rp.PackIDs = *req.PackIDs
		}
		if req.Instruction != nil {
			rp.Instruction = *req.Instruction
		}
	})
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	// DeleteRepo removes only DiffMind metadata; the source repo is untouched.
	if err := s.store.DeleteRepo(r.PathValue("pid"), r.PathValue("rid")); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// repoSuggestion is a candidate repository discovered under a search root.
type repoSuggestion struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleRepoSuggestions scans the project's search roots (falling back to the
// global config roots when the project defines none) for immediate
// subdirectories that look like repositories (contain a .git directory).
func (s *Server) handleRepoSuggestions(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("pid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	roots := project.SearchRoots
	if len(roots) == 0 {
		if gc, err := config.LoadGlobal(); err == nil {
			roots = gc.SearchRoots
		}
	}

	// Exclude repos already added to the project.
	existing := map[string]bool{}
	if repos, err := s.store.ListRepos(project.ID); err == nil {
		for _, rp := range repos {
			existing[filepath.Clean(rp.Path)] = true
		}
	}

	seen := map[string]bool{}
	var out []repoSuggestion
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			full := filepath.Join(root, e.Name())
			if !looksLikeRepo(full) {
				continue
			}
			clean := filepath.Clean(full)
			if existing[clean] || seen[clean] {
				continue
			}
			seen[clean] = true
			out = append(out, repoSuggestion{Name: e.Name(), Path: full})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out, "roots": roots})
}

func looksLikeRepo(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
		return true
	}
	return false
}

func inferGitProvider(url string) string {
	u := strings.ToLower(url)
	if strings.Contains(u, "github.com") {
		return "github"
	}
	if strings.TrimSpace(url) == "" {
		return ""
	}
	return "git"
}
