package runmgr

import "time"

// Event is a single SSE message emitted during a graph run. Types:
//
//	run_started, phase_started, phase_completed, phase_failed, log,
//	run_completed, run_failed, run_cancelled
type Event struct {
	Seq     int       `json:"seq"`
	Type    string    `json:"type"`
	Stage   string    `json:"stage,omitempty"`
	Status  string    `json:"status,omitempty"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}
