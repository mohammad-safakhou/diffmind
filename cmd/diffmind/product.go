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
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/mcpserver"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// Set by release builds with -ldflags. Development builds remain explicit.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func cmdVersion(args []string) {
	if err := runVersion(args, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func runVersion(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: diffmind version [--json]")
	}
	info := versionInfo{Version: version, Commit: commit, Date: date, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(info)
	}
	_, err := fmt.Fprintf(stdout, "diffmind %s (commit %s, built %s, %s/%s, %s)\n", info.Version, info.Commit, info.Date, info.OS, info.Arch, info.Go)
	return err
}

func cmdMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "default project id")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: diffmind mcp [--project ID]")
		os.Exit(2)
	}
	st, err := store.New(config.Home())
	if err != nil {
		fmt.Fprintf(os.Stderr, "store init failed: %v\n", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcpserver.New(query.New(st), *project, version).Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "MCP server failed: %v\n", err)
		os.Exit(1)
	}
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type doctorReport struct {
	OK      bool          `json:"ok"`
	Version string        `json:"version"`
	Home    string        `json:"home"`
	Checks  []doctorCheck `json:"checks"`
}

func cmdDoctor(args []string) {
	code, err := runDoctor(args, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if code != 0 {
		os.Exit(code)
	}
}

func runDoctor(args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if fs.NArg() != 0 {
		return 2, errors.New("usage: diffmind doctor [--json]")
	}

	home := config.Home()
	report := doctorReport{OK: true, Version: version, Home: home}
	add := func(name, status, message string) {
		report.Checks = append(report.Checks, doctorCheck{Name: name, Status: status, Message: message})
		if status == "fail" {
			report.OK = false
		}
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		add("home", "fail", fmt.Sprintf("cannot create %s: %v", home, err))
	} else if f, err := os.CreateTemp(home, ".doctor-*"); err != nil {
		add("home", "fail", fmt.Sprintf("%s is not writable: %v", home, err))
	} else {
		path := f.Name()
		_ = f.Close()
		_ = os.Remove(path)
		add("home", "pass", home+" is writable")
	}
	if path, err := exec.LookPath("git"); err != nil {
		add("git", "fail", "git is required but was not found on PATH")
	} else {
		add("git", "pass", path)
	}
	if path, err := exec.LookPath("docker"); err != nil {
		add("docker", "warn", "Docker is optional; install it for containerized analysis and company deployment")
	} else {
		add("docker", "pass", path)
	}

	if report.OK {
		st, err := store.New(home)
		if err != nil {
			add("store", "fail", err.Error())
		} else {
			projects, err := query.New(st).Projects()
			if err != nil {
				add("projects", "fail", err.Error())
			} else if len(projects) == 0 {
				add("projects", "warn", "no projects yet; start `diffmind` and add repositories")
			} else {
				ready := 0
				for _, p := range projects {
					if p.GraphReady {
						ready++
					}
				}
				if ready == 0 {
					add("graphs", "warn", fmt.Sprintf("%d project(s), but none has a completed graph", len(projects)))
				} else {
					add("graphs", "pass", fmt.Sprintf("%d/%d project(s) have a completed graph", ready, len(projects)))
				}
			}
		}
	}
	if packs, err := knowledge.LoadEnabled(home); err != nil {
		add("knowledge_packs", "fail", err.Error())
	} else {
		add("knowledge_packs", "pass", fmt.Sprintf("%d enabled pack(s) verified", len(packs)))
	}

	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return 1, err
		}
	} else {
		for _, check := range report.Checks {
			fmt.Fprintf(stdout, "%-5s %-18s %s\n", strings.ToUpper(check.Status), check.Name, check.Message)
		}
		if report.OK {
			fmt.Fprintln(stdout, "\nDiffMind is ready.")
		} else {
			fmt.Fprintln(stdout, "\nDiffMind needs attention before use.")
		}
	}
	if !report.OK {
		return 1, nil
	}
	return 0, nil
}
