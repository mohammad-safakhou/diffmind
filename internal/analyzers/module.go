package analyzers

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"diffmind/internal/config"
	"diffmind/internal/facts"
	"diffmind/internal/store"
)

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	root, err := filepath.Abs(opts.Source)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}

	res, err := analyze(ctx, root, opts.SnapshotID, opts.Adapters, opts.Extractors, opts.IncludeTests, opts.AllowMissingAdapters)
	if err != nil {
		return err
	}
	res.report.Offline = opts.Offline

	if opts.Offline && opts.LLMAugment {
		return fmt.Errorf("llm augmentation is disabled in offline mode; rerun with --offline=false to enable --llm-augment")
	}

	llmOpts := llmOptions{
		Enabled:        opts.LLMAugment,
		Model:          opts.LLMModel,
		Task:           opts.LLMTask,
		MaxFiles:       opts.LLMMaxFiles,
		MaxChars:       opts.LLMMaxChars,
		DefaultConf:    0.55,
		TraceOutputDir: filepath.Join(opts.OutDir, "llm", "traces"),
	}
	res.report.LLMEnabled = llmOpts.Enabled
	if llmOpts.Enabled {
		aug, added, tracePath, err := maybeAugmentWithLLM(ctx, root, res.bundle, llmOpts, res.report.SnapshotID)
		if err != nil {
			return fmt.Errorf("llm augmentation failed: %w", err)
		}
		res.bundle = aug
		res.report.LLMFactsAdded = added
		res.report.LLMTracePath = tracePath
		res.report.FactsCount = len(res.bundle.Facts)
		res.report.EvidenceCount = len(res.bundle.Evidence)
	}

	if err := executeAdapterTools(opts.OutDir, root, &res.report); err != nil {
		return err
	}
	if err := mergeAdapterSemanticToolOutputs(root, &res); err != nil {
		return err
	}
	if err := facts.ValidateBundle(res.bundle); err != nil {
		return fmt.Errorf("validate analyzer bundle after adapter semantic merge: %w", err)
	}

	if err := writeAdapterRunManifests(opts.OutDir, &res.report); err != nil {
		return err
	}
	resolvedProfilesPath, resolvedProfilesSHA, ok, err := writeResolvedConfigProfilesArtifact(opts.OutDir, &res.report, res.bundle)
	if err != nil {
		return err
	}
	if ok {
		res.report.ResolvedConfigProfilesPath = resolvedProfilesPath
		res.report.ResolvedConfigProfilesSHA256 = resolvedProfilesSHA
	}

	manifestPath, manifestSHA, err := writeToolchainManifest(opts.OutDir, &res.report)
	if err != nil {
		return err
	}
	res.report.ToolchainManifestPath = manifestPath
	res.report.ToolchainManifestSHA256 = manifestSHA

	bundlePath := filepath.Join(opts.OutDir, "analyzers", "bundle.json")
	reportPath := filepath.Join(opts.OutDir, "analyzers", "report.json")
	if err := writeJSON(bundlePath, res.bundle); err != nil {
		return err
	}
	if err := writeJSON(reportPath, res.report); err != nil {
		return err
	}

	if opts.Persist {
		cfg, err := config.LoadFromEnv()
		if err != nil {
			return fmt.Errorf("load config for persistence: %w", err)
		}
		db, err := store.NewPostgresDB(ctx, cfg.PostgresURL)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		factStore := store.NewFactStore(db)
		if err := factStore.PersistBundle(ctx, res.bundle); err != nil {
			return fmt.Errorf("persist analyzer bundle: %w", err)
		}
	}

	slog.Info("analyzers completed",
		"source", root,
		"snapshot_id", res.report.SnapshotID,
		"facts", res.report.FactsCount,
		"evidence", res.report.EvidenceCount,
		"llm_enabled", res.report.LLMEnabled,
		"llm_facts_added", res.report.LLMFactsAdded,
		"offline", res.report.Offline,
		"toolchain_manifest", res.report.ToolchainManifestPath,
		"bundle_path", bundlePath,
	)
	fmt.Println(bundlePath)
	return nil
}

func parseOptions(args []string) (Options, error) {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	source := fs.String("source", ".", "Repository source path")
	outDir := fs.String("out", ".diffmind", "Output root for analyzer artifacts")
	snapshotID := fs.String("snapshot-id", "", "Optional snapshot id")
	persist := fs.Bool("persist", false, "Persist analyzer facts/evidence into Postgres")
	offline := fs.Bool("offline", true, "Run in offline self-hosted mode (disables network-bound augmentations)")
	allowMissingAdapters := fs.Bool("allow-missing-adapters", false, "Allow selected adapters to be unavailable without failing analyze")
	includeTests := fs.Bool("include-tests", false, "Include test source files (default: false)")
	extractors := fs.String("extractors", "", "Comma-separated extractor names (default: all built-ins)")
	adapters := fs.String("adapters", "", "Comma-separated analyzer adapter names (default: builtin)")
	llmAugment := fs.Bool("llm-augment", false, "Enable bounded LLM augmentation")
	llmModel := fs.String("llm-model", "gpt-5-mini", "LLM model for augmentation")
	llmTask := fs.String("llm-task", "augment-routes-http-config", "LLM augmentation task")
	llmMaxFiles := fs.Int("llm-max-files", 20, "LLM evidence pack max files")
	llmMaxChars := fs.Int("llm-max-chars", 50000, "LLM evidence pack max chars")

	if err := fs.Parse(filterAnalyzeArgs(args)); err != nil {
		return Options{}, fmt.Errorf("parse analyze flags: %w", err)
	}
	return Options{
		Source:               *source,
		OutDir:               *outDir,
		SnapshotID:           *snapshotID,
		Persist:              *persist,
		Offline:              *offline,
		AllowMissingAdapters: *allowMissingAdapters,
		IncludeTests:         *includeTests,
		Extractors:           *extractors,
		Adapters:             *adapters,
		LLMAugment:           *llmAugment,
		LLMModel:             *llmModel,
		LLMTask:              *llmTask,
		LLMMaxFiles:          *llmMaxFiles,
		LLMMaxChars:          *llmMaxChars,
	}, nil
}

func filterAnalyzeArgs(args []string) []string {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--source" || arg == "--out" || arg == "--snapshot-id" || arg == "--extractors" || arg == "--adapters" || arg == "--llm-model" || arg == "--llm-task" || arg == "--llm-max-files" || arg == "--llm-max-chars":
			filtered = append(filtered, arg)
			if i+1 < len(args) {
				i++
				filtered = append(filtered, args[i])
			}
		case arg == "--persist" || arg == "--llm-augment" || arg == "--offline" || arg == "--include-tests" || arg == "--allow-missing-adapters":
			filtered = append(filtered, arg)
		case strings.HasPrefix(arg, "--source=") || strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--snapshot-id=") ||
			strings.HasPrefix(arg, "--extractors=") ||
			strings.HasPrefix(arg, "--adapters=") ||
			strings.HasPrefix(arg, "--persist=") || strings.HasPrefix(arg, "--offline=") || strings.HasPrefix(arg, "--include-tests=") || strings.HasPrefix(arg, "--allow-missing-adapters=") || strings.HasPrefix(arg, "--llm-augment=") || strings.HasPrefix(arg, "--llm-model=") ||
			strings.HasPrefix(arg, "--llm-task=") || strings.HasPrefix(arg, "--llm-max-files=") || strings.HasPrefix(arg, "--llm-max-chars="):
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}
