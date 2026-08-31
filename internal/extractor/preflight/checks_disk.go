package preflight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DiskSpaceCheck verifies that the host has enough free space on
// the partition that hosts ~/.diffmind (base+composite indexer images' build
// contexts, run artifacts).
//
// Two thresholds:
//   - MinFreeBytes_Warn  → SeverityWarn when free < this
//   - MinFreeBytes_Fail  → SeverityFail when free < this
//
// Defaults: 5 GB warn / 1 GB fail. A cold base-image build can
// produce >2 GB of layers; we want the warn threshold to give the
// user enough headroom to actually build successfully.
type DiskSpaceCheck struct {
	Path             string
	MinFreeBytesWarn uint64
	MinFreeBytesFail uint64
}

// NewDiskSpaceCheck constructs a DiskSpaceCheck pointing at
// ~/.diffmind (created if missing).
func NewDiskSpaceCheck() *DiskSpaceCheck {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return &DiskSpaceCheck{
		Path:             filepath.Join(home, ".diffmind"),
		MinFreeBytesWarn: 5 * 1024 * 1024 * 1024, // 5 GB
		MinFreeBytesFail: 1 * 1024 * 1024 * 1024, // 1 GB
	}
}

func (c *DiskSpaceCheck) Name() string  { return "disk" }
func (c *DiskSpaceCheck) Title() string { return "Disk space (~/.diffmind)" }

func (c *DiskSpaceCheck) Run(_ context.Context) Result {
	path := c.Path
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".diffmind")
	}
	// statfs needs the path to exist. Create the directory if it
	// doesn't (idempotent; the orchestrator does the same on
	// every run).
	if err := os.MkdirAll(path, 0o755); err != nil {
		return Result{
			Name:        c.Name(),
			Title:       c.Title(),
			Severity:    SeverityFail,
			Message:     "Cannot create ~/.diffmind",
			Detail:      err.Error(),
			Remediation: "Make sure your home directory is writable.",
		}
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityWarn,
			Message:  "Could not query disk usage",
			Detail:   err.Error(),
		}
	}
	// stat.Bavail is the free-blocks-for-unprivileged-user count;
	// stat.Bsize is the fundamental block size. Multiply to get
	// bytes available. The product can overflow uint32 on 32-bit
	// platforms; cast to uint64 first.
	free := uint64(stat.Bavail) * uint64(stat.Bsize)

	if free < c.MinFreeBytesFail {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityFail,
			Message:  fmt.Sprintf("Only %s free", humanBytes(free)),
			Detail:   fmt.Sprintf("Path: %s; minimum required: %s", path, humanBytes(c.MinFreeBytesFail)),
			Remediation: "Free up disk space. Running `docker image prune` removes unused images; " +
				"removing old run directories clears retained DiffMind artifacts.",
		}
	}
	if free < c.MinFreeBytesWarn {
		return Result{
			Name:        c.Name(),
			Title:       c.Title(),
			Severity:    SeverityWarn,
			Message:     fmt.Sprintf("%s free (cold base-image build needs ~5 GB)", humanBytes(free)),
			Remediation: "Consider freeing some disk space before triggering a cold indexer build.",
		}
	}
	return Result{
		Name:     c.Name(),
		Title:    c.Title(),
		Severity: SeverityOK,
		Message:  humanBytes(free) + " free",
	}
}

// humanBytes returns a compact representation: 1.2 GB, 720 MB, etc.
func humanBytes(b uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.0f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.0f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
