// Package recipe turns a slice of langdetect.Fact into a buildable
// indexer image, by:
//
//  1. Selecting one BASE image per language present in the facts
//     (e.g. diffmind-base-java:21, diffmind-base-node:20).
//  2. Generating a COMPOSITE Dockerfile that FROMs each base image
//     and COPYs the relevant toolchains + SCIP indexer binary into
//     one runtime image.
//  3. Producing a human-readable tag for the composite image, like
//     diffmind-indexer:java21_node20_python3.12.
//
// Base images are themselves built from per-language Dockerfile
// templates embedded in this package. The build flow is:
//
//   - For each fact: build/cache the matching base image.
//   - Then build the composite from those bases via COPY --from=.
//
// Caching is automatic at the Docker layer — base images are
// keyed by their tag (e.g. diffmind-base-java:21), so once built
// they survive across repos with the same language+version combo.
//
// This package does NOT shell out to docker; that's the Builder's
// job in internal/indexer/build.go. Recipe.Plan returns a sequence
// of BuildJobs the Builder then executes in order.
package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/langdetect"
)

// Plan is the full set of builds Recipe wants performed.
//
//	Base[]      → one BuildJob per missing base image; produced
//	              first because the Composite COPYs from them.
//	Composite   → the final image the indexer container runs.
//
// Plan is deterministic given identical input Facts; this is what
// makes per-repo caching work.
type Plan struct {
	Base      []BuildJob
	Composite BuildJob
	// Resolved is the post-resolution view of the input facts
	// (e.g. "Java 17" + fallback "Java 21"), useful for event
	// payloads.
	Resolved []ResolvedFact
}

// BuildJob is a single `docker build -f Dockerfile -t tag .`
// invocation. The Builder writes Dockerfile into Context and
// runs the build there.
type BuildJob struct {
	// Tag is the image tag this job produces (e.g.
	// "diffmind-base-java:21" or "diffmind-indexer:java21_node20").
	Tag string
	// Dockerfile is the full content of the Dockerfile to write.
	Dockerfile string
	// ContextFiles is a map of relative path → contents for any
	// extra files the Dockerfile needs in its build context
	// (e.g. wrapper Go sources for the composite build).
	ContextFiles map[string]string
	// FromImages is the list of bases this job FROMs. The
	// Builder uses this to ensure dependencies are built first.
	FromImages []string
	// Kind is "base" or "composite". Lets the UI render
	// different progress messages.
	Kind string
	// Language is set on base jobs to the language they
	// install. Empty on composite jobs.
	Language langdetect.Language
}

// ResolvedFact is a Fact augmented with the version we will
// actually install (possibly substituting a default when the
// detection didn't surface one).
type ResolvedFact struct {
	Language          langdetect.Language `json:"language"`
	RequestedVersion  string              `json:"requested_version,omitempty"`
	ResolvedVersion   string              `json:"resolved_version"`
	UsedFallback      bool                `json:"used_fallback"`
	BaseImage         string              `json:"base_image"`
	BuildTool         string              `json:"build_tool,omitempty"`
	BuildToolVersion  string              `json:"build_tool_version,omitempty"`
}

// Generate is the public entrypoint. It produces a Plan from the
// detected facts, or an error if no supported language was found.
func Generate(facts []langdetect.Fact) (Plan, error) {
	resolved := make([]ResolvedFact, 0, len(facts))
	for _, f := range facts {
		r := resolve(f)
		if r.BaseImage == "" {
			// Unsupported language; skip silently. The composite
			// build will simply not include this language's
			// indexer, which is the correct behaviour.
			continue
		}
		resolved = append(resolved, r)
	}
	// Stable order so the composite tag is deterministic.
	sort.Slice(resolved, func(i, j int) bool {
		return resolved[i].Language < resolved[j].Language
	})
	if len(resolved) == 0 {
		return Plan{}, fmt.Errorf("recipe: no supported language detected (facts=%d)", len(facts))
	}

	plan := Plan{Resolved: resolved}

	for _, r := range resolved {
		df, files, err := baseDockerfileFor(r)
		if err != nil {
			return Plan{}, fmt.Errorf("recipe: base dockerfile for %s: %w", r.Language, err)
		}
		plan.Base = append(plan.Base, BuildJob{
			Tag:          r.BaseImage,
			Dockerfile:   df,
			ContextFiles: files,
			Kind:         "base",
			Language:     r.Language,
		})
	}

	tag := compositeTag(resolved)
	cdf, cfiles := compositeDockerfile(resolved)
	bases := make([]string, 0, len(resolved))
	for _, r := range resolved {
		bases = append(bases, r.BaseImage)
	}
	plan.Composite = BuildJob{
		Tag:          tag,
		Dockerfile:   cdf,
		ContextFiles: cfiles,
		FromImages:   bases,
		Kind:         "composite",
	}
	return plan, nil
}

// compositeTag formats a stable, human-readable tag. Examples:
//   diffmind-indexer:java21_node20_python3.12
//   diffmind-indexer:go1.22
// The tag is uniquely determined by resolved language+version, so
// two repos with the same combo share the composite image.
func compositeTag(resolved []ResolvedFact) string {
	parts := make([]string, 0, len(resolved))
	for _, r := range resolved {
		parts = append(parts, string(r.Language)+sanitizeForTag(r.ResolvedVersion))
	}
	return "diffmind-indexer:" + strings.Join(parts, "_")
}

// sanitizeForTag strips characters Docker doesn't allow in tags.
// Tags must match [A-Za-z0-9_.-]{1,128}.
func sanitizeForTag(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "latest"
	}
	return b.String()
}

// resolve picks the version we will actually build for. Behaviour:
//
//   - If the fact has a Version we can match → use it directly.
//   - Otherwise → fall back to the language's default LTS, with
//     UsedFallback=true (so the orchestrator can warn the user).
//
// Unsupported languages return BaseImage="" so Generate can skip
// them silently.
func resolve(f langdetect.Fact) ResolvedFact {
	r := ResolvedFact{
		Language:         f.Language,
		RequestedVersion: f.Version,
		BuildTool:        f.BuildTool,
		BuildToolVersion: f.BuildToolVersion,
	}
	spec, ok := supportedLanguages[f.Language]
	if !ok {
		return r
	}
	version := pickVersion(spec, f.Version)
	r.ResolvedVersion = version
	r.UsedFallback = f.Version == "" || (f.Version != version && f.Version != "")
	if f.Version != "" && f.Version == version {
		r.UsedFallback = false
	}
	r.BaseImage = "diffmind-base-" + string(f.Language) + ":" + version
	return r
}

// pickVersion finds the closest spec.Versions entry to the
// requested version; falls back to spec.Default. Matching is
// substring-of-the-major-minor; e.g. "1.22.8" matches "1.22".
func pickVersion(spec languageSpec, requested string) string {
	if requested == "" {
		return spec.Default
	}
	// Exact match first.
	for _, v := range spec.Versions {
		if v == requested {
			return v
		}
	}
	// Major-minor prefix match.
	majMin := requested
	if i := strings.Index(requested, "."); i > 0 {
		// e.g. "1.22.8" → check for prefix "1.22"
		if j := strings.Index(requested[i+1:], "."); j > 0 {
			majMin = requested[:i+1+j]
		}
	}
	for _, v := range spec.Versions {
		if v == majMin {
			return v
		}
	}
	// Major-only match for languages where minor doesn't matter
	// for our purposes (Java, .NET).
	if i := strings.Index(requested, "."); i > 0 {
		major := requested[:i]
		for _, v := range spec.Versions {
			if v == major {
				return v
			}
		}
	}
	return spec.Default
}

// digest produces a short content hash of a Plan, useful for the
// indexer-build-context cache directory naming.
func (p Plan) Digest() string {
	h := sha256.New()
	for _, b := range p.Base {
		h.Write([]byte(b.Tag))
		h.Write([]byte("\x00"))
		h.Write([]byte(b.Dockerfile))
		h.Write([]byte("\x00"))
	}
	h.Write([]byte(p.Composite.Tag))
	h.Write([]byte("\x00"))
	h.Write([]byte(p.Composite.Dockerfile))
	full := hex.EncodeToString(h.Sum(nil))
	return full[:16]
}
