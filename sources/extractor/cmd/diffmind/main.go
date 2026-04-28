package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mohammad-safakhou/diffmind/internal/app"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/ui"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: diffmind <run|batch|validate|list-runs|ui> ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		run(os.Args[2:])
	case "batch":
		batchRun(os.Args[2:])
	case "validate":
		validate(os.Args[2:])
	case "list-runs":
		listRuns(os.Args[2:])
	case "ui":
		serveUI(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	repo := fs.String("repo", "", "absolute path to target codebase")
	cfgPath := fs.String("config", "", "path to diffmind json config")
	opencodeURL := fs.String("opencode-url", "", "OpenCode server base URL")
	opencodeUsername := fs.String("opencode-username", "", "OpenCode basic auth username (default: opencode)")
	opencodePassword := fs.String("opencode-password", "", "OpenCode basic auth password")
	opencodeTimeoutSeconds := fs.Int("opencode-timeout-seconds", 0, "OpenCode request timeout in seconds")
	providerID := fs.String("provider-id", "", "OpenCode provider ID")
	modelID := fs.String("model-id", "", "OpenCode model ID")
	modelVariant := fs.String("model-variant", "", "OpenCode model variant (for example: low, medium, high, max)")
	outDir := fs.String("out", "", "artifact base directory (default .diffmind/runs)")
	workers := fs.Int("workers", 0, "parallel worker count")
	maxCatalogItems := fs.Int("max-catalog-items", 0, "maximum dependency catalog items sent per connection-mapping prompt batch")
	cleanupOpenCodeSessions := fs.Bool("cleanup-opencode-sessions", false, "delete OpenCode sessions after prompts (can trigger server-side FK races)")
	opencodeDeleteDelaySeconds := fs.Int("opencode-delete-delay-seconds", 0, "delay before deleting OpenCode sessions when cleanup is enabled")
	reuseOpenCodeSession := fs.Bool("reuse-opencode-session", false, "reuse a single OpenCode session across prompts in a run")
	skipReexamination := fs.Bool("skip-reexamination", false, "skip stage 2 (LLM re-ask for low-signal seeds) for faster, lower-accuracy runs")
	minConfidence := fs.Float64("min-confidence", -1, "confidence threshold in [0,1]")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)
	util.Info("cli.run", "run command started", map[string]any{
		"repo": *repo, "config": *cfgPath, "opencode_url": *opencodeURL, "workers": *workers,
		"max_catalog_items": *maxCatalogItems, "opencode_timeout_seconds": *opencodeTimeoutSeconds, "model_variant": *modelVariant,
		"cleanup_opencode_sessions": *cleanupOpenCodeSessions, "opencode_delete_delay_seconds": *opencodeDeleteDelaySeconds, "reuse_opencode_session": *reuseOpenCodeSession,
	})

	if *repo == "" {
		fmt.Fprintln(os.Stderr, "--repo is required")
		os.Exit(2)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config load failed:", err)
		os.Exit(1)
	}
	if *opencodeURL != "" {
		cfg.OpenCode.BaseURL = *opencodeURL
	}
	if *opencodeUsername != "" {
		cfg.OpenCode.Username = *opencodeUsername
	}
	if *opencodePassword != "" {
		cfg.OpenCode.Password = *opencodePassword
	}
	if *opencodeTimeoutSeconds > 0 {
		cfg.OpenCode.TimeoutSec = *opencodeTimeoutSeconds
	}
	if *providerID != "" {
		cfg.OpenCode.ProviderID = *providerID
	}
	if *modelID != "" {
		cfg.OpenCode.ModelID = *modelID
	}
	if *modelVariant != "" {
		cfg.OpenCode.ModelVariant = *modelVariant
	}
	if *outDir != "" {
		cfg.Artifacts.BaseDir = *outDir
	}
	if *workers > 0 {
		cfg.Runtime.Workers = *workers
	}
	if *maxCatalogItems > 0 {
		cfg.Runtime.MaxCatalogItems = *maxCatalogItems
	}
	cfg.Runtime.CleanupOpenCodeSessions = *cleanupOpenCodeSessions
	cfg.Runtime.ReuseOpenCodeSession = *reuseOpenCodeSession
	cfg.Runtime.SkipReexamination = *skipReexamination
	if *opencodeDeleteDelaySeconds > 0 {
		cfg.Runtime.OpenCodeDeleteDelaySec = *opencodeDeleteDelaySeconds
	}
	if *minConfidence >= 0 {
		cfg.Quality.MinConfidence = *minConfidence
	}
	if cfg.OpenCode.Password == "" {
		cfg.OpenCode.Password = os.Getenv("OPENCODE_SERVER_PASSWORD")
	}
	if cfg.OpenCode.Username == "" {
		cfg.OpenCode.Username = os.Getenv("OPENCODE_SERVER_USERNAME")
	}

	out, err := app.Run(context.Background(), app.RunInput{RepoPath: *repo, Config: cfg})
	if err != nil {
		util.Error("cli.run", "run command failed", map[string]any{"error": err})
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	util.Info("cli.run", "run command finished", map[string]any{"run_id": out.RunID, "run_dir": out.RunDir})
	fmt.Print(app.PrintSummary(out))
}

func batchRun(args []string) {
	fs := flag.NewFlagSet("batch", flag.ExitOnError)
	reposFlag := fs.String("repos", "", "comma-separated list of repo paths")
	opencodeURL := fs.String("opencode-url", "", "OpenCode server base URL")
	opencodeTimeoutSeconds := fs.Int("opencode-timeout-seconds", 300, "OpenCode request timeout in seconds")
	providerID := fs.String("provider-id", "", "OpenCode provider ID")
	modelID := fs.String("model-id", "", "OpenCode model ID")
	parallel := fs.Int("parallel", 3, "max parallel repo extractions")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)

	if *reposFlag == "" {
		fmt.Fprintln(os.Stderr, "--repos is required (comma-separated paths)")
		os.Exit(2)
	}
	repos := strings.Split(*reposFlag, ",")
	for i := range repos {
		repos[i] = strings.TrimSpace(repos[i])
	}

	util.Info("cli.batch", "batch run starting", map[string]any{"repos": len(repos), "parallel": *parallel})

	type batchResult struct {
		repo string
		out  app.RunOutput
		err  error
	}

	sem := make(chan struct{}, *parallel)
	results := make(chan batchResult, len(repos))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, repo := range repos {
		repo := repo
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			cfg := config.Default()
			cfg.OpenCode.BaseURL = *opencodeURL
			cfg.OpenCode.TimeoutSec = *opencodeTimeoutSeconds
			cfg.OpenCode.ProviderID = *providerID
			cfg.OpenCode.ModelID = *modelID
			cfg.Artifacts.BaseDir = filepath.Join(repo, ".diffmind", "runs")
			if pw := os.Getenv("OPENCODE_SERVER_PASSWORD"); pw != "" {
				cfg.OpenCode.Password = pw
			}

			out, err := app.Run(ctx, app.RunInput{RepoPath: repo, Config: cfg})
			results <- batchResult{repo: repo, out: out, err: err}
		}()
	}

	successes := 0
	failures := 0
	for i := 0; i < len(repos); i++ {
		r := <-results
		base := filepath.Base(r.repo)
		if r.err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "FAIL  %s: %v\n", base, r.err)
		} else {
			successes++
			fmt.Printf("OK    %s -> %s\n", base, r.out.RunDir)
		}
	}
	fmt.Printf("\nBatch complete: %d succeeded, %d failed out of %d repos\n", successes, failures, len(repos))
	if failures > 0 {
		os.Exit(1)
	}
}

func validate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	baseDir := fs.String("out", ".diffmind/runs", "artifact base directory")
	runID := fs.String("run", "", "run id")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)
	util.Info("cli.validate", "validate command started", map[string]any{"run_id": *runID, "out": *baseDir})
	if *runID == "" {
		fmt.Fprintln(os.Stderr, "--run is required")
		os.Exit(2)
	}
	if err := app.ValidateRun(*baseDir, *runID); err != nil {
		util.Error("cli.validate", "validate command failed", map[string]any{"error": err})
		fmt.Fprintln(os.Stderr, "validation failed:", err)
		os.Exit(1)
	}
	util.Info("cli.validate", "validate command finished", map[string]any{"run_id": *runID})
	fmt.Println("run is valid:", *runID)
}

func listRuns(args []string) {
	fs := flag.NewFlagSet("list-runs", flag.ExitOnError)
	baseDir := fs.String("out", ".diffmind/runs", "artifact base directory")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)
	util.Info("cli.list_runs", "list-runs command started", map[string]any{"out": *baseDir})
	runs, err := app.ListRuns(*baseDir)
	if err != nil {
		util.Error("cli.list_runs", "list-runs command failed", map[string]any{"error": err})
		fmt.Fprintln(os.Stderr, "list-runs failed:", err)
		os.Exit(1)
	}
	util.Info("cli.list_runs", "list-runs command finished", map[string]any{"count": len(runs)})
	for _, r := range runs {
		fmt.Println(r)
	}
}

func serveUI(args []string) {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	baseDir := fs.String("out", ".diffmind/runs", "artifact base directory")
	host := fs.String("host", "127.0.0.1", "dashboard host")
	port := fs.Int("port", 8080, "dashboard port")
	uiToken := fs.String("ui-token", "", "optional shared-secret required by /api/* endpoints (set DIFFMIND_UI_TOKEN to override)")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)

	token := *uiToken
	if token == "" {
		token = os.Getenv("DIFFMIND_UI_TOKEN")
	}

	srv := ui.New(*baseDir, *host, *port)
	if token != "" {
		srv.SetToken(token)
	}
	url := fmt.Sprintf("http://%s", srv.Addr())
	util.Info("cli.ui", "starting dashboard", map[string]any{"url": url, "out": *baseDir, "auth": token != ""})
	fmt.Println("DiffMind dashboard:", url)
	if token != "" {
		fmt.Println("Auth token required for /api/* endpoints (X-DiffMind-Token, ?token=, or diffmind_token cookie).")
	}
	fmt.Println("Press Ctrl+C to stop.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Start(ctx); err != nil {
		util.Error("cli.ui", "dashboard stopped with error", map[string]any{"error": err})
		fmt.Fprintln(os.Stderr, "ui failed:", err)
		os.Exit(1)
	}
	util.Info("cli.ui", "dashboard stopped", nil)
}

func configureLogging(verbose, trace bool, logFile string) {
	level := os.Getenv("DIFFMIND_LOG_LEVEL")
	if trace {
		level = "trace"
	} else if verbose && level == "" {
		level = "debug"
	}
	writer := io.Writer(os.Stderr)
	if logFile != "" {
		_ = os.MkdirAll(filepath.Dir(logFile), 0o755)
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: failed to open log file:", err)
		} else {
			writer = io.MultiWriter(os.Stderr, f)
		}
	}
	util.Configure(level, writer)
}
