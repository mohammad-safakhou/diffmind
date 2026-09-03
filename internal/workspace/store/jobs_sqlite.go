package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const queueSchema = 1
const queueApplication = 1145917777
const queueDDL = `
CREATE TABLE jobs (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, trigger TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','cancelled','interrupted')),
 created_at TEXT NOT NULL, not_before TEXT NOT NULL, body BLOB NOT NULL
);
CREATE INDEX jobs_status_created ON jobs(status,created_at,id);
CREATE INDEX jobs_created ON jobs(created_at,id);
CREATE INDEX jobs_project_created ON jobs(project_id,created_at,id);
CREATE INDEX jobs_coalesce ON jobs(project_id,trigger,status,created_at,id);
CREATE UNIQUE INDEX jobs_active_project ON jobs(project_id) WHERE status='running';
CREATE TABLE job_attempts (
 job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
 number INTEGER NOT NULL, status TEXT NOT NULL, duration REAL NOT NULL,
 PRIMARY KEY(job_id,number)
);
CREATE INDEX job_attempts_status ON job_attempts(status);
PRAGMA application_id=1145917777;
PRAGMA user_version=1;`

func (s *Store) queueDir() string  { return filepath.Join(s.root, "queue") }
func (s *Store) queuePath() string { return filepath.Join(s.queueDir(), "queue.sqlite") }

func openQueue(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := url.Values{"mode": {"rw"}, "_txlock": {"immediate"}, "_pragma": {"busy_timeout(5000)", "foreign_keys(1)", "synchronous(FULL)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// The directory is the activation marker. Missing/damaged databases never fall
// back to stale JSON. Connections are scoped to operations, not leaked through
// Store's historically connectionless API. BEGIN IMMEDIATE covers read/modify/
// write admission; a durable commit precedes every successful mutation result.
func (s *Store) lockJobs() (func(), error) {
	s.mu.Lock()
	info, err := os.Lstat(s.queueDir())
	if errors.Is(err, os.ErrNotExist) {
		return s.mu.Unlock, nil
	}
	fail := func(err error) (func(), error) {
		s.mu.Unlock()
		return nil, fmt.Errorf("queue storage unavailable: %w", err)
	}
	if err != nil {
		return fail(err)
	}
	if !info.IsDir() {
		return fail(errors.New("queue must be a directory, not a symlink"))
	}
	info, err = os.Lstat(s.queuePath())
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(errors.New("queue database must be a regular file"))
	}
	db, err := openQueue(s.queuePath())
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		cancel()
		db.Close()
		return fail(err)
	}
	if err = checkQueueSchema(tx); err != nil {
		tx.Rollback()
		cancel()
		db.Close()
		return fail(err)
	}
	s.jobTx = tx
	return func() { tx.Rollback(); s.jobTx = nil; cancel(); db.Close(); s.mu.Unlock() }, nil
}
func checkQueueSchema(tx *sql.Tx) error {
	var version, application int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if err := tx.QueryRow("PRAGMA application_id").Scan(&application); err != nil {
		return err
	}
	if version != queueSchema || application != queueApplication {
		return fmt.Errorf("unsupported queue database: application=%d schema=%d", application, version)
	}
	return nil
}
func decodeJob(body []byte) (*RefreshJob, error) {
	var j RefreshJob
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, err
	}
	if err := validateJob(j); err != nil {
		return nil, err
	}
	return &j, nil
}
func validateJob(j RefreshJob) error {
	if !validID(j.ID) || !validID(j.ProjectID) || j.CreatedAt.IsZero() || j.MaxAttempts < 1 || j.MaxAttempts > 100 || len(j.Attempts) > 100 {
		return errors.New("invalid persisted refresh job")
	}
	switch j.Status {
	case "queued", "running", "succeeded", "failed", "cancelled", "interrupted":
	default:
		return errors.New("invalid refresh job status")
	}
	for i, a := range j.Attempts {
		if a.Number != i+1 || a.StartedAt.IsZero() {
			return errors.New("invalid job attempt history")
		}
		switch a.Status {
		case "running", "succeeded", "failed", "cancelled", "interrupted":
		default:
			return errors.New("invalid job attempt status")
		}
	}
	if j.Status == "running" && (len(j.Attempts) == 0 || j.Attempts[len(j.Attempts)-1].Status != "running") {
		return errors.New("running job has no active attempt")
	}
	return nil
}
func (s *Store) sqlReadJob(id string) (*RefreshJob, error) {
	var body []byte
	if err := s.jobTx.QueryRow("SELECT body FROM jobs WHERE id=?", id).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	j, err := decodeJob(body)
	if err == nil && j.ID != id {
		return nil, errors.New("queue record identity mismatch")
	}
	return j, err
}
func (s *Store) sqlJobs(query string, args ...any) ([]RefreshJob, error) {
	rows, err := s.jobTx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RefreshJob{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		j, err := decodeJob(body)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
func queueTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func sqlPutJob(tx *sql.Tx, j RefreshJob, body []byte) error {
	if err := validateJob(j); err != nil {
		return err
	}
	if body == nil {
		var err error
		body, err = json.Marshal(j)
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO jobs(id,project_id,trigger,status,created_at,not_before,body) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id,trigger=excluded.trigger,status=excluded.status,created_at=excluded.created_at,not_before=excluded.not_before,body=excluded.body`, j.ID, j.ProjectID, j.Trigger, j.Status, queueTime(j.CreatedAt), queueTime(j.NotBefore), body)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM job_attempts WHERE job_id=?", j.ID); err != nil {
		return err
	}
	for _, a := range j.Attempts {
		var duration float64
		if !a.FinishedAt.IsZero() {
			duration = a.FinishedAt.Sub(a.StartedAt).Seconds()
		}
		if _, err = tx.Exec("INSERT INTO job_attempts VALUES(?,?,?,?)", j.ID, a.Number, a.Status, duration); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) writeJob(j RefreshJob) error {
	if err := validateJob(j); err != nil {
		return err
	}
	if s.jobTx == nil {
		return writeJSON(s.jobPath(j.ID), j)
	}
	if err := sqlPutJob(s.jobTx, j, nil); err != nil {
		return err
	}
	return s.jobTx.Commit()
}
func (s *Store) jobAdmission(pid, trigger string) (int, *RefreshJob, error) {
	if s.jobTx != nil {
		var count int
		if err := s.jobTx.QueryRow("SELECT count(*) FROM jobs WHERE status IN ('queued','running')").Scan(&count); err != nil {
			return 0, nil, err
		}
		jobs, err := s.sqlJobs("SELECT body FROM jobs WHERE project_id=? AND trigger=? AND status='queued' ORDER BY created_at,id LIMIT 1", pid, trigger)
		if err != nil {
			return 0, nil, err
		}
		if len(jobs) > 0 {
			return count, &jobs[0], nil
		}
		return count, nil, nil
	}
	all, err := s.jobs()
	if err != nil {
		return 0, nil, err
	}
	count := 0
	var duplicate *RefreshJob
	for _, j := range all {
		if j.Status == "queued" || j.Status == "running" {
			count++
		}
		if duplicate == nil && j.ProjectID == pid && j.Trigger == trigger && j.Status == "queued" {
			copy := j
			duplicate = &copy
		}
	}
	return count, duplicate, nil
}
func (s *Store) nextJob(now time.Time) (*RefreshJob, error) {
	if s.jobTx != nil {
		jobs, err := s.sqlJobs(`SELECT body FROM jobs j WHERE status='queued' AND not_before<=?
   AND NOT EXISTS(SELECT 1 FROM jobs active WHERE active.project_id=j.project_id AND active.status='running')
   ORDER BY created_at,id LIMIT 1`, queueTime(now))
		if err != nil {
			return nil, err
		}
		if len(jobs) > 0 {
			return &jobs[0], nil
		}
		return nil, nil
	}
	all, err := s.jobs()
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, j := range all {
		if j.Status == "running" {
			active[j.ProjectID] = true
		}
	}
	for _, j := range all {
		if j.Status == "queued" && !active[j.ProjectID] && !j.NotBefore.After(now) {
			return &j, nil
		}
	}
	return nil, nil
}

type JobPage struct {
	Jobs       []RefreshJob `json:"jobs"`
	Total      int          `json:"total"`
	NextOffset *int         `json:"next_offset"`
}

// nil projects means trusted global access; an empty non-nil slice means none.
// The caller supplies authorized project IDs BEFORE counting and pagination.
func (s *Store) JobsPage(projects []string, offset, limit int) (JobPage, error) {
	out := JobPage{Jobs: []RefreshJob{}}
	if offset < 0 || limit < 1 || limit > 500 || len(projects) > 10000 {
		return out, errors.New("invalid job page limits")
	}
	for _, pid := range projects {
		if !validID(pid) {
			return out, ErrNotFound
		}
	}
	release, err := s.lockJobs()
	if err != nil {
		return out, err
	}
	defer release()
	if s.jobTx != nil {
		where := ""
		args := []any{}
		if projects != nil {
			if len(projects) == 0 {
				return out, nil
			}
			where = " WHERE project_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(projects)), ",") + ")"
			for _, pid := range projects {
				args = append(args, pid)
			}
		}
		if err = s.jobTx.QueryRow("SELECT count(*) FROM jobs"+where, args...).Scan(&out.Total); err != nil {
			return out, err
		}
		out.Jobs, err = s.sqlJobs("SELECT body FROM jobs"+where+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
	} else {
		all, e := s.jobs()
		if e != nil {
			return out, e
		}
		allowed := map[string]bool{}
		for _, p := range projects {
			allowed[p] = true
		}
		filtered := []RefreshJob{}
		for i := len(all) - 1; i >= 0; i-- {
			if projects == nil || allowed[all[i].ProjectID] {
				filtered = append(filtered, all[i])
			}
		}
		out.Total = len(filtered)
		start := min(offset, out.Total)
		out.Jobs = filtered[start : start+min(limit, out.Total-start)]
	}
	// Do not let a damaged index turn a project-filtered query into disclosure
	// of another project's payload. Full index verification is an offline scan.
	if projects != nil {
		allowed := map[string]bool{}
		for _, pid := range projects {
			allowed[pid] = true
		}
		for _, j := range out.Jobs {
			if !allowed[j.ProjectID] {
				return JobPage{Jobs: []RefreshJob{}}, errors.New("queue project index disagrees with payload")
			}
		}
	}
	if offset < out.Total && len(out.Jobs) < out.Total-offset {
		next := offset + len(out.Jobs)
		out.NextOffset = &next
	}
	return out, err
}

type JobStats struct {
	Jobs     map[string]int
	Attempts map[string]int
	Duration float64
}

func (s *Store) JobStatistics() (JobStats, error) {
	out := JobStats{Jobs: map[string]int{}, Attempts: map[string]int{}}
	release, err := s.lockJobs()
	if err != nil {
		return out, err
	}
	defer release()
	if s.jobTx == nil {
		all, err := s.jobs()
		if err != nil {
			return out, err
		}
		for _, j := range all {
			out.Jobs[j.Status]++
			for _, a := range j.Attempts {
				out.Attempts[a.Status]++
				if !a.FinishedAt.IsZero() {
					out.Duration += a.FinishedAt.Sub(a.StartedAt).Seconds()
				}
			}
		}
		return out, nil
	}
	for _, group := range []struct {
		query string
		dest  map[string]int
	}{{"SELECT status,count(*) FROM jobs GROUP BY status", out.Jobs}, {"SELECT status,count(*) FROM job_attempts GROUP BY status", out.Attempts}} {
		rows, err := s.jobTx.Query(group.query)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var state string
			var count int
			if err := rows.Scan(&state, &count); err != nil {
				rows.Close()
				return out, err
			}
			group.dest[state] = count
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	err = s.jobTx.QueryRow("SELECT coalesce(sum(duration),0) FROM job_attempts").Scan(&out.Duration)
	return out, err
}
