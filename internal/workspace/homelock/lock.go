// Package homelock coordinates offline maintenance with cooperating CLI processes.
// Shared leases allow UI-launched analyzers to run; they are not writer election.
package homelock

import (
	"fmt"
	"os"
	"path/filepath"
)

const FileName = ".diffmind-maintenance.lock"

// AcquireServer elects one local UI/server process without blocking its CLI
// analyzer children or read-only MCP clients. It is not a distributed lease.
func AcquireServer(home string) (func(), error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	f, err := acquire(filepath.Join(home, ".diffmind-server.lock"), true)
	if err != nil {
		return nil, fmt.Errorf("another Diffmind server owns this workspace or its lock is unavailable: %w", err)
	}
	return func() { _ = f.Close() }, nil
}

// Acquire is nonblocking. Never unlink the lock file: another process may hold
// its inode. The OS releases a lease even when a process exits without cleanup.
func Acquire(home string, exclusive bool) (func(), error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	f, err := acquire(filepath.Join(home, FileName), exclusive)
	if err != nil {
		return nil, fmt.Errorf("workspace is in use or unavailable; stop Diffmind (including MCP) before offline maintenance: %w", err)
	}
	return func() { _ = f.Close() }, nil
}
