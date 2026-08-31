// Package repostore persists the dashboard's first-class repositories: the set
// of code repositories a user works with, each remembering where its
// `diffmind.yaml` discovery file lives. It is intentionally tiny — a JSON file
// next to the catalog — and is unioned at read time with repositories discovered
// from extraction runs, so a repo a user has only ever run (never registered)
// still appears.
package repostore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Repo is a registered repository. ID is derived from Path so the same repo
// discovered from a run and registered by hand collapse to one identity.
type Repo struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	FilePath  string    `json:"file_path,omitempty"`
	Name      string    `json:"display_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IDForPath is the stable, URL-safe id for a repository path. Shared with the
// run-derivation in the UI so both sides agree on identity.
func IDForPath(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:16]
}

type Store struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

func NewStore(baseDir string) *Store {
	return &Store{path: filepath.Join(baseDir, "repos.json"), now: time.Now}
}

func (s *Store) List() ([]Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) Get(id string) (Repo, bool, error) {
	repos, err := s.List()
	if err != nil {
		return Repo{}, false, err
	}
	for _, r := range repos {
		if r.ID == id {
			return r, true, nil
		}
	}
	return Repo{}, false, nil
}

// Upsert registers or updates a repository by path-derived id. A blank display
// name defaults to the path's base segment.
func (s *Store) Upsert(in Repo) (Repo, error) {
	in.Path = strings.TrimSpace(in.Path)
	if in.Path == "" {
		return Repo{}, errors.New("path is required")
	}
	in.Path = filepath.Clean(in.Path)
	in.FilePath = strings.TrimSpace(in.FilePath)
	in.ID = IDForPath(in.Path)
	if strings.TrimSpace(in.Name) == "" {
		in.Name = filepath.Base(in.Path)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	repos, err := s.loadLocked()
	if err != nil {
		return Repo{}, err
	}
	now := s.now().UTC()
	found := false
	for i := range repos {
		if repos[i].ID == in.ID {
			in.CreatedAt = repos[i].CreatedAt
			in.UpdatedAt = now
			repos[i] = in
			found = true
			break
		}
	}
	if !found {
		in.CreatedAt = now
		in.UpdatedAt = now
		repos = append(repos, in)
	}
	if err := s.writeLocked(repos); err != nil {
		return Repo{}, err
	}
	return in, nil
}

// Delete unregisters a repository. A repository still seen in run history will
// reappear (derived), only without its remembered file path.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	repos, err := s.loadLocked()
	if err != nil {
		return err
	}
	out := repos[:0]
	for _, r := range repos {
		if r.ID != id {
			out = append(out, r)
		}
	}
	return s.writeLocked(out)
}

func (s *Store) loadLocked() ([]Repo, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Repo{}, nil
	}
	if err != nil {
		return nil, err
	}
	var repos []Repo
	if err := json.Unmarshal(b, &repos); err != nil {
		return nil, err
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

func (s *Store) writeLocked(repos []Repo) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(repos, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".repos-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}
