// DiffMind — Cross-service dependency graph builder.
//
// DiffMind owns projects, repositories, blueprints, and graph runs. With no
// arguments it launches the web UI and run manager; a couple of read-only list
// commands are kept for power users and scripts.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/store"
	"github.com/mohammad-safakhou/diffmind/internal/ui"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

const usage = `DiffMind — Cross-service dependency graph builder.

Usage:
  diffmind [ui]                       Launch the web UI and run manager (default)
  diffmind list projects              List projects
  diffmind list runs --project <id>   List graph runs for a project
  diffmind help                       Show this help

Flags (ui):
  --host        UI server host (default 127.0.0.1)
  --port        UI server port (default 8090)
  --log-level   Log level: info, debug, trace (default info)
  --no-spa-rebuild  Skip automatic SPA rebuild on startup
`

func main() {
	args := os.Args[1:]
	cmd := "ui"
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			fmt.Print(usage)
			return
		case "ui", "list":
			cmd = args[0]
			args = args[1:]
		default:
			// Unknown leading token: treat as flags to the default ui command.
		}
	}

	switch cmd {
	case "ui":
		cmdUI(args)
	case "list":
		cmdList(args)
	default:
		fmt.Print(usage)
		os.Exit(1)
	}
}

func newLogger(level string) *util.Logger {
	switch level {
	case "debug":
		return util.NewLogger(util.LevelDebug)
	case "trace":
		return util.NewLogger(util.LevelTrace)
	default:
		return util.NewLogger(util.LevelInfo)
	}
}

func cmdUI(args []string) {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	host := fs.String("host", "127.0.0.1", "UI server host")
	port := fs.Int("port", 8090, "UI server port")
	logLevel := fs.String("log-level", "info", "Log level: info, debug, trace")
	noSPARebuild := fs.Bool("no-spa-rebuild", false, "skip automatic SPA rebuild on startup")
	_ = fs.Parse(args)

	log := newLogger(*logLevel)

	st, err := store.New(config.Home())
	if err != nil {
		fmt.Fprintf(os.Stderr, "store init failed: %v\n", err)
		os.Exit(1)
	}
	mgr := runmgr.New(st, log, config.DiffMindRunsDir())

	if distDir := ensureSPABuilt(*noSPARebuild); distDir != "" {
		ui.SetDistOverride(distDir)
	}

	srv := ui.New(st, mgr, config.DiffMindRunsDir(), *host, *port, log)
	fmt.Printf("DiffMind dashboard: http://%s\n", srv.Addr())
	fmt.Println("Press Ctrl+C to stop.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "UI server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdList(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: diffmind list <projects|runs --project ID>")
		os.Exit(2)
	}
	st, err := store.New(config.Home())
	if err != nil {
		fmt.Fprintf(os.Stderr, "store init failed: %v\n", err)
		os.Exit(1)
	}
	switch args[0] {
	case "projects":
		ps, err := st.ListProjects()
		if err != nil {
			fmt.Fprintf(os.Stderr, "list projects: %v\n", err)
			os.Exit(1)
		}
		if len(ps) == 0 {
			fmt.Println("(no projects)")
			return
		}
		for _, p := range ps {
			fmt.Printf("%s\t%s\n", p.ID, p.Name)
		}
	case "runs":
		fs := flag.NewFlagSet("list runs", flag.ExitOnError)
		project := fs.String("project", "", "project id")
		_ = fs.Parse(args[1:])
		if *project == "" {
			fmt.Fprintln(os.Stderr, "--project is required")
			os.Exit(2)
		}
		runs, err := st.ListRuns(*project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list runs: %v\n", err)
			os.Exit(1)
		}
		if len(runs) == 0 {
			fmt.Println("(no runs)")
			return
		}
		for _, r := range runs {
			fmt.Printf("%s\t%s\tservices=%d edges=%d\n", r.ID, r.Status, r.ServiceCount, r.EdgeCount)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown list target: %s\n", args[0])
		os.Exit(2)
	}
}
