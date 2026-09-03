package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ProjectLimits bounds admission, not retained history. Zero inherits the
// server ceiling. Policy administration uses the single-server workspace lease.
type ProjectLimits struct {
	Version           int       `json:"version"`
	Revision          int       `json:"revision"`
	MaxPendingJobs    int       `json:"max_pending_jobs"`
	RepositoryWorkers int       `json:"repository_workers"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

var (
	ErrInvalidLimits     = errors.New("invalid project limits")
	ErrLimitsUnavailable = errors.New("project limits unavailable; check stored policy and storage health")
	ErrProjectQueueFull  = errors.New("project pending-job limit reached")
)

// EffectiveLimit never lets a project raise the server-wide ceiling.
func EffectiveLimit(configured, global int) int {
	if configured == 0 {
		return global
	}
	return min(configured, global)
}

func validLimits(p ProjectLimits) bool {
	return p.Version == 1 && p.Revision >= 0 && p.MaxPendingJobs >= 0 && p.MaxPendingJobs <= 10000 && p.RepositoryWorkers >= 0 && p.RepositoryWorkers <= 32
}

// Caller holds s.mu, including queue admission's transaction lock.
func (s *Store) getProjectLimits(pid string) (*ProjectLimits, error) {
	if !validID(pid) {
		return nil, ErrNotFound
	}
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	path := filepath.Join(s.projectDir(pid), "limits.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return &ProjectLimits{Version: 1}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, ErrLimitsUnavailable
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrLimitsUnavailable
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrLimitsUnavailable
	}
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	var disk struct {
		Version           *int       `json:"version"`
		Revision          *int       `json:"revision"`
		MaxPendingJobs    *int       `json:"max_pending_jobs"`
		RepositoryWorkers *int       `json:"repository_workers"`
		UpdatedAt         *time.Time `json:"updated_at"`
	}
	if err := d.Decode(&disk); err != nil || disk.Version == nil || disk.Revision == nil || disk.MaxPendingJobs == nil || disk.RepositoryWorkers == nil || disk.UpdatedAt == nil {
		return nil, ErrLimitsUnavailable
	}
	p := ProjectLimits{Version: *disk.Version, Revision: *disk.Revision, MaxPendingJobs: *disk.MaxPendingJobs, RepositoryWorkers: *disk.RepositoryWorkers, UpdatedAt: *disk.UpdatedAt}
	if !validLimits(p) || p.Revision < 1 || p.UpdatedAt.IsZero() {
		return nil, ErrLimitsUnavailable
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrLimitsUnavailable
	}
	return &p, nil
}

func (s *Store) GetProjectLimits(pid string) (*ProjectLimits, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getProjectLimits(pid)
}

// PutProjectLimits uses compare-and-swap so an outdated admin cannot silently
// overwrite another's quota. Lowering a cap never removes or cancels work.
func (s *Store) PutProjectLimits(pid string, revision, pending, workers int) (*ProjectLimits, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.getProjectLimits(pid)
	if err != nil {
		return nil, err
	}
	if revision < 0 || revision != current.Revision {
		return nil, fmt.Errorf("%w: project limits changed; reload before saving", ErrConflict)
	}
	next := ProjectLimits{Version: 1, Revision: revision + 1, MaxPendingJobs: pending, RepositoryWorkers: workers, UpdatedAt: time.Now().UTC()}
	if !validLimits(next) {
		return nil, ErrInvalidLimits
	}
	path := filepath.Join(s.projectDir(pid), "limits.json")
	if err := writeJSON(path, next); err != nil {
		return nil, ErrLimitsUnavailable
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, ErrLimitsUnavailable
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return nil, ErrLimitsUnavailable
	}
	return &next, nil
}

func (s *Store) checkJobCapacity(pid string, pending, projectPending, capacity int) error {
	policy, err := s.getProjectLimits(pid)
	if err != nil {
		return err
	}
	if capacity < 1 || pending >= capacity {
		return ErrQueueFull
	}
	if policy.MaxPendingJobs > 0 && projectPending >= policy.MaxPendingJobs {
		return ErrProjectQueueFull
	}
	return nil
}

// PendingJobCount counts only queued/running work, not historical attempts.
func (s *Store) PendingJobCount(pid string) (int, error) {
	release, err := s.lockJobs()
	if err != nil {
		return 0, err
	}
	defer release()
	if _, err := s.GetProject(pid); err != nil {
		return 0, err
	}
	_, count, _, err := s.jobAdmission(pid, "")
	return count, err
}
