package classifier

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

func Run(_ context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	root, err := filepath.Abs(opts.Source)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}

	files, stats, err := ScanTree(root)
	if err != nil {
		return err
	}

	report := BuildReport(root, files, stats)
	reportPath := filepath.Join(opts.OutDir, "classification", "report.json")
	if err := writeReport(reportPath, report); err != nil {
		return err
	}

	slog.Info("classification completed",
		"source", root,
		"labels", len(report.Profile.Labels),
		"languages", len(report.Capabilities.Languages),
		"report_path", reportPath,
	)
	fmt.Println(reportPath)
	return nil
}

func parseOptions(args []string) (Options, error) {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	source := fs.String("source", ".", "Repository source path")
	outDir := fs.String("out", ".diffmind", "Output root for classifier artifacts")

	if err := fs.Parse(filterClassifierArgs(args)); err != nil {
		return Options{}, fmt.Errorf("parse scan flags: %w", err)
	}
	return Options{Source: *source, OutDir: *outDir}, nil
}

func filterClassifierArgs(args []string) []string {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--source" || arg == "--out":
			filtered = append(filtered, arg)
			if i+1 < len(args) {
				i++
				filtered = append(filtered, args[i])
			}
		case arg == "-source" || arg == "-out":
			filtered = append(filtered, arg)
			if i+1 < len(args) {
				i++
				filtered = append(filtered, args[i])
			}
		case len(arg) > 9 && arg[:9] == "--source=":
			filtered = append(filtered, arg)
		case len(arg) > 6 && arg[:6] == "--out=":
			filtered = append(filtered, arg)
		case len(arg) > 8 && arg[:8] == "-source=":
			filtered = append(filtered, arg)
		case len(arg) > 5 && arg[:5] == "-out=":
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

func writeReport(path string, report ScanReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create classifier output dir: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal classification report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write classification report: %w", err)
	}
	return nil
}
