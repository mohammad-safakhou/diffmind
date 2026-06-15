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
		fmt.Fprintln(os.Stderr, "usage: diffmind <run|retry|validate|list-runs|eval|ui> ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		run(os.Args[2:])
	case "retry":
		retry(os.Args[2:])
	case "validate":
		validate(os.Args[2:])
	case "list-runs":
		listRuns(os.Args[2:])
	case "eval":
		evalCmd(os.Args[2:])
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
	opencodeTimeoutSeconds := fs.Int("opencode-timeout-seconds", 0, "OpenCode HTTP transport timeout in seconds (0 = use config default 4h fail-safe; primary control is --idle-timeout-seconds)")
	providerID := fs.String("provider-id", "", "OpenCode provider ID")
	modelID := fs.String("model-id", "", "OpenCode model ID")
	modelVariant := fs.String("model-variant", "", "OpenCode model variant (for example: low, medium, high, max)")
	outDir := fs.String("out", "", "artifact base directory (default ~/.diffmind/runs)")
	workers := fs.Int("workers", 0, "parallel worker count")
	maxCatalogItems := fs.Int("max-catalog-items", 0, "maximum dependency catalog items sent per connection-mapping prompt batch")
	cleanupOpenCodeSessions := fs.Bool("cleanup-opencode-sessions", false, "delete OpenCode sessions after prompts (can trigger server-side FK races)")
	opencodeDeleteDelaySeconds := fs.Int("opencode-delete-delay-seconds", 0, "delay before deleting OpenCode sessions when cleanup is enabled")
	reuseOpenCodeSession := fs.Bool("reuse-opencode-session", false, "reuse a single OpenCode session across prompts in a run")
	skipReexamination := fs.Bool("skip-reexamination", false, "skip stage 2 (LLM re-ask for low-signal seeds) for faster, lower-accuracy runs")
	skipDetail := fs.Bool("skip-detail", false, "skip stage 3 (LLM detail enrichment); verified seeds convert straight to entities and high-value fields are backfilled deterministically from the AST")
	discoveryVerify := fs.Bool("discovery-verify", false, "enable the stage-1.5 discovery verification pass (gated to high-variance objectives; fail-soft, keep-biased)")
	discoveryVerifyMode := fs.String("discovery-verify-mode", "", "verification strategy when --discovery-verify is on: reask (re-open + find-missed) or ksample (run K times and union) (empty = use config default reask)")
	discoveryVerifySamples := fs.Int("discovery-verify-samples", 0, "K for ksample verify mode, floored to [1,5] (0 = use config default 2)")
	discoveryFrameworkScope := fs.Bool("discovery-framework-scope", false, "drop discovery-prompt bullets for frameworks the repo shows no trace of (riskier prompt trim; default off)")
	minConfidence := fs.Float64("min-confidence", -1, "confidence threshold in [0,1]")
	idleTimeoutSeconds := fs.Int("idle-timeout-seconds", 0, "abort a prompt after this many seconds without observable progress on the OpenCode session (0 = use config default 120s)")
	promptRetryCount := fs.Int("prompt-retry-count", -1, "retry a prompt this many times after the liveness watchdog declares it stuck (-1 = use config default 3; 0 = disable)")
	maxCallSeconds := fs.Int("max-call-seconds", 0, "hard ceiling on a single LLM call's duration in seconds (0 = use config default 1800s)")
	livenessPollSeconds := fs.Int("liveness-poll-seconds", 0, "how often the liveness watchdog polls OpenCode for progress (0 = use config default 5s)")
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
	cfg, err := config.LoadCentral(*cfgPath)
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
	cfg.Runtime.SkipDetail = *skipDetail
	if *discoveryVerifyMode != "" {
		cfg.Runtime.DiscoveryVerifyMode = *discoveryVerifyMode
	}
	if *discoveryVerifySamples > 0 {
		cfg.Runtime.DiscoveryVerifySamples = *discoveryVerifySamples
	}
	// The verify/framework-scope toggles override the config file only when the
	// flag was explicitly passed, so the flag's false default can't silently
	// clobber a config-file value (Sanitize later floors samples / coerces mode).
	flagSet := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { flagSet[f.Name] = true })
	if flagSet["discovery-verify"] {
		cfg.Runtime.DiscoveryVerify = *discoveryVerify
	}
	if flagSet["discovery-framework-scope"] {
		cfg.Runtime.DiscoveryFrameworkScope = *discoveryFrameworkScope
	}
	if *opencodeDeleteDelaySeconds > 0 {
		cfg.Runtime.OpenCodeDeleteDelaySec = *opencodeDeleteDelaySeconds
	}
	if *idleTimeoutSeconds > 0 {
		cfg.Runtime.IdleTimeoutSec = *idleTimeoutSeconds
	}
	if *promptRetryCount >= 0 {
		cfg.Runtime.PromptRetryCount = *promptRetryCount
	}
	if *maxCallSeconds > 0 {
		cfg.Runtime.MaxCallSeconds = *maxCallSeconds
	}
	if *livenessPollSeconds > 0 {
		cfg.Runtime.LivenessPollSec = *livenessPollSeconds
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
		if out.Failure != nil && out.RunDir != "" {
			fmt.Fprintf(os.Stderr, "failure report: %s\n", filepath.Join(out.RunDir, "run_failure.md"))
			fmt.Fprintf(os.Stderr, "after fixing the cause, retry with: diffmind retry --run %s\n", out.RunID)
		}
		os.Exit(1)
	}
	util.Info("cli.run", "run command finished", map[string]any{"run_id": out.RunID, "run_dir": out.RunDir})
	fmt.Print(app.PrintSummary(out))
}

func retry(args []string) {
	fs := flag.NewFlagSet("retry", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to diffmind json config")
	opencodeURL := fs.String("opencode-url", "", "OpenCode server base URL (overrides config)")
	opencodeUsername := fs.String("opencode-username", "", "OpenCode basic auth username")
	opencodePassword := fs.String("opencode-password", "", "OpenCode basic auth password")
	opencodeTimeoutSeconds := fs.Int("opencode-timeout-seconds", 0, "OpenCode HTTP transport timeout in seconds (0 = use config default 4h fail-safe; primary control is --idle-timeout-seconds)")
	providerID := fs.String("provider-id", "", "OpenCode provider ID (overrides config)")
	modelID := fs.String("model-id", "", "OpenCode model ID (overrides config)")
	modelVariant := fs.String("model-variant", "", "OpenCode model variant")
	promptRetryCount := fs.Int("prompt-retry-count", -1, "retry a prompt this many times after the liveness watchdog declares it stuck (-1 = use config default 3; 0 = disable)")
	outDir := fs.String("out", "", "artifact base directory (default ~/.diffmind/runs)")
	runID := fs.String("run", "", "run id to resume")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)

	if *runID == "" {
		fmt.Fprintln(os.Stderr, "--run is required")
		os.Exit(2)
	}
	cfg, err := config.LoadCentral(*cfgPath)
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
	if *promptRetryCount >= 0 {
		cfg.Runtime.PromptRetryCount = *promptRetryCount
	}
	if cfg.OpenCode.Password == "" {
		cfg.OpenCode.Password = os.Getenv("OPENCODE_SERVER_PASSWORD")
	}
	if cfg.OpenCode.Username == "" {
		cfg.OpenCode.Username = os.Getenv("OPENCODE_SERVER_USERNAME")
	}

	out, err := app.RetryRun(context.Background(), app.RetryInput{
		BaseDir: cfg.Artifacts.BaseDir, RunID: *runID, Config: cfg,
	})
	if err != nil {
		util.Error("cli.retry", "retry command failed", map[string]any{"error": err})
		fmt.Fprintln(os.Stderr, "retry failed:", err)
		if out.Failure != nil && out.RunDir != "" {
			fmt.Fprintf(os.Stderr, "failure report: %s\n", filepath.Join(out.RunDir, "run_failure.md"))
		}
		os.Exit(1)
	}
	util.Info("cli.retry", "retry command finished", map[string]any{"run_id": out.RunID, "run_dir": out.RunDir})
	fmt.Print(app.PrintSummary(out))
}

func validate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	baseDir := fs.String("out", "", "artifact base directory (default ~/.diffmind/runs)")
	runID := fs.String("run", "", "run id")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)
	base := resolveBaseDir(*baseDir)
	util.Info("cli.validate", "validate command started", map[string]any{"run_id": *runID, "out": base})
	if *runID == "" {
		fmt.Fprintln(os.Stderr, "--run is required")
		os.Exit(2)
	}
	if err := app.ValidateRun(base, *runID); err != nil {
		util.Error("cli.validate", "validate command failed", map[string]any{"error": err})
		fmt.Fprintln(os.Stderr, "validation failed:", err)
		os.Exit(1)
	}
	util.Info("cli.validate", "validate command finished", map[string]any{"run_id": *runID})
	fmt.Println("run is valid:", *runID)
}

func listRuns(args []string) {
	fs := flag.NewFlagSet("list-runs", flag.ExitOnError)
	baseDir := fs.String("out", "", "artifact base directory (default ~/.diffmind/runs)")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)
	base := resolveBaseDir(*baseDir)
	util.Info("cli.list_runs", "list-runs command started", map[string]any{"out": base})
	runs, err := app.ListRuns(base)
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
	baseDir := fs.String("out", "", "artifact base directory (default ~/.diffmind/runs)")
	host := fs.String("host", "127.0.0.1", "dashboard host")
	port := fs.Int("port", 8080, "dashboard port")
	uiToken := fs.String("ui-token", "", "optional shared-secret required by /api/* endpoints (set DIFFMIND_UI_TOKEN to override)")
	noSPARebuild := fs.Bool("no-spa-rebuild", false, "skip automatic SPA rebuild on startup (use the embedded or existing dist/ as-is)")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)

	// Auto-rebuild the SPA when running from a development checkout
	// AND the sources are newer than dist/. This makes `go run
	// ./cmd/diffmind ui` "just work" without the user having to
	// remember `npm run build`. When no SPA source tree is on disk
	// (production binary), this returns "" and we serve the embedded
	// bundle. See cmd/diffmind/spabuild.go for the full logic.
	if distDir := ensureSPABuilt(*noSPARebuild); distDir != "" {
		ui.SetDistOverride(distDir)
	}

	token := *uiToken
	if token == "" {
		token = os.Getenv("DIFFMIND_UI_TOKEN")
	}

	base := resolveBaseDir(*baseDir)
	srv := ui.New(base, *host, *port)
	if token != "" {
		srv.SetToken(token)
	}
	url := fmt.Sprintf("http://%s", srv.Addr())
	util.Info("cli.ui", "starting dashboard", map[string]any{"url": url, "out": base, "auth": token != ""})
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

// resolveBaseDir returns the explicit --out value when set, otherwise the
// central ~/.diffmind/runs directory.
func resolveBaseDir(out string) string {
	if strings.TrimSpace(out) != "" {
		return out
	}
	return config.RunsDir()
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
