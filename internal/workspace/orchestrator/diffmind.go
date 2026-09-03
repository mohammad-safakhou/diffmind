package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

// DiffMindRunOptions mirrors the diffmind run CLI flags DiffMind exposes.
// Pointer fields are used where zero is a meaningful explicit value.
type DiffMindRunOptions struct {
	ConfigPath    string   `json:"config_path,omitempty"`
	OutDir        string   `json:"out_dir,omitempty"`
	LogFile       string   `json:"log_file,omitempty"`
	Workers       int      `json:"workers,omitempty"`
	MinConfidence *float64 `json:"min_confidence,omitempty"`
	Verbose       bool     `json:"verbose,omitempty"`
	Trace         bool     `json:"trace,omitempty"`
}

// RunDiffMind triggers a DiffMind run against a repository.
func RunDiffMind(binaryPath, repoPath string, opts DiffMindRunOptions, log *util.Logger) error {
	return RunDiffMindContext(context.Background(), binaryPath, repoPath, opts, log)
}

// RunDiffMindContext stops the analyzer and its child processes on cancellation.
func RunDiffMindContext(ctx context.Context, binaryPath, repoPath string, opts DiffMindRunOptions, log *util.Logger) error {
	args := opts.Args(repoPath)
	log.Info("running DiffMind", "binary", binaryPath, "repo", repoPath)
	cmdName, cmdArgs, cmdDir := diffmindCommand(binaryPath, args)
	cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
	configureAnalyzerCancellation(cmd)
	cmd.WaitDelay = 2 * time.Second
	if cmdDir != "" {
		cmd.Dir = cmdDir
	}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("diffmind run failed: %w\noutput: %s", err, string(output))
	}
	log.Info("DiffMind run complete", "repo", repoPath)
	return nil
}

func (o DiffMindRunOptions) Args(repoPath string) []string {
	args := []string{"run", "--repo", repoPath}
	addString := func(flag, value string) {
		if value != "" {
			args = append(args, flag, value)
		}
	}
	addInt := func(flag string, value int) {
		if value > 0 {
			args = append(args, flag, fmt.Sprint(value))
		}
	}
	addBool := func(flag string, value bool) {
		if value {
			args = append(args, flag)
		}
	}
	addString("--config", o.ConfigPath)
	addString("--out", o.OutDir)
	addString("--log-file", o.LogFile)
	addInt("--workers", o.Workers)
	if o.MinConfidence != nil {
		args = append(args, "--min-confidence", fmt.Sprint(*o.MinConfidence))
	}
	addBool("--verbose", o.Verbose)
	addBool("--trace", o.Trace)
	return args
}

func diffmindCommand(binaryPath string, args []string) (string, []string, string) {
	if binaryPath != "" && binaryPath != "diffmind" {
		return binaryPath, args, ""
	}
	if sourceDir := localDiffMindSourceDir(); sourceDir != "" {
		goArgs := append([]string{"run", "./cmd/diffmind"}, args...)
		return "go", goArgs, sourceDir
	}
	if binaryPath == "" {
		binaryPath = "diffmind"
	}
	return binaryPath, args, ""
}

func localDiffMindSourceDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "cmd", "diffmind", "main.go")
		module := filepath.Join(dir, "go.mod")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			if mod, err := os.Stat(module); err == nil && !mod.IsDir() {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}
