package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// orchestratorConfig is the fully-resolved configuration the
// orchestrator runs against. Constructed from CLI flags + environment
// in main.run(), then frozen.
//
// All paths are absolute. Empty Languages or a single "auto" entry
// means "detect from the source tree".
type orchestratorConfig struct {
	Source    string        // /sources mount (read-only)
	Output    string        // /output/index.scip
	Workdir   string        // /output/work
	Languages []string      // ["auto"] or explicit list
	Timeout   time.Duration // per-indexer wall-clock limit
	Parallel  int           // semaphore size
	KeepWork  bool          // skip cleanup
}

// orchestrate runs the wrapper end-to-end:
//  1. Resolve which languages to index (auto-detect or explicit).
//  2. Dispatch each indexer in parallel up to cfg.Parallel.
//  3. Wait for everything, collect per-language results.
//  4. Merge successful per-language indexes into cfg.Output.
//  5. Build and return the Report.
//
// Returns the Report even on partial failure so the caller can still
// write it. The error is non-nil only if NO index could be produced —
// i.e. every applicable indexer failed or no language was detected.
func orchestrate(cfg orchestratorConfig) (*Report, error) {
	start := time.Now().UTC()

	// ----- 1. Resolve language list -----
	wantedLangs, detected, err := resolveLanguages(cfg)
	if err != nil {
		return nil, err
	}
	if len(wantedLangs) == 0 {
		return &Report{
			SchemaVersion:     reportSchemaVersion,
			IndexPath:         cfg.Output,
			DurationMs:        time.Since(start).Milliseconds(),
			StartedAt:         start,
			FinishedAt:        time.Now().UTC(),
			DetectedLanguages: detected,
			Languages:         nil,
			Warnings:          []string{"no languages detected in source tree"},
		}, fmt.Errorf("no supported languages detected under %s", cfg.Source)
	}

	logf("detected languages: %v", langStrings(wantedLangs))

	// ----- 2. Group by indexer -----
	// Several languages map to the same indexer (Java/Scala/Kotlin → scip-java).
	// We want to invoke each indexer at most once per run, with the union
	// of relevant languages passed through.
	groups := groupByIndexer(wantedLangs)

	// ----- 3. Run indexers in parallel -----
	type jobResult struct {
		group  indexerGroup
		result []LanguageResult
	}

	results := make([]jobResult, len(groups))
	sem := make(chan struct{}, max(1, cfg.Parallel))
	var wg sync.WaitGroup

	for i, g := range groups {
		i, g := i, g
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
			defer cancel()

			res := g.Indexer.Run(ctx, indexerInput{
				Source:    cfg.Source,
				Workdir:   filepath.Join(cfg.Workdir, g.Indexer.Name()),
				Languages: g.Languages,
			})
			results[i] = jobResult{group: g, result: res}
		}()
	}
	wg.Wait()

	// ----- 4. Flatten + sort per-language results -----
	var allLangResults []LanguageResult
	for _, r := range results {
		allLangResults = append(allLangResults, r.result...)
	}
	sort.SliceStable(allLangResults, func(i, j int) bool {
		return allLangResults[i].Name < allLangResults[j].Name
	})

	// ----- 5. Merge successful per-language indexes -----
	var successPaths []string
	for _, r := range allLangResults {
		if r.Status == "ok" && r.IndexPath != "" {
			successPaths = append(successPaths, r.IndexPath)
		}
	}

	var mergeErr error
	var indexBytes int64
	if len(successPaths) == 0 {
		mergeErr = fmt.Errorf("all indexers failed; no per-language indexes to merge")
	} else {
		if err := mergeIndexes(successPaths, cfg.Output); err != nil {
			mergeErr = fmt.Errorf("merge: %w", err)
		} else if st, err := os.Stat(cfg.Output); err == nil {
			indexBytes = st.Size()
		}
	}

	// ----- 6. Cleanup -----
	if !cfg.KeepWork {
		if err := os.RemoveAll(cfg.Workdir); err != nil {
			logf("workdir cleanup failed: %v", err)
		}
	}

	report := &Report{
		SchemaVersion:     reportSchemaVersion,
		IndexPath:         cfg.Output,
		IndexBytes:        indexBytes,
		DurationMs:        time.Since(start).Milliseconds(),
		StartedAt:         start,
		FinishedAt:        time.Now().UTC(),
		DetectedLanguages: detected,
		Languages:         allLangResults,
	}
	if mergeErr != nil {
		report.Warnings = append(report.Warnings, mergeErr.Error())
		return report, mergeErr
	}
	return report, nil
}

// resolveLanguages turns the user-supplied --languages flag into a
// concrete list of canonical language tokens. When the user passes
// "auto", we detect by walking cfg.Source.
//
// Returns the wanted languages, the detected list (empty unless auto
// was used), and a config error for unknown explicit languages.
func resolveLanguages(cfg orchestratorConfig) (wanted []language, detected []string, err error) {
	if len(cfg.Languages) == 1 && cfg.Languages[0] == "auto" {
		found := detectLanguages(cfg.Source)
		for _, l := range found {
			detected = append(detected, string(l))
		}
		return found, detected, nil
	}
	for _, raw := range cfg.Languages {
		lang := validateLanguage(raw)
		if lang == "" {
			return nil, nil, fmt.Errorf("%w: unknown language %q", errConfig, raw)
		}
		wanted = append(wanted, lang)
	}
	return wanted, nil, nil
}

// langStrings is a small helper to render []language as []string for
// log messages. Avoids a noisy `%v` slice formatting that wraps the
// terminal.
func langStrings(in []language) []string {
	out := make([]string, len(in))
	for i, l := range in {
		out[i] = string(l)
	}
	return out
}

// max is included so the file builds on Go versions before 1.21 where
// the builtin doesn't exist. Inlined to avoid an import dance.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// logf writes a timestamped line to stderr. Used for human-readable
// progress; the structured Report on stdout is the machine contract.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[diffmind-index] "+time.Now().UTC().Format("15:04:05.000")+" "+format+"\n", args...)
}
