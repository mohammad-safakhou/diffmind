// Command diffmind-index is the entrypoint of the
// `ghcr.io/anomalyco/diffmind-indexer` container image.
//
// It orchestrates per-language SCIP indexers (scip-java, scip-typescript,
// scip-python, scip-go, scip-ruby, scip-clang) over a source tree mounted
// at --source, then merges the per-language outputs into a single
// index.scip and writes a JSON status report on stdout.
//
// USAGE
//
//	diffmind-index --source DIR --output FILE [--languages LIST] [--workdir DIR]
//
// FLAGS
//
//	--source DIR        Source root to index. Required.
//	                    Typically /sources inside the container, which the
//	                    caller mounts read-only from the DiffMind snapshot.
//	--output FILE       Path where the merged SCIP index will be written.
//	                    Defaults to $DIFFMIND_INDEXER_OUTPUT/index.scip.
//	--languages LIST    Comma-separated list of languages to index. Use
//	                    "auto" (default) to detect from the source tree.
//	                    Valid values: java, scala, kotlin, typescript,
//	                    javascript, python, go, ruby, cpp, c.
//	--workdir DIR       Working directory for per-indexer intermediate
//	                    files (per-language index.scip, build caches).
//	                    Defaults to $DIFFMIND_INDEXER_OUTPUT/work.
//	--timeout DURATION  Per-indexer timeout. Default 30m.
//	--parallel N        How many indexers to run concurrently. Default 4.
//	--keep-work         Don't delete intermediate files on exit. Useful
//	                    for debugging indexer failures.
//
// EXIT CODES
//
//	0   At least one indexer succeeded and the merged index was written.
//	1   All applicable indexers failed, or no language was detected.
//	2   Configuration error (bad flags, missing source dir, etc.).
//
// STATUS REPORT
//
// On stdout, the wrapper writes a JSON object with shape:
//
//	{
//	  "index_path": "/output/index.scip",
//	  "duration_ms": 123456,
//	  "languages": [
//	    {"name": "java", "status": "ok", "index_path": "...", "files": 1234, "duration_ms": 50000},
//	    {"name": "typescript", "status": "skipped", "reason": "no .ts files found"},
//	    {"name": "python", "status": "failed", "error": "scip-python: ..."}
//	  ]
//	}
//
// The report is also written to <output_dir>/index_status.json for
// archival by the runner. Stderr is reserved for human-readable logs.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errConfig) {
			fmt.Fprintln(os.Stderr, "config error:", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "indexer error:", err)
		os.Exit(1)
	}
}

// errConfig is returned for malformed flags / environment.
// It is mapped to exit code 2 to distinguish operator errors from
// indexer execution failures.
var errConfig = errors.New("configuration error")

// run parses flags and dispatches to the orchestrator. It returns
// errConfig for bad inputs and other errors for runtime failures.
//
// We split this out of main() so tests can call it without poking
// os.Args / os.Exit.
func run(args []string) error {
	fs := flag.NewFlagSet("diffmind-index", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		source    string
		output    string
		languages string
		workdir   string
		timeout   time.Duration
		parallel  int
		keepWork  bool
		showVer   bool
	)

	fs.StringVar(&source, "source", "/sources", "Source root to index (mounted from the snapshot)")
	fs.StringVar(&output, "output", "", "Output path for the merged index.scip (default $DIFFMIND_INDEXER_OUTPUT/index.scip)")
	fs.StringVar(&languages, "languages", "auto", "Comma-separated languages or 'auto'")
	fs.StringVar(&workdir, "workdir", "", "Working directory for intermediate files (default $DIFFMIND_INDEXER_OUTPUT/work)")
	fs.DurationVar(&timeout, "timeout", 30*time.Minute, "Per-indexer timeout")
	fs.IntVar(&parallel, "parallel", 4, "Max number of indexers to run concurrently")
	fs.BoolVar(&keepWork, "keep-work", false, "Don't delete the workdir on exit")
	fs.BoolVar(&showVer, "version", false, "Print version and exit")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errConfig, err)
	}

	if showVer {
		fmt.Println(version)
		return nil
	}

	if source == "" {
		return fmt.Errorf("%w: --source is required", errConfig)
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("%w: source %q: %v", errConfig, source, err)
	}

	if output == "" {
		if envOut := os.Getenv("DIFFMIND_INDEXER_OUTPUT"); envOut != "" {
			output = filepath.Join(envOut, "index.scip")
		} else {
			return fmt.Errorf("%w: --output is required (or set DIFFMIND_INDEXER_OUTPUT)", errConfig)
		}
	}
	if workdir == "" {
		base := filepath.Dir(output)
		workdir = filepath.Join(base, "work")
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	cfg := orchestratorConfig{
		Source:    source,
		Output:    output,
		Workdir:   workdir,
		Languages: parseLanguages(languages),
		Timeout:   timeout,
		Parallel:  parallel,
		KeepWork:  keepWork,
	}

	report, err := orchestrate(cfg)
	// Always emit the status report, even if orchestration failed
	// halfway through. The caller (DiffMind) uses it to attribute
	// per-language successes/failures.
	if report != nil {
		writeReport(report, output)
	}
	return err
}

// parseLanguages parses the --languages CLI flag. Accepts "auto",
// "all", or a comma-separated list. Unknown tokens are passed through
// and rejected later by validateLanguage.
func parseLanguages(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "auto" {
		return []string{"auto"}
	}
	if raw == "all" {
		return allLanguages()
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// writeReport persists the JSON status report to two destinations:
//   - <output_dir>/index_status.json: machine-readable record of the
//     run, used by DiffMind's index stage to surface per-language
//     successes / failures in the run manifest.
//   - stdout: same JSON, so callers shelling out to `docker run` get
//     a structured result without having to mount the output dir.
//
// Errors writing the file are logged to stderr but not returned: the
// stdout copy is the authoritative one for the caller.
func writeReport(r *Report, indexPath string) {
	enc, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "report encode:", err)
		return
	}
	// Stdout: machine-readable
	os.Stdout.Write(enc)
	os.Stdout.Write([]byte{'\n'})

	statusPath := filepath.Join(filepath.Dir(indexPath), "index_status.json")
	if err := os.WriteFile(statusPath, enc, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "report write:", err)
	}
}
