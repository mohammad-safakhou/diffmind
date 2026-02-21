package analyzers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
		switch strings.ToLower(strings.TrimSpace(run.Name)) {
		case "gopls":
			if err := executeGoplsAdapter(outDir, sourceRoot, run); err != nil {
				return err
			}
			continue
		case "tsserver":
			if err := executeTsserverAdapter(outDir, sourceRoot, run); err != nil {
				return err
			}
			continue
		case "pyright":
			if err := executePyrightAdapter(outDir, sourceRoot, run); err != nil {
				return err
			}
			continue
		case "jdtls":
			if err := executeJDTLSAdapter(outDir, sourceRoot, run); err != nil {
				return err
			}
			continue
		}

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

func executeGoplsAdapter(outDir string, sourceRoot string, run *AdapterRunItem) error {
	return executeLSPSemanticAdapter(outDir, sourceRoot, run, runGoplsSemanticExtraction)
}

func executeTsserverAdapter(outDir string, sourceRoot string, run *AdapterRunItem) error {
	return executeLSPSemanticAdapter(outDir, sourceRoot, run, runTsserverSemanticExtraction)
}

func executePyrightAdapter(outDir string, sourceRoot string, run *AdapterRunItem) error {
	return executeLSPSemanticAdapter(outDir, sourceRoot, run, runPyrightSemanticExtraction)
}

func executeJDTLSAdapter(outDir string, sourceRoot string, run *AdapterRunItem) error {
	return executeLSPSemanticAdapter(outDir, sourceRoot, run, runJDTLSSemanticExtraction)
}

func executeLSPSemanticAdapter(outDir string, sourceRoot string, run *AdapterRunItem, extractFn func(sourceRoot string, toolPath string) (string, adapterSemanticDocument, error)) error {
	if run == nil {
		return nil
	}
	run.ToolExecStatus = "executed"

	var (
		toolOutputText string
		semanticText   string
		semanticJSON   []byte
	)

	// Keep env-override path for deterministic test stubs.
	if args, ok := adapterSemanticArgs(run.Name); ok {
		toolOutputText = run.Name + " semantic args override enabled"
		out := runAdapterToolCommand(sourceRoot, run.ToolPath, args...)
		raw := strings.TrimSpace(out.stdout)
		if strings.TrimSpace(out.stderr) != "" {
			if raw != "" {
				raw += "\n"
			}
			raw += strings.TrimSpace(out.stderr)
		}
		if out.err != nil {
			run.ToolSemanticStatus = "failed"
		}
		if strings.TrimSpace(raw) != "" {
			semanticText = raw
		}
	} else {
		text, doc, err := extractFn(sourceRoot, run.ToolPath)
		if err != nil {
			run.ToolSemanticStatus = "failed"
			toolOutputText = run.Name + " lsp extraction failed: " + err.Error()
		} else {
			toolOutputText = text
			if len(doc.Packages) > 0 {
				data, mErr := json.Marshal(doc)
				if mErr != nil {
					run.ToolSemanticStatus = "failed"
					toolOutputText += "\nsemantic marshal failed: " + mErr.Error()
				} else {
					semanticJSON = data
					run.ToolSemanticStatus = "executed"
				}
			} else {
				run.ToolSemanticStatus = "executed"
			}
		}
	}

	if strings.TrimSpace(toolOutputText) == "" {
		toolOutputText = run.Name + " adapter executed"
	}
	outputPath, outputSHA, err := writeAdapterToolArtifact(outDir, run.Name, "tool_output.txt", []byte(toolOutputText+"\n"))
	if err != nil {
		return err
	}
	run.ToolOutputPath = outputPath
	run.ToolOutputSHA256 = outputSHA

	switch {
	case len(semanticJSON) > 0:
		semanticPath, semanticSHA, err := writeAdapterToolArtifact(outDir, run.Name, "tool_semantic.json", append(semanticJSON, '\n'))
		if err != nil {
			return err
		}
		run.ToolSemanticPath = semanticPath
		run.ToolSemanticSHA256 = semanticSHA
	case strings.TrimSpace(semanticText) != "":
		semanticPath, semanticSHA, err := writeAdapterToolArtifact(outDir, run.Name, "tool_semantic.json", []byte(semanticText+"\n"))
		if err != nil {
			return err
		}
		run.ToolSemanticPath = semanticPath
		run.ToolSemanticSHA256 = semanticSHA
		if run.ToolSemanticStatus == "" {
			run.ToolSemanticStatus = "executed"
		}
	default:
		if run.ToolSemanticStatus == "" {
			run.ToolSemanticStatus = "executed"
		}
	}
	return nil
}

func writeAdapterToolArtifact(outDir string, adapterName string, suffix string, body []byte) (string, string, error) {
	path := filepath.Join(outDir, "analyzers", "runs", adapterName+"."+suffix)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", fmt.Errorf("create adapter tool output dir: %w", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", "", fmt.Errorf("write adapter tool output: %w", err)
	}
	sum := sha256.Sum256(body)
	return path, hex.EncodeToString(sum[:]), nil
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
	case "jdtls":
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
	case "jdtls":
		envName = "DIFFMIND_JDTLS_SEMANTIC_ARGS"
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
