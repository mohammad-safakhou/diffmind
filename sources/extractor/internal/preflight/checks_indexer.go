package preflight

import (
	"context"
	"os/exec"
	"strings"
)

// IndexerReadinessCheck reports on the state of the SCIP indexer
// image pipeline. It's informational only — emits SeverityOK or
// SeverityWarn, never Fail.
//
// We surface three states the user might want to know about:
//
//   - "no image; build context embedded" (warn / amber) →
//     normal first-run state. The next run will trigger an
//     inline `docker build` (~5-20 min depending on languages).
//   - "image present" (ok) → next run will skip the build.
//   - "docker not available" (skip) → not our concern here;
//     DockerCheck handles that. We just skip and return ok.
//
// The actual image tag is content-addressed at run time from the
// detected languages+versions, so this check can only assert
// readiness of the build CONTEXT, not of a specific image. The
// composite image will be either already-cached (instant) or
// freshly composed from cached base images (seconds) or built
// from scratch (minutes).
type IndexerReadinessCheck struct {
	// CommandPath is the docker binary; defaults to "docker".
	CommandPath string
	// LegacyImageTag is the pre-Sprint 4 single-image tag we used
	// to look for. Still honoured so users with a previously-built
	// image see "image present" until the new per-language flow
	// becomes the cached one.
	LegacyImageTag string
}

// NewIndexerReadinessCheck constructs the check with defaults.
func NewIndexerReadinessCheck() *IndexerReadinessCheck {
	return &IndexerReadinessCheck{LegacyImageTag: "diffmind-indexer:dev"}
}

func (c *IndexerReadinessCheck) Name() string  { return "indexer" }
func (c *IndexerReadinessCheck) Title() string { return "Indexer build context" }

func (c *IndexerReadinessCheck) Run(ctx context.Context) Result {
	bin := c.CommandPath
	if bin == "" {
		bin = "docker"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityOK,
			Message:  "Skipped (Docker not on PATH; see Docker daemon check)",
		}
	}

	// Quick inspect of the legacy image first; if found, return ok
	// so users with a prebuilt image still see green.
	if c.LegacyImageTag != "" {
		cmd := exec.CommandContext(ctx, bin, "image", "inspect", c.LegacyImageTag)
		if err := cmd.Run(); err == nil {
			return Result{
				Name:     c.Name(),
				Title:    c.Title(),
				Severity: SeverityOK,
				Message:  "Pre-built " + c.LegacyImageTag + " image present",
			}
		}
	}

	// Check whether ANY diffmind-indexer-related images exist
	// (per-language base images, composite images). We `docker
	// images --filter reference=diffmind-* --format` and count
	// the lines.
	cmd := exec.CommandContext(ctx, bin, "images",
		"--filter", "reference=diffmind-base-*",
		"--filter", "reference=diffmind-indexer:*",
		"--format", "{{.Repository}}:{{.Tag}}",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityWarn,
			Message:  "Could not list cached indexer images",
			Detail:   strings.TrimSpace(string(out)),
		}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	cached := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			cached++
		}
	}
	if cached > 0 {
		return Result{
			Name:     c.Name(),
			Title:    c.Title(),
			Severity: SeverityOK,
			Message:  "Cached images present: " + strings.Join(lines, ", "),
		}
	}
	return Result{
		Name:        c.Name(),
		Title:       c.Title(),
		Severity:    SeverityWarn,
		Message:     "No cached indexer images yet",
		Remediation: "First run will build per-language base images (~5-20 min cold). Subsequent runs reuse them.",
	}
}
