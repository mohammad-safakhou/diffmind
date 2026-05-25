package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DockerIndexer runs the diffmind-indexer container image via the
// local Docker CLI. We deliberately shell out to `docker` rather than
// link the Docker engine library:
//
//  1. The Docker SDK has a heavy dependency tree (containerd, runc,
//     gRPC) we don't want to vendor into the diffmind binary.
//  2. CI environments and developer machines already have a working
//     `docker` CLI; we should not duplicate authentication, daemon
//     discovery, or volume-mount path translation.
//  3. The CLI's behaviour is stable across Docker versions; the SDK's
//     APIs change between minors.
//
// LIMITATIONS
//
//   - Requires `docker` in PATH. We may add a PodmanIndexer variant
//     later; the interface is already abstract enough.
//   - The host paths in RunRequest must be local-filesystem paths
//     reachable by the Docker daemon. Remote Docker daemons (DOCKER_HOST)
//     would need the snapshot copied to the daemon's host first; we
//     reject that with a clear error.
type DockerIndexer struct {
	// DockerPath is the binary name or absolute path. Defaults to "docker".
	// Useful for tests (set to a mock binary) or for using Podman via
	// its docker shim ("/usr/bin/podman").
	DockerPath string

	// Stderr is where we tee the container's stderr in real time so the
	// caller can show progress to a human. May be nil; in that case
	// stderr is captured to memory only.
	Stderr io.Writer

	// Stdout is where we tee the container's stdout in real time. The
	// JSON report appears here as the last line; we also capture it
	// for parsing. May be nil.
	Stdout io.Writer
}

// NewDockerIndexer constructs an indexer using the `docker` binary on
// PATH. Set DockerPath manually for non-standard installs.
func NewDockerIndexer() *DockerIndexer {
	return &DockerIndexer{DockerPath: "docker"}
}

// Index implements Indexer. Steps:
//
//  1. Validate the request (paths exist, image resolves).
//  2. Pull the image if PullPolicy says so.
//  3. Build the `docker run` command line.
//  4. Run the container with stdout/stderr captured and optionally teed.
//  5. Parse the JSON report from stdout (we find it by looking for the
//     last line that starts with "{").
//  6. Map container paths back to host paths and return the result.
func (d *DockerIndexer) Index(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	image := ResolveImage(req.Image)

	if err := d.pull(ctx, image, req.PullPolicy); err != nil {
		return nil, fmt.Errorf("pull image: %w", err)
	}

	args := d.buildRunArgs(req, image)
	cmd := exec.CommandContext(ctx, d.dockerPath(), args...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = mergeWriters(&stdoutBuf, d.Stdout)
	cmd.Stderr = mergeWriters(&stderrBuf, d.Stderr)

	runErr := cmd.Run()

	report, parseErr := parseReport(stdoutBuf.Bytes())
	exitCode := 0
	if runErr != nil {
		// Distinguish exec-not-found / context-cancellation from
		// container-non-zero-exit. The first two are immediate fatals;
		// the third we still want to return the parsed report for so
		// callers see per-language outcomes.
		var ee *exec.ExitError
		switch {
		case errors.As(runErr, &ee):
			exitCode = ee.ExitCode()
		case errors.Is(ctx.Err(), context.Canceled), errors.Is(ctx.Err(), context.DeadlineExceeded):
			return nil, fmt.Errorf("indexer cancelled: %w", ctx.Err())
		default:
			return nil, fmt.Errorf("docker run: %w (stderr: %s)", runErr, tailString(stderrBuf.Bytes(), 4096))
		}
	}

	if report == nil {
		// No report on stdout: container died before emitting JSON.
		// Surface whatever stderr we have.
		return &RunResult{
			HostStdout:        stdoutBuf.Bytes(),
			HostStderr:        stderrBuf.Bytes(),
			ContainerExitCode: exitCode,
		}, fmt.Errorf("indexer produced no status report (exit=%d, stderr tail=%q): %w",
			exitCode, tailString(stderrBuf.Bytes(), 2048), parseErr)
	}

	if report.SchemaVersion != ReportSchemaVersion {
		return nil, fmt.Errorf("indexer report schema version mismatch: got %d, expected %d (stale image?)",
			report.SchemaVersion, ReportSchemaVersion)
	}

	// Map container-side paths back to host paths. The wrapper writes
	// IndexPath as /output/index.scip; we remap /output to OutputPath.
	if report.IndexPath != "" {
		report.IndexPath = remapPath(report.IndexPath, "/output", req.OutputPath)
	}
	for i := range report.Languages {
		if report.Languages[i].IndexPath != "" {
			report.Languages[i].IndexPath = remapPath(report.Languages[i].IndexPath, "/output", req.OutputPath)
		}
	}

	report.HostStdout = stdoutBuf.Bytes()
	report.HostStderr = stderrBuf.Bytes()
	report.ContainerExitCode = exitCode

	if exitCode != 0 {
		return report, fmt.Errorf("indexer container exited with code %d", exitCode)
	}
	return report, nil
}

// dockerPath returns the configured Docker binary, defaulting to "docker".
func (d *DockerIndexer) dockerPath() string {
	if d.DockerPath != "" {
		return d.DockerPath
	}
	return "docker"
}

// buildRunArgs assembles the `docker run` command line.
//
// PERMISSIONS
//   - --user 0:0    we run as root inside the container so indexers
//                   that create files in /root/.m2 (Maven cache),
//                   /root/.gradle, etc. don't run into chown errors.
//                   The container is ephemeral; host files are only
//                   touched via the explicit volume mounts.
//   - The snapshot mount is :ro to enforce read-only access from the
//                   container side, defense-in-depth on top of the
//                   snapshot directory itself being a copy.
//
// MOUNTS
//   - SourcePath →   /sources:ro
//   - OutputPath →   /output (rw)
//   - ExtraMounts →  user-defined
func (d *DockerIndexer) buildRunArgs(req RunRequest, image string) []string {
	args := []string{
		"run",
		"--rm",
		"--user", "0:0",
		"--init", // PID 1 reaper so timed-out subprocesses don't zombie
	}

	if req.NetworkMode != "" {
		args = append(args, "--network", req.NetworkMode)
	}

	args = append(args,
		"-v", fmt.Sprintf("%s:/sources:ro", req.SourcePath),
		"-v", fmt.Sprintf("%s:/output", req.OutputPath),
	)

	for host, container := range req.ExtraMounts {
		args = append(args, "-v", fmt.Sprintf("%s:%s", host, container))
	}

	args = append(args, "-e", "DIFFMIND_INDEXER_OUTPUT=/output")
	for k, v := range req.ExtraEnv {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	args = append(args, image)

	args = append(args,
		"--source", "/sources",
		"--output", "/output/index.scip",
	)
	if len(req.Languages) > 0 {
		args = append(args, "--languages", strings.Join(req.Languages, ","))
	}
	if req.PerIndexerTimeout > 0 {
		args = append(args, "--timeout", req.PerIndexerTimeout.String())
	}
	if req.Parallel > 0 {
		args = append(args, "--parallel", fmt.Sprintf("%d", req.Parallel))
	}
	if req.KeepWork {
		args = append(args, "--keep-work")
	}
	return args
}

// pull invokes `docker pull` according to the configured policy.
// On PullNever we don't even shell out; on PullIfAbsent we check
// `docker image inspect` first.
func (d *DockerIndexer) pull(ctx context.Context, image string, policy PullPolicy) error {
	switch policy {
	case PullNever:
		return nil
	case "", PullIfAbsent:
		if d.imagePresent(ctx, image) {
			return nil
		}
	case PullAlways:
		// fall through
	default:
		return fmt.Errorf("unknown pull policy %q", policy)
	}
	cmd := exec.CommandContext(ctx, d.dockerPath(), "pull", image)
	cmd.Stdout = d.Stdout
	cmd.Stderr = d.Stderr
	return cmd.Run()
}

// imagePresent uses `docker image inspect` as a presence check.
func (d *DockerIndexer) imagePresent(ctx context.Context, image string) bool {
	cmd := exec.CommandContext(ctx, d.dockerPath(), "image", "inspect", image)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

// ResolveImage returns the image reference to use for a run. Precedence:
//   1. Explicit RunRequest.Image (when non-empty).
//   2. DIFFMIND_INDEXER_IMAGE env var.
//   3. DefaultImage.
func ResolveImage(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if envImg := os.Getenv("DIFFMIND_INDEXER_IMAGE"); envImg != "" {
		return envImg
	}
	return DefaultImage
}

// validateRequest checks paths exist and are absolute.
func validateRequest(req RunRequest) error {
	if req.SourcePath == "" {
		return errors.New("source path is required")
	}
	if req.OutputPath == "" {
		return errors.New("output path is required")
	}
	if !filepath.IsAbs(req.SourcePath) {
		return fmt.Errorf("source path must be absolute: %q", req.SourcePath)
	}
	if !filepath.IsAbs(req.OutputPath) {
		return fmt.Errorf("output path must be absolute: %q", req.OutputPath)
	}
	if st, err := os.Stat(req.SourcePath); err != nil || !st.IsDir() {
		return fmt.Errorf("source path is not a directory: %q", req.SourcePath)
	}
	if err := os.MkdirAll(req.OutputPath, 0o755); err != nil {
		return fmt.Errorf("output path: %w", err)
	}
	return nil
}

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
//   = "/host/runs/X/index.scip"
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

// mergeWriters returns a writer that fans out to both targets. If one
// is nil, the other is returned directly to avoid allocation overhead
// on the hot stdout/stderr path.
func mergeWriters(primary, tee io.Writer) io.Writer {
	if tee == nil {
		return primary
	}
	if primary == nil {
		return tee
	}
	return io.MultiWriter(primary, tee)
}
