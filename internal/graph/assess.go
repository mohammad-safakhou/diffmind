package graph

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"diffmind/internal/graphschema"
)

type assessOptions struct {
	GraphPath       string
	IndexPath       string
	OutPath         string
	ExpectLinksPath string
	FailOnGate      bool
}

type AssessRequest struct {
	GraphPath       string
	IndexPath       string
	OutPath         string
	ExpectLinksPath string
	FailOnGate      bool
}

type AssessResult struct {
	GraphID    string
	GraphPath  string
	ReportPath string
	Passed     bool
}

type mergeQualityReport struct {
	GraphID        string `json:"graph_id"`
	Mode           string `json:"mode"`
	Source         string `json:"source"`
	GeneratedAtUTC string `json:"generated_at_utc"`
	Metrics        struct {
		ServiceCallsTotal              int     `json:"service_calls_total"`
		ServiceCallsWithRepoProvenance int     `json:"service_calls_with_repo_provenance"`
		RepoProvenanceCoverage         float64 `json:"repo_provenance_coverage"`
		UnresolvedAPICalls             int     `json:"unresolved_api_calls"`
		UnresolvedRate                 float64 `json:"unresolved_rate"`
		AmbiguousServiceLinks          int     `json:"ambiguous_service_links"`
		AmbiguousRate                  float64 `json:"ambiguous_rate"`
		CanonicalServiceNodes          int     `json:"canonical_service_nodes"`
		CanonicalAPIHostNodes          int     `json:"canonical_api_host_nodes"`
	} `json:"metrics"`
	Gates struct {
		RepoProvenanceCoverage struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"repo_provenance_coverage"`
		UnresolvedRate struct {
			ThresholdMax float64 `json:"threshold_max"`
			Observed     float64 `json:"observed"`
			Passed       bool    `json:"passed"`
		} `json:"unresolved_rate"`
		AmbiguousRate struct {
			ThresholdMax float64 `json:"threshold_max"`
			Observed     float64 `json:"observed"`
			Passed       bool    `json:"passed"`
		} `json:"ambiguous_rate"`
	} `json:"gates"`
	Benchmark *struct {
		Enabled             bool `json:"enabled"`
		ServiceCallsService struct {
			Expected             int      `json:"expected"`
			Predicted            int      `json:"predicted"`
			Matched              int      `json:"matched"`
			Precision            float64  `json:"precision"`
			Recall               float64  `json:"recall"`
			F1                   float64  `json:"f1"`
			FalsePositives       int      `json:"false_positives"`
			FalseNegatives       int      `json:"false_negatives"`
			FalsePositiveSamples []string `json:"false_positive_samples,omitempty"`
			FalseNegativeSamples []string `json:"false_negative_samples,omitempty"`
		} `json:"service_calls_service"`
		CanonicalServiceAliases struct {
			Expected             int      `json:"expected"`
			Predicted            int      `json:"predicted"`
			Matched              int      `json:"matched"`
			Precision            float64  `json:"precision"`
			Recall               float64  `json:"recall"`
			F1                   float64  `json:"f1"`
			FalsePositives       int      `json:"false_positives"`
			FalseNegatives       int      `json:"false_negatives"`
			FalsePositiveSamples []string `json:"false_positive_samples,omitempty"`
			FalseNegativeSamples []string `json:"false_negative_samples,omitempty"`
		} `json:"canonical_service_aliases"`
		Gates struct {
			PrecisionMin struct {
				Threshold float64 `json:"threshold"`
				Observed  float64 `json:"observed"`
				Passed    bool    `json:"passed"`
			} `json:"precision_min"`
			RecallMin struct {
				Threshold float64 `json:"threshold"`
				Observed  float64 `json:"observed"`
				Passed    bool    `json:"passed"`
			} `json:"recall_min"`
			IdentityPrecisionMin struct {
				Threshold float64 `json:"threshold"`
				Observed  float64 `json:"observed"`
				Passed    bool    `json:"passed"`
			} `json:"identity_precision_min"`
			IdentityRecallMin struct {
				Threshold float64 `json:"threshold"`
				Observed  float64 `json:"observed"`
				Passed    bool    `json:"passed"`
			} `json:"identity_recall_min"`
		} `json:"gates"`
		Passed bool `json:"passed"`
	} `json:"benchmark,omitempty"`
	Passed bool `json:"passed"`
}

type mergeQualityHistoryIndex struct {
	Runs []mergeQualityHistoryEntry `json:"runs"`
}

type mergeQualityHistoryEntry struct {
	RunID          string  `json:"run_id"`
	GraphID        string  `json:"graph_id"`
	GeneratedAtUTC string  `json:"generated_at_utc"`
	Passed         bool    `json:"passed"`
	ReportPath     string  `json:"report_path"`
	SnapshotPath   string  `json:"snapshot_path"`
	LinkageF1      float64 `json:"linkage_f1,omitempty"`
	IdentityF1     float64 `json:"identity_f1,omitempty"`
}

type expectedLinksFile struct {
	ServiceCallsService     []expectedServiceCall           `json:"service_calls_service"`
	CanonicalServiceAliases []expectedCanonicalServiceAlias `json:"canonical_service_aliases"`
}

type expectedServiceCall struct {
	SourceServiceID string `json:"source_service_id"`
	SourceRepoPath  string `json:"source_repo_path"`
	TargetServiceID string `json:"target_service_id"`
	TargetRepoPath  string `json:"target_repo_path"`
}

type expectedCanonicalServiceAlias struct {
	SourceServiceID string `json:"source_service_id"`
	SourceRepoPath  string `json:"source_repo_path"`
	CanonicalKey    string `json:"canonical_key"`
	EnvScope        string `json:"env_scope"`
}

func runAssess(_ context.Context, args []string) error {
	opts, err := parseAssessOptions(args)
	if err != nil {
		return err
	}
	_, err = Assess(context.Background(), AssessRequest{
		GraphPath:       opts.GraphPath,
		IndexPath:       opts.IndexPath,
		OutPath:         opts.OutPath,
		ExpectLinksPath: opts.ExpectLinksPath,
		FailOnGate:      opts.FailOnGate,
	})
	if err != nil {
		return err
	}
	fmt.Println(opts.OutPath)
	return nil
}

func Assess(_ context.Context, req AssessRequest) (AssessResult, error) {
	graphPath := strings.TrimSpace(req.GraphPath)
	var err error
	if graphPath == "" {
		graphPath, err = latestGraphPathFromIndex(req.IndexPath)
		if err != nil {
			return AssessResult{}, err
		}
	}
	graph, err := loadGraph(graphPath)
	if err != nil {
		return AssessResult{}, err
	}
	outPath := strings.TrimSpace(req.OutPath)
	if outPath == "" {
		outPath = filepath.Join(".diffmind", "graph", "merge_quality_report.json")
	}
	expected, err := loadExpectedLinks(req.ExpectLinksPath)
	if err != nil {
		return AssessResult{}, err
	}
	report := evaluateMergeQuality(graph, graphPath, expected)
	if err := writeMergeQualityReport(outPath, report); err != nil {
		return AssessResult{}, err
	}
	if err := writeMergeQualityHistory(outPath, report); err != nil {
		return AssessResult{}, err
	}
	if req.FailOnGate && !report.Passed {
		return AssessResult{}, errors.New("graph merge quality gate failed")
	}
	return AssessResult{
		GraphID:    report.GraphID,
		GraphPath:  graphPath,
		ReportPath: outPath,
		Passed:     report.Passed,
	}, nil
}

func parseAssessOptions(args []string) (assessOptions, error) {
	fs := flag.NewFlagSet("graph assess", flag.ContinueOnError)
	graphPath := fs.String("graph", "", "Graph JSON path (if empty uses latest from --index)")
	indexPath := fs.String("index", filepath.Join(".diffmind", "graph", "index.json"), "Graph index path")
	outPath := fs.String("out", filepath.Join(".diffmind", "graph", "merge_quality_report.json"), "Output merge quality report path")
	expectLinksPath := fs.String("expect-links", "", "Expected service links JSON for benchmark precision/recall")
	failOnGate := fs.Bool("fail-on-gate", false, "Exit non-zero if merge quality gate fails")
	if err := fs.Parse(filterAssessArgs(args)); err != nil {
		return assessOptions{}, fmt.Errorf("parse graph assess flags: %w", err)
	}
	return assessOptions{
		GraphPath:       strings.TrimSpace(*graphPath),
		IndexPath:       strings.TrimSpace(*indexPath),
		OutPath:         strings.TrimSpace(*outPath),
		ExpectLinksPath: strings.TrimSpace(*expectLinksPath),
		FailOnGate:      *failOnGate,
	}, nil
}

func filterAssessArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--graph" || arg == "--index" || arg == "--out" || arg == "--expect-links":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case arg == "--fail-on-gate":
			out = append(out, arg)
		case strings.HasPrefix(arg, "--graph=") || strings.HasPrefix(arg, "--index=") || strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--expect-links=") || strings.HasPrefix(arg, "--fail-on-gate="):
			out = append(out, arg)
		}
	}
	return out
}

func latestGraphPathFromIndex(indexPath string) (string, error) {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return "", fmt.Errorf("read graph index %s: %w", indexPath, err)
	}
	var idx graphschema.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return "", fmt.Errorf("decode graph index %s: %w", indexPath, err)
	}
	if len(idx.Graphs) == 0 {
		return "", fmt.Errorf("graph index %s has no graphs", indexPath)
	}
	return strings.TrimSpace(idx.Graphs[0].Path), nil
}

func loadGraph(path string) (graphschema.Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return graphschema.Graph{}, fmt.Errorf("read graph %s: %w", path, err)
	}
	var graph graphschema.Graph
	if err := json.Unmarshal(data, &graph); err != nil {
		return graphschema.Graph{}, fmt.Errorf("decode graph %s: %w", path, err)
	}
	return graph, nil
}

func evaluateMergeQuality(graph graphschema.Graph, source string, expected *expectedLinksFile) mergeQualityReport {
	rep := mergeQualityReport{
		GraphID:        graph.GraphID,
		Mode:           graph.Mode,
		Source:         source,
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
	}
	serviceCalls := 0
	serviceCallsWithRepoProv := 0
	ambiguousLinks := 0
	unresolved := 0
	canonicalSvc := 0
	canonicalHost := 0

	for _, n := range graph.Nodes {
		switch n.Type {
		case "unresolved_api_call":
			unresolved++
		case "canonical_service":
			canonicalSvc++
		case "canonical_api_host":
			canonicalHost++
		}
	}
	for _, e := range graph.Edges {
		if e.Type != "service_calls_service" {
			continue
		}
		serviceCalls++
		srcID := strings.TrimSpace(fmt.Sprint(e.Attributes["source_service_id"]))
		srcRepo := strings.TrimSpace(fmt.Sprint(e.Attributes["source_repo_path"]))
		tgtID := strings.TrimSpace(fmt.Sprint(e.Attributes["target_service_id"]))
		tgtRepo := strings.TrimSpace(fmt.Sprint(e.Attributes["target_repo_path"]))
		if srcID != "" && srcRepo != "" && tgtID != "" && tgtRepo != "" {
			serviceCallsWithRepoProv++
		}
		ambiguous, _ := e.Attributes["service_match_ambiguous"].(bool)
		if ambiguous {
			ambiguousLinks++
		}
	}

	rep.Metrics.ServiceCallsTotal = serviceCalls
	rep.Metrics.ServiceCallsWithRepoProvenance = serviceCallsWithRepoProv
	rep.Metrics.RepoProvenanceCoverage = ratio(serviceCallsWithRepoProv, serviceCalls)
	rep.Metrics.UnresolvedAPICalls = unresolved
	rep.Metrics.UnresolvedRate = ratio(unresolved, serviceCalls+unresolved)
	rep.Metrics.AmbiguousServiceLinks = ambiguousLinks
	rep.Metrics.AmbiguousRate = ratio(ambiguousLinks, serviceCalls)
	rep.Metrics.CanonicalServiceNodes = canonicalSvc
	rep.Metrics.CanonicalAPIHostNodes = canonicalHost

	rep.Gates.RepoProvenanceCoverage.Threshold = 0.99
	rep.Gates.RepoProvenanceCoverage.Observed = rep.Metrics.RepoProvenanceCoverage
	rep.Gates.RepoProvenanceCoverage.Passed = serviceCalls == 0 || rep.Metrics.RepoProvenanceCoverage >= rep.Gates.RepoProvenanceCoverage.Threshold

	rep.Gates.UnresolvedRate.ThresholdMax = 0.20
	rep.Gates.UnresolvedRate.Observed = rep.Metrics.UnresolvedRate
	rep.Gates.UnresolvedRate.Passed = rep.Metrics.UnresolvedRate <= rep.Gates.UnresolvedRate.ThresholdMax

	rep.Gates.AmbiguousRate.ThresholdMax = 0.05
	rep.Gates.AmbiguousRate.Observed = rep.Metrics.AmbiguousRate
	rep.Gates.AmbiguousRate.Passed = rep.Metrics.AmbiguousRate <= rep.Gates.AmbiguousRate.ThresholdMax

	rep.Passed = rep.Gates.RepoProvenanceCoverage.Passed && rep.Gates.UnresolvedRate.Passed && rep.Gates.AmbiguousRate.Passed
	if expected != nil {
		rep.Benchmark = evaluateBenchmark(graph, expected)
		rep.Passed = rep.Passed && rep.Benchmark.Passed
	}
	return rep
}

func loadExpectedLinks(path string) (*expectedLinksFile, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read expected links %s: %w", path, err)
	}
	var expected expectedLinksFile
	if err := json.Unmarshal(data, &expected); err != nil {
		return nil, fmt.Errorf("decode expected links %s: %w", path, err)
	}
	if err := validateExpectedLinks(expected); err != nil {
		return nil, fmt.Errorf("validate expected links %s: %w", path, err)
	}
	return &expected, nil
}

func validateExpectedLinks(expected expectedLinksFile) error {
	if len(expected.ServiceCallsService) == 0 && len(expected.CanonicalServiceAliases) == 0 {
		return errors.New("expected links must include at least one of service_calls_service or canonical_service_aliases")
	}
	for idx, link := range expected.ServiceCallsService {
		if strings.TrimSpace(link.SourceServiceID) == "" {
			return fmt.Errorf("service_calls_service[%d].source_service_id is required", idx)
		}
		if strings.TrimSpace(link.TargetServiceID) == "" {
			return fmt.Errorf("service_calls_service[%d].target_service_id is required", idx)
		}
	}
	for idx, alias := range expected.CanonicalServiceAliases {
		if strings.TrimSpace(alias.SourceServiceID) == "" {
			return fmt.Errorf("canonical_service_aliases[%d].source_service_id is required", idx)
		}
		if strings.TrimSpace(alias.CanonicalKey) == "" {
			return fmt.Errorf("canonical_service_aliases[%d].canonical_key is required", idx)
		}
	}
	return nil
}

func evaluateBenchmark(graph graphschema.Graph, expected *expectedLinksFile) *struct {
	Enabled             bool `json:"enabled"`
	ServiceCallsService struct {
		Expected             int      `json:"expected"`
		Predicted            int      `json:"predicted"`
		Matched              int      `json:"matched"`
		Precision            float64  `json:"precision"`
		Recall               float64  `json:"recall"`
		F1                   float64  `json:"f1"`
		FalsePositives       int      `json:"false_positives"`
		FalseNegatives       int      `json:"false_negatives"`
		FalsePositiveSamples []string `json:"false_positive_samples,omitempty"`
		FalseNegativeSamples []string `json:"false_negative_samples,omitempty"`
	} `json:"service_calls_service"`
	CanonicalServiceAliases struct {
		Expected             int      `json:"expected"`
		Predicted            int      `json:"predicted"`
		Matched              int      `json:"matched"`
		Precision            float64  `json:"precision"`
		Recall               float64  `json:"recall"`
		F1                   float64  `json:"f1"`
		FalsePositives       int      `json:"false_positives"`
		FalseNegatives       int      `json:"false_negatives"`
		FalsePositiveSamples []string `json:"false_positive_samples,omitempty"`
		FalseNegativeSamples []string `json:"false_negative_samples,omitempty"`
	} `json:"canonical_service_aliases"`
	Gates struct {
		PrecisionMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"precision_min"`
		RecallMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"recall_min"`
		IdentityPrecisionMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"identity_precision_min"`
		IdentityRecallMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"identity_recall_min"`
	} `json:"gates"`
	Passed bool `json:"passed"`
} {
	benchmark := &struct {
		Enabled             bool `json:"enabled"`
		ServiceCallsService struct {
			Expected             int      `json:"expected"`
			Predicted            int      `json:"predicted"`
			Matched              int      `json:"matched"`
			Precision            float64  `json:"precision"`
			Recall               float64  `json:"recall"`
			F1                   float64  `json:"f1"`
			FalsePositives       int      `json:"false_positives"`
			FalseNegatives       int      `json:"false_negatives"`
			FalsePositiveSamples []string `json:"false_positive_samples,omitempty"`
			FalseNegativeSamples []string `json:"false_negative_samples,omitempty"`
		} `json:"service_calls_service"`
		CanonicalServiceAliases struct {
			Expected             int      `json:"expected"`
			Predicted            int      `json:"predicted"`
			Matched              int      `json:"matched"`
			Precision            float64  `json:"precision"`
			Recall               float64  `json:"recall"`
			F1                   float64  `json:"f1"`
			FalsePositives       int      `json:"false_positives"`
			FalseNegatives       int      `json:"false_negatives"`
			FalsePositiveSamples []string `json:"false_positive_samples,omitempty"`
			FalseNegativeSamples []string `json:"false_negative_samples,omitempty"`
		} `json:"canonical_service_aliases"`
		Gates struct {
			PrecisionMin struct {
				Threshold float64 `json:"threshold"`
				Observed  float64 `json:"observed"`
				Passed    bool    `json:"passed"`
			} `json:"precision_min"`
			RecallMin struct {
				Threshold float64 `json:"threshold"`
				Observed  float64 `json:"observed"`
				Passed    bool    `json:"passed"`
			} `json:"recall_min"`
			IdentityPrecisionMin struct {
				Threshold float64 `json:"threshold"`
				Observed  float64 `json:"observed"`
				Passed    bool    `json:"passed"`
			} `json:"identity_precision_min"`
			IdentityRecallMin struct {
				Threshold float64 `json:"threshold"`
				Observed  float64 `json:"observed"`
				Passed    bool    `json:"passed"`
			} `json:"identity_recall_min"`
		} `json:"gates"`
		Passed bool `json:"passed"`
	}{Enabled: true}

	observed := observedServiceCallsService(graph)
	seenObserved := make([]bool, len(observed))
	seenExpected := make([]bool, len(expected.ServiceCallsService))
	expectedList := expected.ServiceCallsService
	matchedExpected := 0
	matchedObserved := 0
	for expIdx, exp := range expectedList {
		for idx, obs := range observed {
			if seenObserved[idx] {
				continue
			}
			if serviceCallMatchesExpected(obs, exp) {
				seenObserved[idx] = true
				seenExpected[expIdx] = true
				matchedExpected++
				matchedObserved++
				break
			}
		}
	}
	benchmark.ServiceCallsService.Expected = len(expectedList)
	benchmark.ServiceCallsService.Predicted = len(observed)
	benchmark.ServiceCallsService.Matched = matchedExpected
	benchmark.ServiceCallsService.FalsePositives = len(observed) - matchedObserved
	benchmark.ServiceCallsService.FalseNegatives = len(expectedList) - matchedExpected
	benchmark.ServiceCallsService.Precision = ratio(matchedObserved, len(observed))
	benchmark.ServiceCallsService.Recall = ratio(matchedExpected, len(expectedList))
	if benchmark.ServiceCallsService.Precision+benchmark.ServiceCallsService.Recall > 0 {
		benchmark.ServiceCallsService.F1 = (2 * benchmark.ServiceCallsService.Precision * benchmark.ServiceCallsService.Recall) / (benchmark.ServiceCallsService.Precision + benchmark.ServiceCallsService.Recall)
	}
	benchmark.ServiceCallsService.FalsePositiveSamples = sampleUnmatchedServiceCalls(observed, seenObserved, 12)
	benchmark.ServiceCallsService.FalseNegativeSamples = sampleUnmatchedServiceCalls(expectedList, seenExpected, 12)

	benchmark.Gates.PrecisionMin.Threshold = 0.95
	benchmark.Gates.PrecisionMin.Observed = benchmark.ServiceCallsService.Precision
	benchmark.Gates.PrecisionMin.Passed = len(expectedList) == 0 || benchmark.ServiceCallsService.Precision >= benchmark.Gates.PrecisionMin.Threshold
	benchmark.Gates.RecallMin.Threshold = 0.95
	benchmark.Gates.RecallMin.Observed = benchmark.ServiceCallsService.Recall
	benchmark.Gates.RecallMin.Passed = len(expectedList) == 0 || benchmark.ServiceCallsService.Recall >= benchmark.Gates.RecallMin.Threshold

	observedAliases := observedCanonicalServiceAliases(graph)
	seenObservedAliases := make([]bool, len(observedAliases))
	seenExpectedAliases := make([]bool, len(expected.CanonicalServiceAliases))
	expectedAliases := expected.CanonicalServiceAliases
	matchedExpectedAliases := 0
	matchedObservedAliases := 0
	for expIdx, exp := range expectedAliases {
		for idx, obs := range observedAliases {
			if seenObservedAliases[idx] {
				continue
			}
			if canonicalAliasMatchesExpected(obs, exp) {
				seenObservedAliases[idx] = true
				seenExpectedAliases[expIdx] = true
				matchedExpectedAliases++
				matchedObservedAliases++
				break
			}
		}
	}
	benchmark.CanonicalServiceAliases.Expected = len(expectedAliases)
	benchmark.CanonicalServiceAliases.Predicted = len(observedAliases)
	benchmark.CanonicalServiceAliases.Matched = matchedExpectedAliases
	benchmark.CanonicalServiceAliases.FalsePositives = len(observedAliases) - matchedObservedAliases
	benchmark.CanonicalServiceAliases.FalseNegatives = len(expectedAliases) - matchedExpectedAliases
	benchmark.CanonicalServiceAliases.Precision = ratio(matchedObservedAliases, len(observedAliases))
	benchmark.CanonicalServiceAliases.Recall = ratio(matchedExpectedAliases, len(expectedAliases))
	if benchmark.CanonicalServiceAliases.Precision+benchmark.CanonicalServiceAliases.Recall > 0 {
		benchmark.CanonicalServiceAliases.F1 = (2 * benchmark.CanonicalServiceAliases.Precision * benchmark.CanonicalServiceAliases.Recall) / (benchmark.CanonicalServiceAliases.Precision + benchmark.CanonicalServiceAliases.Recall)
	}
	benchmark.CanonicalServiceAliases.FalsePositiveSamples = sampleUnmatchedCanonicalAliases(observedAliases, seenObservedAliases, 12)
	benchmark.CanonicalServiceAliases.FalseNegativeSamples = sampleUnmatchedCanonicalAliases(expectedAliases, seenExpectedAliases, 12)
	benchmark.Gates.IdentityPrecisionMin.Threshold = 0.95
	benchmark.Gates.IdentityPrecisionMin.Observed = benchmark.CanonicalServiceAliases.Precision
	benchmark.Gates.IdentityPrecisionMin.Passed = len(expectedAliases) == 0 || benchmark.CanonicalServiceAliases.Precision >= benchmark.Gates.IdentityPrecisionMin.Threshold
	benchmark.Gates.IdentityRecallMin.Threshold = 0.95
	benchmark.Gates.IdentityRecallMin.Observed = benchmark.CanonicalServiceAliases.Recall
	benchmark.Gates.IdentityRecallMin.Passed = len(expectedAliases) == 0 || benchmark.CanonicalServiceAliases.Recall >= benchmark.Gates.IdentityRecallMin.Threshold

	benchmark.Passed = benchmark.Gates.PrecisionMin.Passed &&
		benchmark.Gates.RecallMin.Passed &&
		benchmark.Gates.IdentityPrecisionMin.Passed &&
		benchmark.Gates.IdentityRecallMin.Passed
	return benchmark
}

func observedServiceCallsService(graph graphschema.Graph) []expectedServiceCall {
	observed := make([]expectedServiceCall, 0, len(graph.Edges))
	seen := map[string]struct{}{}
	for _, e := range graph.Edges {
		if e.Type != "service_calls_service" {
			continue
		}
		rec := expectedServiceCall{
			SourceServiceID: strings.TrimSpace(fmt.Sprint(e.Attributes["source_service_id"])),
			SourceRepoPath:  strings.TrimSpace(fmt.Sprint(e.Attributes["source_repo_path"])),
			TargetServiceID: strings.TrimSpace(fmt.Sprint(e.Attributes["target_service_id"])),
			TargetRepoPath:  strings.TrimSpace(fmt.Sprint(e.Attributes["target_repo_path"])),
		}
		if rec.SourceServiceID == "" {
			rec.SourceServiceID = strings.TrimSpace(e.SourceID)
		}
		if rec.TargetServiceID == "" {
			rec.TargetServiceID = strings.TrimSpace(e.TargetID)
		}
		key := strings.Join([]string{rec.SourceServiceID, rec.SourceRepoPath, rec.TargetServiceID, rec.TargetRepoPath}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		observed = append(observed, rec)
	}
	return observed
}

func serviceCallMatchesExpected(observed expectedServiceCall, expected expectedServiceCall) bool {
	if strings.TrimSpace(expected.SourceServiceID) != "" && !strings.EqualFold(strings.TrimSpace(expected.SourceServiceID), strings.TrimSpace(observed.SourceServiceID)) {
		return false
	}
	if strings.TrimSpace(expected.TargetServiceID) != "" && !strings.EqualFold(strings.TrimSpace(expected.TargetServiceID), strings.TrimSpace(observed.TargetServiceID)) {
		return false
	}
	if strings.TrimSpace(expected.SourceRepoPath) != "" && !strings.EqualFold(strings.TrimSpace(expected.SourceRepoPath), strings.TrimSpace(observed.SourceRepoPath)) {
		return false
	}
	if strings.TrimSpace(expected.TargetRepoPath) != "" && !strings.EqualFold(strings.TrimSpace(expected.TargetRepoPath), strings.TrimSpace(observed.TargetRepoPath)) {
		return false
	}
	return true
}

func observedCanonicalServiceAliases(graph graphschema.Graph) []expectedCanonicalServiceAlias {
	observed := make([]expectedCanonicalServiceAlias, 0, len(graph.Edges))
	seen := map[string]struct{}{}
	nodeByID := map[string]graphschema.Node{}
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}
	for _, e := range graph.Edges {
		if e.Type != "service_alias_of_canonical_service" {
			continue
		}
		rec := expectedCanonicalServiceAlias{
			SourceServiceID: strings.TrimSpace(fmt.Sprint(e.Attributes["source_service_id"])),
			SourceRepoPath:  strings.TrimSpace(fmt.Sprint(e.Attributes["source_repo_path"])),
			CanonicalKey:    strings.TrimSpace(fmt.Sprint(e.Attributes["canonical_key"])),
			EnvScope:        strings.TrimSpace(fmt.Sprint(e.Attributes["env_scope"])),
		}
		if rec.SourceServiceID == "" {
			rec.SourceServiceID = strings.TrimSpace(e.SourceID)
		}
		if rec.CanonicalKey == "" {
			if n, ok := nodeByID[e.TargetID]; ok {
				rec.CanonicalKey = strings.TrimSpace(fmt.Sprint(n.Attributes["canonical_key"]))
				if rec.EnvScope == "" {
					rec.EnvScope = strings.TrimSpace(fmt.Sprint(n.Attributes["env_scope"]))
				}
			}
		}
		key := strings.Join([]string{rec.SourceServiceID, rec.SourceRepoPath, rec.CanonicalKey, rec.EnvScope}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		observed = append(observed, rec)
	}
	return observed
}

func canonicalAliasMatchesExpected(observed expectedCanonicalServiceAlias, expected expectedCanonicalServiceAlias) bool {
	if strings.TrimSpace(expected.SourceServiceID) != "" && !strings.EqualFold(strings.TrimSpace(expected.SourceServiceID), strings.TrimSpace(observed.SourceServiceID)) {
		return false
	}
	if strings.TrimSpace(expected.SourceRepoPath) != "" && !strings.EqualFold(strings.TrimSpace(expected.SourceRepoPath), strings.TrimSpace(observed.SourceRepoPath)) {
		return false
	}
	if strings.TrimSpace(expected.CanonicalKey) != "" && !strings.EqualFold(strings.TrimSpace(expected.CanonicalKey), strings.TrimSpace(observed.CanonicalKey)) {
		return false
	}
	if strings.TrimSpace(expected.EnvScope) != "" && !strings.EqualFold(strings.TrimSpace(expected.EnvScope), strings.TrimSpace(observed.EnvScope)) {
		return false
	}
	return true
}

func sampleUnmatchedServiceCalls(items []expectedServiceCall, seen []bool, limit int) []string {
	out := make([]string, 0, minInt(limit, len(items)))
	for i, item := range items {
		if i < len(seen) && seen[i] {
			continue
		}
		out = append(out, formatServiceCall(item))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func sampleUnmatchedCanonicalAliases(items []expectedCanonicalServiceAlias, seen []bool, limit int) []string {
	out := make([]string, 0, minInt(limit, len(items)))
	for i, item := range items {
		if i < len(seen) && seen[i] {
			continue
		}
		out = append(out, formatCanonicalAlias(item))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func formatServiceCall(v expectedServiceCall) string {
	src := strings.TrimSpace(v.SourceServiceID)
	if src == "" {
		src = "?"
	}
	tgt := strings.TrimSpace(v.TargetServiceID)
	if tgt == "" {
		tgt = "?"
	}
	srcRepo := strings.TrimSpace(v.SourceRepoPath)
	if srcRepo == "" {
		srcRepo = "?"
	}
	tgtRepo := strings.TrimSpace(v.TargetRepoPath)
	if tgtRepo == "" {
		tgtRepo = "?"
	}
	return fmt.Sprintf("%s@%s -> %s@%s", src, srcRepo, tgt, tgtRepo)
}

func formatCanonicalAlias(v expectedCanonicalServiceAlias) string {
	src := strings.TrimSpace(v.SourceServiceID)
	if src == "" {
		src = "?"
	}
	srcRepo := strings.TrimSpace(v.SourceRepoPath)
	if srcRepo == "" {
		srcRepo = "?"
	}
	key := strings.TrimSpace(v.CanonicalKey)
	if key == "" {
		key = "?"
	}
	scope := strings.TrimSpace(v.EnvScope)
	if scope == "" {
		scope = "unknown"
	}
	return fmt.Sprintf("%s@%s -> canonical:%s[%s]", src, srcRepo, key, scope)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ratio(num int, den int) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func writeMergeQualityReport(path string, rep mergeQualityReport) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal merge quality report: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create merge quality report dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write merge quality report %s: %w", path, err)
	}
	return nil
}

func writeMergeQualityHistory(reportPath string, rep mergeQualityReport) error {
	historyDir := filepath.Join(filepath.Dir(reportPath), "history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return fmt.Errorf("create merge quality history dir: %w", err)
	}
	runID := time.Now().UTC().Format("20060102T150405.000000000Z")
	graphID := strings.TrimSpace(rep.GraphID)
	if graphID == "" {
		graphID = "unknown"
	}
	graphID = sanitizeFileToken(graphID)
	snapshotPath := filepath.Join(historyDir, fmt.Sprintf("%s_%s.json", runID, graphID))
	if err := writeMergeQualityReport(snapshotPath, rep); err != nil {
		return fmt.Errorf("write merge quality history snapshot: %w", err)
	}

	indexPath := filepath.Join(historyDir, "index.json")
	idx := mergeQualityHistoryIndex{Runs: []mergeQualityHistoryEntry{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(data, &idx)
	}
	entry := mergeQualityHistoryEntry{
		RunID:          runID,
		GraphID:        rep.GraphID,
		GeneratedAtUTC: rep.GeneratedAtUTC,
		Passed:         rep.Passed,
		ReportPath:     reportPath,
		SnapshotPath:   snapshotPath,
	}
	if rep.Benchmark != nil {
		entry.LinkageF1 = rep.Benchmark.ServiceCallsService.F1
		entry.IdentityF1 = rep.Benchmark.CanonicalServiceAliases.F1
	}
	idx.Runs = append([]mergeQualityHistoryEntry{entry}, idx.Runs...)
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal merge quality history index: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		return fmt.Errorf("write merge quality history index: %w", err)
	}
	return nil
}

func sanitizeFileToken(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}
