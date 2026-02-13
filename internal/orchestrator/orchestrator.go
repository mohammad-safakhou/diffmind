package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"diffmind/internal/analyzers"
	"diffmind/internal/classifier"
	"diffmind/internal/consolidation"
	"diffmind/internal/contracts"
	"diffmind/internal/corpus"
	"diffmind/internal/diff"
	"diffmind/internal/golden"
	"diffmind/internal/httpapi"
	"diffmind/internal/parser"
	"diffmind/internal/query"
	"diffmind/internal/snapshot"
)

type runOptions struct {
	Source       string
	Ref          string
	OutDir       string
	Retries      int
	RetryDelayMS int
	Resume       bool
	ReportPath   string
	ForwardArgs  []string
}

type stageReport struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	DurationMS    int64  `json:"duration_ms"`
	Skipped       bool   `json:"skipped"`
	Error         string `json:"error,omitempty"`
	ErrorTaxonomy string `json:"error_taxonomy,omitempty"`
}

type runReport struct {
	GeneratedAtUTC string        `json:"generated_at_utc"`
	Source         string        `json:"source"`
	Ref            string        `json:"ref"`
	OutDir         string        `json:"out_dir"`
	Status         string        `json:"status"`
	Retries        int           `json:"retries"`
	RetryDelayMS   int           `json:"retry_delay_ms"`
	DurationMS     int64         `json:"duration_ms"`
	Error          string        `json:"error,omitempty"`
	ErrorTaxonomy  string        `json:"error_taxonomy,omitempty"`
	Stages         []stageReport `json:"stages"`
}

type stageDef struct {
	Name    string
	Outputs []string
	Invoke  func(context.Context) error
}

var (
	snapshotModule      contracts.SnapshotModule      = snapshot.Module{}
	classifierModule    contracts.ClassifierModule    = classifier.Module{}
	parserModule        contracts.ParserModule        = parser.Module{}
	analyzerModule      contracts.AnalyzerModule      = analyzers.Module{}
	consolidationModule contracts.ConsolidationModule = consolidation.Module{}
)

func RunSnapshot(ctx context.Context, args []string) error {
	return snapshotModule.Run(ctx, args)
}

func RunScan(ctx context.Context, args []string) error {
	return classifierModule.Run(ctx, args)
}

func RunParse(ctx context.Context, args []string) error {
	return parserModule.Run(ctx, args)
}

func RunAnalyze(ctx context.Context, args []string) error {
	return analyzerModule.Run(ctx, args)
}

func RunBundle(ctx context.Context, args []string) error {
	return consolidationModule.Run(ctx, args)
}

func RunQuery(ctx context.Context, args []string) error {
	return query.Run(ctx, args)
}

func RunDiff(ctx context.Context, args []string) error {
	return diff.Run(ctx, args)
}

func RunServe(ctx context.Context, args []string) error {
	return httpapi.Run(ctx, args)
}

func RunCorpus(ctx context.Context, args []string) error {
	return corpus.Run(ctx, args)
}

func RunGolden(ctx context.Context, args []string) error {
	return golden.Run(ctx, args)
}

func RunPipeline(ctx context.Context, args []string) error {
	opts, err := parseRunOptions(args)
	if err != nil {
		return err
	}

	start := time.Now()
	report := runReport{
		GeneratedAtUTC: start.UTC().Format(time.RFC3339),
		Source:         opts.Source,
		Ref:            opts.Ref,
		OutDir:         opts.OutDir,
		Status:         "running",
		Retries:        opts.Retries,
		RetryDelayMS:   opts.RetryDelayMS,
		Stages:         make([]stageReport, 0, 5),
	}

	stages := buildStages(opts)
	for _, stage := range stages {
		sr := runStage(ctx, opts, stage)
		report.Stages = append(report.Stages, sr)
		if sr.Status == "failed" {
			report.Status = "failed"
			report.Error = sr.Error
			report.ErrorTaxonomy = sr.ErrorTaxonomy
			report.DurationMS = time.Since(start).Milliseconds()
			_ = writeRunReport(opts.ReportPath, report)
			return fmt.Errorf("%s stage failed: %s", stage.Name, sr.Error)
		}
	}

	report.Status = "passed"
	report.DurationMS = time.Since(start).Milliseconds()
	if err := writeRunReport(opts.ReportPath, report); err != nil {
		return err
	}
	return nil
}

func parseRunOptions(args []string) (runOptions, error) {
	opts := runOptions{
		Source:       ".",
		Ref:          "HEAD",
		OutDir:       ".diffmind",
		Retries:      1,
		RetryDelayMS: 200,
		Resume:       true,
		ReportPath:   filepath.Join(".diffmind", "run", "report.json"),
		ForwardArgs:  make([]string, 0, len(args)),
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--source":
			if i+1 >= len(args) {
				return runOptions{}, errors.New("missing value for --source")
			}
			i++
			opts.Source = strings.TrimSpace(args[i])
			opts.ForwardArgs = append(opts.ForwardArgs, "--source", args[i])
		case strings.HasPrefix(arg, "--source="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--source="))
			opts.Source = value
			opts.ForwardArgs = append(opts.ForwardArgs, arg)
		case arg == "--ref":
			if i+1 >= len(args) {
				return runOptions{}, errors.New("missing value for --ref")
			}
			i++
			opts.Ref = strings.TrimSpace(args[i])
			opts.ForwardArgs = append(opts.ForwardArgs, "--ref", args[i])
		case strings.HasPrefix(arg, "--ref="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--ref="))
			opts.Ref = value
			opts.ForwardArgs = append(opts.ForwardArgs, arg)
		case arg == "--out":
			if i+1 >= len(args) {
				return runOptions{}, errors.New("missing value for --out")
			}
			i++
			opts.OutDir = strings.TrimSpace(args[i])
			opts.ReportPath = filepath.Join(opts.OutDir, "run", "report.json")
			opts.ForwardArgs = append(opts.ForwardArgs, "--out", args[i])
		case strings.HasPrefix(arg, "--out="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--out="))
			opts.OutDir = value
			opts.ReportPath = filepath.Join(opts.OutDir, "run", "report.json")
			opts.ForwardArgs = append(opts.ForwardArgs, arg)
		case arg == "--retries":
			if i+1 >= len(args) {
				return runOptions{}, errors.New("missing value for --retries")
			}
			i++
			v, err := strconv.Atoi(strings.TrimSpace(args[i]))
			if err != nil || v < 0 {
				return runOptions{}, fmt.Errorf("invalid --retries value %q", args[i])
			}
			opts.Retries = v
		case strings.HasPrefix(arg, "--retries="):
			v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(arg, "--retries=")))
			if err != nil || v < 0 {
				return runOptions{}, fmt.Errorf("invalid --retries value in %q", arg)
			}
			opts.Retries = v
		case arg == "--retry-delay-ms":
			if i+1 >= len(args) {
				return runOptions{}, errors.New("missing value for --retry-delay-ms")
			}
			i++
			v, err := strconv.Atoi(strings.TrimSpace(args[i]))
			if err != nil || v < 0 {
				return runOptions{}, fmt.Errorf("invalid --retry-delay-ms value %q", args[i])
			}
			opts.RetryDelayMS = v
		case strings.HasPrefix(arg, "--retry-delay-ms="):
			v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(arg, "--retry-delay-ms=")))
			if err != nil || v < 0 {
				return runOptions{}, fmt.Errorf("invalid --retry-delay-ms value in %q", arg)
			}
			opts.RetryDelayMS = v
		case arg == "--resume":
			opts.Resume = true
		case arg == "--no-resume":
			opts.Resume = false
		case strings.HasPrefix(arg, "--resume="):
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--resume=")))
			opts.Resume = value == "1" || value == "true" || value == "yes"
		case arg == "--report":
			if i+1 >= len(args) {
				return runOptions{}, errors.New("missing value for --report")
			}
			i++
			opts.ReportPath = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--report="):
			opts.ReportPath = strings.TrimSpace(strings.TrimPrefix(arg, "--report="))
		default:
			opts.ForwardArgs = append(opts.ForwardArgs, arg)
		}
	}

	if opts.Source == "" {
		opts.Source = "."
	}
	if opts.Ref == "" {
		opts.Ref = "HEAD"
	}
	if opts.OutDir == "" {
		opts.OutDir = ".diffmind"
	}
	if opts.ReportPath == "" {
		opts.ReportPath = filepath.Join(opts.OutDir, "run", "report.json")
	}
	return opts, nil
}

func buildStages(opts runOptions) []stageDef {
	sourceOut := []string{"--source", opts.Source, "--out", opts.OutDir}
	snapshotArgs := append([]string{"--source", opts.Source, "--ref", opts.Ref, "--out", opts.OutDir}, opts.ForwardArgs...)
	scanArgs := append(append([]string{}, sourceOut...), opts.ForwardArgs...)
	parseArgs := append(append([]string{}, sourceOut...), opts.ForwardArgs...)
	analyzeArgs := append(append([]string{}, sourceOut...), opts.ForwardArgs...)
	bundleArgs := []string{"--in", filepath.Join(opts.OutDir, "analyzers", "bundle.json"), "--out", opts.OutDir}
	bundleArgs = append(bundleArgs, opts.ForwardArgs...)

	return []stageDef{
		{
			Name:    "snapshot",
			Outputs: nil,
			Invoke: func(ctx context.Context) error {
				return RunSnapshot(ctx, snapshotArgs)
			},
		},
		{
			Name:    "scan",
			Outputs: []string{filepath.Join(opts.OutDir, "classification", "report.json")},
			Invoke: func(ctx context.Context) error {
				return RunScan(ctx, scanArgs)
			},
		},
		{
			Name:    "parse",
			Outputs: []string{filepath.Join(opts.OutDir, "parse", "report.json")},
			Invoke: func(ctx context.Context) error {
				return RunParse(ctx, parseArgs)
			},
		},
		{
			Name:    "analyze",
			Outputs: []string{filepath.Join(opts.OutDir, "analyzers", "bundle.json")},
			Invoke: func(ctx context.Context) error {
				return RunAnalyze(ctx, analyzeArgs)
			},
		},
		{
			Name:    "bundle",
			Outputs: []string{filepath.Join(opts.OutDir, "bundle", "intelligence_bundle.json")},
			Invoke: func(ctx context.Context) error {
				return RunBundle(ctx, bundleArgs)
			},
		},
	}
}

func runStage(ctx context.Context, opts runOptions, stage stageDef) stageReport {
	stageStart := time.Now()
	if opts.Resume && outputsExist(stage.Outputs) {
		return stageReport{
			Name:       stage.Name,
			Status:     "skipped",
			Attempts:   0,
			DurationMS: 0,
			Skipped:    true,
		}
	}

	attempts := 0
	var lastErr error
	for attempts < opts.Retries+1 {
		attempts++
		err := stage.Invoke(ctx)
		if err == nil {
			return stageReport{
				Name:       stage.Name,
				Status:     "passed",
				Attempts:   attempts,
				DurationMS: time.Since(stageStart).Milliseconds(),
			}
		}
		lastErr = err
		if !isRetryable(err) || attempts >= opts.Retries+1 {
			break
		}
		if opts.RetryDelayMS > 0 {
			delay := time.Duration(opts.RetryDelayMS) * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				lastErr = ctx.Err()
				attempts = opts.Retries + 1
			case <-timer.C:
			}
		}
	}

	taxonomy := classifyError(lastErr)
	return stageReport{
		Name:          stage.Name,
		Status:        "failed",
		Attempts:      attempts,
		DurationMS:    time.Since(stageStart).Milliseconds(),
		Error:         lastErr.Error(),
		ErrorTaxonomy: taxonomy,
	}
}

func outputsExist(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return false
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "dial tcp"),
		strings.Contains(msg, "reset by peer"),
		strings.Contains(msg, "temporary"),
		strings.Contains(msg, "i/o timeout"):
		return "transient_network"
	case strings.Contains(msg, "permission denied"),
		strings.Contains(msg, "no such file"),
		strings.Contains(msg, "file exists"):
		return "filesystem"
	case strings.Contains(msg, "decode"),
		strings.Contains(msg, "unmarshal"),
		strings.Contains(msg, "invalid"),
		strings.Contains(msg, "schema"),
		strings.Contains(msg, "parse"):
		return "data_contract"
	default:
		return "unknown"
	}
}

func isRetryable(err error) bool {
	switch classifyError(err) {
	case "timeout", "transient_network":
		return true
	default:
		return false
	}
}

func writeRunReport(path string, rep runReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create run report dir: %w", err)
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write run report %s: %w", path, err)
	}
	return nil
}
