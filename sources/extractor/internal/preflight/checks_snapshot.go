package preflight

import (
	"context"
	"os"
	"path/filepath"
)

// SnapshotWritableCheck verifies that ~/.diffmind/snapshots is
// writable. We touch a probe file inside the directory and delete
// it. A non-writable target (read-only mount, permission denied)
// will be caught here BEFORE the orchestrator burns time copying
// the source tree only to fail at write time.
type SnapshotWritableCheck struct {
	Path string
}

// NewSnapshotWritableCheck constructs a SnapshotWritableCheck
// pointing at the default snapshot parent dir.
func NewSnapshotWritableCheck() *SnapshotWritableCheck {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return &SnapshotWritableCheck{Path: filepath.Join(home, ".diffmind", "snapshots")}
}

func (c *SnapshotWritableCheck) Name() string  { return "snapshots" }
func (c *SnapshotWritableCheck) Title() string { return "Snapshot target writable" }

func (c *SnapshotWritableCheck) Run(_ context.Context) Result {
	path := c.Path
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".diffmind", "snapshots")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return Result{
			Name:        c.Name(),
			Title:       c.Title(),
			Severity:    SeverityFail,
			Message:     "Cannot create snapshot directory",
			Detail:      err.Error(),
			Remediation: "Ensure " + path + " is writable.",
		}
	}
	probe := filepath.Join(path, ".preflight-write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return Result{
			Name:        c.Name(),
			Title:       c.Title(),
			Severity:    SeverityFail,
			Message:     "Snapshot directory not writable",
			Detail:      err.Error(),
			Remediation: "Check the permissions on " + path + " (it must be writable by the user running diffmind).",
		}
	}
	_ = os.Remove(probe)
	return Result{
		Name:     c.Name(),
		Title:    c.Title(),
		Severity: SeverityOK,
		Message:  "Writable",
	}
}
