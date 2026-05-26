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
	"strconv"
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
// requested version; falls back to the highest known version when the
// request is newer than all known versions, or to spec.Default otherwise.
//
// Matching order:
//  1. Exact string match              "21"         → "21"
//  2. Major-minor prefix              "1.22.8"     → "1.22"
//  3. Major-only                      "17.0.1"     → "17"
//  4. Numeric major extracted from
//     a vendor-prefixed string        "corretto-25.0.1.8.1" → major=25 → "25"
//  5. Highest known when newer        "26"         → "25"  (if 25 is max)
//  6. spec.Default
func pickVersion(spec languageSpec, requested string) string {
	if requested == "" {
		return spec.Default
	}

	// normalise: work on the canonical form AND a vendor-stripped form in
	// parallel so that "corretto-25.0.1.8.1" is also tried as "25.0.1.8.1".
	candidates := []string{requested}
	if stripped := stripVendorPrefix(requested); stripped != requested {
		candidates = append(candidates, stripped)
	}

	for _, cand := range candidates {
		// 1. Exact match.
		for _, v := range spec.Versions {
			if v == cand {
				return v
			}
		}
		// 2. Major-minor prefix: "1.22.8" → "1.22".
		majMin := cand
		if i := strings.Index(cand, "."); i > 0 {
			if j := strings.Index(cand[i+1:], "."); j > 0 {
				majMin = cand[:i+1+j]
			}
		}
		for _, v := range spec.Versions {
			if v == majMin {
				return v
			}
		}
		// 3. Major-only: "17.0.1" → "17".
		if i := strings.Index(cand, "."); i > 0 {
			major := cand[:i]
			for _, v := range spec.Versions {
				if v == major {
					return v
				}
			}
		}
		// 4. Plain integer: "25" → exact match already handled above;
		//    but also handles the stripped "25" from "corretto-25.0.1.8.1"
		//    after the dot-split paths above.
		if _, err := strconv.Atoi(cand); err == nil {
			for _, v := range spec.Versions {
				if v == cand {
					return v
				}
			}
		}
	}

	// 5. If the requested value contains a numeric major that is larger than
	//    every known version, return the highest known so the user gets the
	//    closest JDK rather than falling back to an older Default.
	//    e.g. requested="corretto-25.0.1.8.1" → major=25 > max=25 → "25"
	//         requested="26" → major=26 > max=25 → "25"
	if major := extractMajorInt(requested); major > 0 && len(spec.Versions) > 0 {
		highest := spec.Versions[len(spec.Versions)-1]
		if highestInt, err := strconv.Atoi(highest); err == nil && major >= highestInt {
			return highest
		}
		// Also check if it matches any known version directly.
		majStr := strconv.Itoa(major)
		for _, v := range spec.Versions {
			if v == majStr {
				return v
			}
		}
	}

	return spec.Default
}

// stripVendorPrefix removes a leading vendor name from version strings like
// "corretto-25.0.1.8.1" → "25.0.1.8.1", "temurin-21.0.3" → "21.0.3",
// "openjdk-17" → "17". Returns the input unchanged if no prefix is detected.
func stripVendorPrefix(v string) string {
	// Find the first digit run; everything before it (including a trailing
	// hyphen or dot) is considered a vendor prefix.
	for i, ch := range v {
		if ch >= '0' && ch <= '9' {
			return v[i:]
		}
	}
	return v
}

// extractMajorInt extracts the first run of digits from a version string and
// returns it as an integer. Returns 0 if no digit run is found.
// "corretto-25.0.1.8.1" → 25, "21" → 21, "1.22.8" → 1, "jdk-17" → 17.
func extractMajorInt(v string) int {
	start := -1
	for i, ch := range v {
		if ch >= '0' && ch <= '9' {
			if start < 0 {
				start = i
			}
		} else {
			if start >= 0 {
				n, _ := strconv.Atoi(v[start:i])
				return n
			}
		}
	}
	if start >= 0 {
		n, _ := strconv.Atoi(v[start:])
		return n
	}
	return 0
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
