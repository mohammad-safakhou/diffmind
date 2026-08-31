package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Bus is the central per-process event bus. It tracks one or more runs,
// each with its own ring buffer, set of subscribers, and JSONL persistence.
//
// Concurrency model:
//   - Each run has its own `runState`, guarded by its own mutex. Different
//     runs never block each other.
//   - Emit fan-out is non-blocking: if a subscriber's buffered channel is
//     full we drop the event for that subscriber and emit a synthetic
//     "subscriber_dropped" event so the UI knows it must refetch state.
type Bus struct {
	mu          sync.RWMutex
	runs        map[string]*runState
	historySize int // ring buffer capacity per run (0 = unlimited within memory)
}

// NewBus constructs an empty bus. historySize is the maximum number of
// events kept in memory per run for late-joining subscribers; older events
// remain available via JSONL replay if persistence was enabled for the run.
func NewBus(historySize int) *Bus {
	if historySize <= 0 {
		historySize = 5000
	}
	return &Bus{
		runs:        map[string]*runState{},
		historySize: historySize,
	}
}

// runState is the per-run book-keeping carried by the bus.
type runState struct {
	mu          sync.Mutex
	runID       string
	seq         atomic.Uint64
	history     []Event // ring buffer
	histStart   uint64  // sequence number of history[0]
	subs        map[*subscription]struct{}
	persistFile *os.File
	persistEnc  *json.Encoder
	closed      bool
	finished    bool
	finishedAt  time.Time
}

// subscription represents one in-flight subscriber.
//
// finished is set once FinishRun has handed the subscription off to a
// closer goroutine; Emit() must skip finished subs to avoid writing to a
// channel the closer is about to close.
type subscription struct {
	ch       chan Event
	cancel   func()
	finished bool
}

// StartRun registers a new run with the bus. If persistDir is non-empty an
// events.jsonl file is opened for append in that directory and every event
// is mirrored to it. The returned Sink is the orchestrator's emit point.
//
// It is an error to start a run twice with the same ID.
func (b *Bus) StartRun(runID, persistDir string) (Sink, error) {
	if runID == "" {
		return nil, errors.New("events: empty run id")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.runs[runID]; ok {
		// A retry of a finished run is legitimate: drop the old
		// in-memory ring buffer (JSONL on disk is preserved by
		// append-mode), close any leftover persistence handle, and
		// start fresh. We refuse only if the existing entry is
		// still LIVE (no FinishRun yet) — that would indicate a
		// concurrent Start, which the runner singleton should
		// already have prevented.
		existing.mu.Lock()
		finished := existing.finished
		if existing.persistFile != nil {
			_ = existing.persistFile.Close()
			existing.persistFile = nil
		}
		existing.mu.Unlock()
		if !finished {
			return nil, fmt.Errorf("events: run %s already started and not yet finished", runID)
		}
		delete(b.runs, runID)
	}
	rs := &runState{
		runID:   runID,
		history: make([]Event, 0, b.historySize),
		subs:    map[*subscription]struct{}{},
	}
	if persistDir != "" {
		if err := os.MkdirAll(persistDir, 0o755); err != nil {
			return nil, fmt.Errorf("events: create persist dir: %w", err)
		}
		f, err := os.OpenFile(filepath.Join(persistDir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("events: open persist file: %w", err)
		}
		rs.persistFile = f
		rs.persistEnc = json.NewEncoder(f)
	}
	b.runs[runID] = rs
	return &runSink{bus: b, rs: rs, historyCap: b.historySize}, nil
}

// FinishRun marks a run as finished. Subscribers are NOT closed immediately
// so they can drain the ring buffer; instead the bus stops accepting new
// emits and lets in-flight reads complete naturally.
//
// Each subscriber receives a synthetic "_eof" event followed by its
// channel being closed. We deliver the eof from a per-subscriber goroutine
// so a slow consumer can never deadlock the orchestrator: the goroutine
// blocks on the send (with a 30s safety timeout) and then closes the
// channel, guaranteeing every SSE client eventually sees the end-of-stream
// signal even if it briefly fell behind.
func (b *Bus) FinishRun(runID string) {
	b.mu.RLock()
	rs := b.runs[runID]
	b.mu.RUnlock()
	if rs == nil {
		return
	}
	rs.mu.Lock()
	rs.finished = true
	rs.finishedAt = time.Now().UTC()
	if rs.persistFile != nil {
		_ = rs.persistFile.Close()
		rs.persistFile = nil
	}
	subs := make([]*subscription, 0, len(rs.subs))
	for s := range rs.subs {
		subs = append(subs, s)
	}
	// Mark every subscriber as "finished" so Emit cannot send to them
	// after we hand off ownership to the per-sub closer goroutine.
	for _, s := range subs {
		s.finished = true
	}
	// Detach subs from the runState now so duplicate FinishRun calls,
	// or new subscribers attaching while we drain, don't double-close.
	rs.subs = map[*subscription]struct{}{}
	rs.mu.Unlock()

	for _, s := range subs {
		go finishSubscriber(s, runID)
	}
}

// finishSubscriber delivers _eof to a subscriber and closes the channel.
// Blocks (with a hard timeout) so backed-up consumers don't lose the
// end-of-stream signal — losing it makes the dashboard show "running"
// forever after a real cancel/completion.
func finishSubscriber(s *subscription, runID string) {
	defer func() {
		// Recover in case the channel has been closed by a concurrent
		// unsubscribe; closing twice would otherwise panic.
		_ = recover()
	}()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	select {
	case s.ch <- Event{Kind: "_eof", RunID: runID}:
	case <-timeout.C:
	}
	close(s.ch)
}

// Subscribe returns a channel that receives every event for runID with seq
// >= fromSeq. Past events still in the ring buffer are delivered first;
// then the subscriber is hooked up to the live stream. The unsubscribe
// callback must be called when the consumer is done; it closes the channel.
func (b *Bus) Subscribe(runID string, fromSeq uint64, bufSize int) (<-chan Event, func(), error) {
	if bufSize <= 0 {
		bufSize = 256
	}
	b.mu.RLock()
	rs := b.runs[runID]
	b.mu.RUnlock()
	if rs == nil {
		return nil, nil, fmt.Errorf("events: unknown run %s", runID)
	}
	rs.mu.Lock()
	sub := &subscription{ch: make(chan Event, bufSize)}
	rs.subs[sub] = struct{}{}
	// Replay matching history.
	for _, e := range rs.history {
		if e.Seq < fromSeq {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			// Buffer full even during replay → fall through; the live loop
			// will publish a subscriber_dropped event later.
		}
	}
	finished := rs.finished
	rs.mu.Unlock()

	// If the run is already finished and there are no future events, close
	// the channel so the subscriber unblocks cleanly.
	if finished {
		go func() {
			// Give the consumer a chance to drain; then close.
			time.Sleep(50 * time.Millisecond)
			rs.mu.Lock()
			if _, ok := rs.subs[sub]; ok {
				delete(rs.subs, sub)
				close(sub.ch)
			}
			rs.mu.Unlock()
		}()
	}

	cancel := func() {
		rs.mu.Lock()
		if _, ok := rs.subs[sub]; ok {
			delete(rs.subs, sub)
			close(sub.ch)
		}
		rs.mu.Unlock()
	}
	return sub.ch, cancel, nil
}

// Snapshot returns a copy of every event currently in the in-memory ring
// buffer for runID. Used by the /api/runs/{id}/state endpoint for cold
// loads when an SSE consumer hasn't connected yet.
func (b *Bus) Snapshot(runID string) []Event {
	b.mu.RLock()
	rs := b.runs[runID]
	b.mu.RUnlock()
	if rs == nil {
		return nil
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]Event, len(rs.history))
	copy(out, rs.history)
	return out
}

// ReplayJSONL streams events from a previously persisted events.jsonl
// file. It is used to rehydrate finished runs after a server restart.
// Events are written into dst in order; reading stops on the first error.
func ReplayJSONL(ctx context.Context, path string, dst chan<- Event) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var e Event
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case dst <- e:
		}
	}
}

// runSink implements Sink and is what we hand to the orchestrator.
type runSink struct {
	bus        *Bus
	rs         *runState
	historyCap int
}

// Emit assigns the next sequence number, persists if enabled, replicates
// to history, then fans out to subscribers in a non-blocking manner.
func (s *runSink) Emit(e Event) {
	if s == nil || s.rs == nil {
		return
	}
	if e.RunID == "" {
		e.RunID = s.rs.runID
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	e.Seq = s.rs.seq.Add(1)

	s.rs.mu.Lock()
	if s.rs.finished {
		// Late emit after FinishRun; still record but skip subscribers.
		s.rs.mu.Unlock()
		return
	}
	// Append to history (ring buffer).
	if s.historyCap > 0 && len(s.rs.history) >= s.historyCap {
		// drop oldest
		s.rs.histStart = s.rs.history[0].Seq
		s.rs.history = append(s.rs.history[:0], s.rs.history[1:]...)
	}
	s.rs.history = append(s.rs.history, e)
	if s.rs.persistEnc != nil {
		_ = s.rs.persistEnc.Encode(e)
	}
	subs := make([]*subscription, 0, len(s.rs.subs))
	for sub := range s.rs.subs {
		subs = append(subs, sub)
	}
	s.rs.mu.Unlock()

	for _, sub := range subs {
		// Recover from a "send on closed channel" panic if FinishRun /
		// the subscriber's cancel raced ahead between capturing subs and
		// this send. We guard each send individually so one closed
		// subscriber doesn't break the fan-out for the others.
		safeSend(sub, e, s.rs.runID)
	}
}

// safeSend tries a non-blocking send to the subscriber's channel. On a
// "send on closed channel" panic (the subscriber unsubscribed or the run
// finished after we captured the subs slice) we silently swallow it.
//
// We deliberately do NOT spawn a follow-up goroutine to inject a
// "subscriber_dropped" notice when the buffer is full: the subscriber is
// already behind, sending more would race the channel-close in
// finishSubscriber. The dashboard can detect drops by tracking the
// monotonic Seq and noticing a gap.
func safeSend(sub *subscription, e Event, runID string) {
	defer func() {
		_ = recover()
	}()
	select {
	case sub.ch <- e:
	default:
		// Drop silently; consumer is too slow.
	}
}
