// Package events delivers a structured, ordered stream of pipeline events
// from the orchestrator to subscribers (the dashboard SSE endpoint and
// optional disk persistence).
//
// Design goals
//   - Each run owns its own monotonically increasing Seq sequence, so SSE
//     subscribers can resume after a disconnect by sending Last-Event-ID.
//   - Emit is non-blocking to keep the pipeline fast even if the network or
//     disk slows down. Subscribers with full buffers are dropped (with a
//     synthetic "subscriber_dropped" event so the UI knows it lost data).
//   - Events are also durable: every event is appended to a JSONL file
//     inside the run dir so the dashboard can replay finished runs after a
//     server restart.
//
// All event payload values must be JSON-marshalable; use plain Go primitives,
// strings, numbers, bools, and maps.
package events

import (
	"time"
)

// Kind enumerates every event type. Keep this list authoritative so the
// frontend reducer can switch on it without ambiguity.
const (
	KindRunStarted     = "run_started"
	KindRunCompleted   = "run_completed"
	KindRunFailed      = "run_failed"
	KindRunCancelled   = "run_cancelled"
	KindStageStarted   = "stage_started"
	KindStageProgress  = "stage_progress"
	KindStageCompleted = "stage_completed"
	KindJobPending     = "job_pending"
	KindJobStarted     = "job_started"
	KindJobCompleted   = "job_completed"
	KindJobFailed      = "job_failed"
	KindSessionCreated = "session_created"
	KindSessionAborted = "session_aborted"
	KindWatchdogAction = "watchdog_action"
	KindLog            = "log"
	KindSubscriberDrop = "subscriber_dropped"
)

// Status values used inside Event.Status. We keep them as strings so the
// JSON wire format is stable and self-describing.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
	StatusCancelled = "cancelled"
)

// Event is a single point-in-time message describing something that happened
// during a run. It is intentionally flat (no nested structs) so JSON
// reducers on the frontend stay trivial.
type Event struct {
	RunID    string         `json:"run_id"`
	Seq      uint64         `json:"seq"`
	TS       time.Time      `json:"ts"`
	Kind     string         `json:"kind"`
	Stage    string         `json:"stage,omitempty"`
	JobID    string         `json:"job_id,omitempty"`
	ParentID string         `json:"parent_id,omitempty"`
	Status   string         `json:"status,omitempty"`
	Message  string         `json:"message,omitempty"`
	Payload  map[string]any `json:"payload,omitempty"`
}

// Sink is the narrow interface the pipeline depends on. It is implemented
// by the Bus's per-run publisher, by a noop implementation for tests, and
// by tee combinators for fan-out.
type Sink interface {
	Emit(e Event)
}

// NoopSink discards every event. Useful for the CLI path where we don't
// care about live streaming.
type NoopSink struct{}

func (NoopSink) Emit(Event) {}
