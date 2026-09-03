package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// ProjectAccess contains explicit stable proxy-subject grants, not credentials.
// Global administrators bypass grants; project memberships never grant admin.
type ProjectAccess struct {
	Version   int               `json:"version"`
	Revision  int               `json:"revision"`
	Members   map[string]string `json:"members"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
}

var ErrInvalidAccess = errors.New("invalid access policy")

// ValidID identifies a single persisted path segment, never a path or dot segment.
func ValidID(id string) bool { return validID(id) }

func validateAccess(a ProjectAccess) error {
	if a.Version != 1 || a.Revision < 0 || len(a.Members) > 1000 {
		return errors.New("invalid access policy version, revision, or member count")
	}
	for subject, role := range a.Members {
		if subject == "" || strings.TrimSpace(subject) != subject || len(subject) > 256 || strings.IndexFunc(subject, unicode.IsControl) >= 0 {
			return errors.New("member subjects must be exact nonempty identifiers of at most 256 bytes without control characters")
		}
		if role != "viewer" && role != "editor" {
			return errors.New("project member role must be viewer or editor")
		}
	}
	return nil
}

func (s *Store) getProjectAccess(pid string) (*ProjectAccess, error) {
	if !validID(pid) {
		return nil, ErrNotFound
	}
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(s.projectDir(pid), "access.json"))
	if errors.Is(err, os.ErrNotExist) {
		return &ProjectAccess{Version: 1, Members: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 1<<20 {
		return nil, errors.New("access policy too large")
	}
	var a ProjectAccess
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing access policy data")
	}
	if err := validateAccess(a); err != nil {
		return nil, err
	}
	if a.Members == nil {
		a.Members = map[string]string{}
	}
	return &a, nil
}

func (s *Store) GetProjectAccess(pid string) (*ProjectAccess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getProjectAccess(pid)
}

// PutProjectAccess uses optimistic revision checking so simultaneous editors
// cannot silently restore a revoked grant. The filesystem write is atomic.
func (s *Store) PutProjectAccess(pid string, expected int, members map[string]string) (*ProjectAccess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.getProjectAccess(pid)
	if err != nil {
		return nil, err
	}
	if expected < 0 || expected != current.Revision {
		return nil, fmt.Errorf("%w: access policy changed; reload before saving", ErrConflict)
	}
	next := ProjectAccess{Version: 1, Revision: expected + 1, Members: map[string]string{}, UpdatedAt: time.Now().UTC()}
	for subject, role := range members {
		next.Members[subject] = role
	}
	if err := validateAccess(next); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAccess, err)
	}
	if err := writeJSON(filepath.Join(s.projectDir(pid), "access.json"), next); err != nil {
		return nil, err
	}
	return &next, nil
}
