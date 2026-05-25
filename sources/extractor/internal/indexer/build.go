package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

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
// THREAD SAFETY
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
//                       (BuildResult{Built: false}, ErrImageMissing).
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

// prepareContextDir ensures the embedded build context is extracted to
// <BuildContextRoot>/<digest>/ and synthesises the go.mod the
// wrapper-builder Dockerfile stage expects. Idempotent.
func (b *Builder) prepareContextDir(digest string) (string, error) {
	root := b.contextRoot()
	dir := filepath.Join(root, digest)
	stampPath := filepath.Join(dir, ".extracted")

	if _, err := os.Stat(stampPath); err == nil {
		// Warm cache. We deliberately do NOT re-verify the contents
		// against the embedded FS on every call — the digest already
		// proves they match. If a user tampers with the cached dir
		// we'd rather rebuild on Docker's side than silently re-extract.
		return dir, nil
	}

	// Wipe stale partial extractions (interrupted runs etc.).
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	if err := extractEmbed(indexerbuild.Context, dir); err != nil {
		return "", err
	}

	// Synthesise the go.mod the wrapper-builder Dockerfile stage
	// expects at the build-context root. Stdlib-only, no replace
	// directives; the wrapper compiles standalone inside the
	// container.
	goMod := "module diffmindindex\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return "", err
	}

	// Stamp file marks the extraction as complete. Written last so an
	// interrupted run is detected on the next attempt.
	if err := os.WriteFile(stampPath, []byte(digest+"\n"), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// contextRoot is BuildContextRoot or the default
// ~/.diffmind/indexer-build-context/.
func (b *Builder) contextRoot() string {
	if b.BuildContextRoot != "" {
		return b.BuildContextRoot
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".diffmind", "indexer-build-context")
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

// ---------------------------------------------------------------------
// Embedded-FS extraction helpers
// ---------------------------------------------------------------------

// extractEmbed copies every file in src to dst, preserving relative
// paths. We do NOT try to be clever about mode bits — embed.FS
// returns files with mode 0444 regardless, and the docker daemon
// only cares about content.
//
// FILE EXCLUSIONS
//
// We strip *_test.go from the wrapper directory before writing it
// out. Those files live next to the production sources so we can
// `go test ./indexerbuild/wrapper/...`, but they are NOT needed by
// the Docker build (which compiles only the wrapper binary, not its
// test code) and dragging them in would force the in-container `go
// build` to pull testing-package transitive deps for no benefit.
func extractEmbed(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dst, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
}

// computeEmbedDigest hashes every embedded path + content into a
// stable SHA-256. We use this as the cache key so a code change inside
// the wrapper (or a Dockerfile bump) produces a fresh extraction
// directory; old extractions stick around but never collide.
func computeEmbedDigest(src fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Mix path + content into the hash. Use a length prefix on
		// the path so two files whose paths concatenate identically
		// (impossible in practice, but free to defend against) still
		// produce distinct digests.
		fmt.Fprintf(h, "%d:%s\n", len(path), path)
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%d:", len(data))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{'\n'})
		return nil
	})
	if err != nil {
		return "", err
	}
	// 16 hex chars (= 8 bytes) is plenty of entropy for a per-machine
	// cache directory name. Full 64 hex char names are ugly without
	// adding any practical safety.
	full := hex.EncodeToString(h.Sum(nil))
	return full[:16], nil
}

// ---------------------------------------------------------------------
// In-process singleflight for concurrent EnsureImage calls
// ---------------------------------------------------------------------

// builderFlight ensures that concurrent EnsureImage calls for the same
// image share a single underlying build. The standard library does
// not ship a singleflight, so we keep a small one here. Total surface
// is one map + one mutex; the data structure is freed as soon as the
// in-flight call returns.
var builderFlight = &flightGroup{m: map[string]*flightCall{}}

type flightGroup struct {
	mu sync.Mutex
	m  map[string]*flightCall
}

type flightCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

func (g *flightGroup) Do(key string, fn func() (any, error)) (any, error, bool) {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err, true
	}
	c := &flightCall{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err, false
}

// ---------------------------------------------------------------------
// Small writer helpers
// ---------------------------------------------------------------------

// tailBuf is an io.Writer that retains only the last `cap` bytes. The
// stderr+stdout of `docker build` can run to many MB on a cold build;
// we keep enough trailing context to surface error tails without
// growing without bound.
type tailBuf struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) >= t.cap {
		t.buf = append(t.buf[:0], p[len(p)-t.cap:]...)
		return len(p), nil
	}
	if len(t.buf)+len(p) <= t.cap {
		t.buf = append(t.buf, p...)
		return len(p), nil
	}
	keep := t.cap - len(p)
	t.buf = append(t.buf[len(t.buf)-keep:], p...)
	return len(p), nil
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// Reset clears the buffer. Used between BuildKit and legacy-fallback
// build attempts so the second attempt's log tail isn't polluted with
// the first attempt's stderr.
func (t *tailBuf) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = t.buf[:0]
}

// lastLines returns the last n lines of s, used to keep error messages
// from getting unwieldy when docker build fails in stage 60-of-76.
func lastLines(s string, n int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return "..." + "\n" + strings.Join(lines[len(lines)-n:], "\n")
}
