package quality

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
	GeneratedAtUTC string         `json:"generated_at_utc"`
	CorpusReport   string         `json:"corpus_report"`
	GoldenSummary  string         `json:"golden_summary,omitempty"`
	SampleSize     int            `json:"sample_size"`
	Metrics        metrics        `json:"metrics"`
	ByDomain       []metricBucket `json:"by_domain,omitempty"`
	ByLanguage     []metricBucket `json:"by_language,omitempty"`
	Adversarial    advMetrics     `json:"adversarial"`
	Regressions    []regression   `json:"regressions"`
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
		PassRate            float64 `json:"pass_rate"`
		Precision           float64 `json:"precision"`
		Recall              float64 `json:"recall"`
		F1                  float64 `json:"f1"`
		CalibrationErrorMax float64 `json:"calibration_error_max"`
		AdversarialPassRate float64 `json:"adversarial_pass_rate"`
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
		return errors.New("quality subcommand is required: evaluate|gate|dashboard|triage")
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
	default:
		return fmt.Errorf("unsupported quality subcommand %q", args[0])
	}
}

type evaluateOptions struct {
	CorpusPath    string
	GoldenPath    string
	OutPath       string
	DashboardPath string
	TriagePath    string
}

func runEvaluate(args []string) error {
	opts, err := parseEvaluateOptions(args)
	if err != nil {
		return err
	}
	rep, err := evaluate(opts.CorpusPath, opts.GoldenPath)
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
	outPath := fs.String("out", filepath.Join(".diffmind", "quality", "report.json"), "Path to quality report")
	dashboardPath := fs.String("dashboard", filepath.Join(".diffmind", "quality", "dashboard.md"), "Path to quality dashboard markdown")
	triagePath := fs.String("triage", filepath.Join(".diffmind", "quality", "triage.md"), "Path to regression triage markdown")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return evaluateOptions{}, fmt.Errorf("parse quality evaluate flags: %w", err)
	}
	return evaluateOptions{
		CorpusPath:    strings.TrimSpace(*corpusPath),
		GoldenPath:    strings.TrimSpace(*goldenPath),
		OutPath:       strings.TrimSpace(*outPath),
		DashboardPath: strings.TrimSpace(*dashboardPath),
		TriagePath:    strings.TrimSpace(*triagePath),
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

func evaluate(corpusPath string, goldenPath string) (report, error) {
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
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		CorpusReport:   corpusPath,
		GoldenSummary:  goldenPath,
		SampleSize:     len(corpus.Cases),
		Regressions:    collectRegressions(corpus.Cases, golden.Cases),
	}
	result.Metrics = calcMetrics(corpus.Cases, golden.Cases)
	result.ByDomain = calcBuckets(corpus.Cases, golden.Cases, func(c corpusCase) string { return strings.TrimSpace(c.Domain) })
	result.ByLanguage = calcBuckets(corpus.Cases, golden.Cases, func(c corpusCase) string { return strings.TrimSpace(c.Language) })
	result.Adversarial = calcAdversarial(corpus.Cases)
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
		case arg == "--corpus" || arg == "--golden" || arg == "--out" || arg == "--dashboard" || arg == "--triage" || arg == "--report" || arg == "--policy":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--corpus=") || strings.HasPrefix(arg, "--golden=") || strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--dashboard=") || strings.HasPrefix(arg, "--triage=") || strings.HasPrefix(arg, "--report=") || strings.HasPrefix(arg, "--policy="):
			out = append(out, arg)
		}
	}
	return out
}
