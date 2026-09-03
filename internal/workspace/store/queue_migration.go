package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
)

type QueueReport struct {
	Backend         string `json:"backend"`
	Schema          int    `json:"schema"`
	Jobs            int    `json:"jobs"`
	Attempts        int    `json:"attempts"`
	SourceSHA256    string `json:"source_sha256,omitempty"`
	AlreadyMigrated bool   `json:"already_migrated,omitempty"`
}

// MigrateQueue requires all writers stopped. It publishes one validated directory
// atomically and leaves every original JSON byte untouched. No automatic downgrade
// is possible: legacy files are a historical snapshot, not a second live backend.
func MigrateQueue(home string) (QueueReport, error) {
	release, err := homelock.Acquire(home, true)
	if err != nil {
		return QueueReport{}, err
	}
	defer release()
	s, err := New(home)
	if err != nil {
		return QueueReport{}, err
	}
	if _, err := os.Lstat(s.queueDir()); err == nil {
		r, err := s.VerifyQueue()
		r.AlreadyMigrated = true
		return r, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return QueueReport{}, err
	}
	staging, err := os.MkdirTemp(s.root, ".queue-migrate-")
	if err != nil {
		return QueueReport{}, err
	}
	defer os.RemoveAll(staging)
	path := filepath.Join(staging, "queue.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return QueueReport{}, err
	}
	if err = f.Close(); err != nil {
		return QueueReport{}, err
	}
	db, err := openQueue(path)
	if err != nil {
		return QueueReport{}, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return QueueReport{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(queueDDL); err != nil {
		return QueueReport{}, err
	}
	files, err := filepath.Glob(filepath.Join(s.root, "jobs", "*.json"))
	if err != nil {
		return QueueReport{}, err
	}
	if info, err := os.Lstat(filepath.Join(s.root, "jobs")); err == nil && !info.IsDir() {
		return QueueReport{}, errors.New("legacy jobs must be a directory, not a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return QueueReport{}, err
	}
	digest := sha256.New()
	for _, path := range files {
		info, err := os.Lstat(path)
		if err != nil {
			return QueueReport{}, err
		}
		if !info.Mode().IsRegular() || info.Size() > 32<<20 {
			return QueueReport{}, errors.New("legacy job must be a regular file of at most 32 MiB")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return QueueReport{}, err
		}
		// Refuse fields from a future format rather than silently dropping them on
		// the first post-migration update. Initial import preserves exact bytes.
		var j RefreshJob
		d := json.NewDecoder(bytes.NewReader(body))
		d.DisallowUnknownFields()
		if err = d.Decode(&j); err != nil {
			return QueueReport{}, fmt.Errorf("decode legacy job: %w", err)
		}
		var extra any
		if err = d.Decode(&extra); err != io.EOF {
			return QueueReport{}, errors.New("trailing legacy job data")
		}
		if !validID(j.ID) || s.jobPath(j.ID) != path {
			return QueueReport{}, errors.New("legacy job file identity mismatch")
		}
		if err = sqlPutJob(tx, j, body); err != nil {
			return QueueReport{}, err
		}
		sum := sha256.Sum256(body)
		fmt.Fprintf(digest, "%s\x00%x\n", filepath.Base(path), sum)
	}
	if _, err = tx.Exec("CREATE TABLE queue_meta(source_sha256 TEXT NOT NULL)"); err != nil {
		return QueueReport{}, err
	}
	if _, err = tx.Exec("INSERT INTO queue_meta VALUES(?)", hex.EncodeToString(digest.Sum(nil))); err != nil {
		return QueueReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return QueueReport{}, err
	}
	check, err := db.Begin()
	if err != nil {
		return QueueReport{}, err
	}
	report, err := verifyQueueTx(check)
	check.Rollback()
	if err != nil {
		return QueueReport{}, err
	}
	if err = db.Close(); err != nil {
		return QueueReport{}, err
	}
	// Flush the complete database before publishing its activation directory.
	f, err = os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return QueueReport{}, err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return QueueReport{}, err
	}
	if closeErr != nil {
		return QueueReport{}, closeErr
	}
	if _, err = os.Lstat(s.queueDir()); !errors.Is(err, os.ErrNotExist) {
		return QueueReport{}, errors.New("queue destination appeared during migration")
	}
	if err = syncQueueDirectory(staging); err != nil {
		return QueueReport{}, err
	}
	if err = publishQueue(staging, s.queueDir()); err != nil {
		return QueueReport{}, err
	}
	if err = syncQueueDirectory(s.root); err != nil {
		return report, fmt.Errorf("queue activated but parent directory sync failed; verify storage before starting: %w", err)
	}
	return report, nil
}

func syncQueueDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// VerifyQueue checks SQLite integrity plus agreement between indexed columns,
// full job payloads and attempt aggregates. It does not change job history.
func (s *Store) VerifyQueue() (QueueReport, error) {
	release, err := s.lockJobs()
	if err != nil {
		return QueueReport{}, err
	}
	defer release()
	if s.jobTx != nil {
		return verifyQueueTx(s.jobTx)
	}
	jobs, err := s.jobs()
	if err != nil {
		return QueueReport{}, err
	}
	out := QueueReport{Backend: "json"}
	for _, j := range jobs {
		if err := validateJob(j); err != nil {
			return out, err
		}
		out.Jobs++
		out.Attempts += len(j.Attempts)
	}
	return out, nil
}
func verifyQueueTx(tx *sql.Tx) (QueueReport, error) {
	out := QueueReport{Backend: "sqlite", Schema: queueSchema}
	if err := checkQueueSchema(tx); err != nil {
		return out, err
	}
	var integrity string
	if err := tx.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return out, err
	}
	if integrity != "ok" {
		return out, fmt.Errorf("queue integrity check: %s", integrity)
	}
	if err := tx.QueryRow("SELECT source_sha256 FROM queue_meta").Scan(&out.SourceSHA256); err != nil {
		return out, err
	}
	rows, err := tx.Query("SELECT id,project_id,trigger,status,created_at,not_before,body FROM jobs ORDER BY id")
	if err != nil {
		return out, err
	}
	jobs := []RefreshJob{}
	for rows.Next() {
		var id, pid, trigger, status, created, notBefore string
		var body []byte
		if err = rows.Scan(&id, &pid, &trigger, &status, &created, &notBefore, &body); err != nil {
			break
		}
		var j *RefreshJob
		j, err = decodeJob(body)
		if err != nil {
			break
		}
		if j.ID != id || j.ProjectID != pid || j.Trigger != trigger || j.Status != status || queueTime(j.CreatedAt) != created || queueTime(j.NotBefore) != notBefore {
			err = errors.New("queue index and payload disagree")
			break
		}
		jobs = append(jobs, *j)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, j := range jobs {
		out.Jobs++
		out.Attempts += len(j.Attempts)
		rows, err := tx.Query("SELECT number,status,duration FROM job_attempts WHERE job_id=? ORDER BY number", j.ID)
		if err != nil {
			return out, err
		}
		i := 0
		for rows.Next() {
			var number int
			var status string
			var duration float64
			if err = rows.Scan(&number, &status, &duration); err != nil {
				break
			}
			if i >= len(j.Attempts) {
				err = errors.New("unexpected indexed attempt")
				break
			}
			a := j.Attempts[i]
			expected := 0.0
			if !a.FinishedAt.IsZero() {
				expected = a.FinishedAt.Sub(a.StartedAt).Seconds()
			}
			if number != a.Number || status != a.Status || duration != expected {
				err = errors.New("queue attempt index disagrees with history")
				break
			}
			i++
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return out, err
		}
		if i != len(j.Attempts) {
			return out, errors.New("missing indexed attempts")
		}
	}
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM job_attempts").Scan(&count); err != nil {
		return out, err
	}
	if count != out.Attempts {
		return out, errors.New("orphaned indexed attempts")
	}
	return out, nil
}
