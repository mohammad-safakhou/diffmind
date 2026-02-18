package corpus

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"diffmind/internal/analyzers"
	"diffmind/internal/bundleio"
	"diffmind/internal/classifier"
	"diffmind/internal/consolidation"
	"diffmind/internal/parser"
	"diffmind/internal/snapshot"
)

type options struct {
	ManifestPath string
	OutDir       string
}

type manifest struct {
	OutDir   string         `json:"out_dir"`
	FailFast bool           `json:"fail_fast"`
	Cases    []manifestCase `json:"cases"`
}

type manifestCase struct {
	Name         string      `json:"name"`
	Source       string      `json:"source"`
	Ref          string      `json:"ref"`
	Domain       string      `json:"domain,omitempty"`
	Language     string      `json:"language,omitempty"`
	Framework    string      `json:"framework,omitempty"`
	FrameworkVer string      `json:"framework_version,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	Expect       expectation `json:"expect"`
	AnalyzeFlags []string    `json:"analyze_flags"`
}

type expectation struct {
	MinEntities   int      `json:"min_entities"`
	RequiredTypes []string `json:"required_types"`
}

type report struct {
	GeneratedAtUTC string       `json:"generated_at_utc"`
	ManifestPath   string       `json:"manifest_path"`
	OutDir         string       `json:"out_dir"`
	Passed         int          `json:"passed"`
	Failed         int          `json:"failed"`
	Cases          []caseReport `json:"cases"`
}

type caseReport struct {
	Name         string         `json:"name"`
	Source       string         `json:"source"`
	Ref          string         `json:"ref"`
	Domain       string         `json:"domain,omitempty"`
	Language     string         `json:"language,omitempty"`
	Framework    string         `json:"framework,omitempty"`
	FrameworkVer string         `json:"framework_version,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	DurationMS   int64          `json:"duration_ms"`
	CaseOutDir   string         `json:"case_out_dir"`
	BundlePath   string         `json:"bundle_path"`
	EntityCount  int            `json:"entity_count"`
	CountsByType map[string]int `json:"counts_by_type"`
	Confidence   float64        `json:"confidence,omitempty"`
	Status       string         `json:"status"`
	Failures     []string       `json:"failures"`
	Error        string         `json:"error,omitempty"`
}

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	m, manifestDir, err := loadManifest(opts.ManifestPath)
	if err != nil {
		return err
	}
	if len(m.Cases) == 0 {
		return errors.New("manifest has no cases")
	}

	outDir := opts.OutDir
	if outDir == "" {
		outDir = strings.TrimSpace(m.OutDir)
	}
	if outDir == "" {
		outDir = filepath.Join(".diffmind", "corpus")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create corpus out dir: %w", err)
	}

	rep := report{
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		ManifestPath:   opts.ManifestPath,
		OutDir:         outDir,
		Cases:          make([]caseReport, 0, len(m.Cases)),
	}

	for _, c := range m.Cases {
		cr := runCase(ctx, c, manifestDir, outDir)
		rep.Cases = append(rep.Cases, cr)
		if cr.Status == "passed" {
			rep.Passed++
		} else {
			rep.Failed++
			if m.FailFast {
				break
			}
		}
	}

	reportPath := filepath.Join(outDir, "report.json")
	if err := writeReport(reportPath, rep); err != nil {
		return err
	}
	fmt.Println(reportPath)

	if rep.Failed > 0 {
		return fmt.Errorf("corpus failed: %d/%d cases", rep.Failed, len(rep.Cases))
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("corpus", flag.ContinueOnError)
	manifestPath := fs.String("manifest", filepath.Join("corpus", "manifest.json"), "Path to corpus manifest JSON")
	outDir := fs.String("out", "", "Output root for corpus reports and per-case artifacts")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse corpus flags: %w", err)
	}
	return options{
		ManifestPath: strings.TrimSpace(*manifestPath),
		OutDir:       strings.TrimSpace(*outDir),
	}, nil
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--manifest" || arg == "--out":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--manifest=") || strings.HasPrefix(arg, "--out="):
			out = append(out, arg)
		}
	}
	return out
}

func loadManifest(path string) (manifest, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, "", fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, "", fmt.Errorf("decode manifest %s: %w", path, err)
	}
	manifestDir := filepath.Dir(path)
	return m, manifestDir, nil
}

func runCase(ctx context.Context, c manifestCase, manifestDir string, outDir string) caseReport {
	start := time.Now()
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = "unnamed"
	}
	src := strings.TrimSpace(c.Source)
	if src == "" {
		return caseReport{
			Name:       name,
			Source:     c.Source,
			Ref:        c.Ref,
			Status:     "failed",
			Failures:   []string{"missing source"},
			DurationMS: time.Since(start).Milliseconds(),
		}
	}

	if !filepath.IsAbs(src) {
		src = filepath.Join(manifestDir, src)
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return caseReport{
			Name:       name,
			Source:     c.Source,
			Ref:        c.Ref,
			Status:     "failed",
			Failures:   []string{"resolve source path failed"},
			Error:      err.Error(),
			DurationMS: time.Since(start).Milliseconds(),
		}
	}
	src = absSrc
	caseOutDir := filepath.Join(outDir, sanitizeName(name))
	ref := strings.TrimSpace(c.Ref)
	if ref == "" {
		ref = "HEAD"
	}

	err = runPipeline(ctx, src, ref, caseOutDir, c.AnalyzeFlags)
	if err != nil {
		return caseReport{
			Name:       name,
			Source:     src,
			Ref:        ref,
			DurationMS: time.Since(start).Milliseconds(),
			CaseOutDir: caseOutDir,
			Status:     "failed",
			Failures:   []string{"pipeline execution failed"},
			Error:      err.Error(),
		}
	}

	bundlePath := filepath.Join(caseOutDir, "bundle", "intelligence_bundle.json")
	b, err := bundleio.Load(bundlePath)
	if err != nil {
		return caseReport{
			Name:       name,
			Source:     src,
			Ref:        ref,
			DurationMS: time.Since(start).Milliseconds(),
			CaseOutDir: caseOutDir,
			BundlePath: bundlePath,
			Status:     "failed",
			Failures:   []string{"bundle read failed"},
			Error:      err.Error(),
		}
	}

	countsByType := map[string]int{}
	for _, e := range b.Entities {
		countsByType[e.Type]++
	}
	failures := evaluateExpectations(c.Expect, len(b.Entities), countsByType)
	status := "passed"
	if len(failures) > 0 {
		status = "failed"
	}

	return caseReport{
		Name:         name,
		Source:       src,
		Ref:          ref,
		Domain:       strings.TrimSpace(c.Domain),
		Language:     strings.TrimSpace(c.Language),
		Framework:    strings.TrimSpace(c.Framework),
		FrameworkVer: strings.TrimSpace(c.FrameworkVer),
		Tags:         append([]string(nil), c.Tags...),
		DurationMS:   time.Since(start).Milliseconds(),
		CaseOutDir:   caseOutDir,
		BundlePath:   bundlePath,
		EntityCount:  len(b.Entities),
		CountsByType: countsByType,
		Confidence:   caseConfidence(c.Expect, len(failures)),
		Status:       status,
		Failures:     failures,
	}
}

func caseConfidence(expect expectation, failureCount int) float64 {
	checks := 0
	if expect.MinEntities > 0 {
		checks++
	}
	checks += len(expect.RequiredTypes)
	if checks == 0 {
		return 1.0
	}
	passed := checks - failureCount
	if passed < 0 {
		passed = 0
	}
	return float64(passed) / float64(checks)
}

func runPipeline(ctx context.Context, source string, ref string, outDir string, analyzeFlags []string) error {
	snapArgs := []string{"--source", source, "--ref", ref, "--out", outDir}
	if err := snapshot.Run(ctx, snapArgs); err != nil {
		return fmt.Errorf("snapshot stage failed: %w", err)
	}

	baseArgs := []string{"--source", source, "--out", outDir}
	if err := classifier.Run(ctx, baseArgs); err != nil {
		return fmt.Errorf("scan stage failed: %w", err)
	}
	if err := parser.Run(ctx, baseArgs); err != nil {
		return fmt.Errorf("parse stage failed: %w", err)
	}

	analyzeArgs := append([]string{}, baseArgs...)
	analyzeArgs = append(analyzeArgs, analyzeFlags...)
	if err := analyzers.Run(ctx, analyzeArgs); err != nil {
		return fmt.Errorf("analyze stage failed: %w", err)
	}

	bundleArgs := []string{
		"--in", filepath.Join(outDir, "analyzers", "bundle.json"),
		"--out", outDir,
	}
	if err := consolidation.Run(ctx, bundleArgs); err != nil {
		return fmt.Errorf("bundle stage failed: %w", err)
	}
	return nil
}

func evaluateExpectations(expect expectation, entityCount int, countsByType map[string]int) []string {
	failures := make([]string, 0)
	if expect.MinEntities > 0 && entityCount < expect.MinEntities {
		failures = append(failures, fmt.Sprintf("expected at least %d entities, got %d", expect.MinEntities, entityCount))
	}

	required := append([]string(nil), expect.RequiredTypes...)
	sort.Strings(required)
	for _, t := range required {
		if countsByType[t] == 0 {
			failures = append(failures, fmt.Sprintf("required entity type missing: %s", t))
		}
	}
	return failures
}

func sanitizeName(in string) string {
	name := strings.ToLower(strings.TrimSpace(in))
	if name == "" {
		return "case"
	}
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-", "@", "-", "#", "-")
	return replacer.Replace(name)
}

func writeReport(path string, rep report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal corpus report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write corpus report: %w", err)
	}
	return nil
}
