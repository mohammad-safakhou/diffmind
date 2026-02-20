package analyzers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func executeAdapterTools(outDir string, sourceRoot string, report *Report) error {
	if report == nil {
		return nil
	}
	for i := range report.AdapterRuns {
		run := &report.AdapterRuns[i]
		spec, ok := adapterToolSpec(run.Name)
		if !ok || strings.TrimSpace(run.ToolPath) == "" {
			continue
		}

		run.ToolExecStatus = "executed"
		output := runAdapterToolCommand(sourceRoot, run.ToolPath, spec.args...)
		if output.err != nil {
			run.ToolExecStatus = "failed"
		}

		text := strings.TrimSpace(output.stdout)
		if strings.TrimSpace(output.stderr) != "" {
			if text != "" {
				text += "\n"
			}
			text += strings.TrimSpace(output.stderr)
		}
		if text == "" {
			text = "<no-output>"
		}

		path := filepath.Join(outDir, "analyzers", "runs", run.Name+".tool_output.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create adapter tool output dir: %w", err)
		}
		body := []byte(text + "\n")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return fmt.Errorf("write adapter tool output: %w", err)
		}
		sum := sha256.Sum256(body)
		run.ToolOutputPath = path
		run.ToolOutputSHA256 = hex.EncodeToString(sum[:])

		if args, ok := adapterSemanticArgs(run.Name); ok {
			run.ToolSemanticStatus = "executed"
			semanticOut := runAdapterToolCommand(sourceRoot, run.ToolPath, args...)
			semanticText := strings.TrimSpace(semanticOut.stdout)
			if strings.TrimSpace(semanticOut.stderr) != "" {
				if semanticText != "" {
					semanticText += "\n"
				}
				semanticText += strings.TrimSpace(semanticOut.stderr)
			}
			if semanticOut.err != nil {
				run.ToolSemanticStatus = "failed"
			}
			if semanticText != "" {
				semanticPath := filepath.Join(outDir, "analyzers", "runs", run.Name+".tool_semantic.json")
				semanticBody := []byte(semanticText + "\n")
				if err := os.WriteFile(semanticPath, semanticBody, 0o644); err != nil {
					return fmt.Errorf("write adapter semantic output: %w", err)
				}
				semanticSum := sha256.Sum256(semanticBody)
				run.ToolSemanticPath = semanticPath
				run.ToolSemanticSHA256 = hex.EncodeToString(semanticSum[:])
			}
		}
	}
	return nil
}

type adapterToolRunResult struct {
	stdout string
	stderr string
	err    error
}

type adapterToolCommandSpec struct {
	args []string
}

func adapterToolSpec(adapterName string) (adapterToolCommandSpec, bool) {
	switch strings.ToLower(strings.TrimSpace(adapterName)) {
	case "gopls":
		return adapterToolCommandSpec{args: []string{"version"}}, true
	case "tsserver":
		return adapterToolCommandSpec{args: []string{"--version"}}, true
	case "pyright":
		return adapterToolCommandSpec{args: []string{"--version"}}, true
	default:
		return adapterToolCommandSpec{}, false
	}
}

func adapterSemanticArgs(adapterName string) ([]string, bool) {
	envName := ""
	switch strings.ToLower(strings.TrimSpace(adapterName)) {
	case "gopls":
		envName = "DIFFMIND_GOPLS_SEMANTIC_ARGS"
	case "tsserver":
		envName = "DIFFMIND_TSSERVER_SEMANTIC_ARGS"
	case "pyright":
		envName = "DIFFMIND_PYRIGHT_SEMANTIC_ARGS"
	default:
		return nil, false
	}
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return nil, false
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return nil, false
	}
	return parts, true
}

func runAdapterToolCommand(sourceRoot string, binPath string, args ...string) adapterToolRunResult {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, strings.TrimSpace(binPath), args...)
	cmd.Dir = sourceRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return adapterToolRunResult{
			stderr: strings.TrimSpace(string(out)),
			err:    err,
		}
	}
	return adapterToolRunResult{
		stdout: strings.TrimSpace(string(out)),
	}
}
