package llmrun

import (
	"context"
	"sync"
	"testing"
)

type sessionClientFake struct {
	mu      sync.Mutex
	created int
	deleted []string
	aborted []string
}

func (f *sessionClientFake) Enabled() bool { return true }
func (f *sessionClientFake) CreateSession(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	return "session-" + string(rune('0'+f.created)), nil
}
func (f *sessionClientFake) DeleteSession(_ context.Context, sessionID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, sessionID)
	return nil
}
func (f *sessionClientFake) PromptStructured(context.Context, string, string, string, map[string]any) (map[string]any, error) {
	return nil, nil
}
func (f *sessionClientFake) PromptText(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (f *sessionClientFake) AbortSession(_ context.Context, sessionID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborted = append(f.aborted, sessionID)
	return nil
}
func (f *sessionClientFake) ListPermissions(context.Context, string) ([]PendingPermission, error) {
	return nil, nil
}
func (f *sessionClientFake) RespondPermission(context.Context, string, string, string, string) error {
	return nil
}
func (f *sessionClientFake) ListQuestions(context.Context, string) ([]PendingQuestion, error) {
	return nil, nil
}
func (f *sessionClientFake) RejectQuestion(context.Context, string, string) error {
	return nil
}
func (f *sessionClientFake) LookupSessionParent(context.Context, string, string) (string, error) {
	return "", nil
}

func TestSessionManagerReusesAndClosesSharedSession(t *testing.T) {
	client := &sessionClientFake{}
	manager := NewSessionManager(SessionOptions{
		Client: client, Pauser: client, Directory: "/snapshot", Reuse: true, Cleanup: true,
	})
	first, firstCleanup, err := manager.Acquire(context.Background(), "repo_facts")
	if err != nil {
		t.Fatal(err)
	}
	second, secondCleanup, err := manager.Acquire(context.Background(), "discover.http")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || firstCleanup != nil || secondCleanup != nil {
		t.Fatalf("shared sessions differ: first=%q second=%q", first, second)
	}
	manager.Abort("discover.http", first)
	manager.Close()

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.created != 1 {
		t.Fatalf("created = %d, want 1", client.created)
	}
	if len(client.aborted) != 1 || client.aborted[0] != first {
		t.Fatalf("aborted = %v", client.aborted)
	}
	if len(client.deleted) != 1 || client.deleted[0] != first {
		t.Fatalf("deleted = %v", client.deleted)
	}
}

func TestSessionManagerResetCreatesNewSharedSession(t *testing.T) {
	client := &sessionClientFake{}
	manager := NewSessionManager(SessionOptions{Client: client, Reuse: true})
	first, _, err := manager.Acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	manager.ResetAfterStuck()
	second, _, err := manager.Acquire(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("reset reused %q", first)
	}
}
