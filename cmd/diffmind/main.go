// DiffMind — Cross-service dependency graph builder.
//
// DiffMind owns projects, repositories, packs, and graph runs. With no
// arguments it launches the web UI and run manager; a couple of read-only list
// commands are kept for power users and scripts.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/ui"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

const usage = `DiffMind — Cross-service dependency graph builder.

Usage:
  diffmind [ui]                       Launch the web UI and run manager (default)
  diffmind run --repo <path>          Analyze one repository
  diffmind validate --run <id>        Validate an analysis run
  diffmind list-runs                  List analysis runs
  diffmind extractor-ui               Launch the low-level analysis dashboard
  diffmind graph --project <id>       Build a project graph and exit
  diffmind list projects              List projects
  diffmind list runs --project <id>   List graph runs for a project
  diffmind pack <command>             Create, test, install, and manage knowledge packs
  diffmind mcp [--project <id>]       Run the original read-only stdio MCP server
  diffmind agent [--project <id>]     Agent-managed workspace, lifecycle and full MCP control
  diffmind doctor [--json]            Check local installation and graph readiness
  diffmind version [--json]           Print build version information
  diffmind backup <command>          Create, rotate, list, verify, and restore snapshots
  diffmind storage <command>         Migrate or verify refresh queue storage
  diffmind help                       Show this help

Flags (ui):
  --host        UI server host (default 127.0.0.1)
  --port        UI server port (default 8090)
  --log-level   Log level: info, debug, trace (default info)
  --no-spa-rebuild  Skip automatic SPA rebuild on startup
  --auth-token  Require this token via Bearer, Basic password, or X-DiffMind-Token
  --project-access  legacy (default) or scoped per-project user memberships
  --allow-unauthenticated  Allow a non-loopback bind without authentication
  --refresh-interval  Refresh every duration such as 15m or 1h (disabled by default)
  --refresh-on-start  Refresh all projects immediately after startup
  --refresh-concurrency  Maximum repository operations per project (default 4)
  --job-workers  Concurrent queued project jobs (default 2)
  --queue-capacity  Maximum queued and running jobs (default 256)
  --repository-workers  Global concurrent sync/analyzer operations (default 4)
`

func main() {
	args := os.Args[1:]
	cmd := "ui"
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			fmt.Print(usage)
			return
		case "ui", "list", "graph", "run", "validate", "list-runs", "extractor-ui", "pack", "mcp", "agent", "agent-service", "doctor", "version", "backup", "storage":
			cmd = args[0]
			args = args[1:]
		default:
			// Unknown leading token: treat as flags to the default ui command.
		}
	}

	// All data-using CLI commands participate, including analyzer subprocesses.
	// Backup owns its exclusive lease; version/help need no workspace access.
	if cmd != "version" && cmd != "backup" && cmd != "storage" && cmd != "agent" {
		release, err := homelock.Acquire(config.Home(), false)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer release()
	}
	switch cmd {
	case "agent":
		if err := runAgent(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "agent-service":
		listener, err := inheritedAgentListener()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer listener.Close()
		cmdUIListener(args, listener)
	case "storage":
		if err := runStorage(args, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "backup":
		if err := runBackup(args, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "ui":
		cmdUI(args)
	case "list":
		cmdList(args)
	case "graph":
		cmdGraph(args)
	case "run":
		run(args)
	case "validate":
		validate(args)
	case "list-runs":
		listRuns(args)
	case "extractor-ui":
		serveExtractorUI(args)
	case "pack":
		cmdPack(args)
	case "mcp":
		cmdMCP(args)
	case "doctor":
		cmdDoctor(args)
	case "version":
		cmdVersion(args)
	default:
		fmt.Print(usage)
		os.Exit(1)
	}
}

type repeatFlag []string

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (f *repeatFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func cmdGraph(args []string) {
	fs := flag.NewFlagSet("graph", flag.ExitOnError)
	project := fs.String("project", "", "project id")
	logLevel := fs.String("log-level", "info", "Log level: info, debug, trace")
	var repoRuns repeatFlag
	fs.Var(&repoRuns, "repo", "repo_id=diffmind_run_id; repeat to select explicit runs")
	_ = fs.Parse(args)
	if *project == "" {
		fmt.Fprintln(os.Stderr, "--project is required")
		os.Exit(2)
	}

	st, err := store.New(config.Home())
	if err != nil {
		fmt.Fprintf(os.Stderr, "store init failed: %v\n", err)
		os.Exit(1)
	}
	if _, err := st.GetProject(*project); err != nil {
		fmt.Fprintf(os.Stderr, "project not found: %v\n", err)
		os.Exit(1)
	}

	refs, err := graphRefs(st, *project, repoRuns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graph refs: %v\n", err)
		os.Exit(1)
	}
	mgr := runmgr.New(st, newLogger(*logLevel), config.DiffMindRunsDir())
	run, err := mgr.Start(*project, refs, map[string]any{"source": "diffmind graph"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start graph run: %v\n", err)
		os.Exit(1)
	}
	mgr.WaitFor(*project, run.ID)

	done, err := st.GetRun(*project, run.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read graph run: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s\t%s\tservices=%d edges=%d\n", done.ID, done.Status, done.ServiceCount, done.EdgeCount)
	if done.Error != "" {
		fmt.Fprintln(os.Stderr, done.Error)
	}
	if done.Status != store.RunCompleted {
		os.Exit(1)
	}
}

func graphRefs(st *store.Store, project string, explicit []string) ([]store.RunRepoRef, error) {
	if len(explicit) == 0 {
		repos, err := st.ListRepos(project)
		if err != nil {
			return nil, err
		}
		refs := make([]store.RunRepoRef, 0, len(repos))
		for _, repo := range repos {
			if repo.Kind == "infra_repo" || strings.TrimSpace(repo.LastDiffMindRunID) == "" {
				continue
			}
			refs = append(refs, store.RunRepoRef{RepoID: repo.ID, DiffMindRunID: repo.LastDiffMindRunID})
		}
		if len(refs) == 0 {
			return nil, fmt.Errorf("no repos have last_diffmind_run_id; pass --repo repo_id=run_id")
		}
		return refs, nil
	}

	refs := make([]store.RunRepoRef, 0, len(explicit))
	for _, raw := range explicit {
		repoID, runID, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(repoID) == "" || strings.TrimSpace(runID) == "" {
			return nil, fmt.Errorf("invalid --repo %q, expected repo_id=run_id", raw)
		}
		repoID = strings.TrimSpace(repoID)
		runID = strings.TrimSpace(runID)
		if _, err := st.GetRepo(project, repoID); err != nil {
			return nil, fmt.Errorf("repo %s: %w", repoID, err)
		}
		if _, err := st.UpdateRepo(project, repoID, func(repo *store.Repo) {
			repo.LastDiffMindRunID = runID
			if repo.DiffMindFreshness == "stale" {
				repo.DiffMindFreshness = "unknown"
			}
		}); err != nil {
			return nil, fmt.Errorf("update repo %s: %w", repoID, err)
		}
		refs = append(refs, store.RunRepoRef{RepoID: repoID, DiffMindRunID: runID})
	}
	return refs, nil
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
	cmdUIListener(args, nil)
}

func cmdUIListener(args []string, listener net.Listener) {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	host := fs.String("host", "127.0.0.1", "UI server host")
	port := fs.Int("port", 8090, "UI server port")
	logLevel := fs.String("log-level", "info", "Log level: info, debug, trace")
	noSPARebuild := fs.Bool("no-spa-rebuild", false, "skip automatic SPA rebuild on startup")
	authToken := fs.String("auth-token", strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_TOKEN")), "shared-server authentication token (prefer DIFFMIND_AUTH_TOKEN)")
	trustedProxySecret := fs.String("trusted-proxy-secret", strings.TrimSpace(os.Getenv("DIFFMIND_TRUSTED_PROXY_SECRET")), "secret used to trust per-user identity headers from an OIDC proxy")
	allowUnauthenticated := fs.Bool("allow-unauthenticated", false, "allow a non-loopback bind without authentication")
	refreshInterval := fs.String("refresh-interval", strings.TrimSpace(os.Getenv("DIFFMIND_REFRESH_INTERVAL")), "fleet refresh interval, for example 15m or 1h")
	refreshOnStart := fs.Bool("refresh-on-start", envBool("DIFFMIND_REFRESH_ON_START"), "refresh every project immediately after startup")
	refreshConcurrency := fs.Int("refresh-concurrency", envInt("DIFFMIND_REFRESH_CONCURRENCY", 4), "maximum concurrent repository refresh operations per project")
	jobWorkers := fs.Int("job-workers", envInt("DIFFMIND_JOB_WORKERS", 2), "concurrent queued project refresh jobs")
	queueCapacity := fs.Int("queue-capacity", envInt("DIFFMIND_QUEUE_CAPACITY", 256), "maximum queued and running refresh jobs")
	repoWorkers := fs.Int("repository-workers", envInt("DIFFMIND_REPOSITORY_WORKERS", 4), "global concurrent sync/analyzer operations")
	projectAccess := fs.String("project-access", envString("DIFFMIND_PROJECT_ACCESS", "legacy"), "project authorization: legacy global roles or scoped explicit grants")
	_ = fs.Parse(args)
	if err := validateUIExposure(*host, *authToken, *trustedProxySecret, *allowUnauthenticated); err != nil {
		fmt.Fprintf(os.Stderr, "UI server configuration error: %v\n", err)
		os.Exit(2)
	}

	log := newLogger(*logLevel)

	releaseServer, err := homelock.AcquireServer(config.Home())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer releaseServer()
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
	srv.SetVersion(version)
	srv.SetAuthToken(*authToken)
	srv.SetTrustedProxySecret(*trustedProxySecret)
	if err := srv.ConfigureProjectAccess(*projectAccess); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := srv.ConfigureOperations(ui.OperationsConfig{Workers: *jobWorkers, Capacity: *queueCapacity, RepositoryWorkers: *repoWorkers, WebhookSecret: os.Getenv("DIFFMIND_WEBHOOK_SECRET")}); err != nil {
		fmt.Fprintf(os.Stderr, "UI server configuration error: %v\n", err)
		os.Exit(2)
	}
	interval, err := parseOptionalDuration(*refreshInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "UI server configuration error: invalid refresh interval: %v\n", err)
		os.Exit(2)
	}
	if err := srv.ConfigureRefresh(ui.RefreshConfig{Interval: interval, OnStart: *refreshOnStart, Concurrency: *refreshConcurrency}); err != nil {
		fmt.Fprintf(os.Stderr, "UI server configuration error: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("DiffMind dashboard: http://%s\n", srv.Addr())
	fmt.Println("Press Ctrl+C to stop.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if listener != nil {
		// EOF handles a killed/crashed controller as well as normal disconnect.
		lifetime := os.NewFile(4, "agent-owner-lifetime")
		if lifetime == nil {
			fmt.Fprintln(os.Stderr, "missing agent owner pipe")
			return
		}
		defer lifetime.Close()
		go func() { _, _ = io.Copy(io.Discard, lifetime); stop() }()
	}
	var serveErr error
	if listener != nil {
		serveErr = srv.StartListener(ctx, listener)
	} else {
		serveErr = srv.Start(ctx)
	}
	if err := serveErr; err != nil {
		fmt.Fprintf(os.Stderr, "UI server error: %v\n", err)
		os.Exit(1)
	}
}

func validateUIExposure(host, token, trustedProxySecret string, allowUnauthenticated bool) error {
	if allowUnauthenticated || strings.TrimSpace(token) != "" || strings.TrimSpace(trustedProxySecret) != "" || isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("refusing unauthenticated non-loopback bind %q; set DIFFMIND_AUTH_TOKEN or DIFFMIND_TRUSTED_PROXY_SECRET, or pass --allow-unauthenticated", host)
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func parseOptionalDuration(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

func envBool(name string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return err == nil && value
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return fallback
	}
	return value
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
