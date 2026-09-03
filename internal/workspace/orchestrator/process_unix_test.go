//go:build !windows

package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

func TestAnalyzerCancellationStopsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "analyzer")
	marker := filepath.Join(dir, "started")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\necho started > '"+marker+"'\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- RunDiffMindContext(ctx, script, dir, DiffMindRunOptions{}, util.NewLogger(util.LevelInfo))
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("analyzer did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("analyzer descendants kept output pipes open after cancellation")
	}
}
