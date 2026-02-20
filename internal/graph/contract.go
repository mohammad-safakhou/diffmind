package graph

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

	"diffmind/internal/graphschema"
)

type contractOptions struct {
	GraphPath    string
	IndexPath    string
	ContractPath string
	OutPath      string
	FailOnGate   bool
}

type ContractRequest struct {
	GraphPath    string
	IndexPath    string
	ContractPath string
	OutPath      string
	FailOnGate   bool
}

type ContractResult struct {
	GraphID      string
	GraphPath    string
	ContractPath string
	ReportPath   string
	Passed       bool
}

type graphContractFile struct {
	ServiceID  string `json:"service_id,omitempty"`
	Thresholds struct {
		EndpointsRecallMin       float64 `json:"endpoints_recall_min,omitempty"`
		QueuePublishRecallMin    float64 `json:"queue_publish_recall_min,omitempty"`
		QueueConsumeRecallMin    float64 `json:"queue_consume_recall_min,omitempty"`
		SchedulerRecallMin       float64 `json:"scheduler_recall_min,omitempty"`
		DependenciesRecallMin    float64 `json:"dependencies_recall_min,omitempty"`
		EndpointsPrecisionMin    float64 `json:"endpoints_precision_min,omitempty"`
		DependenciesPrecisionMin float64 `json:"dependencies_precision_min,omitempty"`
	} `json:"thresholds,omitempty"`
	Expected struct {
		Endpoints      []string `json:"endpoints,omitempty"`
		QueuePublishes []string `json:"queue_publishes,omitempty"`
		QueueConsumes  []string `json:"queue_consumes,omitempty"`
		Schedulers     []string `json:"schedulers,omitempty"`
		Dependencies   []string `json:"dependencies,omitempty"`
	} `json:"expected"`
}

type graphContractReport struct {
	GraphID        string `json:"graph_id"`
	GraphPath      string `json:"graph_path"`
	ContractPath   string `json:"contract_path"`
	GeneratedAtUTC string `json:"generated_at_utc"`
	ServiceID      string `json:"service_id,omitempty"`
	Surfaces       struct {
		Endpoints      contractSurfaceResult `json:"endpoints"`
		QueuePublishes contractSurfaceResult `json:"queue_publishes"`
		QueueConsumes  contractSurfaceResult `json:"queue_consumes"`
		Schedulers     contractSurfaceResult `json:"schedulers"`
		Dependencies   contractSurfaceResult `json:"dependencies"`
	} `json:"surfaces"`
	Gates struct {
		EndpointsRecallMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"endpoints_recall_min"`
		QueuePublishRecallMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"queue_publish_recall_min"`
		QueueConsumeRecallMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"queue_consume_recall_min"`
		SchedulerRecallMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"scheduler_recall_min"`
		DependenciesRecallMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"dependencies_recall_min"`
		EndpointsPrecisionMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"endpoints_precision_min"`
		DependenciesPrecisionMin struct {
			Threshold float64 `json:"threshold"`
			Observed  float64 `json:"observed"`
			Passed    bool    `json:"passed"`
		} `json:"dependencies_precision_min"`
	} `json:"gates"`
	Passed bool `json:"passed"`
}

type contractSurfaceResult struct {
	Expected                    int                      `json:"expected"`
	Observed                    int                      `json:"observed"`
	Matched                     int                      `json:"matched"`
	Precision                   float64                  `json:"precision"`
	Recall                      float64                  `json:"recall"`
	FalsePositives              int                      `json:"false_positives"`
	FalseNegatives              int                      `json:"false_negatives"`
	FalsePositiveSample         []string                 `json:"false_positive_samples,omitempty"`
	FalseNegativeSample         []string                 `json:"false_negative_samples,omitempty"`
	EvidenceSample              []contractEvidenceSample `json:"evidence_samples,omitempty"`
	FalsePositiveEvidenceSample []contractEvidenceSample `json:"false_positive_evidence_samples,omitempty"`
}

type contractEvidenceSample struct {
	Value string   `json:"value"`
	Links []string `json:"links,omitempty"`
}

func runContract(_ context.Context, args []string) error {
	opts, err := parseContractOptions(args)
	if err != nil {
		return err
	}
	res, err := Contract(context.Background(), ContractRequest{
		GraphPath:    opts.GraphPath,
		IndexPath:    opts.IndexPath,
		ContractPath: opts.ContractPath,
		OutPath:      opts.OutPath,
		FailOnGate:   opts.FailOnGate,
	})
	if err != nil {
		return err
	}
	fmt.Println(res.ReportPath)
	return nil
}

func Contract(_ context.Context, req ContractRequest) (ContractResult, error) {
	contractPath := strings.TrimSpace(req.ContractPath)
	if contractPath == "" {
		return ContractResult{}, errors.New("contract path is required")
	}
	contract, err := loadGraphContract(contractPath)
	if err != nil {
		return ContractResult{}, err
	}

	graphPath := strings.TrimSpace(req.GraphPath)
	if graphPath == "" {
		graphPath, err = latestGraphPathFromIndex(req.IndexPath)
		if err != nil {
			return ContractResult{}, err
		}
	}
	graph, err := loadGraph(graphPath)
	if err != nil {
		return ContractResult{}, err
	}

	outPath := strings.TrimSpace(req.OutPath)
	if outPath == "" {
		outPath = filepath.Join(".diffmind", "graph", "contract_report.json")
	}
	report := evaluateGraphContract(graph, graphPath, contractPath, contract)
	if err := writeContractReport(outPath, report); err != nil {
		return ContractResult{}, err
	}
	if req.FailOnGate && !report.Passed {
		return ContractResult{}, errors.New("graph contract gate failed")
	}
	return ContractResult{
		GraphID:      report.GraphID,
		GraphPath:    graphPath,
		ContractPath: contractPath,
		ReportPath:   outPath,
		Passed:       report.Passed,
	}, nil
}

func writeContractReport(path string, report graphContractReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode contract report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("prepare contract report dir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write contract report %s: %w", path, err)
	}
	return nil
}

func parseContractOptions(args []string) (contractOptions, error) {
	fs := flag.NewFlagSet("graph contract", flag.ContinueOnError)
	graphPath := fs.String("graph", "", "Graph JSON path (if empty uses latest from --index)")
	indexPath := fs.String("index", filepath.Join(".diffmind", "graph", "index.json"), "Graph index path")
	contractPath := fs.String("contract", "", "Expected graph contract JSON path")
	outPath := fs.String("out", filepath.Join(".diffmind", "graph", "contract_report.json"), "Output contract report path")
	failOnGate := fs.Bool("fail-on-gate", false, "Exit non-zero if contract gate fails")
	if err := fs.Parse(filterContractArgs(args)); err != nil {
		return contractOptions{}, fmt.Errorf("parse graph contract flags: %w", err)
	}
	return contractOptions{
		GraphPath:    strings.TrimSpace(*graphPath),
		IndexPath:    strings.TrimSpace(*indexPath),
		ContractPath: strings.TrimSpace(*contractPath),
		OutPath:      strings.TrimSpace(*outPath),
		FailOnGate:   *failOnGate,
	}, nil
}

func filterContractArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--graph" || arg == "--index" || arg == "--contract" || arg == "--out":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case arg == "--fail-on-gate":
			out = append(out, arg)
		case strings.HasPrefix(arg, "--graph=") || strings.HasPrefix(arg, "--index=") || strings.HasPrefix(arg, "--contract=") || strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--fail-on-gate="):
			out = append(out, arg)
		}
	}
	return out
}

func loadGraphContract(path string) (graphContractFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return graphContractFile{}, fmt.Errorf("read contract %s: %w", path, err)
	}
	var c graphContractFile
	if err := json.Unmarshal(data, &c); err != nil {
		return graphContractFile{}, fmt.Errorf("decode contract %s: %w", path, err)
	}
	return c, nil
}

func evaluateGraphContract(graph graphschema.Graph, graphPath string, contractPath string, contract graphContractFile) graphContractReport {
	rep := graphContractReport{
		GraphID:        graph.GraphID,
		GraphPath:      graphPath,
		ContractPath:   contractPath,
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		ServiceID:      strings.TrimSpace(contract.ServiceID),
	}
	observed := extractObservedContractSurfaces(graph)

	rep.Surfaces.Endpoints = compareSets(contract.Expected.Endpoints, observed.Endpoints, observed.EndpointEvidence)
	rep.Surfaces.QueuePublishes = compareSets(contract.Expected.QueuePublishes, observed.QueuePublishes, observed.QueuePublishEvidence)
	rep.Surfaces.QueueConsumes = compareSets(contract.Expected.QueueConsumes, observed.QueueConsumes, observed.QueueConsumeEvidence)
	rep.Surfaces.Schedulers = compareSets(contract.Expected.Schedulers, observed.Schedulers, observed.SchedulerEvidence)
	rep.Surfaces.Dependencies = compareSets(contract.Expected.Dependencies, observed.Dependencies, observed.DependencyEvidence)

	endpointRecallMin := contract.Thresholds.EndpointsRecallMin
	if endpointRecallMin <= 0 {
		endpointRecallMin = 0.95
	}
	queuePublishRecallMin := contract.Thresholds.QueuePublishRecallMin
	if queuePublishRecallMin <= 0 {
		queuePublishRecallMin = 0.95
	}
	queueConsumeRecallMin := contract.Thresholds.QueueConsumeRecallMin
	if queueConsumeRecallMin <= 0 {
		queueConsumeRecallMin = 0.95
	}
	schedulerRecallMin := contract.Thresholds.SchedulerRecallMin
	if schedulerRecallMin <= 0 {
		schedulerRecallMin = 0.90
	}
	dependencyRecallMin := contract.Thresholds.DependenciesRecallMin
	if dependencyRecallMin <= 0 {
		dependencyRecallMin = 0.90
	}
	endpointPrecisionMin := contract.Thresholds.EndpointsPrecisionMin
	if endpointPrecisionMin <= 0 {
		endpointPrecisionMin = 0.80
	}
	dependencyPrecisionMin := contract.Thresholds.DependenciesPrecisionMin
	if dependencyPrecisionMin <= 0 {
		dependencyPrecisionMin = 0.75
	}

	rep.Gates.EndpointsRecallMin.Threshold = endpointRecallMin
	rep.Gates.EndpointsRecallMin.Observed = rep.Surfaces.Endpoints.Recall
	rep.Gates.EndpointsRecallMin.Passed = rep.Surfaces.Endpoints.Expected == 0 || rep.Surfaces.Endpoints.Recall >= endpointRecallMin

	rep.Gates.QueuePublishRecallMin.Threshold = queuePublishRecallMin
	rep.Gates.QueuePublishRecallMin.Observed = rep.Surfaces.QueuePublishes.Recall
	rep.Gates.QueuePublishRecallMin.Passed = rep.Surfaces.QueuePublishes.Expected == 0 || rep.Surfaces.QueuePublishes.Recall >= queuePublishRecallMin

	rep.Gates.QueueConsumeRecallMin.Threshold = queueConsumeRecallMin
	rep.Gates.QueueConsumeRecallMin.Observed = rep.Surfaces.QueueConsumes.Recall
	rep.Gates.QueueConsumeRecallMin.Passed = rep.Surfaces.QueueConsumes.Expected == 0 || rep.Surfaces.QueueConsumes.Recall >= queueConsumeRecallMin

	rep.Gates.SchedulerRecallMin.Threshold = schedulerRecallMin
	rep.Gates.SchedulerRecallMin.Observed = rep.Surfaces.Schedulers.Recall
	rep.Gates.SchedulerRecallMin.Passed = rep.Surfaces.Schedulers.Expected == 0 || rep.Surfaces.Schedulers.Recall >= schedulerRecallMin

	rep.Gates.DependenciesRecallMin.Threshold = dependencyRecallMin
	rep.Gates.DependenciesRecallMin.Observed = rep.Surfaces.Dependencies.Recall
	rep.Gates.DependenciesRecallMin.Passed = rep.Surfaces.Dependencies.Expected == 0 || rep.Surfaces.Dependencies.Recall >= dependencyRecallMin

	rep.Gates.EndpointsPrecisionMin.Threshold = endpointPrecisionMin
	rep.Gates.EndpointsPrecisionMin.Observed = rep.Surfaces.Endpoints.Precision
	rep.Gates.EndpointsPrecisionMin.Passed = rep.Surfaces.Endpoints.Expected == 0 || rep.Surfaces.Endpoints.Observed == 0 || rep.Surfaces.Endpoints.Precision >= endpointPrecisionMin

	rep.Gates.DependenciesPrecisionMin.Threshold = dependencyPrecisionMin
	rep.Gates.DependenciesPrecisionMin.Observed = rep.Surfaces.Dependencies.Precision
	rep.Gates.DependenciesPrecisionMin.Passed = rep.Surfaces.Dependencies.Expected == 0 || rep.Surfaces.Dependencies.Observed == 0 || rep.Surfaces.Dependencies.Precision >= dependencyPrecisionMin

	rep.Passed = rep.Gates.EndpointsRecallMin.Passed &&
		rep.Gates.QueuePublishRecallMin.Passed &&
		rep.Gates.QueueConsumeRecallMin.Passed &&
		rep.Gates.SchedulerRecallMin.Passed &&
		rep.Gates.DependenciesRecallMin.Passed &&
		rep.Gates.EndpointsPrecisionMin.Passed &&
		rep.Gates.DependenciesPrecisionMin.Passed
	return rep
}

type observedSurfaces struct {
	Endpoints            []string
	QueuePublishes       []string
	QueueConsumes        []string
	Schedulers           []string
	Dependencies         []string
	EndpointEvidence     map[string][]string
	QueuePublishEvidence map[string][]string
	QueueConsumeEvidence map[string][]string
	SchedulerEvidence    map[string][]string
	DependencyEvidence   map[string][]string
}

func extractObservedContractSurfaces(graph graphschema.Graph) observedSurfaces {
	nodeByID := map[string]graphschema.Node{}
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}

	endpoints := map[string]struct{}{}
	schedulers := map[string]struct{}{}
	deps := map[string]struct{}{}
	queuePub := map[string]struct{}{}
	queueCon := map[string]struct{}{}
	endpointEvidence := map[string][]string{}
	queuePublishEvidence := map[string][]string{}
	queueConsumeEvidence := map[string][]string{}
	schedulerEvidence := map[string][]string{}
	dependencyEvidence := map[string][]string{}

	for _, n := range graph.Nodes {
		switch n.Type {
		case "endpoint":
			method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(n.Attributes["method"])))
			path := strings.TrimSpace(fmt.Sprint(n.Attributes["path"]))
			if method == "" {
				method = "ANY"
			}
			if method == "SCHEDULE" {
				label := strings.TrimSpace(n.Label)
				if label == "" {
					label = strings.TrimSpace(path)
				}
				if label != "" {
					schedulers[label] = struct{}{}
					schedulerEvidence[label] = appendUnique(schedulerEvidence[label], fmt.Sprintf("graph://node/%s", n.ID))
				}
				continue
			}
			if path != "" {
				key := method + " " + path
				endpoints[key] = struct{}{}
				endpointEvidence[key] = appendUnique(endpointEvidence[key], fmt.Sprintf("graph://node/%s", n.ID))
			}
		}
	}

	for _, e := range graph.Edges {
		switch e.Type {
		case "service_publishes_queue":
			if n, ok := nodeByID[e.TargetID]; ok {
				label := strings.TrimSpace(n.Label)
				if label != "" {
					queuePub[label] = struct{}{}
					queuePublishEvidence[label] = appendUnique(queuePublishEvidence[label], fmt.Sprintf("graph://edge/%s", e.ID))
					queuePublishEvidence[label] = appendUnique(queuePublishEvidence[label], fmt.Sprintf("graph://node/%s", n.ID))
					depKey := "queue:" + label
					deps[depKey] = struct{}{}
					dependencyEvidence[depKey] = appendUnique(dependencyEvidence[depKey], fmt.Sprintf("graph://edge/%s", e.ID))
					dependencyEvidence[depKey] = appendUnique(dependencyEvidence[depKey], fmt.Sprintf("graph://node/%s", n.ID))
				}
			}
		case "queue_delivers_to_service":
			if n, ok := nodeByID[e.SourceID]; ok {
				label := strings.TrimSpace(n.Label)
				if label != "" {
					queueCon[label] = struct{}{}
					queueConsumeEvidence[label] = appendUnique(queueConsumeEvidence[label], fmt.Sprintf("graph://edge/%s", e.ID))
					queueConsumeEvidence[label] = appendUnique(queueConsumeEvidence[label], fmt.Sprintf("graph://node/%s", n.ID))
				}
			}
		case "service_reads_db", "service_writes_db":
			if n, ok := nodeByID[e.TargetID]; ok {
				label := strings.TrimSpace(n.Label)
				if label != "" {
					key := "db:" + label
					deps[key] = struct{}{}
					dependencyEvidence[key] = appendUnique(dependencyEvidence[key], fmt.Sprintf("graph://edge/%s", e.ID))
					dependencyEvidence[key] = appendUnique(dependencyEvidence[key], fmt.Sprintf("graph://node/%s", n.ID))
				}
			}
		case "service_calls_service":
			targetService := strings.TrimSpace(fmt.Sprint(e.Attributes["target_service_id"]))
			if targetService == "" {
				if n, ok := nodeByID[e.TargetID]; ok {
					targetService = strings.TrimSpace(n.ServiceID)
				}
			}
			if targetService != "" {
				key := "service:" + targetService
				deps[key] = struct{}{}
				dependencyEvidence[key] = appendUnique(dependencyEvidence[key], fmt.Sprintf("graph://edge/%s", e.ID))
				dependencyEvidence[key] = appendUnique(dependencyEvidence[key], fmt.Sprintf("graph://node/%s", e.TargetID))
			}
		case "service_calls_endpoint":
			if n, ok := nodeByID[e.TargetID]; ok {
				method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(n.Attributes["method"])))
				path := strings.TrimSpace(fmt.Sprint(n.Attributes["path"]))
				if method == "" {
					method = "ANY"
				}
				if path != "" {
					key := "http:" + method + " " + path
					deps[key] = struct{}{}
					dependencyEvidence[key] = appendUnique(dependencyEvidence[key], fmt.Sprintf("graph://edge/%s", e.ID))
					dependencyEvidence[key] = appendUnique(dependencyEvidence[key], fmt.Sprintf("graph://node/%s", n.ID))
				}
			}
		}
	}

	return observedSurfaces{
		Endpoints:            sortedSetKeys(endpoints),
		QueuePublishes:       sortedSetKeys(queuePub),
		QueueConsumes:        sortedSetKeys(queueCon),
		Schedulers:           sortedSetKeys(schedulers),
		Dependencies:         sortedSetKeys(deps),
		EndpointEvidence:     endpointEvidence,
		QueuePublishEvidence: queuePublishEvidence,
		QueueConsumeEvidence: queueConsumeEvidence,
		SchedulerEvidence:    schedulerEvidence,
		DependencyEvidence:   dependencyEvidence,
	}
}

func compareSets(expected []string, observed []string, evidence map[string][]string) contractSurfaceResult {
	expSet := make(map[string]struct{}, len(expected))
	obsSet := make(map[string]struct{}, len(observed))
	for _, item := range expected {
		v := strings.TrimSpace(item)
		if v != "" {
			expSet[v] = struct{}{}
		}
	}
	for _, item := range observed {
		v := strings.TrimSpace(item)
		if v != "" {
			obsSet[v] = struct{}{}
		}
	}
	matched := 0
	falseNeg := []string{}
	for v := range expSet {
		if _, ok := obsSet[v]; ok {
			matched++
		} else {
			falseNeg = append(falseNeg, v)
		}
	}
	falsePos := []string{}
	for v := range obsSet {
		if _, ok := expSet[v]; !ok {
			falsePos = append(falsePos, v)
		}
	}
	sort.Strings(falseNeg)
	sort.Strings(falsePos)
	observedCount := len(obsSet)
	expectedCount := len(expSet)
	return contractSurfaceResult{
		Expected:                    expectedCount,
		Observed:                    observedCount,
		Matched:                     matched,
		Precision:                   ratio(matched, observedCount),
		Recall:                      ratio(matched, expectedCount),
		FalsePositives:              len(falsePos),
		FalseNegatives:              len(falseNeg),
		FalsePositiveSample:         sampleList(falsePos, 20),
		FalseNegativeSample:         sampleList(falseNeg, 20),
		EvidenceSample:              buildEvidenceSamples(observed, evidence, 20),
		FalsePositiveEvidenceSample: buildEvidenceSamples(falsePos, evidence, 20),
	}
}

func sortedSetKeys(in map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sampleList(items []string, maxCount int) []string {
	if len(items) <= maxCount {
		return items
	}
	return append([]string(nil), items[:maxCount]...)
}

func appendUnique(in []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return in
	}
	for _, existing := range in {
		if existing == value {
			return in
		}
	}
	return append(in, value)
}

func buildEvidenceSamples(values []string, evidence map[string][]string, maxCount int) []contractEvidenceSample {
	if len(values) == 0 {
		return nil
	}
	limit := len(values)
	if limit > maxCount {
		limit = maxCount
	}
	out := make([]contractEvidenceSample, 0, limit)
	for _, value := range values[:limit] {
		sample := contractEvidenceSample{Value: value}
		if links := evidence[strings.TrimSpace(value)]; len(links) > 0 {
			sample.Links = append([]string(nil), links...)
		}
		out = append(out, sample)
	}
	return out
}
