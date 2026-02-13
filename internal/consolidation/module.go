package consolidation

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"diffmind/internal/facts"
)

func Run(_ context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	bundle, err := readFactBundle(opts.InBundle)
	if err != nil {
		return err
	}
	if err := facts.ValidateBundle(bundle); err != nil {
		return fmt.Errorf("input fact bundle invalid: %w", err)
	}

	intel, report, err := consolidate(bundle, opts.SnapshotID)
	if err != nil {
		return err
	}

	bundlePath := filepath.Join(opts.OutDir, "bundle", "intelligence_bundle.json")
	reportPath := filepath.Join(opts.OutDir, "bundle", "report.json")
	if err := writeJSON(bundlePath, intel); err != nil {
		return err
	}
	if err := writeJSON(reportPath, report); err != nil {
		return err
	}

	slog.Info("consolidation completed",
		"snapshot_id", report.SnapshotID,
		"input_facts", report.InputFacts,
		"output_entities", report.OutputEntities,
		"duplicates_merged", report.DuplicatesMerged,
		"bundle_path", bundlePath,
	)
	fmt.Println(bundlePath)
	return nil
}

func parseOptions(args []string) (Options, error) {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	in := fs.String("in", filepath.Join(".diffmind", "analyzers", "bundle.json"), "Input analyzer facts bundle path")
	outDir := fs.String("out", ".diffmind", "Output root for canonical bundle")
	snapshotID := fs.String("snapshot-id", "", "Optional snapshot id override")

	if err := fs.Parse(filterBundleArgs(args)); err != nil {
		return Options{}, fmt.Errorf("parse bundle flags: %w", err)
	}
	return Options{InBundle: *in, OutDir: *outDir, SnapshotID: *snapshotID}, nil
}

func filterBundleArgs(args []string) []string {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--in" || arg == "--out" || arg == "--snapshot-id":
			filtered = append(filtered, arg)
			if i+1 < len(args) {
				i++
				filtered = append(filtered, args[i])
			}
		case strings.HasPrefix(arg, "--in=") || strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--snapshot-id="):
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

func readFactBundle(path string) (facts.Bundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return facts.Bundle{}, fmt.Errorf("read input bundle %s: %w", path, err)
	}
	var b facts.Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return facts.Bundle{}, fmt.Errorf("decode input bundle: %w", err)
	}
	return b, nil
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
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
