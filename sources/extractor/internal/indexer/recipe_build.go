package indexer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/mohammad-safakhou/diffmind/internal/indexerbuild/recipe"
)

// RecipeBuildResult captures the outcome of EnsureRecipe. It mirrors
// the per-job structure of a recipe.Plan so the orchestrator can
// emit per-job events into the event bus.
type RecipeBuildResult struct {
	// Bases is the per-base-image outcome, one entry per
	// plan.Base (same order).
	Bases []SingleBuildResult
	// Composite is the final image build outcome.
	Composite SingleBuildResult
	// Image is the resolved composite tag the orchestrator
	// passes to the indexer container.
	Image string
}

// SingleBuildResult is the outcome of one `docker build` call.
type SingleBuildResult struct {
	Tag        string
	Built      bool   // true if we actually invoked docker build (false on cache hit)
	ContextDir string // where the Dockerfile + context files were materialised
	LogTail    string // last ~64 KB of stdout+stderr
	Err        error  // non-nil if this build failed
}

// EnsureRecipe builds (or reuses) every image the plan describes.
// Order: bases first (in plan.Base order), then composite.
//
// Build failures are surfaced via SingleBuildResult.Err but do NOT
// short-circuit the entire EnsureRecipe call by default — we
// attempt every base so the user gets the full picture in one
// shot. The composite step IS skipped if any base failed (no
// point building a composite from missing layers).
//
// Concurrent EnsureRecipe calls for the same composite tag are
// deduped through the same singleflight registry the legacy
// EnsureImage uses.
func (b *Builder) EnsureRecipe(ctx context.Context, plan recipe.Plan) RecipeBuildResult {
	res := RecipeBuildResult{Image: plan.Composite.Tag}

	// Build bases sequentially. Parallelising would let two bases
	// race on the same disk + daemon and rarely speeds up the
	// total — most of the cold time is per-base download bandwidth.
	allBasesOk := true
	for _, job := range plan.Base {
		sr := b.buildOneRecipeJob(ctx, job)
		res.Bases = append(res.Bases, sr)
		if sr.Err != nil {
			allBasesOk = false
			// Continue to next base so the user sees every
			// failure. The composite step won't run.
		}
	}
	if !allBasesOk {
		res.Composite = SingleBuildResult{
			Tag: plan.Composite.Tag,
			Err: fmt.Errorf("composite skipped: one or more base images failed to build"),
		}
		return res
	}
	res.Composite = b.buildOneRecipeJob(ctx, plan.Composite)
	return res
}

// buildOneRecipeJob materialises a recipe.BuildJob's Dockerfile +
// context files to disk and runs `docker build`. Returns a
// SingleBuildResult. Skips the build if the target image is
// already present locally (a cache hit).
func (b *Builder) buildOneRecipeJob(ctx context.Context, job recipe.BuildJob) SingleBuildResult {
	res := SingleBuildResult{Tag: job.Tag}

	// Cache hit fast path. The image tag is content-addressed via
	// the language+version combo, so a presence check is enough
	// to know it's the right image.
	if b.imagePresent(ctx, job.Tag) {
		return res
	}

	ctxDir, err := b.prepareRecipeContext(job)
	if err != nil {
		res.Err = fmt.Errorf("prepare context for %s: %w", job.Tag, err)
		return res
	}
	res.ContextDir = ctxDir

	tail := &tailBuf{cap: 64 * 1024}
	build := func(useBuildKit bool) error {
		args := []string{
			"build",
			"-f", filepath.Join(ctxDir, "Dockerfile"),
			"-t", job.Tag,
			ctxDir,
		}
		cmd := exec.CommandContext(ctx, b.dockerPath(), args...)
		env := os.Environ()
		if useBuildKit {
			env = append(env, "DOCKER_BUILDKIT=1")
		} else {
			env = append(env, "DOCKER_BUILDKIT=0")
		}
		cmd.Env = env
		tail.Reset()
		cmd.Stdout = mergeWriters(tail, b.Stdout)
		cmd.Stderr = mergeWriters(tail, b.Stderr)
		return cmd.Run()
	}

	if err := build(true); err != nil {
		if needsLegacyFallback(tail.String()) {
			if err := build(false); err != nil {
				res.Built = true
				res.LogTail = tail.String()
				res.Err = fmt.Errorf("docker build (legacy fallback) for %s: %w (tail: %s)",
					job.Tag, err, lastLines(tail.String(), 12))
				return res
			}
		} else {
			res.Built = true
			res.LogTail = tail.String()
			res.Err = fmt.Errorf("docker build for %s: %w (tail: %s)",
				job.Tag, err, lastLines(tail.String(), 12))
			return res
		}
	}
	res.Built = true
	res.LogTail = tail.String()
	return res
}

// prepareRecipeContext writes the Dockerfile + context files for a
// single job to a fresh directory under
// ~/.diffmind/indexer-recipe-context/<tag>/ and returns the
// directory path. The directory is wiped + repopulated on each
// call so partial state from a prior failed build cannot leak.
//
// We use a tag-derived directory name (sanitised) so two jobs for
// different tags never collide.
func (b *Builder) prepareRecipeContext(job recipe.BuildJob) (string, error) {
	root := b.recipeRoot()
	dir := filepath.Join(root, sanitiseDirName(job.Tag))
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Dockerfile first.
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(job.Dockerfile), 0o644); err != nil {
		return "", err
	}
	// Then context files. Paths are relative; we materialise them
	// preserving sub-directories.
	for relPath, content := range job.ContextFiles {
		full := filepath.Join(dir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// recipeRoot returns the per-machine cache root for recipe build
// contexts. Distinct from contextRoot() which serves the legacy
// embed-based flow.
func (b *Builder) recipeRoot() string {
	if b.BuildContextRoot != "" {
		return filepath.Join(b.BuildContextRoot, "recipe")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".diffmind", "indexer-recipe-context")
}

// sanitiseDirName turns a Docker tag into a filesystem-safe dir
// name. ":" → "_", path separators stripped.
func sanitiseDirName(tag string) string {
	out := make([]rune, 0, len(tag))
	for _, r := range tag {
		switch {
		case r == ':' || r == '/' || r == '\\':
			out = append(out, '_')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

// EnsureRecipe is also exposed through a tiny in-process
// singleflight so two pipelines launching against the same
// language combo don't fight each other on disk.
//
// (Defined as a method to share the existing flightGroup.)
var recipeFlight = &recipeFlightGroup{m: map[string]*recipeCall{}}

type recipeFlightGroup struct {
	mu sync.Mutex
	m  map[string]*recipeCall
}
type recipeCall struct {
	wg  sync.WaitGroup
	val RecipeBuildResult
}

func (g *recipeFlightGroup) Do(key string, fn func() RecipeBuildResult) (RecipeBuildResult, bool) {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, true
	}
	c := &recipeCall{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()
	c.val = fn()
	c.wg.Done()
	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()
	return c.val, false
}

// EnsureRecipeSingleflight is a convenience wrapper that dedupes
// concurrent calls. Most callers should prefer this over
// EnsureRecipe directly.
func (b *Builder) EnsureRecipeSingleflight(ctx context.Context, plan recipe.Plan) RecipeBuildResult {
	v, _ := recipeFlight.Do(plan.Composite.Tag, func() RecipeBuildResult {
		return b.EnsureRecipe(ctx, plan)
	})
	return v
}
