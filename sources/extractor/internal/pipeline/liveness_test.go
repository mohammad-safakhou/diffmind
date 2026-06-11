package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/opencode"
)

// scriptedProbe returns whatever the caller pushes into it. Tests
// build a sequence of probeSnapshots and the watchdog consumes them
// in order; once the script is exhausted the last snapshot is
// returned indefinitely so the watchdog can keep polling without
// the test needing to be exact about iteration count.
type scriptedProbe struct {
	mu  sync.Mutex
	seq []probeSnapshot
	pos int
}

func newScriptedProbe(seq ...probeSnapshot) *scriptedProbe {
	return &scriptedProbe{seq: seq}
}

func (p *scriptedProbe) Snapshot(ctx context.Context) (probeSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pos < len(p.seq) {
		s := p.seq[p.pos]
		p.pos++
		return s, nil
	}
	if len(p.seq) == 0 {
		return probeSnapshot{}, nil
	}
	return p.seq[len(p.seq)-1], nil
}

// countingAborter records how many times Abort was called.
type countingAborter struct {
	n atomic.Int32
}

func (c *countingAborter) Abort(ctx context.Context) error {
	c.n.Add(1)
	return nil
}

// snap is a builder for a probeSnapshot. We use it to keep test
// bodies readable.
type snap struct {
	parts    []opencode.MessagePart
	activity int64
	pending  bool
}

func (s snap) build() probeSnapshot {
	session := opencode.SessionState{ID: "ses_test"}
	session.Time.Updated = s.activity
	msg := opencode.Message{}
	msg.Info.ID = "msg_test"
	msg.Parts = s.parts
	return probeSnapshot{
		Session:        session,
		Latest:         msg,
		PermissionWait: s.pending,
	}
}

func textPart(text string, start int64) opencode.MessagePart {
	p := opencode.MessagePart{Type: "text", Text: text}
	t := struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	}{Start: start}
	p.Time = &t
	return p
}

func toolPart(name, status string, start int64) opencode.MessagePart {
	p := opencode.MessagePart{Type: "tool", Tool: name}
	state := struct {
		Status string `json:"status"`
		Time   *struct {
			Start int64 `json:"start"`
			End   int64 `json:"end"`
		} `json:"time,omitempty"`
		Input  map[string]any `json:"input,omitempty"`
		Output string         `json:"output,omitempty"`
		Title  string         `json:"title,omitempty"`
	}{Status: status, Title: name}
	if start > 0 {
		t := struct {
			Start int64 `json:"start"`
			End   int64 `json:"end"`
		}{Start: start}
		state.Time = &t
	}
	p.State = &state
	return p
}

// When the call completes (watchCtx is cancelled) before any
// idle/ceiling threshold trips, runLiveness must return nil — the
// orchestrator interprets that as "no abort needed".
func TestLivenessReturnsNilWhenCancelled(t *testing.T) {
	probe := newScriptedProbe()
	abort := &countingAborter{}
	ctx, cancel := context.WithCancel(context.Background())
	cfg := livenessConfig{
		IdleTimeout:  100 * time.Millisecond,
		MaxCall:      10 * time.Second,
		PollInterval: 10 * time.Millisecond,
	}
	done := make(chan *livenessReport, 1)
	go func() { done <- runLiveness(ctx, cfg, probe, abort, "test", nil) }()

	// Cancel immediately; watchdog must exit cleanly.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case rep := <-done:
		if rep != nil {
			t.Fatalf("expected nil report on cancel; got %+v", rep)
		}
	case <-time.After(time.Second):
		t.Fatalf("watchdog did not exit after cancel")
	}
	if abort.n.Load() != 0 {
		t.Errorf("must not abort when caller cancels: abort count = %d", abort.n.Load())
	}
}

// When parts keep growing on every poll, the idle clock never trips
// and the watchdog stays running indefinitely.
func TestLivenessDoesNotAbortOnSteadyProgress(t *testing.T) {
	// Build a script where each poll sees one more part.
	seq := []probeSnapshot{}
	for i := 1; i <= 30; i++ {
		seq = append(seq, snap{
			parts: append([]opencode.MessagePart{}, textPart("part", int64(i))),
		}.build())
	}
	probe := newScriptedProbe(seq...)
	abort := &countingAborter{}

	ctx, cancel := context.WithCancel(context.Background())
	cfg := livenessConfig{
		IdleTimeout:  50 * time.Millisecond,
		MaxCall:      10 * time.Second,
		PollInterval: 10 * time.Millisecond,
	}
	done := make(chan *livenessReport, 1)
	go func() { done <- runLiveness(ctx, cfg, probe, abort, "test", nil) }()

	// Run for well past one idle window with continuous progress
	// (each poll sees a new part), then cancel.
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case rep := <-done:
		if rep != nil {
			t.Fatalf("steady progress must NOT cause abort; got %+v", rep)
		}
	case <-time.After(time.Second):
		t.Fatalf("watchdog did not exit")
	}
	if abort.n.Load() != 0 {
		t.Errorf("must not abort on continuous progress: count = %d", abort.n.Load())
	}
}

// When parts have stopped growing AND no tool is running AND no
// permission is pending, the watchdog must abort once IdleTimeout
// elapses since the last observed activity.
func TestLivenessAbortsOnIdle(t *testing.T) {
	// Single snapshot, returned forever — no growth.
	frozen := snap{parts: []opencode.MessagePart{textPart("hello", 100)}}.build()
	probe := newScriptedProbe(frozen)
	abort := &countingAborter{}

	cfg := livenessConfig{
		IdleTimeout:  60 * time.Millisecond,
		MaxCall:      10 * time.Second,
		PollInterval: 15 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan *livenessReport, 1)
	go func() { done <- runLiveness(ctx, cfg, probe, abort, "test", nil) }()

	select {
	case rep := <-done:
		if rep == nil {
			t.Fatalf("expected an abort report")
		}
		if !rep.Aborted {
			t.Errorf("report.Aborted must be true; got %+v", rep)
		}
		if rep.Reason == "" {
			t.Errorf("Reason must explain the abort; got empty string")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("watchdog did not abort within 2s; idle timeout was 60ms")
	}
	if abort.n.Load() == 0 {
		t.Errorf("Abort() must have been called at least once")
	}
}

// A tool with status=running must not pause the idle clock forever.
// OpenCode's task tool can spawn a subagent whose inner progress is not
// visible to the parent message; if the parent sees no further progress,
// the watchdog should abort at IdleTimeout instead of waiting for MaxCall.
func TestLivenessAbortsWhenToolRunningWithoutProgress(t *testing.T) {
	frozen := snap{parts: []opencode.MessagePart{toolPart("read", "running", 50)}}.build()
	probe := newScriptedProbe(frozen)
	abort := &countingAborter{}

	cfg := livenessConfig{
		IdleTimeout:  40 * time.Millisecond,
		MaxCall:      10 * time.Second,
		PollInterval: 15 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *livenessReport, 1)
	go func() { done <- runLiveness(ctx, cfg, probe, abort, "test", nil) }()

	select {
	case rep := <-done:
		if rep == nil {
			t.Fatalf("expected abort while running tool made no progress")
		}
		if !rep.Aborted {
			t.Errorf("report.Aborted must be true; got %+v", rep)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("watchdog did not abort running tool")
	}
	if abort.n.Load() == 0 {
		t.Errorf("Abort must be called for stuck running tool")
	}
}

// A pending permission pauses the idle clock — the agent is waiting
// on us, that's not idleness.
func TestLivenessPausesWhilePermissionPending(t *testing.T) {
	frozen := snap{
		parts:   []opencode.MessagePart{textPart("waiting", 50)},
		pending: true,
	}.build()
	probe := newScriptedProbe(frozen)
	abort := &countingAborter{}

	cfg := livenessConfig{
		IdleTimeout:  40 * time.Millisecond,
		MaxCall:      10 * time.Second,
		PollInterval: 15 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *livenessReport, 1)
	go func() { done <- runLiveness(ctx, cfg, probe, abort, "test", nil) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case rep := <-done:
		if rep != nil {
			t.Fatalf("must not abort while permission pending; got %+v", rep)
		}
	case <-time.After(time.Second):
		t.Fatalf("watchdog did not exit")
	}
	if abort.n.Load() != 0 {
		t.Errorf("Abort must not be called: count = %d", abort.n.Load())
	}
}

// MaxCall is a hard ceiling: even with continuous progress, the
// watchdog aborts once we exceed it. A runaway loop can produce a
// new part every second forever; this guards against that.
func TestLivenessAbortsOnHardCeiling(t *testing.T) {
	// Continuous progress: each poll sees a brand new part.
	seq := []probeSnapshot{}
	for i := 1; i <= 100; i++ {
		seq = append(seq, snap{parts: []opencode.MessagePart{textPart("p", int64(i))}}.build())
	}
	probe := newScriptedProbe(seq...)
	abort := &countingAborter{}

	cfg := livenessConfig{
		IdleTimeout:  10 * time.Second, // idle would never trip in this test
		MaxCall:      80 * time.Millisecond,
		PollInterval: 15 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *livenessReport, 1)
	go func() { done <- runLiveness(ctx, cfg, probe, abort, "test", nil) }()

	select {
	case rep := <-done:
		if rep == nil {
			t.Fatalf("expected an abort report from hard ceiling")
		}
		if !rep.Aborted {
			t.Errorf("report.Aborted must be true; got %+v", rep)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("watchdog did not abort on MaxCall within 2s")
	}
}

// Snapshot errors are treated as "no information" — the watchdog
// neither aborts nor counts them as progress. The idle clock keeps
// ticking, but only the genuine idle threshold will trip.
type erroringProbe struct{ n atomic.Int32 }

func (e *erroringProbe) Snapshot(ctx context.Context) (probeSnapshot, error) {
	e.n.Add(1)
	return probeSnapshot{}, errors.New("network blip")
}

func TestLivenessTreatsProbeErrorAsNoInfo(t *testing.T) {
	probe := &erroringProbe{}
	abort := &countingAborter{}
	cfg := livenessConfig{
		IdleTimeout:  60 * time.Millisecond,
		MaxCall:      10 * time.Second,
		PollInterval: 15 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *livenessReport, 1)
	go func() { done <- runLiveness(ctx, cfg, probe, abort, "test", nil) }()

	select {
	case rep := <-done:
		// We DO expect an idle abort once IdleTimeout elapses with
		// no successful snapshots. The probe error itself doesn't
		// "rescue" the call.
		if rep == nil {
			t.Fatalf("expected idle abort even when probes errored")
		}
		if !rep.Aborted {
			t.Errorf("report.Aborted must be true; got %+v", rep)
		}
	case <-time.After(time.Second):
		t.Fatalf("watchdog did not abort within 1s")
	}
	if probe.n.Load() < 2 {
		t.Errorf("expected at least 2 probe attempts; got %d", probe.n.Load())
	}
}
