// DiffMind — Cross-service dependency graph builder.
// Consumes DiffMind architecture artifacts and infrastructure configs
// to produce a unified cross-service dependency graph.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/interview"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/ui"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

const usage = `DiffMind — Cross-service dependency graph builder.

Usage:
  diffmind <command> [flags]

Commands:
  run          Run the full pipeline: collect → resolve → graph
  ui           Launch interactive graph visualization dashboard
  interview    Run AI-driven DevOps interview to generate blueprints
  collect      Collect DiffMind artifacts from all configured repos
  list         List known services from config
  help         Show this help message

Flags:
  --config     Path to diffmind.json config file (default: diffmind.json)
  --log-level  Log level: info, debug, trace (default: info)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}

	command := os.Args[1]
	if command == "help" || command == "--help" || command == "-h" {
		fmt.Print(usage)
		return
	}

	// Parse flags after the command.
	fs := flag.NewFlagSet(command, flag.ExitOnError)
	configPath := fs.String("config", "diffmind.json", "Path to config file")
	logLevel := fs.String("log-level", "info", "Log level: info, debug, trace")
	// UI-specific flags (ignored by other commands)
	fs.String("host", "127.0.0.1", "UI server host")
	fs.Int("port", 8090, "UI server port")
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	// Set up logger.
	var lvl util.LogLevel
	switch *logLevel {
	case "debug":
		lvl = util.LevelDebug
	case "trace":
		lvl = util.LevelTrace
	default:
		lvl = util.LevelInfo
	}
	log := util.NewLogger(lvl)

	// Load config.
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err.Error())
		os.Exit(1)
	}

	switch command {
	case "run":
		cmdRun(cfg, log)
	case "ui":
		cmdUI(cfg, fs)
	case "interview":
		cmdInterview(cfg, log)
	case "collect":
		cmdCollect(cfg, log)
	case "list":
		cmdList(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		fmt.Print(usage)
		os.Exit(1)
	}
}

func loadConfig(path string) (*config.Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		// No config file — use defaults.
		cfg := config.NewDefault()
		return cfg, nil
	}
	return config.LoadFromFile(abs)
}

func createClient(cfg *config.Config) *opencode.Client {
	if cfg.OpenCode.BaseURL == "" {
		return nil
	}
	return opencode.NewClient(
		cfg.OpenCode.BaseURL,
		cfg.OpenCode.ProviderID,
		cfg.OpenCode.ModelID,
		cfg.OpenCode.Variant,
		cfg.OpenCode.Username,
		cfg.OpenCode.Password,
		time.Duration(cfg.OpenCode.Timeout)*time.Second,
	)
}

func cmdUI(cfg *config.Config, fs *flag.FlagSet) {
	h := "127.0.0.1"
	p := 8090

	if f := fs.Lookup("host"); f != nil {
		h = f.Value.String()
	}
	if f := fs.Lookup("port"); f != nil {
		fmt.Sscanf(f.Value.String(), "%d", &p)
	}

	baseDir := cfg.Artifacts.BaseDir
	if baseDir == "" {
		baseDir = ".diffmind/runs"
	}

	serviceRepoDirs := make(map[string]string)
	for _, repo := range cfg.Repos.ServiceRepos {
		serviceRepoDirs[repo.Name] = repo.Path
	}

	srv := ui.New(baseDir, serviceRepoDirs, h, p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "UI server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdRun(cfg *config.Config, log *util.Logger) {
	client := createClient(cfg)

	// Optional: check OpenCode health.
	if client != nil {
		if err := client.Health(); err != nil {
			log.Warn("OpenCode server not reachable; running in deterministic-only mode", "error", err.Error())
			client = nil
		} else {
			log.Info("OpenCode server is healthy")
		}
	}

	pipeline := orchestrator.NewPipeline(cfg, client, log)
	result, err := pipeline.Run()
	if err != nil {
		log.Error("pipeline failed", "error", err.Error())
		os.Exit(1)
	}

	fmt.Println("\n=== DiffMind Run Complete ===")
	fmt.Printf("Services:         %d\n", result.ServiceCount)
	fmt.Printf("Edges:            %d\n", result.EdgeCount)
	fmt.Printf("Duration:         %s\n", result.Duration.Round(time.Millisecond))
	fmt.Printf("Output directory: %s\n", result.OutputDir)

	// Print a summary of edges.
	if result.Graph != nil && len(result.Graph.Edges) > 0 {
		fmt.Println("\nCross-service connections:")
		for _, e := range result.Graph.Edges {
			fmt.Printf("  %s → %s (%s, confidence: %.2f)\n", e.FromService, e.ToService, e.Type, e.Confidence)
		}
	}
	if result.Graph != nil && len(result.Graph.Unresolved) > 0 {
		fmt.Printf("\nUnresolved dependencies: %d\n", len(result.Graph.Unresolved))
		for _, u := range result.Graph.Unresolved {
			fmt.Printf("  %s: %s → %s (%s)\n", u.Service, u.DependencyName, u.Target, u.Reason)
		}
	}
}

func cmdInterview(cfg *config.Config, log *util.Logger) {
	client := createClient(cfg)
	if client != nil {
		if err := client.Health(); err != nil {
			log.Warn("OpenCode not available; interview will generate pattern-based blueprints only")
			client = nil
		}
	}

	// Collect all repo paths.
	var repoPaths []string
	for _, r := range cfg.Repos.ServiceRepos {
		repoPaths = append(repoPaths, r.Path)
	}
	for _, r := range cfg.Repos.InfraRepos {
		repoPaths = append(repoPaths, r.Path)
	}

	outputDir := ".diffmind/blueprints"
	if len(cfg.Blueprints.Dirs) > 0 {
		outputDir = cfg.Blueprints.Dirs[0]
	}

	iv := interview.NewInterviewer(client, log, repoPaths, outputDir)
	bps, err := iv.Run()
	if err != nil {
		log.Error("interview failed", "error", err.Error())
		os.Exit(1)
	}
	fmt.Printf("\nGenerated %d blueprints in %s\n", len(bps), outputDir)
}

func cmdCollect(cfg *config.Config, log *util.Logger) {
	for _, repo := range cfg.Repos.ServiceRepos {
		arch, err := orchestrator.CollectService(repo, log)
		if err != nil {
			log.Warn("failed to collect", "service", repo.Name, "error", err.Error())
			continue
		}
		fmt.Printf("%s: %d exposures, %d dependencies, %d connections\n",
			repo.Name,
			len(arch.Exposures),
			len(arch.Dependencies),
			len(arch.Connections))
	}
}

func cmdList(cfg *config.Config) {
	fmt.Println("Service repos:")
	for _, r := range cfg.Repos.ServiceRepos {
		artifacts := ""
		if r.DiffMindArtifacts != "" {
			artifacts = fmt.Sprintf(" (artifacts: %s)", r.DiffMindArtifacts)
		}
		fmt.Printf("  - %s: %s%s\n", r.Name, r.Path, artifacts)
	}
	fmt.Println("\nInfra repos:")
	for _, r := range cfg.Repos.InfraRepos {
		fmt.Printf("  - %s: %s\n", r.Name, r.Path)
	}
	fmt.Println("\nBlueprint dirs:")
	for _, d := range cfg.Blueprints.Dirs {
		fmt.Printf("  - %s\n", d)
	}

	// Pretty print full config.
	data, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Printf("\nFull config:\n%s\n", string(data))
}
