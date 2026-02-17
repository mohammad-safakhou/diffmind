package app

import (
	"context"
	"fmt"
	"strings"

	"diffmind/internal/config"
	"diffmind/internal/logging"
	"diffmind/internal/orchestrator"
)

var supportedCommands = map[string]func(context.Context, []string) error{
	"snapshot":  orchestrator.RunSnapshot,
	"scan":      orchestrator.RunScan,
	"parse":     orchestrator.RunParse,
	"analyze":   orchestrator.RunAnalyze,
	"bundle":    orchestrator.RunBundle,
	"verify":    orchestrator.RunVerify,
	"query":     orchestrator.RunQuery,
	"diff":      orchestrator.RunDiff,
	"serve":     orchestrator.RunServe,
	"corpus":    orchestrator.RunCorpus,
	"golden":    orchestrator.RunGolden,
	"graph":     orchestrator.RunGraph,
	"quality":   orchestrator.RunQuality,
	"ops":       orchestrator.RunOps,
	"finalgate": orchestrator.RunFinalGate,
	"run":       orchestrator.RunPipeline,
	"help":      func(context.Context, []string) error { return nil },
	"--help":    func(context.Context, []string) error { return nil },
	"-h":        func(context.Context, []string) error { return nil },
	"version":   func(context.Context, []string) error { return nil },
	"--version": func(context.Context, []string) error { return nil },
	"-version":  func(context.Context, []string) error { return nil },
}

func Run(ctx context.Context, args []string) error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := logging.New(cfg.LogLevel)
	logger.Info("diffmind starting", "environment", cfg.Environment)

	if len(args) == 0 || isHelp(args[0]) {
		printUsage()
		return nil
	}

	if isVersion(args[0]) {
		fmt.Println("diffmind extractor dev")
		return nil
	}

	cmd := strings.ToLower(args[0])
	run, ok := supportedCommands[cmd]
	if !ok {
		printUsage()
		return fmt.Errorf("unknown command %q", cmd)
	}

	return run(ctx, args[1:])
}

func isHelp(arg string) bool {
	return arg == "help" || arg == "--help" || arg == "-h"
}

func isVersion(arg string) bool {
	return arg == "version" || arg == "--version" || arg == "-version"
}

func printUsage() {
	fmt.Println("DiffMind Repo Extractor")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  extractor <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  snapshot   Run snapshot module only")
	fmt.Println("  scan       Run classification module only")
	fmt.Println("  parse      Run parser module only")
	fmt.Println("  analyze    Run analyzers module only")
	fmt.Println("  bundle     Run consolidation module only")
	fmt.Println("  verify     Run verification/adjudication module on intelligence bundle")
	fmt.Println("  query      Query canonical intelligence bundle")
	fmt.Println("  diff       Diff two canonical intelligence bundles")
	fmt.Println("  serve      Serve HTTP API for bundle query/diff")
	fmt.Println("  corpus     Run acceptance corpus against multiple repos")
	fmt.Println("  golden     Verify or update corpus golden summaries")
	fmt.Println("  graph      Build and manage service graph artifacts")
	fmt.Println("  quality    Evaluate quality metrics and enforce release gates")
	fmt.Println("  ops        Evaluate SLOs, backup/restore, and rollout plans")
	fmt.Println("  finalgate  Run state-of-the-art completion gate attestation")
	fmt.Println("  run        Run full orchestrated pipeline")
	fmt.Println("  version    Print version")
}
