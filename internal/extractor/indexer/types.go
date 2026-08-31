// Package indexer is the diffmind-side driver for the
// `diffmind-indexer` container image. It detects the image, runs it
// against a source tree (via Docker), and deserializes the resulting
// status report.
//
// The package is deliberately thin: most of the orchestration lives in
// the wrapper binary inside the image (cmd/diffmind-index). This
// package only handles container invocation, error mapping, and the
// JSON contract between host and container.
//
// The contract is described in cmd/diffmind-index/report.go. Any
// breaking change to that contract requires bumping ReportSchemaVersion
// on both sides simultaneously.
package indexer

import (
	"context"
	"time"
)

// ReportSchemaVersion is the version this package expects in the JSON
// report produced by the wrapper. It must match the value in
// cmd/diffmind-index/report.go (reportSchemaVersion). A mismatch
// indicates a stale image and the run is aborted.
const ReportSchemaVersion = 1

// RunRequest is the diffmind-side description of an indexing run.
// It maps 1:1 to wrapper CLI flags.
type RunRequest struct {
	// SourcePath is an absolute host path to the source directory.
	// The runner mounts this read-only at /sources inside the container.
	SourcePath string

	// OutputPath is an absolute host path to a directory where the
	// runner expects the merged index.scip and index_status.json to
	// land. Must exist and be writable. Mounted at /output.
	OutputPath string

	// Languages is the list of languages to index. Use "auto" (or
	// leave empty) to auto-detect from the source tree. Validation is
	// the wrapper's job; this package passes the value through verbatim.
	Languages []string

	// PerIndexerTimeout bounds the wall-clock time of every individual
	// indexer process inside the container. Defaults to 30 minutes
	// when zero. Total wall-clock for the run is roughly
	// (per-indexer timeout) * (active indexers / Parallel).
	PerIndexerTimeout time.Duration

	// Parallel is how many indexers the wrapper runs concurrently.
	// Defaults to 4. Larger values speed up multi-language runs at the
	// cost of more memory inside the container.
	Parallel int

	// KeepWork instructs the wrapper to preserve per-indexer
	// intermediate files. Useful when debugging a partial-success run.
	KeepWork bool

	// Image is the fully-qualified container image reference. Defaults
	// to the value in DefaultImage when empty.
	Image string

	// PullPolicy controls how the runtime fetches the image:
	//   - PullAlways:   pull on every run (slow, always current)
	//   - PullIfAbsent: pull only when not cached locally (default)
	//   - PullNever:    fail if not cached; useful in air-gapped CI
	PullPolicy PullPolicy

	// NetworkMode is the Docker network mode for the container.
	// "host" lets the indexers fetch deps from the host network without
	// proxying; "bridge" (default) gives them the standard NAT'd
	// bridge. For air-gapped environments, set this to "none" — most
	// indexers will fail to resolve external types but will still
	// produce an intra-project index.
	NetworkMode string

	// ExtraEnv is appended verbatim to the container's environment.
	// Useful for passing Maven settings, npm registry URLs, etc.
	ExtraEnv map[string]string

	// ExtraMounts lets the caller mount additional host paths into the
	// container as read-only volumes. Map key is the host path, value
	// is the container path.
	//
	// COMMON USE CASES
	//   - Maven settings.xml in a private repo:
	//       host=/root/.m2 container=/root/.m2:ro
	//   - npm registry config:
	//       host=~/.npmrc container=/root/.npmrc:ro
	ExtraMounts map[string]string
}

// PullPolicy is an enum-style string that controls image pulling.
// Modeled as a string for readability in flags / config files.
type PullPolicy string

const (
	PullAlways   PullPolicy = "always"
	PullIfAbsent PullPolicy = "if-absent"
	PullNever    PullPolicy = "never"
)

// DefaultImage is the image tag diffmind targets when the caller
// hasn't set one explicitly.
//
// Defaults to a LOCALLY-BUILT tag rather than a GHCR pull because:
//
//  1. We want a fresh checkout to run end-to-end with no manual
//     `docker pull` step. The embed-and-auto-build path in
//     internal/indexer/build.go produces this tag.
//  2. The GHCR registry has no public image published yet. Pointing
//     the default at it would force every cold run to fail at
//     `docker pull` before ever attempting the local build.
//
// CI / release builds override this via -ldflags to point at the
// versioned ghcr.io/anomalyco/diffmind-indexer:<semver> image once
// it exists. To override at runtime, set RunRequest.Image or the
// DIFFMIND_INDEXER_IMAGE environment variable; ResolveImage() reads
// both.
var DefaultImage = "diffmind-indexer:dev"

// RunResult is the parsed JSON report returned by the wrapper plus
// metadata captured at the diffmind boundary.
type RunResult struct {
	// SchemaVersion echoes the wrapper's report schema version.
	SchemaVersion int

	// IndexPath is the host path to the merged SCIP index. Derived
	// from the wrapper's container-side path by remapping /output
	// back to RunRequest.OutputPath.
	IndexPath string

	// IndexBytes is the size of the merged SCIP index on disk.
	IndexBytes int64

	// DurationMs is the wall-clock time the wrapper reported.
	DurationMs int64

	// StartedAt and FinishedAt come from the wrapper.
	StartedAt  time.Time
	FinishedAt time.Time

	// DetectedLanguages is the auto-detected language list (empty if
	// languages were specified explicitly).
	DetectedLanguages []string

	// Languages is the per-language outcome list.
	Languages []LanguageResult

	// Warnings is the wrapper's warnings list. DiffMind surfaces these
	// in the run manifest's `warnings` array.
	Warnings []string

	// HostStdout is the raw bytes the wrapper wrote to stdout. Kept
	// for replay and debugging; redundant with the parsed fields above.
	HostStdout []byte

	// HostStderr is the wrapper's stderr (and container engine output).
	// On failure this is the most useful field for triage.
	HostStderr []byte

	// ContainerExitCode is the wrapper process exit code. Zero means
	// at least one indexer succeeded and the merged index was written.
	ContainerExitCode int
}

// LanguageResult mirrors the per-language portion of the wrapper's
// JSON report. Fields are documented in cmd/diffmind-index/report.go.
type LanguageResult struct {
	Name        string `json:"name"`
	Indexer     string `json:"indexer"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	Error       string `json:"error"`
	IndexPath   string `json:"index_path"`
	Files       int    `json:"files"`
	Occurrences int    `json:"occurrences"`
	DurationMs  int64  `json:"duration_ms"`
}

// Indexer is the host-side interface for invoking an indexing run.
// We have one production implementation (DockerIndexer) and a fake
// for tests.
type Indexer interface {
	// Index runs an indexing pass against the given request and
	// returns the parsed result. The error is non-nil if the wrapper
	// itself failed catastrophically (could not start the container,
	// container exited non-zero, JSON report malformed). Partial
	// per-language failures show up in the result's Languages entries
	// instead — the returned error is nil in that case.
	Index(ctx context.Context, req RunRequest) (*RunResult, error)
}
