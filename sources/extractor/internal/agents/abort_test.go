package agents

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// fakeAbortable is an OpenCode fake that fails every prompt AND implements
// pauseHandler so the orchestrator wires up the watchdog and abort-on-failure.
type fakeAbortable struct {
	mu        sync.Mutex
	createIDs []string
	aborted   []string
	prompts   int
}

func (f *fakeAbortable) Enabled() bool { return true }
func (f *fakeAbortable) CreateSession(ctx context.Context, directory string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("s%d", len(f.createIDs)+1)
	f.createIDs = append(f.createIDs, id)
	return id, nil
}
func (f *fakeAbortable) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeAbortable) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	return "", fmt.Errorf("prompt boom")
}
func (f *fakeAbortable) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	f.mu.Lock()
	f.prompts++
	f.mu.Unlock()
	return nil, fmt.Errorf("prompt boom")
}

// pauseHandler implementation
func (f *fakeAbortable) AbortSession(ctx context.Context, sessionID, directory string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborted = append(f.aborted, sessionID)
	return nil
}
func (f *fakeAbortable) ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error) {
	return nil, nil
}
func (f *fakeAbortable) RespondPermission(ctx context.Context, sessionID, permissionID, directory, response string) error {
	return nil
}
func (f *fakeAbortable) ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error) {
	return nil, nil
}
func (f *fakeAbortable) RejectQuestion(ctx context.Context, requestID, directory string) error {
	return nil
}

func (f *fakeAbortable) snap() (creates, aborts []string, prompts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.createIDs...), append([]string{}, f.aborted...), f.prompts
}

// On prompt failure the orchestrator must call AbortSession for every session
// it created. This exercises the bestEffortAbort path.
func TestPromptFailureTriggersAbort(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	f := &fakeAbortable{}

	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	if err != nil {
		t.Fatalf("Run should not error fatally on prompt failures: %v", err)
	}
	creates, aborts, prompts := f.snap()
	if prompts == 0 {
		t.Fatalf("expected at least one PromptStructured call")
	}
	if len(creates) == 0 {
		t.Fatalf("expected at least one CreateSession")
	}
	if len(aborts) == 0 {
		t.Fatalf("expected AbortSession to be called at least once on prompt failure")
	}
	if len(res.Warnings) == 0 {
		t.Fatalf("expected per-stage warnings to be recorded; got %+v", res.Warnings)
	}
	// Aborts should be a subset of created sessions.
	createSet := map[string]struct{}{}
	for _, c := range creates {
		createSet[c] = struct{}{}
	}
	for _, a := range aborts {
		if _, ok := createSet[a]; !ok {
			t.Fatalf("aborted unknown session %q (creates=%v)", a, creates)
		}
	}
}
