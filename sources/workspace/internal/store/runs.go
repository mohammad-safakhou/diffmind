package store

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ListRuns returns every graph run manifest in a project, newest first.
func (s *Store) ListRuns(pid string) ([]RunManifest, error) {
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.runsDir(pid))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]RunManifest, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := s.GetRun(pid, e.Name())
		if err != nil {
			continue
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// GetRun reads a single run manifest.
func (s *Store) GetRun(pid, runID string) (*RunManifest, error) {
	var m RunManifest
	if err := readJSON(filepath.Join(s.RunDir(pid, runID), "manifest.json"), &m); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// CreateRun allocates a timestamp-based run id, creates its directory, and
// writes the initial manifest. The id is made unique by appending -N when the
// second-resolution timestamp collides.
func (s *Store) CreateRun(pid string, m RunManifest) (*RunManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	base := time.Now().UTC().Format("20060102T150405Z")
	id := base
	for i := 2; dirExists(s.RunDir(pid, id)); i++ {
		id = base + "-" + itoa(i)
	}
	m.ID = id
	m.ProjectID = pid
	if m.Status == "" {
		m.Status = RunRunning
	}
	if m.StartedAt.IsZero() {
		m.StartedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Join(s.RunDir(pid, id), "identities"), 0o755); err != nil {
		return nil, err
	}
	if err := s.writeRun(pid, m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SaveRun persists a run manifest (used by the run manager on state changes).
func (s *Store) SaveRun(pid string, m RunManifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeRun(pid, m)
}

func (s *Store) writeRun(pid string, m RunManifest) error {
	return writeJSON(filepath.Join(s.RunDir(pid, m.ID), "manifest.json"), m)
}

// DeleteRun removes a graph run and its artifacts.
func (s *Store) DeleteRun(pid, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.RunDir(pid, runID)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return os.RemoveAll(dir)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
