package main

import "time"

// Report is the JSON shape written to stdout (and persisted to
// <output_dir>/index_status.json) by the wrapper at the end of every
// run. It is the contract between the indexer image and the DiffMind
// host process — both sides MUST keep this stable across image versions.
//
// Backwards-compatible changes (new fields) are fine. Renames and
// removals require bumping IndexerWrapperVersion below and coordinating
// with internal/indexer/result.go in the diffmind repo.
type Report struct {
	// SchemaVersion is bumped whenever a breaking change is made to
	// this struct. The host parser refuses to deserialize an unknown
	// version, so this is the canary.
	SchemaVersion int `json:"schema_version"`

	// IndexPath is the absolute path inside the container at which the
	// merged index.scip was written. Callers should resolve this to a
	// host path via their volume mount mapping.
	IndexPath string `json:"index_path"`

	// IndexBytes is the size of the merged index in bytes. Useful for
	// quickly identifying empty / suspiciously small indexes.
	IndexBytes int64 `json:"index_bytes"`

	// DurationMs is the total wall-clock duration of the wrapper run
	// from flag parsing to merge completion, in milliseconds.
	DurationMs int64 `json:"duration_ms"`

	// StartedAt and FinishedAt bracket the run in UTC. Round-tripping
	// these through JSON uses RFC3339 with nanosecond precision.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`

	// DetectedLanguages is the canonical list of languages the wrapper
	// inferred from the source tree (only populated when --languages=auto).
	// When the user passes an explicit list, this is empty.
	DetectedLanguages []string `json:"detected_languages,omitempty"`

	// Languages lists per-indexer outcomes in stable order.
	Languages []LanguageResult `json:"languages"`

	// Warnings contains soft errors that did not prevent the run from
	// producing an index. The DiffMind host surfaces these in the run
	// manifest as informational diagnostics.
	Warnings []string `json:"warnings,omitempty"`
}

// LanguageResult records what happened for a single (indexer, language)
// pair. We emit one entry per requested language even when multiple
// languages are handled by the same indexer (Java/Scala/Kotlin via
// scip-java) — this keeps the report's audit trail explicit per language.
type LanguageResult struct {
	// Name is the canonical language identifier (lowercased), e.g. "java".
	Name string `json:"name"`

	// Indexer is the binary name that produced this result, e.g.
	// "scip-java". Useful for debugging which tool actually ran.
	Indexer string `json:"indexer,omitempty"`

	// Status is one of:
	//   - "ok":      the indexer completed and produced an index file
	//   - "skipped": no source files of this language were found (auto
	//                detection mode), or the build tool needed by the
	//                indexer is not supported on this snapshot
	//   - "failed":  the indexer ran but returned a non-zero exit code,
	//                or produced an empty/invalid index
	Status string `json:"status"`

	// Reason is a short human-readable explanation for skipped/failed.
	// Empty when Status == "ok".
	Reason string `json:"reason,omitempty"`

	// Error is the trimmed stderr (last ~4 KB) emitted by the indexer
	// process when Status == "failed". Useful for triage without having
	// to re-run with --keep-work.
	Error string `json:"error,omitempty"`

	// IndexPath is the absolute path inside the container to the
	// per-language SCIP index file (before merge). Present when
	// Status == "ok". The merged index supersedes these, but we keep
	// them around for debugging when --keep-work is set.
	IndexPath string `json:"index_path,omitempty"`

	// Files is the number of source files processed by the indexer.
	// Reported as 0 when the indexer doesn't expose this counter.
	Files int `json:"files,omitempty"`

	// Occurrences is the number of SCIP occurrences in the per-language
	// index. A useful sanity check: a Java project with thousands of
	// classes should produce hundreds of thousands of occurrences. A
	// near-zero count almost always means the build failed silently.
	Occurrences int `json:"occurrences,omitempty"`

	// DurationMs is the wall-clock time the indexer process took.
	DurationMs int64 `json:"duration_ms"`
}

// reportSchemaVersion is the value embedded in every Report we emit.
// Bump this when a non-backwards-compatible change lands.
const reportSchemaVersion = 1
