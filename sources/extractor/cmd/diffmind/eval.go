package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/eval"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// evalCmd scores the extractor against hand-labeled fixtures.
//
//	diffmind eval --mode cheap   --fixtures testdata/eval [--min-f1 0.9] [--json out.json]
//	diffmind eval --mode score-run --run <run-id|dir> --fixture <fixture-dir> [--json out.json]
//
// Cheap mode runs the deterministic floor (no LLM, hermetic) over every fixture
// under --fixtures and reports per-objective + overall precision/recall/F1.
// score-run grades an already-finished run directory against one fixture label.
func evalCmd(args []string) {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	mode := fs.String("mode", "cheap", "cheap | score-run")
	fixtures := fs.String("fixtures", "testdata/eval", "directory of labeled fixtures (cheap mode)")
	fixture := fs.String("fixture", "", "single fixture dir (score-run mode)")
	runArg := fs.String("run", "", "run id or run directory to score (score-run mode)")
	outDir := fs.String("out", "", "artifact base directory used to resolve --run (default ~/.diffmind/runs)")
	jsonOut := fs.String("json", "", "optional path to write the machine-readable report")
	minF1 := fs.Float64("min-f1", 0, "exit non-zero if any fixture's overall F1 is below this")
	workers := fs.Int("workers", 4, "AST build worker count (cheap mode)")
	minConfidence := fs.Float64("min-confidence", 0.7, "confidence threshold for the deterministic floor")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, false, *logFile)

	cfg := config.Default()
	cfg.Quality.MinConfidence = *minConfidence
	cfg.Runtime.Workers = *workers

	var reports []eval.Report
	switch *mode {
	case "cheap":
		dirs, err := fixtureDirs(*fixtures)
		if err != nil {
			fmt.Fprintln(os.Stderr, "eval:", err)
			os.Exit(1)
		}
		if len(dirs) == 0 {
			fmt.Fprintf(os.Stderr, "eval: no fixtures (expected.json) found under %s\n", *fixtures)
			os.Exit(1)
		}
		for _, d := range dirs {
			rep, err := eval.RunCheap(context.Background(), d, cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "eval: %s: %v\n", d, err)
				os.Exit(1)
			}
			reports = append(reports, rep)
		}
	case "score-run":
		if *fixture == "" || *runArg == "" {
			fmt.Fprintln(os.Stderr, "eval --mode score-run requires --fixture and --run")
			os.Exit(2)
		}
		runDir := resolveRunDir(*runArg, *outDir)
		rep, err := eval.ScoreRun(runDir, *fixture)
		if err != nil {
			fmt.Fprintln(os.Stderr, "eval:", err)
			os.Exit(1)
		}
		reports = append(reports, rep)
	default:
		fmt.Fprintf(os.Stderr, "eval: unknown --mode %q (cheap|score-run)\n", *mode)
		os.Exit(2)
	}

	failed := false
	for _, rep := range reports {
		eval.RenderTable(os.Stdout, rep)
		fmt.Println()
		if rep.Overall.F1 < *minF1 {
			failed = true
		}
	}
	if *jsonOut != "" {
		writeEvalJSON(*jsonOut, reports)
	}
	util.Info("cli.eval", "eval finished", map[string]any{"fixtures": len(reports), "mode": *mode})
	if failed {
		fmt.Fprintf(os.Stderr, "eval: one or more fixtures below --min-f1 %.2f\n", *minF1)
		os.Exit(1)
	}
}

// fixtureDirs returns every subdirectory of root that holds an expected.json,
// plus root itself if it directly contains one.
func fixtureDirs(root string) ([]string, error) {
	var out []string
	if _, err := os.Stat(filepath.Join(root, "expected.json")); err == nil {
		out = append(out, root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(d, "expected.json")); err == nil {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, nil
}

// resolveRunDir accepts either a run id (resolved under the artifacts base dir)
// or a path to a run directory directly.
func resolveRunDir(runArg, outDir string) string {
	if info, err := os.Stat(runArg); err == nil && info.IsDir() {
		return runArg
	}
	return filepath.Join(resolveBaseDir(outDir), runArg)
}

func writeEvalJSON(path string, reports []eval.Report) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval: write json:", err)
		return
	}
	defer f.Close()
	// One report per fixture: emit an array.
	fmt.Fprintln(f, "[")
	for i, rep := range reports {
		if err := eval.WriteJSON(f, rep); err != nil {
			fmt.Fprintln(os.Stderr, "eval: encode json:", err)
			return
		}
		if i < len(reports)-1 {
			fmt.Fprintln(f, ",")
		}
	}
	fmt.Fprintln(f, "]")
}
