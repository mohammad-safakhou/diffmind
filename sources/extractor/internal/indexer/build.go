package indexer

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/indexerbuild"
)

// Builder builds the diffmind-indexer Docker image from the build
// context embedded into the diffmind binary. It runs `docker build`
// against an extracted copy of indexerbuild.Context.
//
// First call (cold cache) takes ~20-30 minutes — almost all of it is
// JDK / Maven / Node / .NET SDK / Go toolchain provisioning inside the
// Docker layers. Subsequent calls return immediately if the image is
// already present (governed by the AutoBuild policy below).
//
// # THREAD SAFETY
//
// Concurrent EnsureImage calls for the same tag are serialised through
// the in-process singleflight registry below. Two parallel pipelines
// asking for the same image will share one underlying docker build.
type Builder struct {
	// DockerPath is the binary name or absolute path. Defaults to "docker".
	DockerPath string

	// Stderr / Stdout receive the docker subprocess output during
	// build. Both may be nil; the builder also captures stderr to
	// memory for inclusion in BuildResult.LogTail regardless.
	Stderr io.Writer
	Stdout io.Writer

	// BuildContextRoot is the parent directory under which extracted
	// build contexts are cached. Defaults to
	// ~/.diffmind/indexer-build-context/. Each extraction lives in a
	// content-addressed subdirectory so a stale extraction never
	// collides with a fresh embed.
	BuildContextRoot string
}

// AutoBuildPolicy controls when Builder.EnsureImage triggers a build.
//
//	"missing" — build only if `docker image inspect <tag>` says it's
//	            not present. This is the default.
//	"always"  — always rebuild. Useful when iterating on Dockerfile.indexer.
//	"never"   — never auto-build. If the image is missing, fail-soft and
//	            let the caller decide what to do.
type AutoBuildPolicy string

const (
	AutoBuildMissing AutoBuildPolicy = "missing"
	AutoBuildAlways  AutoBuildPolicy = "always"
	AutoBuildNever   AutoBuildPolicy = "never"
)

// BuildResult is the outcome of a build attempt. The fields are
// populated for both success and failure to make logging consistent.
type BuildResult struct {
	// Built reports whether docker build was actually invoked. False
	// when the image was already present and AutoBuild policy didn't
	// force a rebuild.
	Built bool

	// Image is the tag the builder targeted (resolved via ResolveImage).
	Image string

	// ContextDir is the on-disk directory that held the build context
	// the docker daemon read from. Useful for debugging.
	ContextDir string

	// LogTail is the last ~64 KB of build output (stdout + stderr
	// interleaved). Captured even when teed elsewhere.
	LogTail string
}

// NewBuilder constructs a Builder with default settings (docker on
// PATH, no streaming writers, cache under ~/.diffmind).
func NewBuilder() *Builder {
	return &Builder{DockerPath: "docker"}
}

// EnsureImage makes sure `image` is present on the local Docker daemon,
// building it from the embedded context if necessary.
//
// Behaviour by policy:
//   - AutoBuildMissing: only builds when `docker image inspect` fails.
//   - AutoBuildAlways:  builds unconditionally.
//   - AutoBuildNever:   never builds. If image is missing returns
//     (BuildResult{Built: false}, ErrImageMissing).
//
// The returned error is non-nil only on build failure (or for the
// AutoBuildNever / missing combination). A missing-but-already-built
// image is a normal nil return.
func (b *Builder) EnsureImage(ctx context.Context, image string, policy AutoBuildPolicy) (BuildResult, error) {
	if image == "" {
		return BuildResult{}, fmt.Errorf("ensure image: tag is required")
	}
	if policy == "" {
		policy = AutoBuildMissing
	}
	res := BuildResult{Image: image}

	switch policy {
	case AutoBuildAlways:
		// fall through to build
	case AutoBuildNever:
		if b.imagePresent(ctx, image) {
			return res, nil
		}
		return res, ErrImageMissing
	case AutoBuildMissing:
		if b.imagePresent(ctx, image) {
			return res, nil
		}
	default:
		return res, fmt.Errorf("ensure image: unknown policy %q", policy)
	}

	return b.serialisedBuild(ctx, image)
}

// ErrImageMissing is returned by EnsureImage when the image is absent
// and the policy is AutoBuildNever. Callers can use errors.Is to
// distinguish this from genuine build failures.
var ErrImageMissing = fmt.Errorf("indexer image missing and auto-build is disabled")

// serialisedBuild dedupes concurrent builds of the same image so two
// pipelines kicking off in quick succession share one docker build.
func (b *Builder) serialisedBuild(ctx context.Context, image string) (BuildResult, error) {
	v, err, _ := builderFlight.Do(image, func() (any, error) {
		return b.buildOnce(ctx, image)
	})
	if err != nil {
		return BuildResult{Image: image}, err
	}
	return v.(BuildResult), nil
}

// buildOnce performs a single end-to-end build:
//
//  1. Compute the embedded-context digest.
//  2. Extract to a content-addressed temp dir under
//     BuildContextRoot/<digest>/. Skip extraction if the dir already
//     has the expected files (warm cache).
//  3. Synthesise a minimal go.mod next to the wrapper so the in-image
//     compile works without depending on the diffmind module graph.
//  4. Run `docker build -f Dockerfile.indexer -t <image> <ctxDir>`
//     with BuildKit forced on via the env var.
//  5. Return the captured tail of the build log on failure.
func (b *Builder) buildOnce(ctx context.Context, image string) (BuildResult, error) {
	digest, err := computeEmbedDigest(indexerbuild.Context)
	if err != nil {
		return BuildResult{Image: image}, fmt.Errorf("digest embed: %w", err)
	}
	ctxDir, err := b.prepareContextDir(digest)
	if err != nil {
		return BuildResult{Image: image}, fmt.Errorf("prepare context: %w", err)
	}

	// Try BuildKit first; if the daemon / CLI doesn't have buildx
	// available, fall back to the legacy builder. Our Dockerfile uses
	// only classic features (no `RUN --mount`, no secrets, no
	// `# syntax=` extensions) so it builds correctly either way.
	tail := &tailBuf{cap: 64 * 1024}
	buildOnce := func(useBuildKit bool) error {
		args := []string{
			"build",
			"-f", filepath.Join(ctxDir, indexerbuild.DockerfileName),
			"-t", image,
			ctxDir,
		}
		cmd := exec.CommandContext(ctx, b.dockerPath(), args...)
		env := os.Environ()
		if useBuildKit {
			env = append(env, "DOCKER_BUILDKIT=1")
		} else {
			// Explicitly turn BuildKit OFF so the daemon picks the
			// legacy builder. We do this even though "no env var"
			// already means "legacy" — being explicit makes the
			// retry attempt deterministic across machines whose
			// shell exports DOCKER_BUILDKIT=1 globally.
			env = append(env, "DOCKER_BUILDKIT=0")
		}
		cmd.Env = env
		// Reset tail for the second attempt so error tails are clean.
		tail.Reset()
		cmd.Stdout = mergeWriters(tail, b.Stdout)
		cmd.Stderr = mergeWriters(tail, b.Stderr)
		return cmd.Run()
	}

	if err := buildOnce(true); err != nil {
		// Detect the "buildx component missing" failure mode. Common
		// on macOS when colima + Homebrew's docker CLI are in use
		// without Docker Desktop's plugin path. Falling back is
		// strictly safer than failing the run — the legacy builder
		// has been around since 2013 and handles our Dockerfile fine.
		if needsLegacyFallback(tail.String()) {
			if err := buildOnce(false); err != nil {
				return BuildResult{
						Built:      true,
						Image:      image,
						ContextDir: ctxDir,
						LogTail:    tail.String(),
					}, fmt.Errorf("docker build (legacy fallback): %w (tail: %s)",
						err, lastLines(tail.String(), 8))
			}
		} else {
			return BuildResult{
				Built:      true,
				Image:      image,
				ContextDir: ctxDir,
				LogTail:    tail.String(),
			}, fmt.Errorf("docker build: %w (tail: %s)", err, lastLines(tail.String(), 8))
		}
	}

	return BuildResult{
		Built:      true,
		Image:      image,
		ContextDir: ctxDir,
		LogTail:    tail.String(),
	}, nil
}

// needsLegacyFallback returns true when the BuildKit attempt failed
// in a way that indicates buildx is missing from the active docker
// context (typical on macOS with colima + Homebrew's `docker` CLI).
// We match on the daemon's error string — the only stable signal
// across Docker versions.
//
// Examples of strings we treat as "fall back to legacy":
//
//	ERROR: BuildKit is enabled but the buildx component is missing
//	docker: unknown command: docker buildx
//	"docker buildx build" not implemented
func needsLegacyFallback(logTail string) bool {
	t := strings.ToLower(logTail)
	patterns := []string{
		"buildx component is missing",
		"buildx component is broken",
		"unknown command: docker buildx",
		"not implemented",
	}
	for _, p := range patterns {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// dockerPath returns the configured Docker binary, defaulting to "docker".
func (b *Builder) dockerPath() string {
	if b.DockerPath != "" {
		return b.DockerPath
	}
	return "docker"
}

// imagePresent reports whether the image is on the local daemon.
// Implemented via `docker image inspect`, which exits 0 iff the image
// exists. We discard the daemon's own stdout/stderr — only the exit
// code is consulted.
func (b *Builder) imagePresent(ctx context.Context, image string) bool {
	cmd := exec.CommandContext(ctx, b.dockerPath(), "image", "inspect", image)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}
