package events

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBusEmitAssignsMonotonicSeq(t *testing.T) {
	b := NewBus(100)
	sink, err := b.StartRun("r1", "")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	for i := 0; i < 5; i++ {
		sink.Emit(Event{Kind: KindLog, Message: "hi"})
	}
	snap := b.Snapshot("r1")
	if len(snap) != 5 {
		t.Fatalf("expected 5 events, got %d", len(snap))
	}
	for i := 1; i < len(snap); i++ {
		if snap[i].Seq != snap[i-1].Seq+1 {
			t.Fatalf("expected monotonic seq, got %d after %d", snap[i].Seq, snap[i-1].Seq)
		}
	}
}

func TestBusSubscribeReplaysHistoryThenStreamsLive(t *testing.T) {
	b := NewBus(100)
	sink, _ := b.StartRun("r1", "")
	sink.Emit(Event{Kind: KindStageStarted})
	sink.Emit(Event{Kind: KindStageProgress})

	ch, cancel, err := b.Subscribe("r1", 0, 16)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	// Read replayed events (non-blocking with timeout).
	timeout := time.After(time.Second)
	got := []Event{}
loop:
	for len(got) < 2 {
		select {
		case e := <-ch:
			got = append(got, e)
		case <-timeout:
			break loop
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 replayed events, got %d", len(got))
	}
	// New event should arrive live.
	go sink.Emit(Event{Kind: KindStageCompleted})
	select {
	case e := <-ch:
		if e.Kind != KindStageCompleted {
			t.Fatalf("expected stage_completed, got %s", e.Kind)
		}
	case <-time.After(time.Second):
		t.Fatalf("did not receive live event")
	}
}

func TestBusSubscribeFromSeqSkipsOlder(t *testing.T) {
	b := NewBus(100)
	sink, _ := b.StartRun("r1", "")
	for i := 0; i < 5; i++ {
		sink.Emit(Event{Kind: KindLog})
	}
	ch, cancel, err := b.Subscribe("r1", 4, 16)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	got := []uint64{}
	timeout := time.After(500 * time.Millisecond)
read:
	for {
		select {
		case e := <-ch:
			got = append(got, e.Seq)
		case <-timeout:
			break read
		}
	}
	for _, seq := range got {
		if seq < 4 {
			t.Fatalf("got seq %d, expected >= 4", seq)
		}
	}
}

func TestBusJSONLPersistence(t *testing.T) {
	dir := t.TempDir()
	b := NewBus(100)
	sink, err := b.StartRun("r1", dir)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	sink.Emit(Event{Kind: KindStageStarted, Stage: "discovery"})
	sink.Emit(Event{Kind: KindStageCompleted, Stage: "discovery"})
	b.FinishRun("r1")

	path := filepath.Join(dir, "events.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected events.jsonl: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := make(chan Event, 16)
	done := make(chan error, 1)
	go func() { done <- ReplayJSONL(ctx, path, out) }()

	// Drain the `out` channel until replay is done AND nothing more is
	// queued. A naive select that exits the loop as soon as `done`
	// fires races with the producer: ReplayJSONL can complete (signal
	// `done`) with events still buffered in `out`, and we would miss
	// them. Wait for `done` first, then drain whatever is left.
	got := []Event{}
	replayErrCh := make(chan error, 1)
	go func() { replayErrCh <- <-done }()
	for {
		select {
		case e := <-out:
			got = append(got, e)
			if len(got) == 2 {
				if err := <-replayErrCh; err != nil {
					t.Fatalf("ReplayJSONL: %v", err)
				}
				return
			}
		case err := <-replayErrCh:
			if err != nil {
				t.Fatalf("ReplayJSONL: %v", err)
			}
			// Replay finished; drain any remaining buffered events.
			for {
				select {
				case e := <-out:
					got = append(got, e)
				default:
					if len(got) < 2 {
						t.Fatalf("expected 2 events from JSONL replay, got %d", len(got))
					}
					return
				}
			}
		}
	}
}

func TestBusRingBufferDropsOldestWhenFull(t *testing.T) {
	b := NewBus(3)
	sink, _ := b.StartRun("r1", "")
	for i := 0; i < 10; i++ {
		sink.Emit(Event{Kind: KindLog})
	}
	snap := b.Snapshot("r1")
	if len(snap) != 3 {
		t.Fatalf("expected ring buffer of 3, got %d", len(snap))
	}
	// The last 3 events should have seq 8, 9, 10.
	if snap[0].Seq != 8 || snap[2].Seq != 10 {
		t.Fatalf("unexpected seqs: %d…%d", snap[0].Seq, snap[2].Seq)
	}
}

func TestBusFinishRunAlwaysSignalsSubscriberEvenIfBuffered(t *testing.T) {
	// Regression for the "UI shows running after cancel" bug: FinishRun
	// must guarantee subscribers see _eof and then have their channel
	// closed, even when the channel has events queued that the consumer
	// hasn't drained yet.
	b := NewBus(100)
	sink, err := b.StartRun("r1", "")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	ch, _, err := b.Subscribe("r1", 0, 4) // tiny buffer
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// Fill the buffer way beyond capacity; many events will land in
	// history-replay before live, then live emit drops.
	for i := 0; i < 50; i++ {
		sink.Emit(Event{Kind: KindLog})
	}
	b.FinishRun("r1")

	// Drain everything, expecting _eof at the end and the channel closed.
	timeout := time.After(2 * time.Second)
	gotEOF := false
	closed := false
loop:
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				closed = true
				break loop
			}
			if e.Kind == "_eof" {
				gotEOF = true
			}
		case <-timeout:
			t.Fatalf("timed out waiting for eof+close")
		}
	}
	if !gotEOF {
		t.Fatalf("expected to receive _eof event")
	}
	if !closed {
		t.Fatalf("expected channel to be closed after eof")
	}
}

func TestBusConcurrentEmitsAreSafe(t *testing.T) {
	b := NewBus(10000)
	sink, _ := b.StartRun("r1", "")
	const N = 1000
	const W = 8
	var wg sync.WaitGroup
	for w := 0; w < W; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < N; i++ {
				sink.Emit(Event{Kind: KindLog})
			}
		}()
	}
	wg.Wait()
	snap := b.Snapshot("r1")
	if len(snap) != N*W {
		t.Fatalf("expected %d events, got %d", N*W, len(snap))
	}
}
