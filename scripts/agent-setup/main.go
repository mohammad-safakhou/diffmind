// agent-setup is run BY an authorized host agent, not by the end user.
// It installs the current source and returns machine-readable MCP registration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type setupOptions struct {
	Repo, BinDir, Home, Name string
	Replace                  bool
}
type launchConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}
type setupResult struct {
	Binary       string                  `json:"binary"`
	Home         string                  `json:"home"`
	SourceCommit string                  `json:"source_commit"`
	MCPServers   map[string]launchConfig `json:"mcpServers"`
	NextAction   string                  `json:"next_action"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out, diagnostics io.Writer) error {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("agent-setup", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	opts := setupOptions{}
	fs.StringVar(&opts.Repo, "repo-root", ".", "DiffMind source checkout")
	fs.StringVar(&opts.BinDir, "bin-dir", filepath.Join(userHome, ".local", "bin"), "installation directory")
	fs.StringVar(&opts.Home, "home", filepath.Join(userHome, ".diffmind-work"), "private persistent workspace")
	fs.StringVar(&opts.Name, "name", "diffmind-work", "MCP registration name")
	fs.BoolVar(&opts.Replace, "replace", false, "replace an existing executable after checking its path")
	if err = fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	result, err := setup(opts, diagnostics)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(result)
}
func setup(opts setupOptions, diagnostics io.Writer) (setupResult, error) {
	var empty setupResult
	for _, name := range []string{"go", "git"} {
		if _, e := exec.LookPath(name); e != nil {
			return empty, fmt.Errorf("%s missing: the host agent must install the documented prerequisites with user-authorized system permissions", name)
		}
	}
	if opts.Name == "" || strings.ContainsAny(opts.Name, "/\\\r\n\x00") {
		return empty, errors.New("invalid registration name")
	}
	repo, err := filepath.Abs(opts.Repo)
	if err != nil {
		return empty, err
	}
	content, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil || !strings.HasPrefix(string(content), "module github.com/mohammad-safakhou/diffmind\n") {
		return empty, errors.New("repo-root is not a DiffMind checkout")
	}
	binDir, err := filepath.Abs(opts.BinDir)
	if err != nil {
		return empty, err
	}
	home, err := filepath.Abs(opts.Home)
	if err != nil {
		return empty, err
	}
	if home == string(filepath.Separator) || containsPath(repo, home) || containsPath(home, repo) {
		return empty, errors.New("choose a dedicated workspace, not a filesystem or repository root")
	}
	binary := filepath.Join(binDir, "diffmind")
	if info, e := os.Lstat(binary); e == nil {
		if !info.Mode().IsRegular() {
			return empty, errors.New("refusing to replace a non-regular installation")
		}
		if !opts.Replace {
			return empty, errors.New("binary already exists; inspect it, then explicitly use --replace for an authorized upgrade")
		}
	} else if !os.IsNotExist(e) {
		return empty, e
	}
	if info, e := os.Lstat(home); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return empty, errors.New("existing workspace must be a private real directory (0700); do not silently change unrelated directory permissions")
		}
	} else if !os.IsNotExist(e) {
		return empty, e
	}
	if err = os.MkdirAll(binDir, 0755); err != nil {
		return empty, err
	}
	stage, err := os.MkdirTemp(binDir, ".diffmind-install-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(stage)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	git := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	git.Dir = repo
	commit := "unknown"
	if b, e := git.Output(); e == nil {
		commit = strings.TrimSpace(string(b))
	}
	candidate := filepath.Join(stage, "diffmind")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", "-X main.commit="+commit+" -X main.date="+time.Now().UTC().Format(time.RFC3339), "-o", candidate, "./cmd/diffmind")
	build.Dir = repo
	build.Stdout = diagnostics
	build.Stderr = diagnostics
	if err = build.Run(); err != nil {
		return empty, fmt.Errorf("build failed (Go >=1.26.6, Git and C/CGO compiler required): %w", err)
	}
	// Probe in installer-owned temporary state, never modify the user's workspace
	// before the candidate is known to start successfully.
	probeHome := filepath.Join(stage, "probe")
	doctor := exec.CommandContext(ctx, candidate, "doctor", "--json")
	doctor.Env = cleanEnv(probeHome)
	doctor.Stderr = diagnostics
	b, err := doctor.Output()
	if err != nil {
		return empty, fmt.Errorf("candidate doctor failed: %w", err)
	}
	var health struct{ OK bool }
	if json.Unmarshal(b, &health) != nil || !health.OK {
		return empty, errors.New("candidate doctor did not report healthy")
	}
	if err = os.MkdirAll(home, 0700); err != nil {
		return empty, err
	}
	if opts.Replace {
		err = os.Rename(candidate, binary)
	} else {
		err = os.Link(candidate, binary)
	}
	if err != nil {
		return empty, err
	}
	result := setupResult{Binary: binary, Home: home, SourceCommit: commit, MCPServers: map[string]launchConfig{opts.Name: {Command: binary, Args: []string{"agent"}, Env: map[string]string{"DIFFMIND_HOME": home}}}, NextAction: "Host agent: merge this MCP entry into your client's PERSONAL configuration, preserving unrelated servers; reconnect/discover tools, then use describe_management, create_project, import preview, start_ingestion, poll completion and verify graph evidence. Do not ask the user to run commands or click the UI."}
	return result, nil
}

func containsPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func cleanEnv(home string) []string {
	var result []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "DIFFMIND_") && key != "GITHUB_TOKEN" && key != "GH_TOKEN" {
			result = append(result, entry)
		}
	}
	return append(result, "DIFFMIND_HOME="+home)
}
