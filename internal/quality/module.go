package quality

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"diffmind/internal/graph"
)

type corpusReport struct {
	GeneratedAtUTC string       `json:"generated_at_utc"`
	ManifestPath   string       `json:"manifest_path"`
	OutDir         string       `json:"out_dir"`
	Passed         int          `json:"passed"`
	Failed         int          `json:"failed"`
	Cases          []corpusCase `json:"cases"`
}

type corpusCase struct {
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	EntityCount  int            `json:"entity_count"`
	CountsByType map[string]int `json:"counts_by_type"`
	Domain       string         `json:"domain,omitempty"`
	Language     string         `json:"language,omitempty"`
	Framework    string         `json:"framework,omitempty"`
	FrameworkVer string         `json:"framework_version,omitempty"`
	DurationMS   int64          `json:"duration_ms,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Failures     []string       `json:"failures,omitempty"`
	Confidence   float64        `json:"confidence,omitempty"`
}

type goldenSummary struct {
	Cases []goldenCase `json:"cases"`
}

type goldenCase struct {
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	EntityCount  int            `json:"entity_count"`
	CountsByType map[string]int `json:"counts_by_type"`
}

type report struct {
	GeneratedAtUTC     string              `json:"generated_at_utc"`
	CorpusReport       string              `json:"corpus_report"`
	GoldenSummary      string              `json:"golden_summary,omitempty"`
	MergeQualityReport string              `json:"merge_quality_report,omitempty"`
	SampleSize         int                 `json:"sample_size"`
	Metrics            metrics             `json:"metrics"`
	ByDomain           []metricBucket      `json:"by_domain,omitempty"`
	ByLanguage         []metricBucket      `json:"by_language,omitempty"`
	ByFrameworkVer     []metricBucket      `json:"by_framework_version,omitempty"`
	MergeQuality       mergeQualitySummary `json:"merge_quality,omitempty"`
	Adversarial        advMetrics          `json:"adversarial"`
	Drift              driftMetrics        `json:"drift"`
	Benchmark          benchMetrics        `json:"benchmark"`
	RuntimePlan        runtimePlan         `json:"runtime_reconciliation"`
	Regressions        []regression        `json:"regressions"`
}

type mergeQualitySummary struct {
	Present                bool    `json:"present"`
	Passed                 bool    `json:"passed"`
	RepoProvenanceCoverage float64 `json:"repo_provenance_coverage"`
	UnresolvedRate         float64 `json:"unresolved_rate"`
	AmbiguousRate          float64 `json:"ambiguous_rate"`
	BenchmarkPresent       bool    `json:"benchmark_present"`
	BenchmarkPassed        bool    `json:"benchmark_passed"`
	LinkagePrecision       float64 `json:"linkage_precision"`
	LinkageRecall          float64 `json:"linkage_recall"`
	LinkageF1              float64 `json:"linkage_f1"`
	IdentityPrecision      float64 `json:"identity_precision"`
	IdentityRecall         float64 `json:"identity_recall"`
	IdentityF1             float64 `json:"identity_f1"`
}

type metrics struct {
	PassRate         float64 `json:"pass_rate"`
	Precision        float64 `json:"precision"`
	Recall           float64 `json:"recall"`
	F1               float64 `json:"f1"`
	CalibrationError float64 `json:"calibration_error"`
}

type metricBucket struct {
	Name             string  `json:"name"`
	Cases            int     `json:"cases"`
	PassRate         float64 `json:"pass_rate"`
	Precision        float64 `json:"precision"`
	Recall           float64 `json:"recall"`
	F1               float64 `json:"f1"`
	CalibrationError float64 `json:"calibration_error"`
}

type advMetrics struct {
	Cases    int     `json:"cases"`
	Passed   int     `json:"passed"`
	PassRate float64 `json:"pass_rate"`
}

type driftMetrics struct {
	Cases     int     `json:"cases"`
	Detected  int     `json:"detected"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
}

type benchMetrics struct {
	Cases        int   `json:"cases"`
	P50Duration  int64 `json:"p50_duration_ms"`
	P95Duration  int64 `json:"p95_duration_ms"`
	MaxDuration  int64 `json:"max_duration_ms"`
	AvgDuration  int64 `json:"avg_duration_ms"`
	TotalRuntime int64 `json:"total_runtime_ms"`
}

type runtimePlan struct {
	ContractVersion string   `json:"contract_version"`
	Phase           string   `json:"phase"`
	Enabled         bool     `json:"enabled"`
	PublishBlocking bool     `json:"publish_blocking"`
	InputSignals    []string `json:"input_signals"`
	MatchStrategy   string   `json:"match_strategy"`
	OutputStates    []string `json:"output_states"`
}

type regression struct {
	CaseName  string   `json:"case_name"`
	Severity  string   `json:"severity"`
	Domain    string   `json:"domain,omitempty"`
	Language  string   `json:"language,omitempty"`
	Failures  []string `json:"failures,omitempty"`
	WasStatus string   `json:"was_status"`
	NowStatus string   `json:"now_status"`
}

type gatePolicy struct {
	Thresholds struct {
		PassRate                      float64 `json:"pass_rate"`
		Precision                     float64 `json:"precision"`
		Recall                        float64 `json:"recall"`
		F1                            float64 `json:"f1"`
		CalibrationErrorMax           float64 `json:"calibration_error_max"`
		AdversarialPassRate           float64 `json:"adversarial_pass_rate"`
		FrameworkMatrixRate           float64 `json:"framework_matrix_pass_rate"`
		DriftPrecision                float64 `json:"drift_precision"`
		DriftRecall                   float64 `json:"drift_recall"`
		DriftF1                       float64 `json:"drift_f1"`
		BenchmarkP95MSMax             int64   `json:"benchmark_p95_ms_max"`
		MergeQualityRequired          bool    `json:"merge_quality_required"`
		MergeQualityBenchmarkRequired bool    `json:"merge_quality_benchmark_required"`
		MergeQualityLinkagePrecision  float64 `json:"merge_quality_linkage_precision"`
		MergeQualityLinkageRecall     float64 `json:"merge_quality_linkage_recall"`
		MergeQualityIdentityPrecision float64 `json:"merge_quality_identity_precision"`
		MergeQualityIdentityRecall    float64 `json:"merge_quality_identity_recall"`
	} `json:"thresholds"`
	Severity1 struct {
		RegressionsMax int `json:"regressions_max"`
	} `json:"severity1"`
}

type gateResult struct {
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures"`
}

func Run(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("quality subcommand is required: evaluate|gate|dashboard|triage|calibrate-baselines")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "evaluate":
		return runEvaluate(args[1:])
	case "gate":
		return runGate(args[1:])
	case "dashboard":
		return runDashboard(args[1:])
	case "triage":
		return runTriage(args[1:])
	case "calibrate-baselines":
		return runCalibrateBaselines(args[1:])
	default:
		return fmt.Errorf("unsupported quality subcommand %q", args[0])
	}
}

type evaluateOptions struct {
	CorpusPath        string
	GoldenPath        string
	MergeQualityPath  string
	GraphIndexPath    string
	ExpectedLinksPath string
	MergeQualityAuto  bool
	OutPath           string
	DashboardPath     string
	TriagePath        string
}

func runEvaluate(args []string) error {
	opts, err := parseEvaluateOptions(args)
	if err != nil {
		return err
	}
	if opts.MergeQualityAuto {
		refresh := false
		if _, statErr := os.Stat(opts.MergeQualityPath); os.IsNotExist(statErr) {
			refresh = true
		} else if strings.TrimSpace(opts.ExpectedLinksPath) != "" && mergeQualityReportMissingBenchmark(opts.MergeQualityPath) {
			refresh = true
		}
		if refresh {
			if _, assessErr := graph.Assess(context.Background(), graph.AssessRequest{
				IndexPath:       opts.GraphIndexPath,
				OutPath:         opts.MergeQualityPath,
				ExpectLinksPath: opts.ExpectedLinksPath,
			}); assessErr != nil {
				if strings.TrimSpace(opts.ExpectedLinksPath) != "" {
					return fmt.Errorf("auto-generate merge quality report: %w", assessErr)
				}
			}
		}
	}
	rep, err := evaluate(opts.CorpusPath, opts.GoldenPath, opts.MergeQualityPath)
	if err != nil {
		return err
	}
	if err := writeJSON(opts.OutPath, rep); err != nil {
		return err
	}
	if err := writeDashboard(opts.DashboardPath, rep); err != nil {
		return err
	}
	if err := writeTriage(opts.TriagePath, rep); err != nil {
		return err
	}
	fmt.Println(opts.OutPath)
	return nil
}

func parseEvaluateOptions(args []string) (evaluateOptions, error) {
	fs := flag.NewFlagSet("quality evaluate", flag.ContinueOnError)
	corpusPath := fs.String("corpus", filepath.Join(".diffmind", "corpus", "report.json"), "Path to corpus report")
	goldenPath := fs.String("golden", filepath.Join("corpus", "golden", "summary.json"), "Path to golden summary")
	mergeQualityPath := fs.String("merge-quality", filepath.Join(".diffmind", "graph", "merge_quality_report.json"), "Path to graph merge quality report")
	graphIndexPath := fs.String("graph-index", filepath.Join(".diffmind", "graph", "index.json"), "Path to graph index for merge-quality auto generation")
	expectedLinksPath := fs.String("merge-quality-expect-links", "", "Expected service links JSON passed to graph assess when auto-generating merge quality")
	mergeQualityAuto := fs.Bool("merge-quality-auto", true, "Auto-generate merge-quality report when missing")
	outPath := fs.String("out", filepath.Join(".diffmind", "quality", "report.json"), "Path to quality report")
	dashboardPath := fs.String("dashboard", filepath.Join(".diffmind", "quality", "dashboard.md"), "Path to quality dashboard markdown")
	triagePath := fs.String("triage", filepath.Join(".diffmind", "quality", "triage.md"), "Path to regression triage markdown")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return evaluateOptions{}, fmt.Errorf("parse quality evaluate flags: %w", err)
	}
	return evaluateOptions{
		CorpusPath:        strings.TrimSpace(*corpusPath),
		GoldenPath:        strings.TrimSpace(*goldenPath),
		MergeQualityPath:  strings.TrimSpace(*mergeQualityPath),
		GraphIndexPath:    strings.TrimSpace(*graphIndexPath),
		ExpectedLinksPath: strings.TrimSpace(*expectedLinksPath),
		MergeQualityAuto:  *mergeQualityAuto,
		OutPath:           strings.TrimSpace(*outPath),
		DashboardPath:     strings.TrimSpace(*dashboardPath),
		TriagePath:        strings.TrimSpace(*triagePath),
	}, nil
}

type gateOptions struct {
	ReportPath string
	PolicyPath string
	OutPath    string
}

func runGate(args []string) error {
	opts, err := parseGateOptions(args)
	if err != nil {
		return err
	}
	rep, err := readQualityReport(opts.ReportPath)
	if err != nil {
		return err
	}
	policy, err := readPolicy(opts.PolicyPath)
	if err != nil {
		return err
	}
	result := evaluateGate(rep, policy)
	if opts.OutPath != "" {
		if err := writeJSON(opts.OutPath, result); err != nil {
			return err
		}
	}
	if !result.Passed {
		return fmt.Errorf("quality gate failed: %s", strings.Join(result.Failures, "; "))
	}
	fmt.Println("quality gate passed")
	return nil
}

func parseGateOptions(args []string) (gateOptions, error) {
	fs := flag.NewFlagSet("quality gate", flag.ContinueOnError)
	reportPath := fs.String("report", filepath.Join(".diffmind", "quality", "report.json"), "Path to evaluated quality report")
	policyPath := fs.String("policy", filepath.Join("quality", "policy.json"), "Path to quality gate policy")
	outPath := fs.String("out", filepath.Join(".diffmind", "quality", "gate_result.json"), "Path to gate result")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return gateOptions{}, fmt.Errorf("parse quality gate flags: %w", err)
	}
	return gateOptions{ReportPath: strings.TrimSpace(*reportPath), PolicyPath: strings.TrimSpace(*policyPath), OutPath: strings.TrimSpace(*outPath)}, nil
}

func runDashboard(args []string) error {
	fs := flag.NewFlagSet("quality dashboard", flag.ContinueOnError)
	reportPath := fs.String("report", filepath.Join(".diffmind", "quality", "report.json"), "Path to quality report")
	outPath := fs.String("out", filepath.Join(".diffmind", "quality", "dashboard.md"), "Path to dashboard markdown")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return fmt.Errorf("parse quality dashboard flags: %w", err)
	}
	rep, err := readQualityReport(strings.TrimSpace(*reportPath))
	if err != nil {
		return err
	}
	if err := writeDashboard(strings.TrimSpace(*outPath), rep); err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(*outPath))
	return nil
}

func runTriage(args []string) error {
	fs := flag.NewFlagSet("quality triage", flag.ContinueOnError)
	reportPath := fs.String("report", filepath.Join(".diffmind", "quality", "report.json"), "Path to quality report")
	outPath := fs.String("out", filepath.Join(".diffmind", "quality", "triage.md"), "Path to triage markdown")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return fmt.Errorf("parse quality triage flags: %w", err)
	}
	rep, err := readQualityReport(strings.TrimSpace(*reportPath))
	if err != nil {
		return err
	}
	if err := writeTriage(strings.TrimSpace(*outPath), rep); err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(*outPath))
	return nil
}

type calibrateOptions struct {
	SummaryPath          string
	SummariesCSV         string
	OutPath              string
	MinSamples           int
	IncludeFixtures      bool
	Percentile           float64
	MetricMargin         float64
	TaskMargin           float64
	SectionMargin        float64
	FloorPrecision       float64
	FloorRecall          float64
	FloorF1              float64
	FloorPassRate        float64
	FloorTaskPassRate    float64
	FloorSectionCoverage float64
}

type sourceBaselineRule struct {
	MinPrecision            float64 `json:"min_precision"`
	MinRecall               float64 `json:"min_recall"`
	MinF1                   float64 `json:"min_f1"`
	MinPassRate             float64 `json:"min_pass_rate"`
	MinTaskPassRate         float64 `json:"min_task_pass_rate"`
	MinSectionCoverageRatio float64 `json:"min_section_coverage_ratio"`
	RequireContractGate     bool    `json:"require_contract_gate,omitempty"`
}

type sourceBaselineCalibrationPolicy struct {
	Default sourceBaselineRule            `json:"default"`
	Sources map[string]sourceBaselineRule `json:"sources"`
	Meta    calibrationMeta               `json:"meta"`
}

type calibrationMeta struct {
	GeneratedAtUTC  string  `json:"generated_at_utc"`
	SummaryPath     string  `json:"summary_path"`
	IncludeFixtures bool    `json:"include_fixtures"`
	MinSamples      int     `json:"min_samples"`
	Percentile      float64 `json:"percentile"`
	SourceCount     int     `json:"source_count"`
	RunCount        int     `json:"run_count"`
}

type releaseGateSummary struct {
	Runs []releaseGateRun `json:"runs"`
}

type releaseGateRun struct {
	SourceID   string `json:"source_id"`
	SourceType string `json:"source_type"`
	Gates      struct {
		ContractGateApplicable bool `json:"contract_gate_applicable"`
	} `json:"gates"`
	Scorecard struct {
		Accuracy struct {
			PassRate  float64 `json:"pass_rate"`
			Precision float64 `json:"precision"`
			Recall    float64 `json:"recall"`
			F1        float64 `json:"f1"`
		} `json:"accuracy"`
		Completeness struct {
			SectionCoverageRatio float64 `json:"section_coverage_ratio"`
		} `json:"completeness"`
		TaskPassRate float64 `json:"task_pass_rate"`
	} `json:"scorecard"`
}

func runCalibrateBaselines(args []string) error {
	opts, err := parseCalibrateOptions(args)
	if err != nil {
		return err
	}
	summaryPaths := collectCalibrateSummaryPaths(opts)
	if len(summaryPaths) == 0 {
		return errors.New("quality calibrate-baselines requires at least one summary path")
	}
	merged := releaseGateSummary{Runs: []releaseGateRun{}}
	for _, path := range summaryPaths {
		summary, readErr := readReleaseGateSummary(path)
		if readErr != nil {
			return readErr
		}
		merged.Runs = append(merged.Runs, summary.Runs...)
	}
	opts.SummaryPath = strings.Join(summaryPaths, ",")
	policy, err := calibrateBaselines(merged, opts)
	if err != nil {
		return err
	}
	if err := writeJSON(opts.OutPath, policy); err != nil {
		return err
	}
	fmt.Println(opts.OutPath)
	return nil
}

func parseCalibrateOptions(args []string) (calibrateOptions, error) {
	fs := flag.NewFlagSet("quality calibrate-baselines", flag.ContinueOnError)
	summaryPath := fs.String("summary", filepath.Join(".diffmind", "release-gate-m6", "summary.json"), "Path to release-gate summary.json")
	summariesCSV := fs.String("summaries", "", "Comma-separated list of release-gate summary paths to merge for calibration")
	outPath := fs.String("out", filepath.Join("quality", "source_baselines.e2e.json"), "Output path for calibrated source baseline policy")
	minSamples := fs.Int("min-samples", 1, "Minimum runs per source to emit a source-specific baseline")
	includeFixtures := fs.Bool("include-fixtures", false, "Include fixture runs when calibrating source baselines")
	percentile := fs.Float64("percentile", 0.25, "Percentile to use for observed metric baseline (0..1)")
	metricMargin := fs.Float64("metric-margin", 0.03, "Margin subtracted from precision/recall/f1/pass_rate percentiles")
	taskMargin := fs.Float64("task-margin", 0.02, "Margin subtracted from task_pass_rate percentile")
	sectionMargin := fs.Float64("section-margin", 0.05, "Margin subtracted from section_coverage_ratio percentile")
	floorPrecision := fs.Float64("floor-precision", 0.40, "Lower bound for min_precision")
	floorRecall := fs.Float64("floor-recall", 0.40, "Lower bound for min_recall")
	floorF1 := fs.Float64("floor-f1", 0.40, "Lower bound for min_f1")
	floorPassRate := fs.Float64("floor-pass-rate", 0.50, "Lower bound for min_pass_rate")
	floorTaskPassRate := fs.Float64("floor-task-pass-rate", 0.95, "Lower bound for min_task_pass_rate")
	floorSectionCoverage := fs.Float64("floor-section-coverage", 0.60, "Lower bound for min_section_coverage_ratio")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return calibrateOptions{}, fmt.Errorf("parse quality calibrate-baselines flags: %w", err)
	}
	return calibrateOptions{
		SummaryPath:          strings.TrimSpace(*summaryPath),
		SummariesCSV:         strings.TrimSpace(*summariesCSV),
		OutPath:              strings.TrimSpace(*outPath),
		MinSamples:           maxInt(*minSamples, 1),
		IncludeFixtures:      *includeFixtures,
		Percentile:           clamp01(*percentile),
		MetricMargin:         clamp01(*metricMargin),
		TaskMargin:           clamp01(*taskMargin),
		SectionMargin:        clamp01(*sectionMargin),
		FloorPrecision:       clamp01(*floorPrecision),
		FloorRecall:          clamp01(*floorRecall),
		FloorF1:              clamp01(*floorF1),
		FloorPassRate:        clamp01(*floorPassRate),
		FloorTaskPassRate:    clamp01(*floorTaskPassRate),
		FloorSectionCoverage: clamp01(*floorSectionCoverage),
	}, nil
}

func collectCalibrateSummaryPaths(opts calibrateOptions) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	appendPath := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, exists := seen[p]; exists {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	appendPath(opts.SummaryPath)
	for _, part := range strings.Split(opts.SummariesCSV, ",") {
		appendPath(part)
	}
	return out
}

func calibrateBaselines(summary releaseGateSummary, opts calibrateOptions) (sourceBaselineCalibrationPolicy, error) {
	grouped := map[string][]releaseGateRun{}
	eligibleRuns := make([]releaseGateRun, 0, len(summary.Runs))
	for _, run := range summary.Runs {
		sourceID := strings.TrimSpace(run.SourceID)
		if sourceID == "" {
			continue
		}
		isFixture := strings.EqualFold(strings.TrimSpace(run.SourceType), "fixture")
		if isFixture && !opts.IncludeFixtures {
			continue
		}
		eligibleRuns = append(eligibleRuns, run)
		grouped[sourceID] = append(grouped[sourceID], run)
	}
	if len(eligibleRuns) == 0 {
		return sourceBaselineCalibrationPolicy{}, errors.New("no eligible runs found in release-gate summary for baseline calibration")
	}

	sources := map[string]sourceBaselineRule{}
	for sourceID, runs := range grouped {
		if len(runs) < opts.MinSamples {
			continue
		}
		sources[sourceID] = buildRuleFromRuns(runs, opts)
	}
	if len(sources) == 0 {
		return sourceBaselineCalibrationPolicy{}, errors.New("no sources met min-samples requirement for baseline calibration")
	}
	defaultRule := buildRuleFromRuns(eligibleRuns, opts)
	defaultRule.RequireContractGate = false

	return sourceBaselineCalibrationPolicy{
		Default: defaultRule,
		Sources: sources,
		Meta: calibrationMeta{
			GeneratedAtUTC:  time.Now().UTC().Format(time.RFC3339),
			SummaryPath:     strings.TrimSpace(opts.SummaryPath),
			IncludeFixtures: opts.IncludeFixtures,
			MinSamples:      opts.MinSamples,
			Percentile:      opts.Percentile,
			SourceCount:     len(sources),
			RunCount:        len(eligibleRuns),
		},
	}, nil
}

func buildRuleFromRuns(runs []releaseGateRun, opts calibrateOptions) sourceBaselineRule {
	precision := make([]float64, 0, len(runs))
	recall := make([]float64, 0, len(runs))
	f1 := make([]float64, 0, len(runs))
	passRate := make([]float64, 0, len(runs))
	taskRate := make([]float64, 0, len(runs))
	sectionCoverage := make([]float64, 0, len(runs))
	requireContract := false
	for _, run := range runs {
		precision = append(precision, clamp01(run.Scorecard.Accuracy.Precision))
		recall = append(recall, clamp01(run.Scorecard.Accuracy.Recall))
		f1 = append(f1, clamp01(run.Scorecard.Accuracy.F1))
		passRate = append(passRate, clamp01(run.Scorecard.Accuracy.PassRate))
		taskRate = append(taskRate, clamp01(run.Scorecard.TaskPassRate))
		sectionCoverage = append(sectionCoverage, clamp01(run.Scorecard.Completeness.SectionCoverageRatio))
		requireContract = requireContract || run.Gates.ContractGateApplicable
	}
	return sourceBaselineRule{
		MinPrecision:            calibrateMetric(precision, opts.Percentile, opts.MetricMargin, opts.FloorPrecision),
		MinRecall:               calibrateMetric(recall, opts.Percentile, opts.MetricMargin, opts.FloorRecall),
		MinF1:                   calibrateMetric(f1, opts.Percentile, opts.MetricMargin, opts.FloorF1),
		MinPassRate:             calibrateMetric(passRate, opts.Percentile, opts.MetricMargin, opts.FloorPassRate),
		MinTaskPassRate:         calibrateMetric(taskRate, opts.Percentile, opts.TaskMargin, opts.FloorTaskPassRate),
		MinSectionCoverageRatio: calibrateMetric(sectionCoverage, opts.Percentile, opts.SectionMargin, opts.FloorSectionCoverage),
		RequireContractGate:     requireContract,
	}
}

func calibrateMetric(values []float64, percentile float64, margin float64, floor float64) float64 {
	if len(values) == 0 {
		return clamp01(floor)
	}
	p := percentileFloat(values, percentile)
	v := p - margin
	if v < floor {
		v = floor
	}
	return clamp01(v)
}

func percentileFloat(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		p = 0
	}
	if p >= 1 {
		p = 1
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	idx := int(math.Round(float64(len(sorted)-1) * p))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func evaluate(corpusPath string, goldenPath string, mergeQualityPath string) (report, error) {
	corpus, err := readCorpusReport(corpusPath)
	if err != nil {
		return report{}, err
	}
	golden := goldenSummary{}
	if strings.TrimSpace(goldenPath) != "" {
		if g, err := readGoldenSummary(goldenPath); err == nil {
			golden = g
		}
	}

	result := report{
		GeneratedAtUTC:     time.Now().UTC().Format(time.RFC3339),
		CorpusReport:       corpusPath,
		GoldenSummary:      goldenPath,
		MergeQualityReport: mergeQualityPath,
		SampleSize:         len(corpus.Cases),
		Regressions:        collectRegressions(corpus.Cases, golden.Cases),
	}
	result.Metrics = calcMetrics(corpus.Cases, golden.Cases)
	result.ByDomain = calcBuckets(corpus.Cases, golden.Cases, func(c corpusCase) string { return strings.TrimSpace(c.Domain) })
	result.ByLanguage = calcBuckets(corpus.Cases, golden.Cases, func(c corpusCase) string { return strings.TrimSpace(c.Language) })
	result.ByFrameworkVer = calcBuckets(corpus.Cases, golden.Cases, func(c corpusCase) string {
		name := strings.TrimSpace(c.Framework)
		ver := strings.TrimSpace(c.FrameworkVer)
		if name == "" && ver == "" {
			return ""
		}
		if ver == "" {
			return name
		}
		if name == "" {
			return "unknown@" + ver
		}
		return name + "@" + ver
	})
	result.Adversarial = calcAdversarial(corpus.Cases)
	result.Drift = calcDrift(corpus.Cases)
	result.Benchmark = calcBenchmark(corpus.Cases)
	result.MergeQuality = readMergeQualitySummary(mergeQualityPath)
	result.RuntimePlan = defaultRuntimePlan()
	return result, nil
}

func calcMetrics(cases []corpusCase, golden []goldenCase) metrics {
	passCount := 0
	for _, c := range cases {
		if strings.EqualFold(c.Status, "passed") {
			passCount++
		}
	}
	precision, recall, f1 := calcPRF(cases, golden)
	return metrics{
		PassRate:         ratio(passCount, len(cases)),
		Precision:        precision,
		Recall:           recall,
		F1:               f1,
		CalibrationError: calcCalibrationError(cases),
	}
}

func calcBuckets(cases []corpusCase, golden []goldenCase, keyFn func(corpusCase) string) []metricBucket {
	byKey := map[string][]corpusCase{}
	for _, c := range cases {
		k := keyFn(c)
		if k == "" {
			continue
		}
		byKey[k] = append(byKey[k], c)
	}
	goldByName := map[string]goldenCase{}
	for _, g := range golden {
		goldByName[g.Name] = g
	}
	out := make([]metricBucket, 0, len(byKey))
	for k, subset := range byKey {
		subGold := make([]goldenCase, 0, len(subset))
		for _, c := range subset {
			if g, ok := goldByName[c.Name]; ok {
				subGold = append(subGold, g)
			}
		}
		m := calcMetrics(subset, subGold)
		out = append(out, metricBucket{
			Name:             k,
			Cases:            len(subset),
			PassRate:         m.PassRate,
			Precision:        m.Precision,
			Recall:           m.Recall,
			F1:               m.F1,
			CalibrationError: m.CalibrationError,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func calcAdversarial(cases []corpusCase) advMetrics {
	total := 0
	passed := 0
	for _, c := range cases {
		if !hasTag(c.Tags, "adversarial") && !hasTag(c.Tags, "edge_case") {
			continue
		}
		total++
		if strings.EqualFold(c.Status, "passed") {
			passed++
		}
	}
	return advMetrics{Cases: total, Passed: passed, PassRate: ratio(passed, total)}
}

func calcDrift(cases []corpusCase) driftMetrics {
	var tp, fp, fn int
	tracked := 0
	for _, c := range cases {
		expected := hasTag(c.Tags, "drift_expected")
		predicted := hasTag(c.Tags, "drift_detected")
		if !predicted {
			for _, f := range c.Failures {
				if strings.Contains(strings.ToLower(strings.TrimSpace(f)), "drift") {
					predicted = true
					break
				}
			}
		}
		if !expected && !predicted {
			continue
		}
		tracked++
		switch {
		case expected && predicted:
			tp++
		case !expected && predicted:
			fp++
		case expected && !predicted:
			fn++
		}
	}

	precision := 1.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	recall := 1.0
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return driftMetrics{
		Cases:     tracked,
		Detected:  tp + fp,
		Precision: precision,
		Recall:    recall,
		F1:        f1,
	}
}

func calcBenchmark(cases []corpusCase) benchMetrics {
	durations := make([]int64, 0, len(cases))
	var total int64
	var max int64
	for _, c := range cases {
		if c.DurationMS <= 0 {
			continue
		}
		d := c.DurationMS
		durations = append(durations, d)
		total += d
		if d > max {
			max = d
		}
	}
	if len(durations) == 0 {
		return benchMetrics{}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return benchMetrics{
		Cases:        len(durations),
		P50Duration:  percentileDuration(durations, 0.50),
		P95Duration:  percentileDuration(durations, 0.95),
		MaxDuration:  max,
		AvgDuration:  total / int64(len(durations)),
		TotalRuntime: total,
	}
}

func percentileDuration(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func defaultRuntimePlan() runtimePlan {
	return runtimePlan{
		ContractVersion: "v0",
		Phase:           "phase2-preparation",
		Enabled:         false,
		PublishBlocking: false,
		InputSignals: []string{
			"api_gateway_access_logs",
			"queue_broker_delivery_events",
			"runtime_db_query_observations",
			"service_dependency_telemetry",
		},
		MatchStrategy: "canonical_id_and_evidence_overlap",
		OutputStates: []string{
			"confirmed",
			"contradicted",
			"missing_runtime_signal",
			"runtime_only_unmapped",
		},
	}
}

func collectRegressions(cases []corpusCase, golden []goldenCase) []regression {
	goldByName := map[string]goldenCase{}
	for _, g := range golden {
		goldByName[g.Name] = g
	}
	out := make([]regression, 0)
	for _, c := range cases {
		g, ok := goldByName[c.Name]
		if !ok {
			continue
		}
		if strings.EqualFold(g.Status, "passed") && strings.EqualFold(c.Status, "failed") {
			sev := "sev2"
			if hasTag(c.Tags, "critical") || hasTag(c.Tags, "sev1") {
				sev = "sev1"
			}
			out = append(out, regression{
				CaseName:  c.Name,
				Severity:  sev,
				Domain:    c.Domain,
				Language:  c.Language,
				Failures:  append([]string(nil), c.Failures...),
				WasStatus: g.Status,
				NowStatus: c.Status,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity == out[j].Severity {
			return out[i].CaseName < out[j].CaseName
		}
		return out[i].Severity < out[j].Severity
	})
	return out
}

func calcPRF(cases []corpusCase, golden []goldenCase) (float64, float64, float64) {
	goldByName := map[string]goldenCase{}
	for _, g := range golden {
		goldByName[g.Name] = g
	}
	var tp, fp, fn float64
	for _, c := range cases {
		g, ok := goldByName[c.Name]
		if !ok {
			continue
		}
		types := map[string]struct{}{}
		for k := range c.CountsByType {
			types[k] = struct{}{}
		}
		for k := range g.CountsByType {
			types[k] = struct{}{}
		}
		for typ := range types {
			actual := c.CountsByType[typ]
			expected := g.CountsByType[typ]
			if actual < expected {
				tp += float64(actual)
				fn += float64(expected - actual)
			} else {
				tp += float64(expected)
				fp += float64(actual - expected)
			}
		}
	}
	precision := 1.0
	if tp+fp > 0 {
		precision = tp / (tp + fp)
	}
	recall := 1.0
	if tp+fn > 0 {
		recall = tp / (tp + fn)
	}
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return precision, recall, f1
}

func calcCalibrationError(cases []corpusCase) float64 {
	if len(cases) == 0 {
		return 0
	}
	type bin struct {
		n          int
		sumConf    float64
		sumCorrect float64
	}
	bins := make([]bin, 10)
	for _, c := range cases {
		conf := c.Confidence
		if conf < 0 {
			conf = 0
		}
		if conf > 1 {
			conf = 1
		}
		idx := int(conf * 10)
		if idx >= 10 {
			idx = 9
		}
		bins[idx].n++
		bins[idx].sumConf += conf
		if strings.EqualFold(c.Status, "passed") {
			bins[idx].sumCorrect += 1
		}
	}
	total := float64(len(cases))
	ece := 0.0
	for _, b := range bins {
		if b.n == 0 {
			continue
		}
		avgConf := b.sumConf / float64(b.n)
		acc := b.sumCorrect / float64(b.n)
		gap := acc - avgConf
		if gap < 0 {
			gap = -gap
		}
		ece += (float64(b.n) / total) * gap
	}
	return ece
}

func evaluateGate(rep report, policy gatePolicy) gateResult {
	failures := make([]string, 0)
	if rep.Metrics.PassRate < policy.Thresholds.PassRate {
		failures = append(failures, fmt.Sprintf("pass_rate %.4f < %.4f", rep.Metrics.PassRate, policy.Thresholds.PassRate))
	}
	if rep.Metrics.Precision < policy.Thresholds.Precision {
		failures = append(failures, fmt.Sprintf("precision %.4f < %.4f", rep.Metrics.Precision, policy.Thresholds.Precision))
	}
	if rep.Metrics.Recall < policy.Thresholds.Recall {
		failures = append(failures, fmt.Sprintf("recall %.4f < %.4f", rep.Metrics.Recall, policy.Thresholds.Recall))
	}
	if rep.Metrics.F1 < policy.Thresholds.F1 {
		failures = append(failures, fmt.Sprintf("f1 %.4f < %.4f", rep.Metrics.F1, policy.Thresholds.F1))
	}
	if rep.Metrics.CalibrationError > policy.Thresholds.CalibrationErrorMax {
		failures = append(failures, fmt.Sprintf("calibration_error %.4f > %.4f", rep.Metrics.CalibrationError, policy.Thresholds.CalibrationErrorMax))
	}
	if rep.Adversarial.Cases > 0 && rep.Adversarial.PassRate < policy.Thresholds.AdversarialPassRate {
		failures = append(failures, fmt.Sprintf("adversarial_pass_rate %.4f < %.4f", rep.Adversarial.PassRate, policy.Thresholds.AdversarialPassRate))
	}
	if policy.Thresholds.FrameworkMatrixRate > 0 && len(rep.ByFrameworkVer) > 0 {
		matrixCases := 0
		matrixPassed := 0
		for _, b := range rep.ByFrameworkVer {
			matrixCases += b.Cases
			matrixPassed += int(float64(b.Cases)*b.PassRate + 0.5)
		}
		matrixRate := ratio(matrixPassed, matrixCases)
		if matrixRate < policy.Thresholds.FrameworkMatrixRate {
			failures = append(failures, fmt.Sprintf("framework_matrix_pass_rate %.4f < %.4f", matrixRate, policy.Thresholds.FrameworkMatrixRate))
		}
	}
	if policy.Thresholds.DriftPrecision > 0 && rep.Drift.Cases > 0 && rep.Drift.Precision < policy.Thresholds.DriftPrecision {
		failures = append(failures, fmt.Sprintf("drift_precision %.4f < %.4f", rep.Drift.Precision, policy.Thresholds.DriftPrecision))
	}
	if policy.Thresholds.DriftRecall > 0 && rep.Drift.Cases > 0 && rep.Drift.Recall < policy.Thresholds.DriftRecall {
		failures = append(failures, fmt.Sprintf("drift_recall %.4f < %.4f", rep.Drift.Recall, policy.Thresholds.DriftRecall))
	}
	if policy.Thresholds.DriftF1 > 0 && rep.Drift.Cases > 0 && rep.Drift.F1 < policy.Thresholds.DriftF1 {
		failures = append(failures, fmt.Sprintf("drift_f1 %.4f < %.4f", rep.Drift.F1, policy.Thresholds.DriftF1))
	}
	if policy.Thresholds.BenchmarkP95MSMax > 0 && rep.Benchmark.Cases > 0 && rep.Benchmark.P95Duration > policy.Thresholds.BenchmarkP95MSMax {
		failures = append(failures, fmt.Sprintf("benchmark_p95_ms %d > %d", rep.Benchmark.P95Duration, policy.Thresholds.BenchmarkP95MSMax))
	}
	if rep.MergeQuality.Present && !rep.MergeQuality.Passed {
		failures = append(failures, "merge_quality gate failed")
	}
	if policy.Thresholds.MergeQualityRequired && !rep.MergeQuality.Present {
		failures = append(failures, "merge_quality report missing")
	}
	if policy.Thresholds.MergeQualityBenchmarkRequired {
		if !rep.MergeQuality.Present {
			failures = append(failures, "merge_quality report missing for benchmark requirement")
		} else if !rep.MergeQuality.BenchmarkPresent {
			failures = append(failures, "merge_quality benchmark missing")
		} else if !rep.MergeQuality.BenchmarkPassed {
			failures = append(failures, "merge_quality benchmark gate failed")
		}
	}
	if policy.Thresholds.MergeQualityLinkagePrecision > 0 {
		if !rep.MergeQuality.Present || !rep.MergeQuality.BenchmarkPresent {
			failures = append(failures, "merge_quality linkage precision unavailable")
		} else if rep.MergeQuality.LinkagePrecision < policy.Thresholds.MergeQualityLinkagePrecision {
			failures = append(failures, fmt.Sprintf("merge_quality linkage_precision %.4f < %.4f", rep.MergeQuality.LinkagePrecision, policy.Thresholds.MergeQualityLinkagePrecision))
		}
	}
	if policy.Thresholds.MergeQualityLinkageRecall > 0 {
		if !rep.MergeQuality.Present || !rep.MergeQuality.BenchmarkPresent {
			failures = append(failures, "merge_quality linkage recall unavailable")
		} else if rep.MergeQuality.LinkageRecall < policy.Thresholds.MergeQualityLinkageRecall {
			failures = append(failures, fmt.Sprintf("merge_quality linkage_recall %.4f < %.4f", rep.MergeQuality.LinkageRecall, policy.Thresholds.MergeQualityLinkageRecall))
		}
	}
	if policy.Thresholds.MergeQualityIdentityPrecision > 0 {
		if !rep.MergeQuality.Present || !rep.MergeQuality.BenchmarkPresent {
			failures = append(failures, "merge_quality identity precision unavailable")
		} else if rep.MergeQuality.IdentityPrecision < policy.Thresholds.MergeQualityIdentityPrecision {
			failures = append(failures, fmt.Sprintf("merge_quality identity_precision %.4f < %.4f", rep.MergeQuality.IdentityPrecision, policy.Thresholds.MergeQualityIdentityPrecision))
		}
	}
	if policy.Thresholds.MergeQualityIdentityRecall > 0 {
		if !rep.MergeQuality.Present || !rep.MergeQuality.BenchmarkPresent {
			failures = append(failures, "merge_quality identity recall unavailable")
		} else if rep.MergeQuality.IdentityRecall < policy.Thresholds.MergeQualityIdentityRecall {
			failures = append(failures, fmt.Sprintf("merge_quality identity_recall %.4f < %.4f", rep.MergeQuality.IdentityRecall, policy.Thresholds.MergeQualityIdentityRecall))
		}
	}
	sev1 := 0
	for _, r := range rep.Regressions {
		if strings.EqualFold(r.Severity, "sev1") {
			sev1++
		}
	}
	if sev1 > policy.Severity1.RegressionsMax {
		failures = append(failures, fmt.Sprintf("sev1_regressions %d > %d", sev1, policy.Severity1.RegressionsMax))
	}
	return gateResult{Passed: len(failures) == 0, Failures: failures}
}

func writeDashboard(path string, rep report) error {
	lines := []string{
		"# Quality Dashboard",
		"",
		fmt.Sprintf("Generated: %s", rep.GeneratedAtUTC),
		fmt.Sprintf("Sample size: %d", rep.SampleSize),
		"",
		"## Overall",
		fmt.Sprintf("- pass_rate: %.4f", rep.Metrics.PassRate),
		fmt.Sprintf("- precision: %.4f", rep.Metrics.Precision),
		fmt.Sprintf("- recall: %.4f", rep.Metrics.Recall),
		fmt.Sprintf("- f1: %.4f", rep.Metrics.F1),
		fmt.Sprintf("- calibration_error: %.4f", rep.Metrics.CalibrationError),
		fmt.Sprintf("- adversarial_pass_rate: %.4f (%d/%d)", rep.Adversarial.PassRate, rep.Adversarial.Passed, rep.Adversarial.Cases),
		fmt.Sprintf("- drift_precision: %.4f", rep.Drift.Precision),
		fmt.Sprintf("- drift_recall: %.4f", rep.Drift.Recall),
		fmt.Sprintf("- drift_f1: %.4f", rep.Drift.F1),
		fmt.Sprintf("- benchmark_p95_ms: %d", rep.Benchmark.P95Duration),
		fmt.Sprintf("- merge_quality_present: %t", rep.MergeQuality.Present),
		fmt.Sprintf("- merge_quality_passed: %t", rep.MergeQuality.Passed),
		fmt.Sprintf("- merge_quality_repo_provenance_coverage: %.4f", rep.MergeQuality.RepoProvenanceCoverage),
		fmt.Sprintf("- merge_quality_unresolved_rate: %.4f", rep.MergeQuality.UnresolvedRate),
		fmt.Sprintf("- merge_quality_ambiguous_rate: %.4f", rep.MergeQuality.AmbiguousRate),
		fmt.Sprintf("- merge_quality_benchmark_present: %t", rep.MergeQuality.BenchmarkPresent),
		fmt.Sprintf("- merge_quality_benchmark_passed: %t", rep.MergeQuality.BenchmarkPassed),
		fmt.Sprintf("- merge_quality_linkage_precision: %.4f", rep.MergeQuality.LinkagePrecision),
		fmt.Sprintf("- merge_quality_linkage_recall: %.4f", rep.MergeQuality.LinkageRecall),
		fmt.Sprintf("- merge_quality_linkage_f1: %.4f", rep.MergeQuality.LinkageF1),
		fmt.Sprintf("- merge_quality_identity_precision: %.4f", rep.MergeQuality.IdentityPrecision),
		fmt.Sprintf("- merge_quality_identity_recall: %.4f", rep.MergeQuality.IdentityRecall),
		fmt.Sprintf("- merge_quality_identity_f1: %.4f", rep.MergeQuality.IdentityF1),
		"",
		"## Regressions",
		fmt.Sprintf("- total: %d", len(rep.Regressions)),
	}
	for _, r := range rep.Regressions {
		lines = append(lines, fmt.Sprintf("- [%s] %s", r.Severity, r.CaseName))
	}
	lines = append(lines, "", "## By Domain")
	for _, b := range rep.ByDomain {
		lines = append(lines, fmt.Sprintf("- %s: pass_rate=%.4f f1=%.4f calibration_error=%.4f", b.Name, b.PassRate, b.F1, b.CalibrationError))
	}
	lines = append(lines, "", "## By Language")
	for _, b := range rep.ByLanguage {
		lines = append(lines, fmt.Sprintf("- %s: pass_rate=%.4f f1=%.4f calibration_error=%.4f", b.Name, b.PassRate, b.F1, b.CalibrationError))
	}
	lines = append(lines, "", "## By Framework Version")
	for _, b := range rep.ByFrameworkVer {
		lines = append(lines, fmt.Sprintf("- %s: pass_rate=%.4f f1=%.4f calibration_error=%.4f", b.Name, b.PassRate, b.F1, b.CalibrationError))
	}
	lines = append(lines, "", "## Runtime Reconciliation (Phase-2 Plan)")
	lines = append(lines, fmt.Sprintf("- contract_version: %s", rep.RuntimePlan.ContractVersion))
	lines = append(lines, fmt.Sprintf("- phase: %s", rep.RuntimePlan.Phase))
	lines = append(lines, fmt.Sprintf("- enabled: %t", rep.RuntimePlan.Enabled))
	lines = append(lines, fmt.Sprintf("- publish_blocking: %t", rep.RuntimePlan.PublishBlocking))
	return writeText(path, strings.Join(lines, "\n")+"\n")
}

func writeTriage(path string, rep report) error {
	lines := []string{
		"# Quality Regression Triage",
		"",
		fmt.Sprintf("Generated: %s", rep.GeneratedAtUTC),
		"",
		"## Severity-1",
	}
	for _, r := range rep.Regressions {
		if !strings.EqualFold(r.Severity, "sev1") {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s/%s): %s", r.CaseName, emptyFallback(r.Domain, "unknown-domain"), emptyFallback(r.Language, "unknown-language"), strings.Join(r.Failures, "; ")))
	}
	lines = append(lines, "", "## Severity-2")
	for _, r := range rep.Regressions {
		if strings.EqualFold(r.Severity, "sev1") {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s/%s): %s", r.CaseName, emptyFallback(r.Domain, "unknown-domain"), emptyFallback(r.Language, "unknown-language"), strings.Join(r.Failures, "; ")))
	}
	lines = append(lines, "", "## Runbook", "1. Confirm regression reproducibility by rerunning corpus case.", "2. Inspect parser/analyzer/entity-count deltas against golden case.", "3. Patch extractor/resolver and rerun quality evaluate + quality gate.", "4. Do not release until severity-1 regressions are zero.")
	return writeText(path, strings.Join(lines, "\n")+"\n")
}

func emptyFallback(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func hasTag(tags []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, t := range tags {
		if strings.ToLower(strings.TrimSpace(t)) == target {
			return true
		}
	}
	return false
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func readCorpusReport(path string) (corpusReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return corpusReport{}, fmt.Errorf("read corpus report %s: %w", path, err)
	}
	var rep corpusReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return corpusReport{}, fmt.Errorf("decode corpus report %s: %w", path, err)
	}
	return rep, nil
}

func readGoldenSummary(path string) (goldenSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return goldenSummary{}, fmt.Errorf("read golden summary %s: %w", path, err)
	}
	var g goldenSummary
	if err := json.Unmarshal(data, &g); err != nil {
		return goldenSummary{}, fmt.Errorf("decode golden summary %s: %w", path, err)
	}
	return g, nil
}

func readQualityReport(path string) (report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report{}, fmt.Errorf("read quality report %s: %w", path, err)
	}
	var rep report
	if err := json.Unmarshal(data, &rep); err != nil {
		return report{}, fmt.Errorf("decode quality report %s: %w", path, err)
	}
	return rep, nil
}

func readPolicy(path string) (gatePolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return gatePolicy{}, fmt.Errorf("read quality policy %s: %w", path, err)
	}
	var p gatePolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return gatePolicy{}, fmt.Errorf("decode quality policy %s: %w", path, err)
	}
	return p, nil
}

func readReleaseGateSummary(path string) (releaseGateSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return releaseGateSummary{}, fmt.Errorf("read release gate summary %s: %w", path, err)
	}
	var summary releaseGateSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return releaseGateSummary{}, fmt.Errorf("decode release gate summary %s: %w", path, err)
	}
	return summary, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write json %s: %w", path, err)
	}
	return nil
}

func writeText(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--corpus" || arg == "--golden" || arg == "--merge-quality" || arg == "--graph-index" || arg == "--merge-quality-expect-links" || arg == "--out" || arg == "--dashboard" || arg == "--triage" || arg == "--report" || arg == "--policy" || arg == "--summary" || arg == "--summaries" || arg == "--min-samples" || arg == "--percentile" || arg == "--metric-margin" || arg == "--task-margin" || arg == "--section-margin" || arg == "--floor-precision" || arg == "--floor-recall" || arg == "--floor-f1" || arg == "--floor-pass-rate" || arg == "--floor-task-pass-rate" || arg == "--floor-section-coverage":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case arg == "--merge-quality-auto" || arg == "--include-fixtures":
			out = append(out, arg)
		case strings.HasPrefix(arg, "--corpus=") || strings.HasPrefix(arg, "--golden=") || strings.HasPrefix(arg, "--merge-quality=") || strings.HasPrefix(arg, "--graph-index=") || strings.HasPrefix(arg, "--merge-quality-expect-links=") || strings.HasPrefix(arg, "--merge-quality-auto=") || strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--dashboard=") || strings.HasPrefix(arg, "--triage=") || strings.HasPrefix(arg, "--report=") || strings.HasPrefix(arg, "--policy=") || strings.HasPrefix(arg, "--summary=") || strings.HasPrefix(arg, "--summaries=") || strings.HasPrefix(arg, "--min-samples=") || strings.HasPrefix(arg, "--include-fixtures=") || strings.HasPrefix(arg, "--percentile=") || strings.HasPrefix(arg, "--metric-margin=") || strings.HasPrefix(arg, "--task-margin=") || strings.HasPrefix(arg, "--section-margin=") || strings.HasPrefix(arg, "--floor-precision=") || strings.HasPrefix(arg, "--floor-recall=") || strings.HasPrefix(arg, "--floor-f1=") || strings.HasPrefix(arg, "--floor-pass-rate=") || strings.HasPrefix(arg, "--floor-task-pass-rate=") || strings.HasPrefix(arg, "--floor-section-coverage="):
			out = append(out, arg)
		}
	}
	return out
}

func readMergeQualitySummary(path string) mergeQualitySummary {
	path = strings.TrimSpace(path)
	if path == "" {
		return mergeQualitySummary{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return mergeQualitySummary{}
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return mergeQualitySummary{}
	}
	out := mergeQualitySummary{Present: true}
	if v, ok := payload["passed"].(bool); ok {
		out.Passed = v
	}
	if metrics, ok := payload["metrics"].(map[string]any); ok {
		if v, ok := metrics["repo_provenance_coverage"].(float64); ok {
			out.RepoProvenanceCoverage = v
		}
		if v, ok := metrics["unresolved_rate"].(float64); ok {
			out.UnresolvedRate = v
		}
		if v, ok := metrics["ambiguous_rate"].(float64); ok {
			out.AmbiguousRate = v
		}
	}
	if benchmark, ok := payload["benchmark"].(map[string]any); ok {
		out.BenchmarkPresent = true
		if v, ok := benchmark["passed"].(bool); ok {
			out.BenchmarkPassed = v
		}
		if svc, ok := benchmark["service_calls_service"].(map[string]any); ok {
			if v, ok := svc["precision"].(float64); ok {
				out.LinkagePrecision = v
			}
			if v, ok := svc["recall"].(float64); ok {
				out.LinkageRecall = v
			}
			if v, ok := svc["f1"].(float64); ok {
				out.LinkageF1 = v
			}
		}
		if svc, ok := benchmark["canonical_service_aliases"].(map[string]any); ok {
			if v, ok := svc["precision"].(float64); ok {
				out.IdentityPrecision = v
			}
			if v, ok := svc["recall"].(float64); ok {
				out.IdentityRecall = v
			}
			if v, ok := svc["f1"].(float64); ok {
				out.IdentityF1 = v
			}
		}
	}
	return out
}

func mergeQualityReportMissingBenchmark(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return true
	}
	benchmark, ok := payload["benchmark"].(map[string]any)
	if !ok || benchmark == nil {
		return true
	}
	_, hasServiceCalls := benchmark["service_calls_service"].(map[string]any)
	_, hasAliases := benchmark["canonical_service_aliases"].(map[string]any)
	return !hasServiceCalls && !hasAliases
}
