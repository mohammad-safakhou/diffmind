package llmrun

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/events"
)

type executorFake struct {
	mu              sync.Mutex
	structuredCalls int
	textCalls       int
	structured      map[string]any
	structuredErr   error
	text            string
}

func (f *executorFake) Enabled() bool { return true }
func (f *executorFake) CreateSession(context.Context, string) (string, error) {
	return "session-1", nil
}
func (f *executorFake) DeleteSession(context.Context, string, string) error { return nil }
func (f *executorFake) PromptStructured(context.Context, string, string, string, map[string]any) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.structuredCalls++
	return f.structured, f.structuredErr
}
func (f *executorFake) PromptText(context.Context, string, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.textCalls++
	return f.text, nil
}
func (f *executorFake) GetSession(context.Context, string, string) (SessionState, error) {
	return SessionState{ID: "session-1", Input: 10, Output: 4, Reasoning: 1, Cost: 0.01}, nil
}

type eventSink struct {
	mu     sync.Mutex
	events []events.Event
}

func (s *eventSink) Emit(event events.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func TestExecutorRecoversMissingStructuredPayloadWithText(t *testing.T) {
	client := &executorFake{
		structuredErr: errors.New("no structured payload in response"),
		text:          `{"items":["recovered"]}`,
	}
	sink := &eventSink{}
	executor := NewExecutor(ExecutorOptions{
		Client: client, Sink: sink, Directory: "/snapshot",
	})
	payload, err := executor.Prompt(context.Background(), "discover.http", "prompt", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if payload["items"] == nil {
		t.Fatalf("payload = %#v", payload)
	}
	client.mu.Lock()
	if client.structuredCalls != 1 || client.textCalls != 1 {
		t.Fatalf("structured=%d text=%d", client.structuredCalls, client.textCalls)
	}
	client.mu.Unlock()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	completed := sink.events[len(sink.events)-1]
	if completed.Kind != events.KindLLMCallCompleted || completed.Payload["fallback"] != "text" {
		t.Fatalf("completion event = %+v", completed)
	}
}

func TestExecutorRecordsTokensBeforeCompletion(t *testing.T) {
	client := &executorFake{structured: map[string]any{"items": []any{}}}
	sink := &eventSink{}
	var totals TokenTotals
	executor := NewExecutor(ExecutorOptions{
		Client: client, Tokens: client, Totals: &totals, Sink: sink,
	})
	if _, err := executor.Prompt(context.Background(), "discover.http", "prompt", nil); err != nil {
		t.Fatal(err)
	}
	stage := totals.Stage("discovery")
	if stage == nil || stage.Total() != 15 {
		t.Fatalf("discovery tokens = %+v", stage)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	completed := sink.events[len(sink.events)-1]
	tokens, ok := completed.Payload["tokens"].(map[string]any)
	if !ok || tokens["total"] != 15 {
		t.Fatalf("completion tokens = %#v", completed.Payload["tokens"])
	}
}
