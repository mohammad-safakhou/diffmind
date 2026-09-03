package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
)

func TestQueueMigrationPreservesHistoryAndOriginalBytes(t *testing.T) {
	s, pid := jobStore(t)
	job, _, err := s.EnqueueJob(pid, "github_push", "delivery", "digest", 8)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.ClaimJob(time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishJob(job.ID, JobAttempt{Status: "failed", Error: "synthetic"}, false, 0); err != nil {
			t.Fatal(err)
		}
	}
	original, err := s.GetJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(s.jobPath(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	report, err := MigrateQueue(s.HomeDir())
	if err != nil || report.Jobs != 1 || report.Attempts != 2 || report.Schema != 1 || len(report.SourceSHA256) != 64 {
		t.Fatalf("report %+v %v", report, err)
	}
	after, err := s.GetJob(job.ID)
	if err != nil || !reflect.DeepEqual(original, after) {
		t.Fatalf("history changed %+v %v", after, err)
	}
	legacy, err := os.ReadFile(s.jobPath(job.ID))
	if err != nil || !bytes.Equal(body, legacy) {
		t.Fatal("original JSON changed")
	}
	release, err := s.lockJobs()
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	err = s.jobTx.QueryRow("SELECT body FROM jobs WHERE id=?", job.ID).Scan(&stored)
	release()
	if err != nil || !bytes.Equal(stored, body) {
		t.Fatal("imported JSON bytes changed")
	}
	duplicate, dup, err := s.EnqueueJob(pid, "github_push", "delivery", "digest", 8)
	if err != nil || !dup || duplicate.ID != job.ID {
		t.Fatalf("delivery lost %+v %v", duplicate, err)
	}
	if _, err := s.CancelJob(job.ID); err != nil {
		t.Fatal(err)
	}
	again, err := MigrateQueue(s.HomeDir())
	if err != nil || !again.AlreadyMigrated {
		t.Fatalf("idempotence %+v %v", again, err)
	}
	final, _ := s.GetJob(job.ID)
	if final.Status != "cancelled" {
		t.Fatal("remigration restored stale JSON")
	}
}

func TestQueueMigrationRejectsCorruptionAndUnsafeSources(t *testing.T) {
	for _, kind := range []string{"malformed", "future-field", "id-mismatch", "symlink", "missing-db", "future-schema", "corrupt-db"} {
		t.Run(kind, func(t *testing.T) {
			s, pid := jobStore(t)
			j, _, err := s.EnqueueJob(pid, "manual", "", "", 8)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(s.jobPath(j.ID))
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "malformed":
				err = os.WriteFile(s.jobPath(j.ID), []byte("{"), 0600)
			case "future-field":
				var fields map[string]any
				json.Unmarshal(original, &fields)
				fields["future"] = true
				body, _ := json.Marshal(fields)
				err = os.WriteFile(s.jobPath(j.ID), body, 0600)
			case "id-mismatch":
				copy := *j
				copy.ID = "another"
				err = writeJSON(s.jobPath(j.ID), copy)
			case "symlink":
				err = os.Rename(s.jobPath(j.ID), s.jobPath(j.ID)+".saved")
				if err == nil {
					err = os.Symlink(s.jobPath(j.ID)+".saved", s.jobPath(j.ID))
				}
			default:
				if _, err := MigrateQueue(s.HomeDir()); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "missing-db":
					err = os.Remove(s.queuePath())
				case "corrupt-db":
					err = os.WriteFile(s.queuePath(), []byte("broken"), 0600)
				case "future-schema":
					db, e := openQueue(s.queuePath())
					if e != nil {
						t.Fatal(e)
					}
					_, err = db.Exec("PRAGMA user_version=99")
					db.Close()
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.GetJob(j.ID); err == nil {
					t.Fatal("fell back to stale JSON")
				}
				if _, _, err := s.EnqueueJob(pid, "manual", "", "", 8); err == nil {
					t.Fatal("accepted work on unavailable database")
				}
				if _, err := MigrateQueue(s.HomeDir()); err == nil {
					t.Fatal("replaced damaged active database")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := MigrateQueue(s.HomeDir()); err == nil {
				t.Fatal("invalid migration succeeded")
			}
			if _, err := os.Lstat(s.queueDir()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed migration activated backend: %v", err)
			}
			leftovers, _ := filepath.Glob(filepath.Join(s.root, ".queue-migrate-*"))
			if len(leftovers) > 0 {
				t.Fatal("staging leaked")
			}
		})
	}
}

func TestQueueMigrationMaintenanceLease(t *testing.T) {
	s, _ := jobStore(t)
	release, err := homelock.Acquire(s.HomeDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateQueue(s.HomeDir()); err == nil {
		t.Fatal("migration ignored live process")
	}
	release()
	if _, err := MigrateQueue(s.HomeDir()); err != nil {
		t.Fatal(err)
	}
}

func TestQueuePublicationDoesNotReplaceDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "marker"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := publishQueue(source, destination); err == nil {
		t.Fatal("replaced competing empty destination")
	}
	if _, err := os.Stat(filepath.Join(source, "marker")); err != nil {
		t.Fatal("unpublished data lost")
	}
}

func TestSQLitePaginationStatisticsAndIndexes(t *testing.T) {
	s, pid := jobStore(t)
	p, err := s.CreateProject(Project{Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	// Equal seconds, differing nanoseconds exercise stable timestamp indexing.
	instant := time.Date(2020, 1, 2, 3, 4, 5, 0, time.FixedZone("offset", 3600))
	for i := 0; i < 30; i++ {
		project := pid
		if i%2 == 0 {
			project = p.ID
		}
		j := RefreshJob{ID: fmt.Sprintf("job-%02d", i), ProjectID: project, Trigger: "manual", Status: "succeeded", MaxAttempts: 3, CreatedAt: instant.Add(time.Duration(i) * time.Nanosecond), NotBefore: instant, Attempts: []JobAttempt{{Number: 1, Status: "succeeded", StartedAt: instant, FinishedAt: instant.Add(time.Second)}}}
		if err := writeJSON(s.jobPath(j.ID), j); err != nil {
			t.Fatal(err)
		}
	}
	wantPage, err := s.JobsPage([]string{pid}, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	wantStats, err := s.JobStatistics()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateQueue(s.HomeDir()); err != nil {
		t.Fatal(err)
	}
	got, err := s.JobsPage([]string{pid}, 2, 5)
	if err != nil || !reflect.DeepEqual(wantPage, got) || got.Total != 15 || got.NextOffset == nil {
		t.Fatalf("page %+v %v", got, err)
	}
	stats, err := s.JobStatistics()
	if err != nil || !reflect.DeepEqual(wantStats, stats) {
		t.Fatalf("stats %+v %v", stats, err)
	}
	denied, err := s.JobsPage([]string{}, 0, 5)
	if err != nil || denied.Total != 0 || len(denied.Jobs) != 0 {
		t.Fatal("empty allowlist exposed jobs")
	}
	tail, err := s.JobsPage(nil, 1000, 5)
	if err != nil || tail.Total != 30 || len(tail.Jobs) != 0 || tail.NextOffset != nil {
		t.Fatal("bad final page")
	}
	release, err := s.lockJobs()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.jobTx.Query("EXPLAIN QUERY PLAN SELECT body FROM jobs WHERE project_id=? ORDER BY created_at DESC,id DESC LIMIT 5", pid)
	if err != nil {
		release()
		t.Fatal(err)
	}
	plan := ""
	for rows.Next() {
		var a, b, c int
		var detail string
		rows.Scan(&a, &b, &c, &detail)
		plan += detail
	}
	rows.Close()
	release()
	if !strings.Contains(plan, "jobs_project_created") {
		t.Fatalf("pagination did not use index: %s", plan)
	}
	if _, err := s.VerifyQueue(); err != nil {
		t.Fatal(err)
	}
	// Verification detects metadata damage even when SQLite page integrity is OK.
	db, err := openQueue(s.queuePath())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("UPDATE jobs SET trigger='tampered' WHERE id='job-00'")
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyQueue(); err == nil {
		t.Fatal("index/payload mismatch accepted")
	}
	db, err = openQueue(s.queuePath())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("UPDATE jobs SET project_id=? WHERE id='job-00'", pid)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if page, err := s.JobsPage([]string{pid}, 0, 500); err == nil || len(page.Jobs) != 0 {
		t.Fatalf("damaged index disclosed foreign payload: %+v %v", page, err)
	}
}

func TestSQLiteCrossProcessAtomicAdmission(t *testing.T) {
	s, pid := jobStore(t)
	if _, err := MigrateQueue(s.HomeDir()); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(exe, "-test.run=^TestSQLiteProcessHelper$")
			cmd.Env = append(os.Environ(), "DIFFMIND_TEST_SQLITE_HOME="+s.HomeDir(), "DIFFMIND_TEST_SQLITE_PROJECT="+pid, fmt.Sprintf("DIFFMIND_TEST_SQLITE_TRIGGER=trigger-%d", i))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child: %v %s", err, out)
			}
		}(i)
	}
	wg.Wait()
	jobs, err := s.ListJobs("")
	if err != nil || len(jobs) != 1 || jobs[0].Status != "running" || len(jobs[0].Attempts) != 1 {
		t.Fatalf("atomic capacity/claim: %+v %v", jobs, err)
	}
	if err := s.RecoverJobs(); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ListJobs("")
	if err != nil || jobs[0].Status != "queued" || jobs[0].Attempts[0].Status != "interrupted" {
		t.Fatal("restart recovery lost claim")
	}
}
func TestSQLiteProcessHelper(t *testing.T) {
	home := os.Getenv("DIFFMIND_TEST_SQLITE_HOME")
	if home == "" {
		t.Skip("subprocess helper")
	}
	s, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.EnqueueJob(os.Getenv("DIFFMIND_TEST_SQLITE_PROJECT"), os.Getenv("DIFFMIND_TEST_SQLITE_TRIGGER"), "", "", 1)
	if err != nil && !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	if _, err := s.ClaimJob(time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteCrashRollsBackUncommittedClaim(t *testing.T) {
	s, pid := jobStore(t)
	j, _, err := s.EnqueueJob(pid, "manual", "", "", 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateQueue(s.HomeDir()); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSQLiteCrashHelper$")
	cmd.Env = append(os.Environ(), "DIFFMIND_TEST_SQLITE_CRASH="+s.HomeDir(), "DIFFMIND_TEST_SQLITE_JOB="+j.ID)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	scan := bufio.NewScanner(out)
	if !scan.Scan() || scan.Text() != "ready" {
		t.Fatal("child failed to stage uncommitted claim")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	after, err := s.GetJob(j.ID)
	if err != nil || !reflect.DeepEqual(j, after) {
		t.Fatalf("crash published uncommitted attempt: %+v %v", after, err)
	}
	if _, err = s.VerifyQueue(); err != nil {
		t.Fatal(err)
	}
}
func TestSQLiteCrashHelper(t *testing.T) {
	home := os.Getenv("DIFFMIND_TEST_SQLITE_CRASH")
	if home == "" {
		t.Skip("crash subprocess helper")
	}
	s, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.lockJobs()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	j, err := s.readJob(os.Getenv("DIFFMIND_TEST_SQLITE_JOB"))
	if err != nil {
		t.Fatal(err)
	}
	j.Status = "running"
	j.Attempts = append(j.Attempts, JobAttempt{Number: 1, Status: "running", StartedAt: time.Now().UTC()})
	if err = sqlPutJob(s.jobTx, *j, nil); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	for {
		time.Sleep(time.Second)
	} // parent kills this process before COMMIT
}

func BenchmarkQueueClaimWithHistory(b *testing.B) {
	for _, backend := range []string{"json", "sqlite"} {
		b.Run(backend, func(b *testing.B) {
			s, err := New(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			p, err := s.CreateProject(Project{Name: "benchmark"})
			if err != nil {
				b.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Join(s.root, "jobs"), 0700); err != nil {
				b.Fatal(err)
			}
			instant := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 10000; i++ {
				j := RefreshJob{ID: fmt.Sprintf("history-%05d", i), ProjectID: p.ID, Trigger: "manual", Status: "succeeded", MaxAttempts: 3, CreatedAt: instant, NotBefore: instant, Attempts: []JobAttempt{}}
				body, _ := json.Marshal(j)
				if err = os.WriteFile(s.jobPath(j.ID), body, 0600); err != nil {
					b.Fatal(err)
				}
			}
			if backend == "sqlite" {
				if _, err = MigrateQueue(s.HomeDir()); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				j, err := s.ClaimJob(time.Now())
				if err != nil || j != nil {
					b.Fatalf("claim %+v %v", j, err)
				}
			}
		})
	}
}
