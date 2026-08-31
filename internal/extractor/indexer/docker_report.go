package indexer

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// parseReport scans stdout for the JSON object the wrapper emits at
// the end of the run. The wrapper writes indented JSON (via
// json.MarshalIndent), and the outer `{` always appears at column 0;
// inner `{` from nested objects appear indented. We exploit that to
// find the start of the LAST top-level JSON object even if the stdout
// contains earlier log noise.
//
// Algorithm:
//  1. Walk lines from the end backwards looking for the LAST line that
//     is exactly "}" (or "}\n") — the matching outer close-brace.
//  2. From there, scan backwards for the matching "{" at column 0
//     (un-indented).
//  3. Try to unmarshal the substring between them.
//
// Falls back to a per-line scan if step 1 doesn't find a closing brace
// (for single-line JSON variants emitted by other tooling).
func parseReport(stdout []byte) (*RunResult, error) {
	lines := bytes.Split(stdout, []byte{'\n'})

	// Walk backwards for the closing brace at column 0.
	closeIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := bytes.TrimRight(lines[i], "\r")
		// A closing brace alone at column 0.
		if bytes.Equal(trimmed, []byte("}")) {
			closeIdx = i
			break
		}
	}

	type rawReport struct {
		SchemaVersion     int              `json:"schema_version"`
		IndexPath         string           `json:"index_path"`
		IndexBytes        int64            `json:"index_bytes"`
		DurationMs        int64            `json:"duration_ms"`
		StartedAt         time.Time        `json:"started_at"`
		FinishedAt        time.Time        `json:"finished_at"`
		DetectedLanguages []string         `json:"detected_languages"`
		Languages         []LanguageResult `json:"languages"`
		Warnings          []string         `json:"warnings"`
	}

	if closeIdx >= 0 {
		// Find the matching open brace at column 0 above closeIdx.
		openIdx := -1
		for i := closeIdx - 1; i >= 0; i-- {
			trimmed := bytes.TrimRight(lines[i], "\r")
			// First line that starts with "{" at column 0.
			if len(trimmed) > 0 && trimmed[0] == '{' {
				openIdx = i
				break
			}
		}
		if openIdx >= 0 {
			candidate := bytes.Join(lines[openIdx:closeIdx+1], []byte{'\n'})
			var raw rawReport
			if err := json.Unmarshal(candidate, &raw); err == nil {
				return makeRunResult(raw), nil
			}
		}
	}

	// Fallback: single-line JSON. Walk lines from the end and try each
	// one that starts with "{".
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var raw rawReport
		if err := json.Unmarshal(line, &raw); err == nil {
			return makeRunResult(raw), nil
		}
	}

	return nil, errors.New("no JSON report found in stdout")
}

// makeRunResult builds the RunResult from an unmarshalled raw report.
// Pulled out so both parse paths (multi-line and single-line) share
// the construction logic.
func makeRunResult(raw struct {
	SchemaVersion     int              `json:"schema_version"`
	IndexPath         string           `json:"index_path"`
	IndexBytes        int64            `json:"index_bytes"`
	DurationMs        int64            `json:"duration_ms"`
	StartedAt         time.Time        `json:"started_at"`
	FinishedAt        time.Time        `json:"finished_at"`
	DetectedLanguages []string         `json:"detected_languages"`
	Languages         []LanguageResult `json:"languages"`
	Warnings          []string         `json:"warnings"`
}) *RunResult {
	return &RunResult{
		SchemaVersion:     raw.SchemaVersion,
		IndexPath:         raw.IndexPath,
		IndexBytes:        raw.IndexBytes,
		DurationMs:        raw.DurationMs,
		StartedAt:         raw.StartedAt,
		FinishedAt:        raw.FinishedAt,
		DetectedLanguages: raw.DetectedLanguages,
		Languages:         raw.Languages,
		Warnings:          raw.Warnings,
	}
}

// remapPath translates a path emitted by the container (containerPath)
// from the container-side prefix to the equivalent host-side path.
//
// Example: remapPath("/output/index.scip", "/output", "/host/runs/X")
//
//	= "/host/runs/X/index.scip"
//
// If containerPath doesn't start with prefix we return it unchanged
// (this preserves any absolute paths the wrapper might emit for diag
// purposes).
func remapPath(containerPath, prefix, hostPrefix string) string {
	if !strings.HasPrefix(containerPath, prefix) {
		return containerPath
	}
	rest := strings.TrimPrefix(containerPath, prefix)
	return filepath.Join(hostPrefix, rest)
}

// tailString returns the trailing n bytes of b as a string, useful
// for embedding stderr tails into error messages without bloating them.
func tailString(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}
