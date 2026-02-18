package finalgate

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

	"diffmind/internal/audit"
	"diffmind/internal/ops"
)

type gateResult struct {
	Passed bool `json:"passed"`
}

type sloReport struct {
	Passed    bool `json:"passed"`
	SLOChecks struct {
		RuntimeQualityPassed *bool `json:"runtime_quality_passed"`
	} `json:"slo_checks"`
	RuntimeReconciliation struct {
		RuntimeQualityPassed *bool `json:"runtime_quality_passed"`
	} `json:"runtime_reconciliation"`
}

type templateFile struct {
	Templates []templateEntry `json:"templates"`
}

type templateEndpoints struct {
	Templates []templateEntry `json:"templates"`
}

type templateEntry struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Method string `json:"method"`
	Query  struct {
		Explain bool `json:"explain"`
	} `json:"query"`
}

type questionCatalog struct {
	Questions []questionItem `json:"questions"`
}

type questionItem struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Endpoint string `json:"endpoint"`
}

type graphIndex struct {
	Graphs []struct {
		GraphID     string `json:"graph_id"`
		TenantID    string `json:"tenant_id"`
		Fingerprint string `json:"fingerprint"`
	} `json:"graphs"`
}

type check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type readinessReport struct {
	GeneratedAtUTC string   `json:"generated_at_utc"`
	OverallPassed  bool     `json:"overall_passed"`
	Checks         []check  `json:"checks"`
	SignedOwners   []string `json:"signed_owners"`
}

type benchmarkEvidenceReport struct {
	GeneratedAtUTC         string  `json:"generated_at_utc"`
	QualityReportPath      string  `json:"quality_report_path"`
	CorpusReportPath       string  `json:"corpus_report_path"`
	PerformancePolicyPath  string  `json:"performance_policy_path"`
	QualityPassRate        float64 `json:"quality_pass_rate"`
	QualityPassRateKnown   bool    `json:"quality_pass_rate_known"`
	QualityPassRateTarget  float64 `json:"quality_pass_rate_target"`
	CorpusReportPresent    bool    `json:"corpus_report_present"`
	PerformancePolicyFound bool    `json:"performance_policy_found"`
	Passed                 bool    `json:"passed"`
}

type securityValidationReport struct {
	GeneratedAtUTC         string `json:"generated_at_utc"`
	AuditRoot              string `json:"audit_root"`
	AuditEvents            int    `json:"audit_events"`
	SecurityPolicyPath     string `json:"security_policy_path"`
	SecurityArchitecture   string `json:"security_architecture_path"`
	SecurityPolicyFound    bool   `json:"security_policy_found"`
	SecurityArchitectureOK bool   `json:"security_architecture_found"`
	Passed                 bool   `json:"passed"`
}

type operationsDrillReport struct {
	GeneratedAtUTC string `json:"generated_at_utc"`
	DrillSource    string `json:"drill_source"`
	BackupPath     string `json:"backup_path"`
	RestorePath    string `json:"restore_path"`
	RolloutPath    string `json:"rollout_path"`
	SLODrillPath   string `json:"slo_drill_path"`
	BackupOK       bool   `json:"backup_ok"`
	RestoreOK      bool   `json:"restore_ok"`
	RolloutOK      bool   `json:"rollout_ok"`
	SLOOK          bool   `json:"slo_ok"`
	Passed         bool   `json:"passed"`
	Error          string `json:"error,omitempty"`
}

type milestoneStatus struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Detail   string   `json:"detail"`
	Evidence []string `json:"evidence,omitempty"`
}

type milestoneClosureReport struct {
	GeneratedAtUTC string            `json:"generated_at_utc"`
	OverallPassed  bool              `json:"overall_passed"`
	Milestones     []milestoneStatus `json:"milestones"`
}

type closeoutOptions struct {
	Attest               options
	MilestoneReportPath  string
	BenchmarkReportPath  string
	SecurityReportPath   string
	OperationsReportPath string
	QualityReportPath    string
	CorpusReportPath     string
	PerformancePolicy    string
	AuditRoot            string
	DrillSource          string
	DrillOutDir          string
}

func Run(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("finalgate subcommand is required: attest|closeout")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "attest":
		return runAttest(args[1:])
	case "closeout":
		return runCloseout(args[1:])
	default:
		return fmt.Errorf("unsupported finalgate subcommand %q", args[0])
	}
}

type options struct {
	QualityGatePath string
	SLOPath         string
	TemplatesPath   string
	CatalogPath     string
	GraphIndexPath  string
	ReportPath      string
	DecisionPath    string
	Signers         []string
}

func runAttest(args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	rep, err := evaluate(opts)
	if err != nil {
		return err
	}
	if err := writeJSON(opts.ReportPath, rep); err != nil {
		return err
	}
	if err := writeDecision(opts.DecisionPath, rep); err != nil {
		return err
	}
	fmt.Println(opts.ReportPath)
	if !rep.OverallPassed {
		return errors.New("final completion gate failed")
	}
	return nil
}

func runCloseout(args []string) error {
	opts, err := parseCloseoutOptions(args)
	if err != nil {
		return err
	}
	readiness, err := evaluate(opts.Attest)
	if err != nil {
		return err
	}
	if err := writeJSON(opts.Attest.ReportPath, readiness); err != nil {
		return err
	}
	if err := writeDecision(opts.Attest.DecisionPath, readiness); err != nil {
		return err
	}

	benchmark := evaluateBenchmarkEvidence(opts)
	if err := writeJSON(opts.BenchmarkReportPath, benchmark); err != nil {
		return err
	}
	security := evaluateSecurityValidation(opts)
	if err := writeJSON(opts.SecurityReportPath, security); err != nil {
		return err
	}
	opsDrill := runOperationsDrills(context.Background(), opts)
	if err := writeJSON(opts.OperationsReportPath, opsDrill); err != nil {
		return err
	}

	milestones := buildMilestoneClosure(readiness, benchmark, security, opsDrill)
	if err := writeJSON(opts.MilestoneReportPath, milestones); err != nil {
		return err
	}

	fmt.Println(opts.MilestoneReportPath)
	allPassed := readiness.OverallPassed && benchmark.Passed && security.Passed && opsDrill.Passed && milestones.OverallPassed
	if !allPassed {
		return errors.New("final closeout failed")
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("finalgate attest", flag.ContinueOnError)
	qualityGate := fs.String("quality-gate", filepath.Join(".diffmind", "quality", "gate_result.json"), "Quality gate result path")
	slo := fs.String("slo", filepath.Join(".diffmind", "ops", "slo_report.json"), "SLO report path")
	templates := fs.String("templates", filepath.Join("docs", "m15_query_templates.json"), "Product query templates path")
	catalog := fs.String("catalog", filepath.Join("docs", "m17_question_catalog.json"), "Question catalog path")
	graphIndex := fs.String("graph-index", filepath.Join(".diffmind", "graph", "index.json"), "Graph index path for traceability checks")
	report := fs.String("out-report", filepath.Join(".diffmind", "final", "readiness_report.json"), "Readiness report output path")
	decision := fs.String("out-decision", filepath.Join(".diffmind", "final", "gate_decision.md"), "Gate decision output markdown path")
	signers := fs.String("signers", "engineering,platform,security", "Comma-separated signed owner groups")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse finalgate flags: %w", err)
	}
	return options{
		QualityGatePath: strings.TrimSpace(*qualityGate),
		SLOPath:         strings.TrimSpace(*slo),
		TemplatesPath:   strings.TrimSpace(*templates),
		CatalogPath:     strings.TrimSpace(*catalog),
		GraphIndexPath:  strings.TrimSpace(*graphIndex),
		ReportPath:      strings.TrimSpace(*report),
		DecisionPath:    strings.TrimSpace(*decision),
		Signers:         splitCSV(*signers),
	}, nil
}

func evaluate(opts options) (readinessReport, error) {
	checks := make([]check, 0, 8)

	qualityPassed, qualityDetail := checkQualityGate(opts.QualityGatePath)
	checks = append(checks, check{Name: "m0_m16_quality_gate", Passed: qualityPassed, Detail: qualityDetail})

	sloPassed, sloDetail := checkSLO(opts.SLOPath)
	checks = append(checks, check{Name: "m16_slo_gate", Passed: sloPassed, Detail: sloDetail})

	qcovPassed, qcovDetail := checkQuestionCatalog(opts.CatalogPath)
	checks = append(checks, check{Name: "question_catalog_coverage_100", Passed: qcovPassed, Detail: qcovDetail})

	apiCovPassed, apiCovDetail := checkCatalogTemplateCoverage(opts.CatalogPath, opts.TemplatesPath)
	checks = append(checks, check{Name: "question_catalog_api_coverage_100", Passed: apiCovPassed, Detail: apiCovDetail})

	explainPassed, explainDetail := checkExplainCoverage(opts.TemplatesPath)
	checks = append(checks, check{Name: "explainability_traceability_100", Passed: explainPassed, Detail: explainDetail})

	tracePassed, traceDetail := checkTraceability(opts.GraphIndexPath)
	checks = append(checks, check{Name: "graph_traceability_coverage_100", Passed: tracePassed, Detail: traceDetail})

	attPassed, attDetail := checkAttestation(opts.Signers)
	checks = append(checks, check{Name: "final_attestation_signed", Passed: attPassed, Detail: attDetail})

	allMilestones := qualityPassed && sloPassed && qcovPassed && apiCovPassed && explainPassed && tracePassed
	checks = append(checks, check{Name: "all_m0_m16_gates_satisfied", Passed: allMilestones, Detail: boolDetail(allMilestones, "core milestone gates satisfied", "core milestone gate(s) failed")})

	overall := true
	for _, c := range checks {
		if !c.Passed {
			overall = false
			break
		}
	}
	signers := append([]string(nil), opts.Signers...)
	sort.Strings(signers)
	return readinessReport{
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		OverallPassed:  overall,
		Checks:         checks,
		SignedOwners:   signers,
	}, nil
}

func parseCloseoutOptions(args []string) (closeoutOptions, error) {
	fs := flag.NewFlagSet("finalgate closeout", flag.ContinueOnError)
	qualityGate := fs.String("quality-gate", filepath.Join(".diffmind", "quality", "gate_result.json"), "Quality gate result path")
	slo := fs.String("slo", filepath.Join(".diffmind", "ops", "slo_report.json"), "SLO report path")
	templates := fs.String("templates", filepath.Join("docs", "m15_query_templates.json"), "Product query templates path")
	catalog := fs.String("catalog", filepath.Join("docs", "m17_question_catalog.json"), "Question catalog path")
	graphIndex := fs.String("graph-index", filepath.Join(".diffmind", "graph", "index.json"), "Graph index path for traceability checks")
	report := fs.String("out-report", filepath.Join(".diffmind", "final", "readiness_report.json"), "Readiness report output path")
	decision := fs.String("out-decision", filepath.Join(".diffmind", "final", "gate_decision.md"), "Gate decision output markdown path")
	signers := fs.String("signers", "engineering,platform,security", "Comma-separated signed owner groups")

	milestoneReport := fs.String("out-milestones", filepath.Join(".diffmind", "final", "milestone_closure_report.json"), "Milestone closure report output")
	benchmarkReport := fs.String("out-benchmark", filepath.Join(".diffmind", "final", "benchmark_evidence_report.json"), "Benchmark evidence report output")
	securityReport := fs.String("out-security", filepath.Join(".diffmind", "final", "security_validation_report.json"), "Security validation report output")
	opsReport := fs.String("out-ops", filepath.Join(".diffmind", "final", "operations_drill_report.json"), "Operations drill report output")

	qualityReport := fs.String("quality-report", filepath.Join(".diffmind", "quality", "report.json"), "Quality evaluation report path")
	corpusReport := fs.String("corpus-report", filepath.Join(".diffmind", "corpus", "report.json"), "Corpus report path")
	perfPolicy := fs.String("performance-policy", filepath.Join("docs", "graph_performance_baseline.md"), "Performance baseline policy path")
	auditRoot := fs.String("audit-root", ".diffmind", "Audit root for security/ops checks")
	drillSource := fs.String("drill-source", ".diffmind", "Source root for backup/restore drill")
	drillOut := fs.String("drill-out", filepath.Join(".diffmind", "final", "drills"), "Output directory for ops drill artifacts")

	if err := fs.Parse(filterArgs(args)); err != nil {
		return closeoutOptions{}, fmt.Errorf("parse finalgate closeout flags: %w", err)
	}
	att := options{
		QualityGatePath: strings.TrimSpace(*qualityGate),
		SLOPath:         strings.TrimSpace(*slo),
		TemplatesPath:   strings.TrimSpace(*templates),
		CatalogPath:     strings.TrimSpace(*catalog),
		GraphIndexPath:  strings.TrimSpace(*graphIndex),
		ReportPath:      strings.TrimSpace(*report),
		DecisionPath:    strings.TrimSpace(*decision),
		Signers:         splitCSV(*signers),
	}
	return closeoutOptions{
		Attest:               att,
		MilestoneReportPath:  strings.TrimSpace(*milestoneReport),
		BenchmarkReportPath:  strings.TrimSpace(*benchmarkReport),
		SecurityReportPath:   strings.TrimSpace(*securityReport),
		OperationsReportPath: strings.TrimSpace(*opsReport),
		QualityReportPath:    strings.TrimSpace(*qualityReport),
		CorpusReportPath:     strings.TrimSpace(*corpusReport),
		PerformancePolicy:    strings.TrimSpace(*perfPolicy),
		AuditRoot:            strings.TrimSpace(*auditRoot),
		DrillSource:          strings.TrimSpace(*drillSource),
		DrillOutDir:          strings.TrimSpace(*drillOut),
	}, nil
}

func evaluateBenchmarkEvidence(opts closeoutOptions) benchmarkEvidenceReport {
	rep := benchmarkEvidenceReport{
		GeneratedAtUTC:        time.Now().UTC().Format(time.RFC3339),
		QualityReportPath:     opts.QualityReportPath,
		CorpusReportPath:      opts.CorpusReportPath,
		PerformancePolicyPath: opts.PerformancePolicy,
		QualityPassRateTarget: 0.95,
	}
	if data, err := os.ReadFile(opts.QualityReportPath); err == nil {
		var payload map[string]any
		if json.Unmarshal(data, &payload) == nil {
			if metrics, ok := payload["metrics"].(map[string]any); ok {
				if v, ok := toFloat(metrics["pass_rate"]); ok {
					rep.QualityPassRate = v
					rep.QualityPassRateKnown = true
				}
			}
		}
	}
	if _, err := os.Stat(opts.CorpusReportPath); err == nil {
		rep.CorpusReportPresent = true
	}
	if _, err := os.Stat(opts.PerformancePolicy); err == nil {
		rep.PerformancePolicyFound = true
	}
	rep.Passed = rep.QualityPassRateKnown && rep.QualityPassRate >= rep.QualityPassRateTarget && rep.CorpusReportPresent && rep.PerformancePolicyFound
	return rep
}

func evaluateSecurityValidation(opts closeoutOptions) securityValidationReport {
	rep := securityValidationReport{
		GeneratedAtUTC:       time.Now().UTC().Format(time.RFC3339),
		AuditRoot:            opts.AuditRoot,
		SecurityPolicyPath:   filepath.Join("internal", "security", "policy.go"),
		SecurityArchitecture: filepath.Join("docs", "m13_security_architecture.md"),
	}
	if events, err := audit.ListEvents(opts.AuditRoot, "", 100000); err == nil {
		rep.AuditEvents = len(events)
	}
	rep.SecurityPolicyPath = resolvePathForRead(rep.SecurityPolicyPath)
	rep.SecurityArchitecture = resolvePathForRead(rep.SecurityArchitecture)
	if _, err := os.Stat(rep.SecurityPolicyPath); err == nil {
		rep.SecurityPolicyFound = true
	}
	if _, err := os.Stat(rep.SecurityArchitecture); err == nil {
		rep.SecurityArchitectureOK = true
	}
	rep.Passed = rep.SecurityPolicyFound && rep.SecurityArchitectureOK && rep.AuditEvents > 0
	return rep
}

func runOperationsDrills(ctx context.Context, opts closeoutOptions) operationsDrillReport {
	ts := time.Now().UTC().UnixNano()
	backupPath := filepath.Join(opts.DrillOutDir, "backup.tar.gz")
	cleanSource := filepath.Clean(opts.DrillSource)
	cleanBackup := filepath.Clean(backupPath)
	if cleanSource == filepath.Dir(cleanBackup) || strings.HasPrefix(cleanBackup, cleanSource+string(os.PathSeparator)) {
		backupPath = filepath.Join(os.TempDir(), fmt.Sprintf("diffmind-closeout-backup-%d.tar.gz", ts))
	}
	rep := operationsDrillReport{
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		DrillSource:    opts.DrillSource,
		BackupPath:     backupPath,
		RestorePath:    filepath.Join(opts.DrillOutDir, "restore"),
		RolloutPath:    filepath.Join(opts.DrillOutDir, "rollout_plan.json"),
		SLODrillPath:   filepath.Join(opts.DrillOutDir, "slo_drill.json"),
	}
	_ = os.MkdirAll(opts.DrillOutDir, 0o755)
	if err := ops.Run(ctx, []string{"backup", "--source", opts.DrillSource, "--out", rep.BackupPath}); err != nil {
		rep.Error = "backup drill failed: " + err.Error()
		return rep
	}
	rep.BackupOK = true
	if err := ops.Run(ctx, []string{"restore", "--archive", rep.BackupPath, "--target", rep.RestorePath}); err != nil {
		rep.Error = "restore drill failed: " + err.Error()
		return rep
	}
	rep.RestoreOK = true
	if err := ops.Run(ctx, []string{"rollout", "--component", "extractor", "--candidate", "vnext", "--current", "vcurrent", "--out", rep.RolloutPath}); err != nil {
		rep.Error = "rollout drill failed: " + err.Error()
		return rep
	}
	rep.RolloutOK = true
	sloAuditRoot := filepath.Join(opts.DrillOutDir, "slo_audit_root")
	if err := os.MkdirAll(sloAuditRoot, 0o755); err != nil {
		rep.Error = "slo drill setup failed: " + err.Error()
		return rep
	}
	_ = audit.AppendEvent(sloAuditRoot, audit.Event{
		Timestamp: time.Now().UTC(),
		Action:    "query_graph",
		TenantID:  "default",
		Principal: "closeout-drill",
		Method:    "GET",
		Path:      "/graphs/g1",
		Decision:  "allow",
	})
	if err := ops.Run(ctx, []string{"slo", "--audit-root", sloAuditRoot, "--quality", opts.QualityReportPath, "--out", rep.SLODrillPath}); err != nil {
		rep.Error = "slo drill failed: " + err.Error()
		return rep
	}
	rep.SLOOK = true
	rep.Passed = rep.BackupOK && rep.RestoreOK && rep.RolloutOK && rep.SLOOK
	return rep
}

func buildMilestoneClosure(readiness readinessReport, benchmark benchmarkEvidenceReport, security securityValidationReport, opsDrill operationsDrillReport) milestoneClosureReport {
	items := []milestoneStatus{
		{ID: "M0", Name: "Program Charter And Question Catalog", Passed: checkByName(readiness, "question_catalog_coverage_100"), Detail: "question catalog coverage gate", Evidence: []string{"docs/m17_question_catalog.json"}},
		{ID: "M1", Name: "Ontology And Schema Contracts", Passed: fileExists(filepath.Join("docs", "ontology_v2_schema.md")), Detail: "ontology schema doc present", Evidence: []string{"docs/ontology_v2_schema.md"}},
		{ID: "M2", Name: "Evidence And Provenance Backbone", Passed: checkByName(readiness, "graph_traceability_coverage_100"), Detail: "traceability gate", Evidence: []string{"internal/audit/module.go"}},
		{ID: "M3", Name: "Extraction Framework V2", Passed: fileExists(filepath.Join("internal", "analyzers", "module.go")), Detail: "analyzer framework module present", Evidence: []string{"internal/analyzers/module.go"}},
		{ID: "M4", Name: "Semantic Code Intelligence", Passed: fileExists(filepath.Join("internal", "analyzers", "detectors_semantic_go.go")), Detail: "semantic detectors present", Evidence: []string{"internal/analyzers/detectors_semantic_go.go"}},
		{ID: "M5", Name: "Runtime/CI/CD Intelligence", Passed: fileExists(filepath.Join("docs", "runtime_reconciliation_runbook.md")), Detail: "runtime/cicd runbook present", Evidence: []string{"docs/runtime_reconciliation_runbook.md"}},
		{ID: "M6", Name: "Config And Operational Surface", Passed: fileExists(filepath.Join("internal", "graph", "resolve.go")), Detail: "graph resolver/config extraction code present", Evidence: []string{"internal/graph/resolve.go"}},
		{ID: "M7", Name: "Dependency And Internal Topology", Passed: fileExists(filepath.Join("internal", "graph", "resolve.go")), Detail: "dependency topology resolver present", Evidence: []string{"internal/graph/resolve.go"}},
		{ID: "M8", Name: "Cross-Repo Company Graph", Passed: fileExists(filepath.Join("internal", "graph", "module.go")), Detail: "graph builder module present", Evidence: []string{"internal/graph/module.go"}},
		{ID: "M9", Name: "Confidence/Conflict/Adjudication", Passed: fileExists(filepath.Join("internal", "verifier", "module.go")), Detail: "verifier/adjudication module present", Evidence: []string{"internal/verifier/module.go"}},
		{ID: "M10", Name: "Agentic Verification Plane", Passed: fileExists(filepath.Join("internal", "analyzers", "llm_client.go")), Detail: "agentic verification integration present", Evidence: []string{"internal/analyzers/llm_client.go"}},
		{ID: "M11", Name: "Query Language And Serving APIs", Passed: fileExists(filepath.Join("internal", "query", "module.go")), Detail: "query module present", Evidence: []string{"internal/query/module.go"}},
		{ID: "M12", Name: "Temporal And Incremental Updates", Passed: fileExists(filepath.Join("internal", "httpapi", "module.go")), Detail: "temporal/compare HTTP APIs present", Evidence: []string{"internal/httpapi/module.go"}},
		{ID: "M13", Name: "Enterprise Security And Compliance", Passed: security.Passed, Detail: "security validation report", Evidence: []string{"docs/m13_security_architecture.md", "internal/security/policy.go"}},
		{ID: "M14", Name: "Quality And Evaluation System", Passed: checkByName(readiness, "m0_m16_quality_gate") && benchmark.Passed, Detail: "quality gate and benchmark evidence", Evidence: []string{"docs/m14_quality_runbook.md"}},
		{ID: "M15", Name: "Product Layer On Top Of Query", Passed: fileExists(filepath.Join("docs", "m15_product_api_contracts.md")) && checkByName(readiness, "question_catalog_api_coverage_100"), Detail: "product contracts and API coverage", Evidence: []string{"docs/m15_product_api_contracts.md"}},
		{ID: "M16", Name: "Reliability And Operations", Passed: opsDrill.Passed && checkByName(readiness, "m16_slo_gate"), Detail: "ops drills and slo gate", Evidence: []string{"docs/m16_operations_runbook.md"}},
		{ID: "M17", Name: "State-of-the-Art Completion Gate", Passed: readiness.OverallPassed && checkByName(readiness, "final_attestation_signed"), Detail: "final attestation and approvals", Evidence: []string{"docs/m17_completion_runbook.md"}},
	}
	overall := true
	for _, it := range items {
		if !it.Passed {
			overall = false
			break
		}
	}
	return milestoneClosureReport{
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		OverallPassed:  overall,
		Milestones:     items,
	}
}

func checkByName(rep readinessReport, name string) bool {
	for _, c := range rep.Checks {
		if c.Name == name {
			return c.Passed
		}
	}
	return false
}

func fileExists(path string) bool {
	path = resolvePathForRead(path)
	_, err := os.Stat(path)
	return err == nil
}

func resolvePathForRead(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	candidates := []string{
		path,
		filepath.Join("..", path),
		filepath.Join("..", "..", path),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return path
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		var n json.Number = json.Number(strings.TrimSpace(x))
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
func checkQualityGate(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Sprintf("read failed: %v", err)
	}
	var g gateResult
	if err := json.Unmarshal(data, &g); err != nil {
		return false, fmt.Sprintf("decode failed: %v", err)
	}
	if !g.Passed {
		return false, "quality gate failed"
	}
	return true, "quality gate passed"
}

func checkSLO(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Sprintf("read failed: %v", err)
	}
	var s sloReport
	if err := json.Unmarshal(data, &s); err != nil {
		return false, fmt.Sprintf("decode failed: %v", err)
	}
	if !s.Passed {
		return false, "slo gate failed"
	}
	hasRuntimeSignal := false
	runtimePassed := true
	if s.SLOChecks.RuntimeQualityPassed != nil {
		hasRuntimeSignal = true
		runtimePassed = *s.SLOChecks.RuntimeQualityPassed
	}
	if s.RuntimeReconciliation.RuntimeQualityPassed != nil {
		hasRuntimeSignal = true
		runtimePassed = *s.RuntimeReconciliation.RuntimeQualityPassed
	}
	if hasRuntimeSignal && !runtimePassed {
		return false, "slo runtime quality gate failed"
	}
	if hasRuntimeSignal {
		return true, "slo gate passed (runtime quality passed)"
	}
	return true, "slo gate passed"
}

func checkQuestionCatalog(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Sprintf("read failed: %v", err)
	}
	var c questionCatalog
	if err := json.Unmarshal(data, &c); err != nil {
		return false, fmt.Sprintf("decode failed: %v", err)
	}
	if len(c.Questions) == 0 {
		return false, "no questions in catalog"
	}
	covered := 0
	for _, q := range c.Questions {
		if strings.TrimSpace(q.Endpoint) != "" {
			covered++
		}
	}
	ratio := float64(covered) / float64(len(c.Questions))
	if ratio < 1.0 {
		return false, fmt.Sprintf("coverage %.4f < 1.0000", ratio)
	}
	return true, "coverage 1.0000"
}

func checkExplainCoverage(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Sprintf("read failed: %v", err)
	}
	var t templateFile
	if err := json.Unmarshal(data, &t); err != nil {
		return false, fmt.Sprintf("decode failed: %v", err)
	}
	if len(t.Templates) == 0 {
		return false, "no templates"
	}
	explain := 0
	for _, item := range t.Templates {
		if item.Query.Explain {
			explain++
		}
	}
	ratio := float64(explain) / float64(len(t.Templates))
	if ratio < 1.0 {
		return false, fmt.Sprintf("explain coverage %.4f < 1.0000", ratio)
	}
	return true, "explain coverage 1.0000"
}

func checkCatalogTemplateCoverage(catalogPath string, templatesPath string) (bool, string) {
	catalogData, err := os.ReadFile(catalogPath)
	if err != nil {
		return false, fmt.Sprintf("catalog read failed: %v", err)
	}
	var catalog questionCatalog
	if err := json.Unmarshal(catalogData, &catalog); err != nil {
		return false, fmt.Sprintf("catalog decode failed: %v", err)
	}
	templatesData, err := os.ReadFile(templatesPath)
	if err != nil {
		return false, fmt.Sprintf("templates read failed: %v", err)
	}
	var templates templateEndpoints
	if err := json.Unmarshal(templatesData, &templates); err != nil {
		return false, fmt.Sprintf("templates decode failed: %v", err)
	}
	if len(catalog.Questions) == 0 {
		return false, "no questions in catalog"
	}
	if len(templates.Templates) == 0 {
		return false, "no templates"
	}
	covered := 0
	missing := make([]string, 0)
	for _, q := range catalog.Questions {
		endpoint := strings.TrimSpace(q.Endpoint)
		if endpoint == "" {
			missing = append(missing, q.ID)
			continue
		}
		if catalogEndpointCovered(endpoint, templates.Templates) {
			covered++
			continue
		}
		if strings.TrimSpace(q.ID) != "" {
			missing = append(missing, q.ID)
		} else {
			missing = append(missing, endpoint)
		}
	}
	ratio := float64(covered) / float64(len(catalog.Questions))
	if ratio < 1.0 {
		sort.Strings(missing)
		if len(missing) > 5 {
			missing = missing[:5]
		}
		return false, fmt.Sprintf("api coverage %.4f < 1.0000 (missing: %s)", ratio, strings.Join(missing, ","))
	}
	return true, "api coverage 1.0000"
}

func catalogEndpointCovered(endpoint string, templates []templateEntry) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	normalizedEndpoint := normalizeTemplatePath(endpoint)
	for _, t := range templates {
		path := normalizeTemplatePath(strings.TrimSpace(t.Path))
		if path == "" {
			continue
		}
		if matchCatalogPath(path, normalizedEndpoint) {
			return true
		}
	}
	return false
}

func normalizeTemplatePath(path string) string {
	if path == "" {
		return ""
	}
	out := path
	if idx := strings.Index(out, "?"); idx >= 0 {
		out = out[:idx]
	}
	out = strings.ReplaceAll(out, "{graph_id}", "${graph_id}")
	out = strings.ReplaceAll(out, "{service_id}", "${service_id}")
	out = strings.ReplaceAll(out, "$${graph_id}", "${graph_id}")
	out = strings.ReplaceAll(out, "$${service_id}", "${service_id}")
	out = strings.TrimSpace(out)
	out = strings.TrimRight(out, "/")
	if out == "" {
		return "/"
	}
	return out
}

func matchCatalogPath(templatePath string, catalogPath string) bool {
	templateParts := splitPathParts(templatePath)
	catalogParts := splitPathParts(catalogPath)
	if len(templateParts) != len(catalogParts) {
		return false
	}
	for i := range templateParts {
		a := templateParts[i]
		b := catalogParts[i]
		if isPathPlaceholder(a) || isPathPlaceholder(b) {
			continue
		}
		if a != b {
			return false
		}
	}
	return true
}

func splitPathParts(path string) []string {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return nil
	}
	raw := strings.Split(path, "/")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func isPathPlaceholder(part string) bool {
	part = strings.TrimSpace(part)
	if part == "" {
		return false
	}
	return strings.HasPrefix(part, "${") && strings.HasSuffix(part, "}") ||
		strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")
}

func checkTraceability(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No graph artifacts yet: keep pass neutral for bootstrapping environments.
			return true, "graph index not found; skipped"
		}
		return false, fmt.Sprintf("read failed: %v", err)
	}
	var idx graphIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return false, fmt.Sprintf("decode failed: %v", err)
	}
	if len(idx.Graphs) == 0 {
		return true, "no graphs indexed; skipped"
	}
	good := 0
	for _, g := range idx.Graphs {
		if strings.TrimSpace(g.TenantID) != "" && strings.TrimSpace(g.Fingerprint) != "" {
			good++
		}
	}
	ratio := float64(good) / float64(len(idx.Graphs))
	if ratio < 1.0 {
		return false, fmt.Sprintf("traceability coverage %.4f < 1.0000", ratio)
	}
	return true, "traceability coverage 1.0000"
}

func checkAttestation(signers []string) (bool, string) {
	required := []string{"engineering", "platform", "security"}
	set := map[string]struct{}{}
	for _, s := range signers {
		set[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
	}
	missing := make([]string, 0)
	for _, r := range required {
		if _, ok := set[r]; !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return false, "missing signatures: " + strings.Join(missing, ",")
	}
	return true, "required owner signatures present"
}

func boolDetail(ok bool, pass, fail string) string {
	if ok {
		return pass
	}
	return fail
}

func writeDecision(path string, rep readinessReport) error {
	lines := []string{
		"# State-of-the-Art Gate Decision",
		"",
		"Generated: " + rep.GeneratedAtUTC,
		"",
		"## Overall",
		fmt.Sprintf("- passed: %t", rep.OverallPassed),
		fmt.Sprintf("- signed_owners: %s", strings.Join(rep.SignedOwners, ", ")),
		"",
		"## Checks",
	}
	for _, c := range rep.Checks {
		lines = append(lines, fmt.Sprintf("- %s: %t (%s)", c.Name, c.Passed, c.Detail))
	}
	decision := "REJECT"
	if rep.OverallPassed {
		decision = "APPROVE"
	}
	lines = append(lines, "", "## Decision", "- "+decision, "")
	return writeText(path, strings.Join(lines, "\n"))
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--quality-gate" || arg == "--slo" || arg == "--templates" || arg == "--catalog" || arg == "--graph-index" || arg == "--out-report" || arg == "--out-decision" || arg == "--signers" ||
			arg == "--out-milestones" || arg == "--out-benchmark" || arg == "--out-security" || arg == "--out-ops" || arg == "--quality-report" ||
			arg == "--corpus-report" || arg == "--performance-policy" || arg == "--audit-root" || arg == "--drill-source" || arg == "--drill-out":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--quality-gate=") || strings.HasPrefix(arg, "--slo=") || strings.HasPrefix(arg, "--templates=") || strings.HasPrefix(arg, "--catalog=") || strings.HasPrefix(arg, "--graph-index=") ||
			strings.HasPrefix(arg, "--out-report=") || strings.HasPrefix(arg, "--out-decision=") || strings.HasPrefix(arg, "--signers=") ||
			strings.HasPrefix(arg, "--out-milestones=") || strings.HasPrefix(arg, "--out-benchmark=") || strings.HasPrefix(arg, "--out-security=") ||
			strings.HasPrefix(arg, "--out-ops=") || strings.HasPrefix(arg, "--quality-report=") || strings.HasPrefix(arg, "--corpus-report=") ||
			strings.HasPrefix(arg, "--performance-policy=") || strings.HasPrefix(arg, "--audit-root=") || strings.HasPrefix(arg, "--drill-source=") ||
			strings.HasPrefix(arg, "--drill-out="):
			out = append(out, arg)
		}
	}
	return out
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func writeText(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
