package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var durableID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

func validID(id string) bool { return durableID.MatchString(id) }

func (s *Store) GetIngestionAttempt(pid, id string, attempt int) (*Ingestion, error) {
	if !validID(pid) || !validID(id) || attempt < 1 {
		return nil, ErrNotFound
	}
	var in Ingestion
	err := readJSON(filepath.Join(s.projectDir(pid), "ingestions", id, fmt.Sprintf("attempt-%06d.json", attempt)), &in)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &in, nil
}

// Each attempt has its own atomic checkpoint. A new job or retry never replaces
// earlier attempts. The latest ingestion.json remains a compatibility projection.
func (s *Store) archiveIngestion(in Ingestion) error {
	if in.Attempt < 1 {
		in.Attempt = 1
	}
	if !validID(in.ID) || !validID(in.ProjectID) {
		return fmt.Errorf("invalid ingestion identity or attempt")
	}
	return writeJSON(filepath.Join(s.projectDir(in.ProjectID), "ingestions", in.ID, fmt.Sprintf("attempt-%06d.json", in.Attempt)), in)
}

func (s *Store) IngestionHistory(pid string) ([]Ingestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(pid) {
		return nil, ErrNotFound
	}
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	out := []Ingestion{}
	files, err := filepath.Glob(filepath.Join(s.projectDir(pid), "ingestions", "*", "attempt-*.json"))
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		var in Ingestion
		if err := readJSON(file, &in); err != nil {
			return nil, err
		}
		in.Request = nil
		out = append(out, in)
	}
	// Older workspaces have only a latest projection. Include it until migrated.
	if len(out) == 0 {
		if in, err := s.GetIngestion(pid); err == nil {
			in.Request = nil
			out = append(out, *in)
		} else if err != ErrNotFound && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].Attempt > out[j].Attempt
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}
