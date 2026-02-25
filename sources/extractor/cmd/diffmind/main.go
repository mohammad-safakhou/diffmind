package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/app"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: diffmind <run|validate|list-runs> ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		run(os.Args[2:])
	case "validate":
		validate(os.Args[2:])
	case "list-runs":
		listRuns(os.Args[2:])
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
	outDir := fs.String("out", "", "artifact base directory (default .diffmind/runs)")
	workers := fs.Int("workers", 0, "parallel worker count")
	maxEntitiesPerObjective := fs.Int("max-entities-per-objective", 0, "maximum entities discovered per objective per round")
	maxCatalogItems := fs.Int("max-catalog-items", 0, "maximum catalog items shared between agents")
	cleanupOpenCodeSessions := fs.Bool("cleanup-opencode-sessions", false, "delete OpenCode sessions after prompts (can trigger server-side FK races)")
	opencodeDeleteDelaySeconds := fs.Int("opencode-delete-delay-seconds", 0, "delay before deleting OpenCode sessions when cleanup is enabled")
	minConfidence := fs.Float64("min-confidence", -1, "confidence threshold in [0,1]")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	trace := fs.Bool("trace", false, "enable trace logs (very noisy)")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, *trace, *logFile)
	util.Info("cli.run", "run command started", map[string]any{
		"repo": *repo, "config": *cfgPath, "opencode_url": *opencodeURL, "workers": *workers,
		"max_entities_per_objective": *maxEntitiesPerObjective,
		"max_catalog_items":          *maxCatalogItems, "opencode_timeout_seconds": *opencodeTimeoutSeconds,
		"cleanup_opencode_sessions": *cleanupOpenCodeSessions, "opencode_delete_delay_seconds": *opencodeDeleteDelaySeconds,
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
	if *outDir != "" {
		cfg.Artifacts.BaseDir = *outDir
	}
	if *workers > 0 {
		cfg.Runtime.Workers = *workers
	}
	if *maxEntitiesPerObjective > 0 {
		cfg.Runtime.MaxEntitiesPerObjective = *maxEntitiesPerObjective
	}
	if *maxCatalogItems > 0 {
		cfg.Runtime.MaxCatalogItems = *maxCatalogItems
	}
	cfg.Runtime.CleanupOpenCodeSessions = *cleanupOpenCodeSessions
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
