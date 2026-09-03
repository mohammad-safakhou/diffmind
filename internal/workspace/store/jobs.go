package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var ErrQueueFull = errors.New("refresh queue is full")
var ErrDeliveryConflict = errors.New("delivery ID was already used for a different payload")

type JobAttempt struct {
	Synced      int       `json:"synced"`
	Analyzed    int       `json:"analyzed"`
	Reused      int       `json:"reused"`
	Number      int       `json:"number"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	IngestionID string    `json:"ingestion_id,omitempty"`
	GraphRunID  string    `json:"graph_run_id,omitempty"`
}
type RefreshJob struct {
	ID              string       `json:"id"`
	ProjectID       string       `json:"project_id"`
	Trigger         string       `json:"trigger"`
	Status          string       `json:"status"`
	PayloadDigest   string       `json:"payload_digest,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	NotBefore       time.Time    `json:"not_before"`
	CancelRequested bool         `json:"cancel_requested,omitempty"`
	MaxAttempts     int          `json:"max_attempts"`
	Attempts        []JobAttempt `json:"attempts"`
}

func (s *Store) jobPath(id string) string { return filepath.Join(s.root, "jobs", id+".json") }
func (s *Store) readJob(id string) (*RefreshJob, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	if s.jobTx != nil {
		return s.sqlReadJob(id)
	}
	var job RefreshJob
	if err := readJSON(s.jobPath(id), &job); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if job.ID != id || !validID(job.ProjectID) {
		return nil, fmt.Errorf("invalid persisted refresh job")
	}
	return &job, nil
}
func (s *Store) GetJob(id string) (*RefreshJob, error) {
	release, err := s.lockJobs()
	if err != nil {
		return nil, err
	}
	defer release()
	return s.readJob(id)
}
func (s *Store) jobs() ([]RefreshJob, error) {
	if s.jobTx != nil {
		return s.sqlJobs("SELECT body FROM jobs ORDER BY created_at,id")
	}
	files, err := filepath.Glob(filepath.Join(s.root, "jobs", "*.json"))
	if err != nil {
		return nil, err
	}
	out := []RefreshJob{}
	for _, file := range files {
		var j RefreshJob
		if err := readJSON(file, &j); err != nil {
			return nil, err
		}
		if !validID(j.ID) || !validID(j.ProjectID) || s.jobPath(j.ID) != file {
			return nil, fmt.Errorf("invalid job file")
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}
func (s *Store) ListJobs(pid string) ([]RefreshJob, error) {
	release, err := s.lockJobs()
	if err != nil {
		return nil, err
	}
	defer release()
	if pid != "" {
		if !validID(pid) {
			return nil, ErrNotFound
		}
		if _, err := s.GetProject(pid); err != nil {
			return nil, err
		}
	}
	all, err := s.jobs()
	if err != nil {
		return nil, err
	}
	out := []RefreshJob{}
	for i := len(all) - 1; i >= 0; i-- {
		if pid == "" || all[i].ProjectID == pid {
			out = append(out, all[i])
		}
	}
	return out, nil
}
func (s *Store) EnqueueJob(pid, trigger, delivery, digest string, capacity int) (*RefreshJob, bool, error) {
	release, err := s.lockJobs()
	if err != nil {
		return nil, false, err
	}
	defer release()
	if !validID(pid) {
		return nil, false, ErrNotFound
	}
	if _, err := s.GetProject(pid); err != nil {
		return nil, false, err
	}
	id := ""
	if delivery != "" {
		hash := sha256.Sum256([]byte(pid + "\x00" + delivery))
		id = "delivery-" + hex.EncodeToString(hash[:])
		if job, err := s.readJob(id); err == nil {
			if job.PayloadDigest != digest {
				return nil, false, ErrDeliveryConflict
			}
			return job, true, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, false, err
		}
	} else {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return nil, false, err
		}
		id = "refresh-" + hex.EncodeToString(bytes[:])
	}
	pending, duplicate, err := s.jobAdmission(pid, trigger)
	if err != nil {
		return nil, false, err
	}
	if delivery == "" && duplicate != nil {
		return duplicate, true, nil
	}
	if capacity < 1 || pending >= capacity {
		return nil, false, ErrQueueFull
	}
	now := time.Now().UTC()
	job := RefreshJob{ID: id, ProjectID: pid, Trigger: trigger, Status: "queued", PayloadDigest: digest, CreatedAt: now, UpdatedAt: now, NotBefore: now, MaxAttempts: 3, Attempts: []JobAttempt{}}
	if err := s.writeJob(job); err != nil {
		return nil, false, err
	}
	return &job, false, nil
}

// ClaimJob commits an attempt before invoking work, and serializes projects
// across all workers. Busy projects are deferred without burning attempts.
func (s *Store) ClaimJob(now time.Time) (*RefreshJob, error) {
	release, err := s.lockJobs()
	if err != nil {
		return nil, err
	}
	defer release()
	j, err := s.nextJob(now)
	if err != nil || j == nil {
		return nil, err
	}
	j.Status = "running"
	j.UpdatedAt = now
	j.Attempts = append(j.Attempts, JobAttempt{Number: len(j.Attempts) + 1, Status: "running", StartedAt: now})
	if err := s.writeJob(*j); err != nil {
		return nil, err
	}
	return j, nil
}
func (s *Store) FinishJob(id string, result JobAttempt, busy bool, delay time.Duration) error {
	release, err := s.lockJobs()
	if err != nil {
		return err
	}
	defer release()
	j, err := s.readJob(id)
	if err != nil {
		return err
	}
	if j.Status != "running" || len(j.Attempts) == 0 {
		return ErrConflict
	}
	now := time.Now().UTC()
	j.UpdatedAt = now
	if busy && !j.CancelRequested {
		j.Attempts = j.Attempts[:len(j.Attempts)-1]
		j.Status = "queued"
		j.NotBefore = now.Add(delay)
	} else {
		last := &j.Attempts[len(j.Attempts)-1]
		result.Number = last.Number
		result.StartedAt = last.StartedAt
		result.FinishedAt = now
		if j.CancelRequested {
			result.Status = "cancelled"
		}
		*last = result
		j.Status = result.Status
		if (result.Status == "failed" || result.Status == "interrupted") && len(j.Attempts) < j.MaxAttempts {
			j.Status = "queued"
			j.NotBefore = now.Add(delay)
		}
	}
	return s.writeJob(*j)
}
func (s *Store) CancelJob(id string) (*RefreshJob, error) {
	release, err := s.lockJobs()
	if err != nil {
		return nil, err
	}
	defer release()
	j, err := s.readJob(id)
	if err != nil {
		return nil, err
	}
	if j.Status != "queued" && j.Status != "running" {
		return nil, ErrConflict
	}
	j.CancelRequested = true
	if j.Status == "queued" {
		j.Status = "cancelled"
	}
	j.UpdatedAt = time.Now().UTC()
	if err := s.writeJob(*j); err != nil {
		return nil, err
	}
	return j, nil
}
func (s *Store) RetryJob(id string, capacity int) (*RefreshJob, error) {
	release, err := s.lockJobs()
	if err != nil {
		return nil, err
	}
	defer release()
	j, err := s.readJob(id)
	if err != nil {
		return nil, err
	}
	if j.Status != "failed" && j.Status != "cancelled" && j.Status != "interrupted" {
		return nil, ErrConflict
	}
	if len(j.Attempts) >= 100 {
		return nil, fmt.Errorf("%w: job attempt limit reached; enqueue a new refresh", ErrConflict)
	}
	pending, _, err := s.jobAdmission("", "")
	if err != nil {
		return nil, err
	}
	if pending >= capacity {
		return nil, ErrQueueFull
	}
	j.Status = "queued"
	j.CancelRequested = false
	j.MaxAttempts = min(100, len(j.Attempts)+3)
	j.NotBefore = time.Now().UTC()
	j.UpdatedAt = j.NotBefore
	if err := s.writeJob(*j); err != nil {
		return nil, err
	}
	return j, nil
}

// Called once before workers start, never while another writer uses the volume.
func (s *Store) RecoverJobs() error {
	release, err := s.lockJobs()
	if err != nil {
		return err
	}
	var all []RefreshJob
	if s.jobTx != nil {
		all, err = s.sqlJobs("SELECT body FROM jobs WHERE status='running' ORDER BY created_at,id")
	} else {
		all, err = s.jobs()
	}
	release()
	if err != nil {
		return err
	}
	for _, j := range all {
		if j.Status == "running" {
			if err := s.FinishJob(j.ID, JobAttempt{Status: "interrupted", Error: "server stopped before attempt completion"}, false, 0); err != nil {
				return err
			}
		}
	}
	return nil
}
