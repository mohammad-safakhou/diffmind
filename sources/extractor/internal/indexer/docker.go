package indexer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
