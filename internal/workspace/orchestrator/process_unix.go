//go:build !windows

package orchestrator

import (
	"os/exec"
	"syscall"
)

func configureAnalyzerCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
