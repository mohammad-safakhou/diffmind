package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/eval"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// evalCmd scores the extractor against hand-labeled fixtures.
//
//	diffmind eval --mode cheap   --fixtures testdata/eval [--min-f1 0.9] [--json out.json]
//	diffmind eval --mode score-run --run <run-id|dir> --fixture <fixture-dir> [--json out.json]
//	diffmind eval --mode variance --runs <id1,id2,...> [--min-core-union 0.95] [--json out.json]
//
// Cheap mode runs the deterministic floor over every fixture under --fixtures
// and reports per-objective + overall precision/recall/F1.
// score-run grades an already-finished run directory against one fixture label.
// variance compares K finished runs of the SAME repo and reports per-objective
// run-to-run stability (count mean/stdev, core/union ratio, pairwise Jaccard),
// using the same identity the scorer/dedup use.
func evalCmd(args []string) {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	mode := fs.String("mode", "cheap", "cheap | score-run | variance")
	fixtures := fs.String("fixtures", "testdata/eval", "directory of labeled fixtures (cheap mode)")
	fixture := fs.String("fixture", "", "single fixture dir (score-run mode)")
	runArg := fs.String("run", "", "run id or run directory to score (score-run mode)")
	runsArg := fs.String("runs", "", "comma-separated run ids/dirs of the SAME repo (variance mode)")
	outDir := fs.String("out", "", "artifact base directory used to resolve --run/--runs (default ~/.diffmind/runs)")
	jsonOut := fs.String("json", "", "optional path to write the machine-readable report")
	minF1 := fs.Float64("min-f1", 0, "exit non-zero if any fixture's overall F1 is below this")
	minCoreUnion := fs.Float64("min-core-union", 0, "variance mode: exit non-zero if any objective's core/union is below this")
	workers := fs.Int("workers", 4, "AST build worker count (cheap mode)")
	minConfidence := fs.Float64("min-confidence", 0.7, "confidence threshold for the deterministic floor")
	verbose := fs.Bool("verbose", false, "enable debug logs")
	logFile := fs.String("log-file", "", "optional log file path")
	fs.Parse(args)
	configureLogging(*verbose, false, *logFile)

	cfg := config.Default()
	cfg.Quality.MinConfidence = *minConfidence
	cfg.Runtime.Workers = *workers

	if *mode == "variance" {
		runVarianceMode(*runsArg, *outDir, *jsonOut, *minCoreUnion)
		return
	}

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

// runVarianceMode loads K finished runs of the same repo and reports
// per-objective run-to-run stability. With --min-core-union it exits non-zero
// when any objective falls below the threshold (a CI stability gate).
func runVarianceMode(runsArg, outDir, jsonOut string, minCoreUnion float64) {
	ids := splitCSV(runsArg)
	if len(ids) < 2 {
		fmt.Fprintln(os.Stderr, "eval --mode variance requires --runs with at least 2 run ids/dirs")
		os.Exit(2)
	}
	exts := make([]eval.Extracted, 0, len(ids))
	validIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		dir := resolveRunDir(id, outDir)
		ext, err := eval.LoadRunArtifacts(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: load %s: %v\n", dir, err)
			os.Exit(1)
		}
		// Guard against incomplete/failed runs (no artifacts) which would
		// otherwise produce a misleading 0.00 variance against empty sets.
		if len(ext.Exposures)+len(ext.Dependencies)+len(ext.Connections) == 0 {
			fmt.Fprintf(os.Stderr, "eval: skipping %s — no artifacts (incomplete/failed run?)\n", id)
			continue
		}
		exts = append(exts, ext)
		validIDs = append(validIDs, id)
	}
	if len(exts) < 2 {
		fmt.Fprintf(os.Stderr, "eval: need at least 2 runs WITH artifacts for variance; got %d valid\n", len(exts))
		os.Exit(2)
	}
	rep := eval.Variance(exts, validIDs)
	eval.RenderVariance(os.Stdout, rep)
	if jsonOut != "" {
		f, err := os.Create(jsonOut)
		if err != nil {
			fmt.Fprintln(os.Stderr, "eval: write json:", err)
		} else {
			defer f.Close()
			if err := eval.WriteVarianceJSON(f, rep); err != nil {
				fmt.Fprintln(os.Stderr, "eval: encode json:", err)
			}
		}
	}
	failed := false
	if minCoreUnion > 0 {
		for _, o := range rep.Objectives {
			if o.CoreUnion < minCoreUnion {
				fmt.Fprintf(os.Stderr, "eval: objective %q core/union %.2f below --min-core-union %.2f\n",
					o.Objective, o.CoreUnion, minCoreUnion)
				failed = true
			}
		}
	}
	util.Info("cli.eval", "variance finished", map[string]any{"runs": len(exts)})
	if failed {
		os.Exit(1)
	}
}

// splitCSV splits a comma-separated list, trimming spaces and dropping empties.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
