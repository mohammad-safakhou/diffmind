package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectLimitsPersistenceValidationAndCAS(t *testing.T) {
	s, pid := jobStore(t)
	p, err := s.GetProjectLimits(pid)
	if err != nil || p.Version != 1 || p.Revision != 0 || p.MaxPendingJobs != 0 || p.RepositoryWorkers != 0 {
		t.Fatalf("defaults %+v %v", p, err)
	}
	for _, values := range [][2]int{{-1, 1}, {10001, 1}, {1, -1}, {1, 33}} {
		if _, err := s.PutProjectLimits(pid, 0, values[0], values[1]); !errors.Is(err, ErrInvalidLimits) {
			t.Fatalf("invalid %v: %v", values, err)
		}
	}
	var wg sync.WaitGroup
	var saved atomic.Int32
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.PutProjectLimits(pid, 0, 2, 1)
			if err == nil {
				saved.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if saved.Load() != 1 {
		t.Fatalf("CAS saves %d", saved.Load())
	}
	p, _ = s.GetProjectLimits(pid)
	reopened, err := New(s.HomeDir())
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.GetProjectLimits(pid)
	if err != nil || !reflect.DeepEqual(p, again) || p.UpdatedAt.IsZero() {
		t.Fatalf("restart %+v %v", again, err)
	}
	info, err := os.Stat(filepath.Join(s.projectDir(pid), "limits.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %v %v", info, err)
	}
	for _, id := range []string{"../outside", "missing", "..", "a/b"} {
		if _, err := s.GetProjectLimits(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unsafe id %q: %v", id, err)
		}
		if _, err := s.PutProjectLimits(id, 0, 1, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unsafe write %q: %v", id, err)
		}
	}
	for _, c := range []struct{ configured, global, want int }{{0, 4, 4}, {1, 4, 1}, {32, 4, 4}} {
		if got := EffectiveLimit(c.configured, c.global); got != c.want {
			t.Fatalf("effective %v = %d", c, got)
		}
	}
}

func TestProjectLimitsFailClosedWithoutOverwriting(t *testing.T) {
	for _, content := range []string{"{", "null", `{"version":2}`, `{"version":1}`, `{"version":1,"unknown":1}`, `{"version":1} {}`, `{"version":1,"repository_workers":33}`, `{"version":1,"revision":1,"max_pending_jobs":null,"repository_workers":1,"updated_at":"2026-01-01T00:00:00Z"}`, strings.Repeat(" ", 4097)} {
		t.Run(fmt.Sprintf("case-%d", len(content)), func(t *testing.T) {
			s, pid := jobStore(t)
			path := filepath.Join(s.projectDir(pid), "limits.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetProjectLimits(pid); !errors.Is(err, ErrLimitsUnavailable) {
				t.Fatalf("read %v", err)
			}
			if _, err := s.PutProjectLimits(pid, 0, 1, 1); !errors.Is(err, ErrLimitsUnavailable) {
				t.Fatalf("write %v", err)
			}
			if _, _, err := s.EnqueueJob(pid, "manual", "", "", 10); !errors.Is(err, ErrLimitsUnavailable) {
				t.Fatalf("admission %v", err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != content {
				t.Fatal("corrupt policy overwritten")
			}
		})
	}
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			s, pid := jobStore(t)
			path := filepath.Join(s.projectDir(pid), "limits.json")
			var err error
			if kind == "symlink" {
				target := filepath.Join(t.TempDir(), "policy.json")
				if err := os.WriteFile(target, []byte(`{"version":1}`), 0600); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(target, path)
			} else {
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PutProjectLimits(pid, 0, 1, 1); !errors.Is(err, ErrLimitsUnavailable) {
				t.Fatalf("unsafe policy %v", err)
			}
		})
	}
}

func TestProjectQueueLimitsAllAdmissionsAndHistory(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			s, pid := jobStore(t)
			other, err := s.CreateProject(Project{Name: "Other"})
			if err != nil {
				t.Fatal(err)
			}
			if backend == "sqlite" {
				if _, err := MigrateQueue(s.HomeDir()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.PutProjectLimits(pid, 0, 1, 1); err != nil {
				t.Fatal(err)
			}
			j, _, err := s.EnqueueJob(pid, "manual", "", "", 20)
			if err != nil {
				t.Fatal(err)
			}
			if dup, isDup, err := s.EnqueueJob(pid, "manual", "", "", 20); err != nil || !isDup || dup.ID != j.ID {
				t.Fatalf("manual dedup %+v %v", dup, err)
			}
			claimed, err := s.ClaimJob(time.Now())
			if err != nil || claimed.ID != j.ID {
				t.Fatalf("claim %+v %v", claimed, err)
			}
			for _, trigger := range []string{"manual", "scheduled", "github_push"} {
				if _, _, err := s.EnqueueJob(pid, trigger, "new-"+trigger, "digest", 20); !errors.Is(err, ErrProjectQueueFull) {
					t.Fatalf("%s bypass %v", trigger, err)
				}
			}
			// Automatic attempts keep their admitted slot even after failure.
			for attempt := 1; attempt <= 3; attempt++ {
				if attempt > 1 {
					if _, err := s.ClaimJob(time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.FinishJob(j.ID, JobAttempt{Status: "failed", Error: "test"}, false, 0); err != nil {
					t.Fatal(err)
				}
				if count, err := s.PendingJobCount(pid); err != nil || count != map[bool]int{true: 0, false: 1}[attempt == 3] {
					t.Fatalf("pending %d %v", count, err)
				}
			}
			history, _ := s.GetJob(j.ID)
			webhook, _, err := s.EnqueueJob(pid, "github_push", "delivery", "digest", 20)
			if err != nil {
				t.Fatal(err)
			}
			if _, dup, err := s.EnqueueJob(pid, "github_push", "delivery", "digest", 20); err != nil || !dup {
				t.Fatalf("webhook dedup %v", err)
			}
			if _, _, err := s.EnqueueJob(pid, "github_push", "delivery", "changed", 20); !errors.Is(err, ErrDeliveryConflict) {
				t.Fatalf("digest %v", err)
			}
			if _, err := s.RetryJob(j.ID, 20); !errors.Is(err, ErrProjectQueueFull) {
				t.Fatalf("retry bypass %v", err)
			}
			after, _ := s.GetJob(j.ID)
			if !reflect.DeepEqual(history, after) {
				t.Fatal("rejected retry changed history")
			}
			if _, _, err := s.EnqueueJob(other.ID, "scheduled", "", "", 20); err != nil {
				t.Fatalf("other project blocked %v", err)
			}
			if _, err := s.CancelJob(webhook.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RetryJob(j.ID, 20); err != nil {
				t.Fatal(err)
			}
			after, _ = s.GetJob(j.ID)
			if !reflect.DeepEqual(history.Attempts, after.Attempts) || !history.CreatedAt.Equal(after.CreatedAt) {
				t.Fatal("retry lost original timestamps")
			}
			// Relax the project cap: global capacity remains authoritative.
			if _, err := s.PutProjectLimits(pid, 1, 0, 0); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.EnqueueJob(pid, "scheduled", "", "", 2); !errors.Is(err, ErrQueueFull) {
				t.Fatalf("global bypass %v", err)
			}
		})
	}
}

func TestProjectQueueLimitConcurrentAndLowered(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			s, pid := jobStore(t)
			if backend == "sqlite" {
				if _, err := MigrateQueue(s.HomeDir()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.PutProjectLimits(pid, 0, 5, 2); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			var admitted atomic.Int32
			for i := range 30 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					writer := s
					// Independent handles exercise SQLite's transaction boundary,
					// not just one Store's in-process mutex.
					if backend == "sqlite" {
						var err error
						writer, err = New(s.HomeDir())
						if err != nil {
							t.Error(err)
							return
						}
					}
					_, _, err := writer.EnqueueJob(pid, "github_push", fmt.Sprint(i), "digest", 100)
					if err == nil {
						admitted.Add(1)
					} else if !errors.Is(err, ErrProjectQueueFull) {
						t.Error(err)
					}
				}(i)
			}
			wg.Wait()
			if admitted.Load() != 5 {
				t.Fatalf("admitted %d", admitted.Load())
			}
			before, _ := s.ListJobs(pid)
			if _, err := s.PutProjectLimits(pid, 1, 1, 1); err != nil {
				t.Fatal(err)
			}
			after, _ := s.ListJobs(pid)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("lowering changed jobs")
			}
			if _, _, err := s.EnqueueJob(pid, "manual", "", "", 100); !errors.Is(err, ErrProjectQueueFull) {
				t.Fatalf("lowered cap %v", err)
			}
			for _, j := range before {
				if _, err := s.CancelJob(j.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := s.EnqueueJob(pid, "manual", "", "", 100); err != nil {
				t.Fatalf("drained cap %v", err)
			}
		})
	}
}
