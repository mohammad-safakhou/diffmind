package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
)

// ErrNotFound is returned when an entity does not exist.
var ErrNotFound = errors.New("not found")

// Store is the filesystem-backed persistence layer. All methods are safe for
// concurrent use; a single mutex serialises mutations (the data volume is tiny
// — a handful of small JSON files per project).
type Store struct {
	root string // DiffMind home (projects live under <root>/projects)
	mu   sync.Mutex
}

// New constructs a Store rooted at the given home directory. An empty home
// resolves to config.Home().
func New(home string) (*Store, error) {
	if home == "" {
		home = config.Home()
	}
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		return nil, fmt.Errorf("create store root: %w", err)
	}
	return &Store{root: home}, nil
}

func (s *Store) projectsDir() string         { return filepath.Join(s.root, "projects") }
func (s *Store) projectDir(id string) string { return filepath.Join(s.projectsDir(), id) }
func (s *Store) blueprintsDir(pid string) string {
	return filepath.Join(s.projectDir(pid), "blueprints")
}
func (s *Store) reposDir(pid string) string { return filepath.Join(s.projectDir(pid), "repos") }
func (s *Store) runsDir(pid string) string  { return filepath.Join(s.projectDir(pid), "runs") }
func (s *Store) worktreesDir(pid string) string {
	return filepath.Join(s.projectDir(pid), "worktrees")
}

func (s *Store) WorktreeDir(pid, repoID string) string {
	return filepath.Join(s.worktreesDir(pid), repoID)
}

// RunDir exposes the on-disk directory for a graph run (used by the run
// manager to write graph.json / events.jsonl / identities).
func (s *Store) RunDir(pid, runID string) string {
	return filepath.Join(s.runsDir(pid), runID)
}

// ---------------------------------------------------------------------------
// Projects
// ---------------------------------------------------------------------------

// ListProjects returns every project, newest first.
func (s *Store) ListProjects() ([]Project, error) {
	entries, err := os.ReadDir(s.projectsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Project, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := s.GetProject(e.Name())
		if err != nil {
			continue // skip unreadable project dirs rather than failing the list
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// GetProject reads a single project.
func (s *Store) GetProject(id string) (*Project, error) {
	var p Project
	if err := readJSON(filepath.Join(s.projectDir(id), "project.json"), &p); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// CreateProject creates a project with a slug derived from its name.
func (s *Store) CreateProject(p Project) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("project name is required")
	}
	id := s.uniqueSlug(s.projectsDir(), p.Name)
	now := time.Now().UTC()
	p.ID = id
	p.CreatedAt = now
	p.UpdatedAt = now
	for _, sub := range []string{"blueprints", "repos", "runs", "worktrees"} {
		if err := os.MkdirAll(filepath.Join(s.projectDir(id), sub), 0o755); err != nil {
			return nil, err
		}
	}
	if err := writeJSON(filepath.Join(s.projectDir(id), "project.json"), p); err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateProject applies a mutation function to a project and persists it.
func (s *Store) UpdateProject(id string, mutate func(*Project)) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.GetProject(id)
	if err != nil {
		return nil, err
	}
	mutate(p)
	p.ID = id
	p.UpdatedAt = time.Now().UTC()
	if err := writeJSON(filepath.Join(s.projectDir(id), "project.json"), *p); err != nil {
		return nil, err
	}
	return p, nil
}

// DeleteProject removes a project and all its data from disk.
func (s *Store) DeleteProject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.projectDir(id)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return os.RemoveAll(dir)
}

// ---------------------------------------------------------------------------
// Repos
// ---------------------------------------------------------------------------

// ListRepos returns every repo in a project, by name.
func (s *Store) ListRepos(pid string) ([]Repo, error) {
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.reposDir(pid))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Repo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r, err := s.GetRepo(pid, e.Name())
		if err != nil {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetRepo reads a single repo.
func (s *Store) GetRepo(pid, id string) (*Repo, error) {
	var r Repo
	if err := readJSON(filepath.Join(s.reposDir(pid), id, "repo.json"), &r); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	normalizeRepoDefaults(&r)
	return &r, nil
}

// CreateRepo adds a repo to a project.
func (s *Store) CreateRepo(pid string, r Repo) (*Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Path) == "" && strings.TrimSpace(r.GitURL) == "" {
		return nil, fmt.Errorf("repo path or git_url is required")
	}
	if strings.TrimSpace(r.Name) == "" {
		r.Name = repoNameFromSource(r)
	}
	if r.Kind == "" {
		r.Kind = "service_repo"
	}
	normalizeRepoDefaults(&r)
	id := s.uniqueSlug(s.reposDir(pid), r.Name)
	now := time.Now().UTC()
	r.ID = id
	r.CreatedAt = now
	r.UpdatedAt = now
	if r.SourceType == "git" || r.GitURL != "" {
		r.SourceType = "git"
		if r.ClonePath == "" {
			r.ClonePath = s.WorktreeDir(pid, id)
		}
		if r.Path == "" {
			r.Path = r.ClonePath
		}
	}
	if err := os.MkdirAll(filepath.Join(s.reposDir(pid), id), 0o755); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(s.reposDir(pid), id, "repo.json"), r); err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateRepo mutates and persists a repo.
func (s *Store) UpdateRepo(pid, id string, mutate func(*Repo)) (*Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.GetRepo(pid, id)
	if err != nil {
		return nil, err
	}
	mutate(r)
	r.ID = id
	normalizeRepoDefaults(r)
	r.UpdatedAt = time.Now().UTC()
	if err := writeJSON(filepath.Join(s.reposDir(pid), id, "repo.json"), *r); err != nil {
		return nil, err
	}
	return r, nil
}

func normalizeRepoDefaults(r *Repo) {
	if r.SourceType == "" {
		if r.GitURL != "" {
			r.SourceType = "git"
		} else {
			r.SourceType = "local"
		}
	}
	if r.Team == "" {
		r.Team = "default"
	}
	if r.DiffMindFreshness == "" {
		r.DiffMindFreshness = "unknown"
	}
	if r.Path == "" && r.ClonePath != "" {
		r.Path = r.ClonePath
	}
}

func repoNameFromSource(r Repo) string {
	if r.GitURL != "" {
		u := strings.TrimSuffix(strings.TrimSpace(r.GitURL), "/")
		u = strings.TrimSuffix(u, ".git")
		if idx := strings.LastIndex(u, "/"); idx >= 0 && idx+1 < len(u) {
			return u[idx+1:]
		}
		if idx := strings.LastIndex(u, ":"); idx >= 0 && idx+1 < len(u) {
			return u[idx+1:]
		}
	}
	return filepath.Base(strings.TrimRight(r.Path, "/"))
}

// DeleteRepo removes only DiffMind's metadata for the repo; the source
// repository on disk is never touched.
func (s *Store) DeleteRepo(pid, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.reposDir(pid), id)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return os.RemoveAll(dir)
}

// ---------------------------------------------------------------------------
// Blueprints
// ---------------------------------------------------------------------------

// BlueprintsDir exposes the project's blueprints directory for the pipeline,
// which loads blueprints by scanning a directory of JSON files.
func (s *Store) BlueprintsDir(pid string) string { return s.blueprintsDir(pid) }

// ListBlueprints returns the metadata for every blueprint in a project.
func (s *Store) ListBlueprints(pid string) ([]BlueprintMeta, error) {
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.blueprintsDir(pid))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]BlueprintMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		meta := BlueprintMeta{ID: id, Name: id}
		if info, err := e.Info(); err == nil {
			meta.UpdatedAt = info.ModTime().UTC()
		}
		// Best-effort: surface the blueprint's declared name.
		if raw, err := s.GetBlueprint(pid, id); err == nil {
			var probe struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &probe) == nil && probe.Name != "" {
				meta.Name = probe.Name
			}
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetBlueprint returns the raw JSON body of a blueprint.
func (s *Store) GetBlueprint(pid, id string) (json.RawMessage, error) {
	data, err := os.ReadFile(filepath.Join(s.blueprintsDir(pid), id+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return json.RawMessage(data), nil
}

// PutBlueprint writes a blueprint body to a given id (create or replace). The
// caller is responsible for validating the body first (see ValidateBlueprint).
func (s *Store) PutBlueprint(pid, id string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetProject(pid); err != nil {
		return err
	}
	if err := os.MkdirAll(s.blueprintsDir(pid), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.blueprintsDir(pid), id+".json"), body, 0o644)
}

// CreateBlueprint writes a new blueprint with a slug derived from its declared
// name (or "blueprint"), returning the allocated id.
func (s *Store) CreateBlueprint(pid, name string, body []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetProject(pid); err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.blueprintsDir(pid), 0o755); err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		name = "blueprint"
	}
	id := s.uniqueBlueprintSlug(pid, name)
	if err := os.WriteFile(filepath.Join(s.blueprintsDir(pid), id+".json"), body, 0o644); err != nil {
		return "", err
	}
	return id, nil
}

// DeleteBlueprint removes a blueprint file.
func (s *Store) DeleteBlueprint(pid, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.blueprintsDir(pid), id+".json")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return os.Remove(path)
}

// ---------------------------------------------------------------------------
// slug helpers
// ---------------------------------------------------------------------------

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts an arbitrary name into a URL-safe slug.
func Slugify(name string) string {
	s := slugRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "item"
	}
	return s
}

// uniqueSlug returns a slug for name that does not collide with an existing
// directory under parent, appending -2, -3, … as needed.
func (s *Store) uniqueSlug(parent, name string) string {
	base := Slugify(name)
	candidate := base
	for i := 2; dirExists(filepath.Join(parent, candidate)); i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

// uniqueBlueprintSlug is like uniqueSlug but checks for a <slug>.json file.
func (s *Store) uniqueBlueprintSlug(pid, name string) string {
	base := Slugify(name)
	candidate := base
	for i := 2; fileExists(filepath.Join(s.blueprintsDir(pid), candidate+".json")); i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
