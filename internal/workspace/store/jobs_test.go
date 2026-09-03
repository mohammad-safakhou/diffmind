package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func jobStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(Project{Name: "Jobs"})
	if err != nil {
		t.Fatal(err)
	}
	return s, p.ID
}
func TestJobsDurabilityDedupCapacityAndAttempts(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			s, pid := jobStore(t)
			if backend == "sqlite" {
				if _, err := MigrateQueue(s.HomeDir()); err != nil {
					t.Fatal(err)
				}
			}
			testJobDurability(t, s, pid)
		})
	}
}
func testJobDurability(t *testing.T, s *Store, pid string) {
	j, dup, err := s.EnqueueJob(pid, "github_push", "delivery-1", "digest-1", 1)
	if err != nil || dup {
		t.Fatalf("enqueue %+v %v", j, err)
	}
	duplicate, dup, err := s.EnqueueJob(pid, "github_push", "delivery-1", "digest-1", 1)
	if err != nil || !dup || duplicate.ID != j.ID {
		t.Fatalf("dedup %+v %v", duplicate, err)
	}
	if _, _, err := s.EnqueueJob(pid, "github_push", "delivery-1", "other", 1); !errors.Is(err, ErrDeliveryConflict) {
		t.Fatalf("conflict %v", err)
	}
	if _, _, err := s.EnqueueJob(pid, "manual", "", "", 1); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("capacity %v", err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		claimed, err := s.ClaimJob(time.Now().UTC())
		if err != nil || claimed == nil || len(claimed.Attempts) != attempt {
			t.Fatalf("claim %+v %v", claimed, err)
		}
		if err := s.FinishJob(j.ID, JobAttempt{Status: "failed", Error: "test failure"}, false, 0); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := New(s.HomeDir())
	if err != nil {
		t.Fatal(err)
	}
	final, err := reopened.GetJob(j.ID)
	if err != nil || final.Status != "failed" || len(final.Attempts) != 3 {
		t.Fatalf("history %+v %v", final, err)
	}
	for _, a := range final.Attempts {
		if a.StartedAt.IsZero() || a.FinishedAt.Before(a.StartedAt) || a.Error != "test failure" {
			t.Fatalf("attempt %+v", a)
		}
	}
	if _, err := reopened.RetryJob(j.ID, 1); err != nil {
		t.Fatal(err)
	}
	claimed, err := reopened.ClaimJob(time.Now())
	if err != nil || len(claimed.Attempts) != 4 {
		t.Fatalf("retry %+v %v", claimed, err)
	}
	if err := reopened.RecoverJobs(); err != nil {
		t.Fatal(err)
	}
	recovered, _ := reopened.GetJob(j.ID)
	if recovered.Status != "queued" || recovered.Attempts[3].Status != "interrupted" {
		t.Fatalf("recovery %+v", recovered)
	}
	if _, err := reopened.CancelJob(j.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.RecoverJobs(); err != nil {
		t.Fatal(err)
	}
	recovered, _ = reopened.GetJob(j.ID)
	if recovered.Status != "cancelled" {
		t.Fatalf("cancel resurrected %+v", recovered)
	}
	duplicate, dup, err = reopened.EnqueueJob(pid, "github_push", "delivery-1", "digest-1", 1)
	if err != nil || !dup || duplicate.Status != "cancelled" {
		t.Fatalf("dedup lost after restart: %+v %v", duplicate, err)
	}
}
func TestJobsConcurrentDedupAndProjectSerialization(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			s, pid := jobStore(t)
			if backend == "sqlite" {
				if _, err := MigrateQueue(s.HomeDir()); err != nil {
					t.Fatal(err)
				}
			}
			testJobConcurrency(t, s, pid)
		})
	}
}
func testJobConcurrency(t *testing.T, s *Store, pid string) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.EnqueueJob(pid, "github_push", "same", "digest", 5); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	all, _ := s.ListJobs("")
	if len(all) != 1 {
		t.Fatalf("duplicate jobs %d", len(all))
	}
	if _, _, err := s.EnqueueJob(pid, "manual", "", "", 5); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimJob(time.Now())
	if err != nil || first == nil {
		t.Fatal(err)
	}
	if second, err := s.ClaimJob(time.Now()); err != nil || second != nil {
		t.Fatalf("same project claimed twice %+v %v", second, err)
	}
	if err := s.FinishJob(first.ID, JobAttempt{}, true, 0); err != nil {
		t.Fatal(err)
	}
	deferred, _ := s.GetJob(first.ID)
	if len(deferred.Attempts) != 0 || deferred.Status != "queued" {
		t.Fatalf("busy burned attempt %+v", deferred)
	}
	claim, _ := s.ClaimJob(time.Now())
	if _, err := s.CancelJob(claim.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishJob(claim.ID, JobAttempt{Status: "succeeded"}, false, 0); err != nil {
		t.Fatal(err)
	}
	final, _ := s.GetJob(claim.ID)
	if final.Status != "cancelled" {
		t.Fatalf("cancellation intent lost %+v", final)
	}
}
func TestIngestionHistoryPreservesRetriesAndLegacy(t *testing.T) {
	s, pid := jobStore(t)
	first, err := s.CreateIngestion(pid, Ingestion{Status: IngestionRunning})
	if err != nil {
		t.Fatal(err)
	}
	first.Status = IngestionFailed
	first.Errors = []string{"failed first"}
	first.FinishedAt = time.Now().UTC()
	if err := s.SaveIngestion(pid, *first); err != nil {
		t.Fatal(err)
	}
	retry := *first
	retry.Attempt = 2
	retry.Status = IngestionCompleted
	retry.Errors = nil
	if err := s.SaveIngestion(pid, retry); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIngestion(pid, Ingestion{Status: IngestionCompleted}); err != nil {
		t.Fatal(err)
	}
	history, err := s.IngestionHistory(pid)
	if err != nil || len(history) != 3 {
		t.Fatalf("history %+v %v", history, err)
	}
	old, err := s.GetIngestionAttempt(pid, first.ID, 1)
	if err != nil || old.Status != IngestionFailed || len(old.Errors) != 1 || !old.StartedAt.Equal(first.StartedAt) {
		t.Fatalf("old %+v %v", old, err)
	}
	if err := s.SaveIngestion(pid, *first); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale job replaced latest: %v", err)
	}
	if _, err := s.IngestionHistory("../bad"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsafe ID %v", err)
	}
}
func TestJobsCorruptionAndFailedPersistenceDoNotAcknowledge(t *testing.T) {
	s, pid := jobStore(t)
	if err := os.WriteFile(filepath.Join(s.HomeDir(), "jobs"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnqueueJob(pid, "manual", "", "", 4); err == nil {
		t.Fatal("accepted without durable write")
	}
	s2, pid2 := jobStore(t)
	j, _, _ := s2.EnqueueJob(pid2, "manual", "", "", 4)
	if err := os.WriteFile(s2.jobPath(j.ID), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.ClaimJob(time.Now()); err == nil {
		t.Fatal("corrupt queue silently skipped")
	}
}
