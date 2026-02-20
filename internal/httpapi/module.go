package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"diffmind/internal/audit"
	"diffmind/internal/bundleio"
	"diffmind/internal/contracts"
	"diffmind/internal/diff"
	"diffmind/internal/finalgate"
	graphpkg "diffmind/internal/graph"
	"diffmind/internal/graphschema"
	opspkg "diffmind/internal/ops"
	"diffmind/internal/quality"
	"diffmind/internal/query"
	runtimepkg "diffmind/internal/runtime"
	"diffmind/internal/security"
	"diffmind/internal/verifier"
)

//go:embed ui/*
var uiFiles embed.FS

type options struct {
	Addr       string
	BundlePath string
	GraphRoot  string
}

type uiDefaults struct {
	DefaultBundlePath string `json:"default_bundle_path"`
	GraphRoot         string `json:"graph_root"`
	BuildDefaults     struct {
		Mode               string `json:"mode"`
		OutDir             string `json:"out_dir"`
		ManifestPath       string `json:"manifest_path"`
		ServiceID          string `json:"service_id"`
		ServiceName        string `json:"service_name"`
		BundlePath         string `json:"bundle_path"`
		AnalyzerBundlePath string `json:"analyzer_bundle_path"`
	} `json:"build_defaults"`
}

type graphFilters struct {
	IncludeInferred bool
	EdgeTypeFilter  string
	TypeFilter      string
	ServiceFilter   string
	RepoFilter      string
	NodeFilter      string
	SectionFilter   string
	ClassFilter     string
	ConfidenceMin   float64
	Verification    string
	AdapterID       string
	ProvVersion     string
	ConflictStatus  string
	Environment     string
	QueryText       string
}

type publishPolicy struct {
	IncludeDisputed bool
}

type queryPagination struct {
	NodeLimit int
	EdgeLimit int
}

type metricItem struct {
	NodeID string `json:"node_id"`
	Label  string `json:"label"`
	Count  int    `json:"count"`
}

type graphMetrics struct {
	GraphID         string         `json:"graph_id"`
	NodeCount       int            `json:"node_count"`
	EdgeCount       int            `json:"edge_count"`
	EdgeTypeCounts  map[string]int `json:"edge_type_counts"`
	TopCallers      []metricItem   `json:"top_callers"`
	TopDependencies []metricItem   `json:"top_dependencies"`
}

type evidenceNode struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	ServiceID string `json:"service_id,omitempty"`
}

type evidenceProvenance struct {
	Graph         graphschema.GraphProvenance   `json:"graph"`
	SourceService graphschema.ServiceProvenance `json:"source_service,omitempty"`
	TargetService graphschema.ServiceProvenance `json:"target_service,omitempty"`
}

type evidenceDetail struct {
	GraphID       string                    `json:"graph_id"`
	EdgeID        string                    `json:"edge_id"`
	EdgeType      string                    `json:"edge_type"`
	Confidence    float64                   `json:"confidence"`
	Inferred      bool                      `json:"inferred"`
	Source        evidenceNode              `json:"source"`
	Target        evidenceNode              `json:"target"`
	SourceService *graphschema.ServiceMeta  `json:"source_service,omitempty"`
	TargetService *graphschema.ServiceMeta  `json:"target_service,omitempty"`
	EvidenceRefs  []graphschema.EvidenceRef `json:"evidence_refs"`
	Provenance    evidenceProvenance        `json:"provenance"`
}

type graphCompareRequest struct {
	FromGraphID string `json:"from_graph_id"`
	ToGraphID   string `json:"to_graph_id"`
	FromAt      string `json:"from_at,omitempty"`
	ToAt        string `json:"to_at,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

type nodeCompareItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	ServiceID string `json:"service_id,omitempty"`
}

type edgeCompareItem struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	SourceID   string  `json:"source_id"`
	TargetID   string  `json:"target_id"`
	Confidence float64 `json:"confidence"`
}

type nodeChangeItem struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	ServiceID string         `json:"service_id,omitempty"`
	Keys      []string       `json:"keys"`
	Before    map[string]any `json:"before,omitempty"`
	After     map[string]any `json:"after,omitempty"`
}

type edgeChangeItem struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	SourceID string         `json:"source_id"`
	TargetID string         `json:"target_id"`
	Keys     []string       `json:"keys"`
	Before   map[string]any `json:"before,omitempty"`
	After    map[string]any `json:"after,omitempty"`
}

type graphCompare struct {
	CompareID    string            `json:"compare_id,omitempty"`
	GeneratedAt  time.Time         `json:"generated_at,omitempty"`
	TenantID     string            `json:"tenant_id,omitempty"`
	FromGraphID  string            `json:"from_graph_id"`
	ToGraphID    string            `json:"to_graph_id"`
	FromCounts   map[string]int    `json:"from_counts"`
	ToCounts     map[string]int    `json:"to_counts"`
	AddedNodes   []nodeCompareItem `json:"added_nodes"`
	RemovedNodes []nodeCompareItem `json:"removed_nodes"`
	ChangedNodes []nodeChangeItem  `json:"changed_nodes"`
	AddedEdges   []edgeCompareItem `json:"added_edges"`
	RemovedEdges []edgeCompareItem `json:"removed_edges"`
	ChangedEdges []edgeChangeItem  `json:"changed_edges"`
}

type compareImpact struct {
	CompareID       string              `json:"compare_id"`
	FromGraphID     string              `json:"from_graph_id"`
	ToGraphID       string              `json:"to_graph_id"`
	ImpactedNodeIDs []string            `json:"impacted_node_ids"`
	ImpactedEdgeIDs []string            `json:"impacted_edge_ids"`
	Counts          map[string]int      `json:"counts"`
	Reasons         map[string][]string `json:"reasons"`
}

type compareSummary struct {
	CompareID    string    `json:"compare_id"`
	GeneratedAt  time.Time `json:"generated_at"`
	TenantID     string    `json:"tenant_id,omitempty"`
	FromGraphID  string    `json:"from_graph_id"`
	ToGraphID    string    `json:"to_graph_id"`
	AddedNodes   int       `json:"added_nodes"`
	RemovedNodes int       `json:"removed_nodes"`
	ChangedNodes int       `json:"changed_nodes"`
	AddedEdges   int       `json:"added_edges"`
	RemovedEdges int       `json:"removed_edges"`
	ChangedEdges int       `json:"changed_edges"`
	Path         string    `json:"path"`
}

type compareIndex struct {
	Compares   []compareSummary `json:"compares"`
	NextBefore string           `json:"next_before,omitempty"`
}

type incrementalPlanRequest struct {
	GraphID      string   `json:"graph_id"`
	ChangedFiles []string `json:"changed_files"`
	Hops         int      `json:"hops,omitempty"`
}

type incrementalPlan struct {
	PlanID            string              `json:"plan_id"`
	GeneratedAt       time.Time           `json:"generated_at"`
	TenantID          string              `json:"tenant_id,omitempty"`
	GraphID           string              `json:"graph_id"`
	ChangedFiles      []string            `json:"changed_files"`
	Hops              int                 `json:"hops"`
	SeedNodes         []string            `json:"seed_nodes"`
	ImpactedNodeIDs   []string            `json:"impacted_node_ids"`
	ImpactedEdgeIDs   []string            `json:"impacted_edge_ids"`
	ImpactedByReason  map[string][]string `json:"impacted_by_reason"`
	ImpactGraph       graphschema.Graph   `json:"impact_graph"`
	RecommendedAction map[string]any      `json:"recommended_action"`
}

type incrementalPlanSummary struct {
	PlanID        string    `json:"plan_id"`
	GeneratedAt   time.Time `json:"generated_at"`
	TenantID      string    `json:"tenant_id,omitempty"`
	GraphID       string    `json:"graph_id"`
	ChangedFiles  int       `json:"changed_files"`
	SeedNodes     int       `json:"seed_nodes"`
	ImpactedNodes int       `json:"impacted_nodes"`
	ImpactedEdges int       `json:"impacted_edges"`
	Path          string    `json:"path"`
}

type incrementalPlanIndex struct {
	Plans      []incrementalPlanSummary `json:"plans"`
	NextBefore string                   `json:"next_before,omitempty"`
}

type adjudicationRequest struct {
	TargetID   string         `json:"target_id"`
	TargetKind string         `json:"target_kind,omitempty"`
	Decision   string         `json:"decision"`
	Reason     string         `json:"reason,omitempty"`
	Source     string         `json:"source,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	Confidence *float64       `json:"confidence,omitempty"`
	Actor      string         `json:"actor,omitempty"`
}

type adjudicationRecord struct {
	ID                 string         `json:"id"`
	GraphID            string         `json:"graph_id"`
	TenantID           string         `json:"tenant_id,omitempty"`
	TargetID           string         `json:"target_id"`
	TargetKind         string         `json:"target_kind"`
	TargetType         string         `json:"target_type,omitempty"`
	ServiceID          string         `json:"service_id,omitempty"`
	Decision           string         `json:"decision"`
	Reason             string         `json:"reason,omitempty"`
	Source             string         `json:"source,omitempty"`
	Actor              string         `json:"actor,omitempty"`
	ConfidenceBefore   float64        `json:"confidence_before,omitempty"`
	ConfidenceAfter    float64        `json:"confidence_after,omitempty"`
	VerificationBefore string         `json:"verification_before,omitempty"`
	VerificationAfter  string         `json:"verification_after,omitempty"`
	Attributes         map[string]any `json:"attributes,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type adjudicationIndex struct {
	GraphID       string               `json:"graph_id"`
	GeneratedAt   time.Time            `json:"generated_at"`
	Adjudications []adjudicationRecord `json:"adjudications"`
}

type verifyRunRequest struct {
	InBundle         string   `json:"in_bundle"`
	OutDir           string   `json:"out_dir,omitempty"`
	OutBundle        string   `json:"out_bundle,omitempty"`
	ReviewQueuePath  string   `json:"review_queue_path,omitempty"`
	GraphID          string   `json:"graph_id,omitempty"`
	TenantID         string   `json:"tenant_id,omitempty"`
	PromoteThreshold *float64 `json:"promote_threshold,omitempty"`
	DisputeThreshold *float64 `json:"dispute_threshold,omitempty"`
	StrictEvidence   *bool    `json:"strict_evidence,omitempty"`
	TwoPass          *bool    `json:"two_pass,omitempty"`
}

type verifyRunSummary struct {
	RunID         string    `json:"run_id"`
	GeneratedAt   time.Time `json:"generated_at"`
	TenantID      string    `json:"tenant_id,omitempty"`
	GraphID       string    `json:"graph_id,omitempty"`
	SnapshotID    string    `json:"snapshot_id,omitempty"`
	InBundle      string    `json:"in_bundle"`
	OutDir        string    `json:"out_dir"`
	OutBundle     string    `json:"out_bundle"`
	ReportPath    string    `json:"report_path"`
	QueuePath     string    `json:"queue_path"`
	Verified      int       `json:"verified"`
	NeedsReview   int       `json:"needs_review"`
	Disputed      int       `json:"disputed"`
	QueueItems    int       `json:"queue_items"`
	LowConfidence int       `json:"low_confidence"`
}

type verifyRunIndex struct {
	Runs       []verifyRunSummary `json:"runs"`
	NextBefore string             `json:"next_before,omitempty"`
}

type runtimeReconcileSummary struct {
	ReconcileID  string    `json:"reconcile_id"`
	GeneratedAt  time.Time `json:"generated_at"`
	TenantID     string    `json:"tenant_id,omitempty"`
	GraphID      string    `json:"graph_id"`
	Claims       int       `json:"claims"`
	Observations int       `json:"observations"`
	Confirmed    int       `json:"confirmed"`
	Contradicted int       `json:"contradicted"`
	Unmapped     int       `json:"runtime_only_unmapped"`
	NeedsReview  int       `json:"needs_review"`
	Path         string    `json:"path"`
}

type runtimeReconcileIndex struct {
	Runs       []runtimeReconcileSummary `json:"runs"`
	NextBefore string                    `json:"next_before,omitempty"`
}

var (
	errCompareFromAtNotFound = errors.New("from graph not found for from_at")
	errCompareToAtNotFound   = errors.New("to graph not found for to_at")
)

type runtimeReconcileRecord struct {
	ReconcileID string                                 `json:"reconcile_id"`
	GeneratedAt time.Time                              `json:"generated_at"`
	TenantID    string                                 `json:"tenant_id,omitempty"`
	Request     contracts.RuntimeReconciliationRequest `json:"request"`
	Result      contracts.RuntimeReconciliationResult  `json:"result"`
}

type runtimeReconcileCompare struct {
	FromReconcileID     string   `json:"from_reconcile_id"`
	ToReconcileID       string   `json:"to_reconcile_id"`
	FromGraphID         string   `json:"from_graph_id"`
	ToGraphID           string   `json:"to_graph_id"`
	ConfirmedAdded      []string `json:"confirmed_added"`
	ConfirmedRemoved    []string `json:"confirmed_removed"`
	ContradictedAdded   []string `json:"contradicted_added"`
	ContradictedRemoved []string `json:"contradicted_removed"`
	UnmappedAdded       []string `json:"runtime_only_unmapped_added"`
	UnmappedRemoved     []string `json:"runtime_only_unmapped_removed"`
	NeedsReviewAdded    []string `json:"needs_review_added"`
	NeedsReviewRemoved  []string `json:"needs_review_removed"`
}

type runtimeReconcileGraphSummary struct {
	GraphID      string    `json:"graph_id"`
	Runs         int       `json:"runs"`
	Claims       int       `json:"claims"`
	Observations int       `json:"observations"`
	Confirmed    int       `json:"confirmed"`
	Contradicted int       `json:"contradicted"`
	Unmapped     int       `json:"runtime_only_unmapped"`
	NeedsReview  int       `json:"needs_review"`
	LastRunAt    time.Time `json:"last_run_at"`
}

type runtimeReconcileReport struct {
	GeneratedAt       time.Time                      `json:"generated_at"`
	TenantID          string                         `json:"tenant_id,omitempty"`
	GraphID           string                         `json:"graph_id,omitempty"`
	From              string                         `json:"from,omitempty"`
	To                string                         `json:"to,omitempty"`
	TotalRuns         int                            `json:"total_runs"`
	TotalClaims       int                            `json:"total_claims"`
	TotalObservations int                            `json:"total_observations"`
	TotalConfirmed    int                            `json:"total_confirmed"`
	TotalContradicted int                            `json:"total_contradicted"`
	TotalUnmapped     int                            `json:"total_runtime_only_unmapped"`
	TotalNeedsReview  int                            `json:"total_needs_review"`
	ConfirmedRate     float64                        `json:"confirmed_rate"`
	ContradictedRate  float64                        `json:"contradicted_rate"`
	NeedsReviewRate   float64                        `json:"needs_review_rate"`
	UnmappedRate      float64                        `json:"runtime_only_unmapped_rate"`
	TopGraphs         []runtimeReconcileGraphSummary `json:"top_graphs"`
	LatestRun         *runtimeReconcileSummary       `json:"latest_run,omitempty"`
}

type finalGateAttestRequest struct {
	QualityGatePath        string   `json:"quality_gate_path"`
	MergeQualityPath       string   `json:"merge_quality_path"`
	MergeQualityExpectPath string   `json:"merge_quality_expect_links_path"`
	SLOPath                string   `json:"slo_path"`
	TemplatesPath          string   `json:"templates_path"`
	CatalogPath            string   `json:"catalog_path"`
	GraphIndexPath         string   `json:"graph_index_path"`
	OutReportPath          string   `json:"out_report_path"`
	OutDecisionPath        string   `json:"out_decision_path"`
	Signers                []string `json:"signers"`
}

type finalGateCloseoutRequest struct {
	QualityGatePath        string   `json:"quality_gate_path"`
	MergeQualityPath       string   `json:"merge_quality_path"`
	MergeQualityExpectPath string   `json:"merge_quality_expect_links_path"`
	SLOPath                string   `json:"slo_path"`
	TemplatesPath          string   `json:"templates_path"`
	CatalogPath            string   `json:"catalog_path"`
	GraphIndexPath         string   `json:"graph_index_path"`
	ContractReportPath     string   `json:"contract_report_path"`
	QualityReportPath      string   `json:"quality_report_path"`
	CorpusReportPath       string   `json:"corpus_report_path"`
	PerformancePolicyPath  string   `json:"performance_policy_path"`
	AuditRoot              string   `json:"audit_root"`
	DrillSource            string   `json:"drill_source"`
	DrillOutDir            string   `json:"drill_out_dir"`
	OutReportPath          string   `json:"out_report_path"`
	OutDecisionPath        string   `json:"out_decision_path"`
	OutMilestonesPath      string   `json:"out_milestones_path"`
	OutBenchmarkPath       string   `json:"out_benchmark_path"`
	OutSecurityPath        string   `json:"out_security_path"`
	OutOpsPath             string   `json:"out_ops_path"`
	OutClosureRulesPath    string   `json:"out_closure_rules_path"`
	Signers                []string `json:"signers"`
}

type graphMergeQualityAssessRequest struct {
	GraphPath       string `json:"graph_path"`
	IndexPath       string `json:"index_path"`
	OutPath         string `json:"out_path"`
	ExpectLinksPath string `json:"expect_links_path"`
	FailOnGate      bool   `json:"fail_on_gate"`
}

type architectureTaskAssessRequest struct {
	FocusNodeID      string `json:"focus_node_id,omitempty"`
	OutPath          string `json:"out_path,omitempty"`
	ExportSubgraph   bool   `json:"export_subgraph,omitempty"`
	SubgraphOutPath  string `json:"subgraph_out_path,omitempty"`
	IncludeGraphData bool   `json:"include_graph_data,omitempty"`
}

type qualityEvaluateRequest struct {
	CorpusPath        string `json:"corpus_path"`
	GoldenPath        string `json:"golden_path"`
	MergeQualityPath  string `json:"merge_quality_path"`
	GraphIndexPath    string `json:"graph_index_path"`
	ExpectedLinksPath string `json:"merge_quality_expect_links_path"`
	MergeQualityAuto  *bool  `json:"merge_quality_auto"`
	OutPath           string `json:"out_path"`
	DashboardPath     string `json:"dashboard_path"`
	TriagePath        string `json:"triage_path"`
}

type qualityGateRequest struct {
	ReportPath string `json:"report_path"`
	PolicyPath string `json:"policy_path"`
	OutPath    string `json:"out_path"`
}

type opsBackupRequest struct {
	SourceRoot string `json:"source_root"`
	OutPath    string `json:"out_path"`
}

type opsRestoreRequest struct {
	ArchivePath string `json:"archive_path"`
	TargetRoot  string `json:"target_root"`
}

type opsRolloutRequest struct {
	Component string `json:"component"`
	Candidate string `json:"candidate"`
	Current   string `json:"current"`
	OutPath   string `json:"out_path"`
}

type opsDrillRequest struct {
	SourceRoot    string `json:"source_root"`
	QualityPath   string `json:"quality_path"`
	DrillOutDir   string `json:"drill_out_dir"`
	ArchivePath   string `json:"archive_path"`
	RestoreTarget string `json:"restore_target"`
	RolloutPath   string `json:"rollout_path"`
	SLOOutPath    string `json:"slo_out_path"`
}

type opsSLOEvaluateRequest struct {
	ForceIncident bool   `json:"force_incident,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type opsIncidentRecord struct {
	IncidentID string         `json:"incident_id"`
	CreatedAt  time.Time      `json:"created_at"`
	TenantID   string         `json:"tenant_id,omitempty"`
	Status     string         `json:"status"`
	Reason     string         `json:"reason,omitempty"`
	SLOPassed  bool           `json:"slo_passed"`
	SLOPayload map[string]any `json:"slo_payload"`
}

type opsIncidentSummary struct {
	IncidentID string    `json:"incident_id"`
	CreatedAt  time.Time `json:"created_at"`
	TenantID   string    `json:"tenant_id,omitempty"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	SLOPassed  bool      `json:"slo_passed"`
	Path       string    `json:"path"`
}

type opsIncidentIndex struct {
	Incidents  []opsIncidentSummary `json:"incidents"`
	NextBefore string               `json:"next_before,omitempty"`
}

type opsUITelemetryEvent struct {
	TenantID     string         `json:"tenant_id,omitempty"`
	Principal    string         `json:"principal,omitempty"`
	SessionID    string         `json:"session_id"`
	EventType    string         `json:"event_type"`
	TaskID       string         `json:"task_id,omitempty"`
	Status       string         `json:"status,omitempty"`
	DurationMS   int64          `json:"duration_ms,omitempty"`
	DeadEnd      bool           `json:"dead_end,omitempty"`
	TimestampUTC string         `json:"timestamp_utc,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type opsUITelemetrySummary struct {
	TotalEvents       int                `json:"total_events"`
	TotalSessions     int                `json:"total_sessions"`
	DeadEndEvents     int                `json:"dead_end_events"`
	ByEventType       map[string]int     `json:"by_event_type"`
	ByTask            map[string]int     `json:"by_task"`
	AvgDurationByTask map[string]float64 `json:"avg_duration_ms_by_task"`
}

type opsUITelemetryResponse struct {
	Path    string                `json:"path"`
	Events  []opsUITelemetryEvent `json:"events"`
	Summary opsUITelemetrySummary `json:"summary"`
}

type productTemplate struct {
	ID      string         `json:"id"`
	Product string         `json:"product"`
	Method  string         `json:"method"`
	Path    string         `json:"path"`
	Query   map[string]any `json:"query,omitempty"`
	Payload any            `json:"payload,omitempty"`
}

type productTemplateFile struct {
	Templates []productTemplate `json:"templates"`
}

type productTemplateExecuteRequest struct {
	TemplateID    string         `json:"template_id"`
	TemplatePath  string         `json:"template_path,omitempty"`
	Vars          map[string]any `json:"vars,omitempty"`
	IncludeResult bool           `json:"include_result,omitempty"`
	DryRun        bool           `json:"dry_run,omitempty"`
}

type productQuestionFile struct {
	Questions []struct {
		ID       string `json:"id"`
		Question string `json:"question"`
		Endpoint string `json:"endpoint"`
	} `json:"questions"`
}

type productQuestionExecuteRequest struct {
	QuestionID   string         `json:"question_id"`
	CatalogPath  string         `json:"catalog_path,omitempty"`
	TemplatePath string         `json:"template_path,omitempty"`
	Vars         map[string]any `json:"vars,omitempty"`
}

type productQuestionRunRequest struct {
	QuestionIDs    []string                  `json:"question_ids,omitempty"`
	CatalogPath    string                    `json:"catalog_path,omitempty"`
	TemplatePath   string                    `json:"template_path,omitempty"`
	Vars           map[string]any            `json:"vars,omitempty"`
	VarsByQuestion map[string]map[string]any `json:"vars_by_question,omitempty"`
}

type queryTemplate struct {
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Path    string         `json:"path"`
	Query   map[string]any `json:"query,omitempty"`
	Payload any            `json:"payload,omitempty"`
}

type queryTemplateFile struct {
	Templates []queryTemplate `json:"templates"`
}

type productPathContract struct {
	Product string
	Method  string
}

type queryExecuteRequest struct {
	GraphID          string  `json:"graph_id"`
	Type             string  `json:"type,omitempty"`
	EdgeType         string  `json:"edge_type,omitempty"`
	ServiceID        string  `json:"service_id,omitempty"`
	RepoPath         string  `json:"repo_path,omitempty"`
	NodeID           string  `json:"node_id,omitempty"`
	Section          string  `json:"section,omitempty"`
	Class            string  `json:"class,omitempty"`
	Verification     string  `json:"verification_state,omitempty"`
	AdapterID        string  `json:"adapter_id,omitempty"`
	ProvVersion      string  `json:"provenance_version,omitempty"`
	ConflictStatus   string  `json:"conflict_status,omitempty"`
	Environment      string  `json:"environment,omitempty"`
	QueryText        string  `json:"q,omitempty"`
	ConfidenceMin    float64 `json:"confidence_min,omitempty"`
	IncludeInferred  bool    `json:"include_inferred,omitempty"`
	IncludeDisputed  bool    `json:"include_disputed,omitempty"`
	IncludeSensitive bool    `json:"include_sensitive,omitempty"`
	Explain          bool    `json:"explain,omitempty"`
	NodeLimit        int     `json:"node_limit,omitempty"`
	EdgeLimit        int     `json:"edge_limit,omitempty"`
	MaxAgeHours      int     `json:"max_age_hours,omitempty"`
}

type queryTemplateExecuteRequest struct {
	TemplateID   string         `json:"template_id"`
	TemplatePath string         `json:"template_path,omitempty"`
	Vars         map[string]any `json:"vars,omitempty"`
	DryRun       bool           `json:"dry_run,omitempty"`
}

type routeMetrics struct {
	Route            string    `json:"route"`
	Requests         int64     `json:"requests"`
	Errors           int64     `json:"errors"`
	AuthFailures     int64     `json:"auth_failures"`
	TotalDurationMS  float64   `json:"total_duration_ms"`
	P95DurationMS    float64   `json:"p95_duration_ms"`
	LastStatus       int       `json:"last_status"`
	LastAccessedAt   time.Time `json:"last_accessed_at,omitempty"`
	AvailabilityRate float64   `json:"availability_rate"`
}

type runtimeMetrics struct {
	mu     sync.Mutex
	routes map[string]*routeMetrics
}

var apiMetrics = &runtimeMetrics{routes: map[string]*routeMetrics{}}

const maxNeighborhoodHops = 12

type productPRReviewRequest struct {
	GraphID      string   `json:"graph_id"`
	ChangedNodes []string `json:"changed_nodes"`
	MaxFindings  int      `json:"max_findings"`
}

type productFinding struct {
	Severity string         `json:"severity"`
	Title    string         `json:"title"`
	NodeID   string         `json:"node_id,omitempty"`
	EdgeID   string         `json:"edge_id,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

type productPRReviewResponse struct {
	GraphID   string           `json:"graph_id"`
	Findings  []productFinding `json:"findings"`
	Summary   map[string]int   `json:"summary"`
	Generated time.Time        `json:"generated_at"`
}

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              opts.Addr,
		Handler:           newMux(opts.BundlePath, opts.GraphRoot),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve http api: %w", err)
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address")
	bundle := fs.String("bundle", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "Default canonical intelligence bundle path")
	graphRoot := fs.String("graph-root", filepath.Join(".diffmind", "graph"), "Graph artifacts root directory")

	if err := fs.Parse(filterArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse serve flags: %w", err)
	}
	return options{
		Addr:       strings.TrimSpace(*addr),
		BundlePath: strings.TrimSpace(*bundle),
		GraphRoot:  strings.TrimSpace(*graphRoot),
	}, nil
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--addr" || arg == "--bundle" || arg == "--graph-root":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--addr=") || strings.HasPrefix(arg, "--bundle=") || strings.HasPrefix(arg, "--graph-root="):
			out = append(out, arg)
		}
	}
	return out
}

func newMux(defaultBundlePath string, graphRoot string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", instrument("/health", http.HandlerFunc(handleHealth)))
	mux.Handle("/defaults", instrument("/defaults", handleDefaults(defaultBundlePath, graphRoot)))
	mux.Handle("/entities", instrument("/entities", handleEntities(defaultBundlePath)))
	mux.Handle("/diff", instrument("/diff", http.HandlerFunc(handleDiff)))
	mux.Handle("/graphs", instrument("/graphs", handleGraphs(graphRoot)))
	mux.Handle("/graphs/merge-quality", instrument("/graphs/merge-quality", handleGraphMergeQuality(graphRoot)))
	mux.Handle("/quality/evaluate", instrument("/quality/evaluate", handleQualityEvaluate(graphRoot)))
	mux.Handle("/quality/gate", instrument("/quality/gate", handleQualityGate(graphRoot)))
	mux.Handle("/quality/report", instrument("/quality/report", handleQualityReport(graphRoot)))
	mux.Handle("/quality/dashboard", instrument("/quality/dashboard", handleQualityDashboard(graphRoot)))
	mux.Handle("/quality/triage", instrument("/quality/triage", handleQualityTriage(graphRoot)))
	mux.Handle("/graphs/at", instrument("/graphs/at", handleGraphAt(graphRoot)))
	mux.Handle("/graphs/build", instrument("/graphs/build", handleGraphBuildAlias(graphRoot)))
	mux.Handle("/graphs/compare", instrument("/graphs/compare", handleGraphsCompare(graphRoot)))
	mux.Handle("/graphs/compare/", instrument("/graphs/compare/:id", handleGraphsCompareByID(graphRoot)))
	mux.Handle("/graphs/incremental", instrument("/graphs/incremental", handleGraphIncremental(graphRoot)))
	mux.Handle("/graphs/incremental/", instrument("/graphs/incremental/:id", handleGraphIncrementalByID(graphRoot)))
	mux.Handle("/graphs/", instrument("/graphs/:id", handleGraphByID(graphRoot)))
	mux.Handle("/compliance/audit", instrument("/compliance/audit", handleAuditList(graphRoot)))
	mux.Handle("/compliance/audit/integrity", instrument("/compliance/audit/integrity", handleAuditIntegrity(graphRoot)))
	mux.Handle("/compliance/audit/export", instrument("/compliance/audit/export", handleAuditExport(graphRoot)))
	mux.Handle("/compliance/audit/export/verify", instrument("/compliance/audit/export/verify", handleAuditExportVerify(graphRoot)))
	mux.Handle("/compliance/audit/evidence-bundle", instrument("/compliance/audit/evidence-bundle", handleAuditEvidenceBundle(graphRoot)))
	mux.Handle("/compliance/audit/retention", instrument("/compliance/audit/retention", handleAuditRetention(graphRoot)))
	mux.Handle("/products/templates", instrument("/products/templates", handleProductTemplates(graphRoot)))
	mux.Handle("/products/templates/validate", instrument("/products/templates/validate", handleProductTemplateValidate(graphRoot)))
	mux.Handle("/products/templates/execute", instrument("/products/templates/execute", handleProductTemplateExecute(graphRoot)))
	mux.Handle("/products/questions", instrument("/products/questions", handleProductQuestions(graphRoot)))
	mux.Handle("/products/questions/execute", instrument("/products/questions/execute", handleProductQuestionExecute(graphRoot)))
	mux.Handle("/products/questions/run", instrument("/products/questions/run", handleProductQuestionRun(graphRoot)))
	mux.Handle("/products/questions/coverage", instrument("/products/questions/coverage", handleProductQuestionCoverage(graphRoot)))
	mux.Handle("/products/pr-review", instrument("/products/pr-review", handleProductPRReview(graphRoot)))
	mux.Handle("/query/templates", instrument("/query/templates", handleQueryTemplates(graphRoot)))
	mux.Handle("/query/templates/validate", instrument("/query/templates/validate", handleQueryTemplateValidate(graphRoot)))
	mux.Handle("/query/templates/execute", instrument("/query/templates/execute", handleQueryTemplateExecute(graphRoot)))
	mux.Handle("/query/execute", instrument("/query/execute", handleQueryExecute(graphRoot)))
	mux.Handle("/products/docs/", instrument("/products/docs/:graph_id", handleProductDocs(graphRoot)))
	mux.Handle("/products/runtime/", instrument("/products/runtime/:graph_id", handleProductRuntime(graphRoot)))
	mux.Handle("/products/topology/", instrument("/products/topology/:graph_id", handleProductTopology(graphRoot)))
	mux.Handle("/products/company/", instrument("/products/company/:graph_id", handleProductCompany(graphRoot)))
	mux.Handle("/products/trust/", instrument("/products/trust/:graph_id", handleProductTrust(graphRoot)))
	mux.Handle("/products/architecture/", instrument("/products/architecture/:graph_id", handleProductArchitecture(graphRoot)))
	mux.Handle("/products/mapper/", instrument("/products/mapper/:graph_id", handleProductMapper(graphRoot)))
	mux.Handle("/products/governance/", instrument("/products/governance/:graph_id", handleProductGovernance(graphRoot)))
	mux.Handle("/ops/metrics", instrument("/ops/metrics", handleOpsMetrics(graphRoot)))
	mux.Handle("/ops/slo", instrument("/ops/slo", handleOpsSLO(graphRoot)))
	mux.Handle("/ops/slo/evaluate", instrument("/ops/slo/evaluate", handleOpsSLOEvaluate(graphRoot)))
	mux.Handle("/ops/incidents", instrument("/ops/incidents", handleOpsIncidents(graphRoot)))
	mux.Handle("/ops/incidents/", instrument("/ops/incidents/:id", handleOpsIncidentByID(graphRoot)))
	mux.Handle("/ops/ui-telemetry", instrument("/ops/ui-telemetry", handleOpsUITelemetry(graphRoot)))
	mux.Handle("/ops/rollout-policy", instrument("/ops/rollout-policy", handleOpsRolloutPolicy(graphRoot)))
	mux.Handle("/ops/backup", instrument("/ops/backup", handleOpsBackup(graphRoot)))
	mux.Handle("/ops/restore", instrument("/ops/restore", handleOpsRestore(graphRoot)))
	mux.Handle("/ops/rollout", instrument("/ops/rollout", handleOpsRollout(graphRoot)))
	mux.Handle("/ops/drill", instrument("/ops/drill", handleOpsDrill(graphRoot)))
	mux.Handle("/final/attest", instrument("/final/attest", handleFinalGateAttest(graphRoot)))
	mux.Handle("/final/closeout", instrument("/final/closeout", handleFinalCloseout(graphRoot)))
	mux.Handle("/final/readiness", instrument("/final/readiness", handleFinalReadiness(graphRoot)))
	mux.Handle("/final/decision", instrument("/final/decision", handleFinalDecision(graphRoot)))
	mux.Handle("/final/milestones", instrument("/final/milestones", handleFinalMilestones(graphRoot)))
	mux.Handle("/final/benchmark", instrument("/final/benchmark", handleFinalBenchmark(graphRoot)))
	mux.Handle("/final/security", instrument("/final/security", handleFinalSecurity(graphRoot)))
	mux.Handle("/final/ops", instrument("/final/ops", handleFinalOps(graphRoot)))
	mux.Handle("/final/closure-rules", instrument("/final/closure-rules", handleFinalClosureRules(graphRoot)))
	mux.Handle("/runtime/plan", instrument("/runtime/plan", handleRuntimePlan(graphRoot)))
	mux.Handle("/runtime/reconcile", instrument("/runtime/reconcile", handleRuntimeReconcile(graphRoot)))
	mux.Handle("/runtime/reconcile/report", instrument("/runtime/reconcile/report", handleRuntimeReconcileReport(graphRoot)))
	mux.Handle("/runtime/reconcile/compare", instrument("/runtime/reconcile/compare", handleRuntimeReconcileCompare(graphRoot)))
	mux.Handle("/runtime/reconcile/", instrument("/runtime/reconcile/:id", handleRuntimeReconcileByID(graphRoot)))
	mux.Handle("/runtime/claims/", instrument("/runtime/claims/:graph_id", handleRuntimeClaims(graphRoot)))
	mux.Handle("/verify/run", instrument("/verify/run", handleVerifyRun(graphRoot)))
	mux.Handle("/verify/runs", instrument("/verify/runs", handleVerifyRuns(graphRoot)))
	mux.Handle("/verify/runs/", instrument("/verify/runs/:id", handleVerifyRunByID(graphRoot)))
	if sub, err := fs.Sub(uiFiles, "ui"); err == nil {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	}
	return mux
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func instrument(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, req)
		apiMetrics.record(route, rec.status, time.Since(start))
	})
}

func handleGraphBuildAlias(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleGraphsBuild(graphRoot, w, r)
	}
}

func handleGraphsCompare(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionCompareGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			listCompareHistory(graphRoot, r, w)
			return
		case http.MethodDelete:
			authCtx, ok := authorizeRequest(w, r, security.ActionDeleteCompare, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			keepLatest := 0
			if q := strings.TrimSpace(r.URL.Query().Get("keep_latest")); q != "" {
				n, err := strconv.Atoi(q)
				if err != nil || n < 0 {
					writeError(w, http.StatusBadRequest, "invalid keep_latest")
					return
				}
				keepLatest = n
			}
			deleted, err := pruneCompareHistory(graphRoot, keepLatest)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recordAudit(graphRoot, authCtx, r, security.ActionDeleteCompare, "allow", "compare_history_pruned", map[string]any{"deleted": deleted})
			writeJSON(w, http.StatusOK, map[string]any{
				"deleted":       deleted,
				"keep_latest":   keepLatest,
				"history_prune": true,
			})
			return
		case http.MethodPost:
			// continue below
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionCompareGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot)
		if !ok {
			return
		}
		var req graphCompareRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		req.FromGraphID = strings.TrimSpace(req.FromGraphID)
		req.ToGraphID = strings.TrimSpace(req.ToGraphID)
		if req.FromGraphID == "" || req.ToGraphID == "" {
			var err error
			req.FromGraphID, req.ToGraphID, err = resolveCompareGraphIDs(graphRoot, req)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, errCompareFromAtNotFound) || errors.Is(err, errCompareToAtNotFound) {
					writeError(w, http.StatusNotFound, err.Error())
					return
				}
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}

		fromGraph, err := loadGraph(graphRoot, req.FromGraphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "from graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		toGraph, err := loadGraph(graphRoot, req.ToGraphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "to graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if graphTenant(fromGraph) != graphTenant(toGraph) {
			writeError(w, http.StatusBadRequest, "cross-tenant compare is not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionCompareGraph, graphTenant(fromGraph), false, false, graphRoot); !ok {
			return
		}
		filters, err := parseGraphFilters(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid confidence_min")
			return
		}
		if hasGraphFilter(filters) {
			fromGraph = filterGraph(fromGraph, filters)
			toGraph = filterGraph(toGraph, filters)
		}
		policy := parsePublishPolicy(r)
		fromGraph = applyStrictPublishPolicy(fromGraph, policy)
		toGraph = applyStrictPublishPolicy(toGraph, policy)
		result := buildGraphCompare(fromGraph, toGraph)
		result.TenantID = graphTenant(fromGraph)
		if err := persistCompareResult(graphRoot, &result); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionCompareGraph, "allow", "compare_created", map[string]any{"compare_id": result.CompareID})
		writeJSON(w, http.StatusOK, result)
	}
}

func handleGraphsCompareByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.Trim(strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/graphs/compare/")), "/")
		if raw == "" {
			writeError(w, http.StatusBadRequest, "compare id is required")
			return
		}
		parts := strings.Split(raw, "/")
		compareID := strings.TrimSpace(parts[0])
		compareAction := ""
		if len(parts) > 1 {
			compareAction = strings.ToLower(strings.TrimSpace(strings.Join(parts[1:], "/")))
		}
		if compareID == "" {
			writeError(w, http.StatusBadRequest, "compare id is required")
			return
		}

		switch r.Method {
		case http.MethodGet:
			payload, err := loadCompareResult(graphRoot, compareID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "compare not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if _, ok := authorizeRequest(w, r, security.ActionCompareGraph, normalizeTenant(payload.TenantID), false, false, graphRoot); !ok {
				return
			}
			if compareAction == "impact" {
				impact := buildCompareImpact(payload)
				if parseBoolDefault(r.URL.Query().Get("explain"), false) {
					writeJSON(w, http.StatusOK, map[string]any{
						"impact": impact,
						"explain": map[string]any{
							"selection":      "compare impacted surface",
							"compare_id":     payload.CompareID,
							"from_graph_id":  payload.FromGraphID,
							"to_graph_id":    payload.ToGraphID,
							"impacted_nodes": len(impact.ImpactedNodeIDs),
							"impacted_edges": len(impact.ImpactedEdgeIDs),
							"reason_bucket_ids": map[string]int{
								"added_nodes":    len(impact.Reasons["added_nodes"]),
								"removed_nodes":  len(impact.Reasons["removed_nodes"]),
								"changed_nodes":  len(impact.Reasons["changed_nodes"]),
								"added_edges":    len(impact.Reasons["added_edges"]),
								"removed_edges":  len(impact.Reasons["removed_edges"]),
								"changed_edges":  len(impact.Reasons["changed_edges"]),
								"edge_endpoints": len(impact.Reasons["edge_endpoints"]),
							},
						},
					})
					return
				}
				writeJSON(w, http.StatusOK, impact)
				return
			}
			if compareAction == "impact/subgraph" {
				targetGraphID := strings.TrimSpace(r.URL.Query().Get("graph_id"))
				if targetGraphID == "" {
					targetGraphID = payload.ToGraphID
				}
				if targetGraphID != payload.FromGraphID && targetGraphID != payload.ToGraphID {
					writeError(w, http.StatusBadRequest, "graph_id must match compared graph ids")
					return
				}
				hops := 1
				if q := strings.TrimSpace(r.URL.Query().Get("hops")); q != "" {
					n, err := strconv.Atoi(q)
					if err != nil || n < 0 || n > maxNeighborhoodHops {
						writeError(w, http.StatusBadRequest, "invalid hops")
						return
					}
					hops = n
				}
				graph, err := loadGraph(graphRoot, targetGraphID)
				if err != nil {
					if os.IsNotExist(err) {
						writeError(w, http.StatusNotFound, "graph not found")
						return
					}
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot); !ok {
					return
				}
				impact := buildCompareImpact(payload)
				seeds := map[string]struct{}{}
				for _, id := range impact.ImpactedNodeIDs {
					seeds[id] = struct{}{}
				}
				subgraph := buildNeighborhoodSubgraph(graph, seeds, hops)
				resp := map[string]any{
					"compare_id":   payload.CompareID,
					"graph_id":     targetGraphID,
					"hops":         hops,
					"seed_nodes":   impact.ImpactedNodeIDs,
					"impact_graph": subgraph,
				}
				if parseBoolDefault(r.URL.Query().Get("explain"), false) {
					resp["explain"] = map[string]any{
						"selection":        "compare impact neighborhood",
						"compare_id":       payload.CompareID,
						"target_graph_id":  targetGraphID,
						"hops":             hops,
						"seed_nodes_count": len(impact.ImpactedNodeIDs),
						"impact_nodes":     len(subgraph.Nodes),
						"impact_edges":     len(subgraph.Edges),
					}
				}
				writeJSON(w, http.StatusOK, resp)
				return
			}
			if compareAction != "" {
				writeError(w, http.StatusNotFound, "compare subresource not found")
				return
			}
			writeJSON(w, http.StatusOK, payload)
		case http.MethodDelete:
			if compareAction != "" {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			payload, err := loadCompareResult(graphRoot, compareID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "compare not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			authCtx, ok := authorizeRequest(w, r, security.ActionDeleteCompare, normalizeTenant(payload.TenantID), false, true, graphRoot)
			if !ok {
				return
			}
			if err := deleteCompareResult(graphRoot, compareID); err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "compare not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recordAudit(graphRoot, authCtx, r, security.ActionDeleteCompare, "allow", "compare_deleted", map[string]any{"compare_id": compareID})
			writeJSON(w, http.StatusOK, map[string]any{
				"deleted":    true,
				"compare_id": compareID,
			})
		default:
			w.Header().Set("Allow", "GET, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

func handleGraphIncremental(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			listIncrementalPlans(graphRoot, r, w)
			return
		case http.MethodPost:
			authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			req := incrementalPlanRequest{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
				return
			}
			req.GraphID = strings.TrimSpace(req.GraphID)
			if req.GraphID == "" {
				writeError(w, http.StatusBadRequest, "graph_id is required")
				return
			}
			changedFiles := normalizeChangedFiles(req.ChangedFiles)
			if len(changedFiles) == 0 {
				writeError(w, http.StatusBadRequest, "changed_files is required")
				return
			}
			graph, err := loadGraph(graphRoot, req.GraphID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "graph not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if _, ok := authorizeRequest(w, r, security.ActionBuildGraph, graphTenant(graph), false, true, graphRoot); !ok {
				return
			}
			hops := req.Hops
			if hops < 0 || hops > maxNeighborhoodHops {
				writeError(w, http.StatusBadRequest, "invalid hops")
				return
			}
			if hops == 0 {
				hops = 2
			}
			plan := buildIncrementalPlan(graph, incrementalPlanRequest{
				GraphID:      req.GraphID,
				ChangedFiles: changedFiles,
				Hops:         hops,
			})
			plan.TenantID = graphTenant(graph)
			if err := persistIncrementalPlan(graphRoot, plan); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "incremental_plan_created", map[string]any{
				"plan_id":        plan.PlanID,
				"graph_id":       plan.GraphID,
				"changed_files":  len(plan.ChangedFiles),
				"impacted_nodes": len(plan.ImpactedNodeIDs),
			})
			writeJSON(w, http.StatusOK, plan)
			return
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}
}

func handleGraphIncrementalByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		raw := strings.Trim(strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/graphs/incremental/")), "/")
		if raw == "" {
			writeError(w, http.StatusBadRequest, "plan id is required")
			return
		}
		parts := strings.Split(raw, "/")
		planID := strings.TrimSpace(parts[0])
		action := ""
		if len(parts) > 1 {
			action = strings.ToLower(strings.TrimSpace(strings.Join(parts[1:], "/")))
		}
		plan, err := loadIncrementalPlan(graphRoot, planID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "incremental plan not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(plan.TenantID), false, false, graphRoot); !ok {
			return
		}
		switch action {
		case "":
			writeJSON(w, http.StatusOK, plan)
			return
		case "subgraph":
			resp := map[string]any{
				"plan_id":      plan.PlanID,
				"graph_id":     plan.GraphID,
				"hops":         plan.Hops,
				"seed_nodes":   plan.SeedNodes,
				"impact_graph": plan.ImpactGraph,
			}
			if parseBoolDefault(r.URL.Query().Get("explain"), false) {
				resp["explain"] = map[string]any{
					"selection":        "incremental impacted neighborhood",
					"plan_id":          plan.PlanID,
					"graph_id":         plan.GraphID,
					"seed_nodes_count": len(plan.SeedNodes),
					"impact_nodes":     len(plan.ImpactGraph.Nodes),
					"impact_edges":     len(plan.ImpactGraph.Edges),
					"hops":             plan.Hops,
					"changed_files":    len(plan.ChangedFiles),
				}
			}
			writeJSON(w, http.StatusOK, resp)
			return
		default:
			writeError(w, http.StatusNotFound, "incremental subresource not found")
			return
		}
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

func handleEntities(defaultBundlePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryEntities, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, filepath.Join(".diffmind", "graph"))
		if !ok {
			return
		}
		view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
		if view == "" {
			view = "all"
		}
		if !query.ValidateView(view) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported view %q", view))
			return
		}

		bundlePath := strings.TrimSpace(r.URL.Query().Get("bundle"))
		if bundlePath == "" {
			bundlePath = defaultBundlePath
		}

		b, err := bundleio.Load(bundlePath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		rows := query.FilterEntities(b.Entities, view)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Type == rows[j].Type {
				return rows[i].NaturalKey < rows[j].NaturalKey
			}
			return rows[i].Type < rows[j].Type
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"snapshot_id": b.SnapshotID,
			"view":        view,
			"count":       len(rows),
			"entities":    rows,
		})
		_ = authCtx
	}
}

func handleDiff(w http.ResponseWriter, r *http.Request) {
	if _, ok := authorizeRequest(w, r, security.ActionQueryEntities, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, filepath.Join(".diffmind", "graph")); !ok {
		return
	}
	fromPath := strings.TrimSpace(r.URL.Query().Get("from"))
	toPath := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromPath == "" || toPath == "" {
		writeError(w, http.StatusBadRequest, "missing required query params: from and to")
		return
	}

	fromBundle, err := bundleio.Load(fromPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	toBundle, err := bundleio.Load(toPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, diff.BuildReport(fromBundle, toBundle))
}

func handleGraphs(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGraphsList(graphRoot, w, r)
			return
		case http.MethodPost:
			handleGraphsBuild(graphRoot, w, r)
			return
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}
}

func handleGraphsList(graphRoot string, w http.ResponseWriter, r *http.Request) {
	authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot)
	if !ok {
		return
	}
	indexPath := filepath.Join(graphRoot, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, graphschema.Index{Graphs: []graphschema.Summary{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var index graphschema.Index
	if err := json.Unmarshal(data, &index); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode graph index: %v", err))
		return
	}
	if !authCtx.HasRole("platform_admin") {
		filtered := make([]graphschema.Summary, 0, len(index.Graphs))
		for _, g := range index.Graphs {
			if summaryTenant(g) == normalizeTenant(authCtx.TenantID) {
				filtered = append(filtered, g)
			}
		}
		index.Graphs = filtered
	}
	writeJSON(w, http.StatusOK, index)
}

type graphBuildRequest struct {
	ManifestPath       string   `json:"manifest_path"`
	Sources            []string `json:"sources"`
	OutDir             string   `json:"out_dir"`
	Persist            bool     `json:"persist"`
	Mode               string   `json:"mode"`
	ServiceID          string   `json:"service_id"`
	ServiceName        string   `json:"service_name"`
	BundlePath         string   `json:"bundle_path"`
	AnalyzerBundlePath string   `json:"analyzer_bundle_path"`
	BaseURLs           []string `json:"base_urls"`
}

func handleGraphsBuild(graphRoot string, w http.ResponseWriter, r *http.Request) {
	authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("read request body: %v", err))
		return
	}

	var req graphBuildRequest
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
	}
	if req.OutDir == "" {
		req.OutDir = defaultOutDirFromGraphRoot(graphRoot)
	}
	if req.BundlePath == "" {
		req.BundlePath = filepath.Join(req.OutDir, "bundle", "intelligence_bundle.json")
	}
	if req.AnalyzerBundlePath == "" {
		req.AnalyzerBundlePath = filepath.Join(req.OutDir, "analyzers", "bundle.json")
	}
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.Mode == "" {
		req.Mode = "auto"
	}
	// Multi mode fallback: if client does not provide sources and manifest is absent,
	// auto-discover from out_dir and its parent so UI works with minimal input.
	if req.Mode == "multi" && len(req.Sources) == 0 {
		manifestPath := strings.TrimSpace(req.ManifestPath)
		if manifestPath == "" {
			manifestPath = filepath.Join(req.OutDir, "graph", "services.yaml")
		}
		if !fileExists(manifestPath) {
			req.Sources = discoverSourcesFromOutDir(req.OutDir)
			req.ManifestPath = ""
		}
	}

	result, err := graphpkg.Build(r.Context(), graphpkg.BuildRequest{
		ManifestPath:       req.ManifestPath,
		Sources:            req.Sources,
		OutDir:             req.OutDir,
		Persist:            req.Persist,
		TenantID:           normalizeTenant(authCtx.TenantID),
		Mode:               req.Mode,
		ServiceID:          req.ServiceID,
		ServiceName:        req.ServiceName,
		BundlePath:         req.BundlePath,
		AnalyzerBundlePath: req.AnalyzerBundlePath,
		BaseURLs:           req.BaseURLs,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "deny", "build_failed", map[string]any{"error": err.Error()})
		return
	}
	recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "build_succeeded", map[string]any{"graph_id": result.GraphID})
	writeJSON(w, http.StatusCreated, result)
}

func handleDefaults(defaultBundlePath string, graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		outDir := defaultOutDirFromGraphRoot(graphRoot)
		resp := uiDefaults{
			DefaultBundlePath: defaultBundlePath,
			GraphRoot:         graphRoot,
		}
		resp.BuildDefaults.Mode = "auto"
		resp.BuildDefaults.OutDir = outDir
		resp.BuildDefaults.ManifestPath = filepath.Join(outDir, "graph", "services.yaml")
		resp.BuildDefaults.ServiceID = "service.local"
		resp.BuildDefaults.ServiceName = "Local Service"
		resp.BuildDefaults.BundlePath = filepath.Join(outDir, "bundle", "intelligence_bundle.json")
		resp.BuildDefaults.AnalyzerBundlePath = filepath.Join(outDir, "analyzers", "bundle.json")
		writeJSON(w, http.StatusOK, resp)
	}
}

func discoverSourcesFromOutDir(outDir string) []string {
	out := make([]string, 0, 2)
	seen := map[string]struct{}{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			return
		}
		if _, exists := seen[abs]; exists {
			return
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	add(outDir)
	parent := filepath.Dir(strings.TrimSpace(outDir))
	if parent != "" && parent != "." && parent != outDir {
		add(parent)
	}
	return out
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func defaultOutDirFromGraphRoot(graphRoot string) string {
	trimmed := strings.TrimSpace(graphRoot)
	if trimmed == "" {
		return ".diffmind"
	}
	cleaned := filepath.Clean(trimmed)
	if filepath.Base(cleaned) == "graph" {
		return filepath.Dir(cleaned)
	}
	return cleaned
}

func handleGraphByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/graphs/")
		rest = strings.TrimSpace(strings.Trim(rest, "/"))
		if rest == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		parts := strings.Split(rest, "/")
		graphID := parts[0]
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}

		if len(parts) == 3 && parts[1] == "evidence" {
			authCtx, ok = authorizeRequest(w, r, security.ActionReadEvidence, graphTenant(graph), true, false, graphRoot)
			if !ok {
				return
			}
			edgeID := strings.TrimSpace(parts[2])
			if edgeID == "" {
				writeError(w, http.StatusBadRequest, "edge id is required")
				return
			}
			nodeByID := map[string]graphschema.Node{}
			for _, n := range graph.Nodes {
				nodeByID[n.ID] = n
			}
			serviceByID := map[string]graphschema.ServiceMeta{}
			for _, svc := range graph.Meta.Services {
				serviceByID[svc.ID] = svc
			}

			for _, e := range graph.Edges {
				if e.ID == edgeID {
					srcNode := nodeByID[e.SourceID]
					dstNode := nodeByID[e.TargetID]
					srcService, srcOK := serviceByID[srcNode.ServiceID]
					dstService, dstOK := serviceByID[dstNode.ServiceID]

					payload := evidenceDetail{
						GraphID:    graphID,
						EdgeID:     edgeID,
						EdgeType:   e.Type,
						Confidence: e.Confidence,
						Inferred:   e.Inferred,
						Source: evidenceNode{
							ID:        srcNode.ID,
							Type:      srcNode.Type,
							Label:     srcNode.Label,
							ServiceID: srcNode.ServiceID,
						},
						Target: evidenceNode{
							ID:        dstNode.ID,
							Type:      dstNode.Type,
							Label:     dstNode.Label,
							ServiceID: dstNode.ServiceID,
						},
						EvidenceRefs: e.EvidenceRefs,
						Provenance: evidenceProvenance{
							Graph: graph.Meta.Provenance,
						},
					}
					if srcOK {
						svc := srcService
						payload.SourceService = &svc
						payload.Provenance.SourceService = svc.Provenance
					}
					if dstOK {
						svc := dstService
						payload.TargetService = &svc
						payload.Provenance.TargetService = svc.Provenance
					}

					includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
					_ = includeSensitive
					if !security.CanReadRawEvidence(authCtx) {
						for i := range payload.EvidenceRefs {
							payload.EvidenceRefs[i].FilePath = "[REDACTED]"
							payload.EvidenceRefs[i].StartLine = 0
							payload.EvidenceRefs[i].StartCol = 0
							payload.EvidenceRefs[i].EndLine = 0
							payload.EvidenceRefs[i].EndCol = 0
						}
					}
					writeJSON(w, http.StatusOK, payload)
					return
				}
			}
			writeError(w, http.StatusNotFound, "edge not found")
			return
		}
		if len(parts) >= 2 && parts[1] == "conflicts" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			writeJSON(w, http.StatusOK, buildConflictStore(graph, graphID))
			return
		}
		if len(parts) >= 2 && parts[1] == "adjudications" {
			switch r.Method {
			case http.MethodGet:
				index, err := loadAdjudicationIndex(graphRoot, graphID)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if len(parts) == 3 && parts[2] == "summary" {
					writeJSON(w, http.StatusOK, buildAdjudicationSummary(index))
					return
				}
				decisionFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("decision")))
				targetKind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target_kind")))
				limit := 0
				if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
					n, err := strconv.Atoi(raw)
					if err != nil || n < 0 {
						writeError(w, http.StatusBadRequest, "invalid limit")
						return
					}
					limit = n
				}
				items := make([]adjudicationRecord, 0, len(index.Adjudications))
				for _, rec := range index.Adjudications {
					if decisionFilter != "" && !strings.EqualFold(rec.Decision, decisionFilter) {
						continue
					}
					if targetKind != "" && !strings.EqualFold(rec.TargetKind, targetKind) {
						continue
					}
					items = append(items, rec)
					if limit > 0 && len(items) >= limit {
						break
					}
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"graph_id":      graphID,
					"count":         len(items),
					"total":         len(index.Adjudications),
					"adjudications": items,
				})
				return
			case http.MethodPost:
				authCtx, ok = authorizeRequest(w, r, security.ActionBuildGraph, graphTenant(graph), false, true, graphRoot)
				if !ok {
					return
				}
				req := adjudicationRequest{}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
					return
				}
				record, err := buildAdjudicationRecord(graph, graphID, graphTenant(graph), req, authCtx)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				if err := persistAdjudicationRecord(graphRoot, graphID, record); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "adjudication_recorded", map[string]any{
					"graph_id":    graphID,
					"target_id":   record.TargetID,
					"target_kind": record.TargetKind,
					"decision":    record.Decision,
				})
				writeJSON(w, http.StatusOK, map[string]any{
					"graph_id":     graphID,
					"adjudication": record,
				})
				return
			default:
				w.Header().Set("Allow", "GET, POST")
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
		}

		filters, err := parseGraphFilters(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid confidence_min")
			return
		}

		if hasGraphFilter(filters) {
			graph = filterGraph(graph, filters)
		}
		graph = applyStrictPublishPolicy(graph, parsePublishPolicy(r))
		includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
		graph = security.RedactGraph(graph, authCtx, includeSensitive)
		graph = annotateGraphFreshness(graph, time.Now().UTC(), parseMaxAgeHours(r.URL.Query().Get("max_age_hours")))
		pagination, err := parseQueryPagination(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		graph = applyQueryPagination(graph, pagination)
		if len(parts) == 2 && parts[1] == "query" {
			explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
			if !explain {
				writeJSON(w, http.StatusOK, graph)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"graph":   graph,
				"explain": buildGraphExplain(graph),
			})
			return
		}
		if len(parts) == 2 && parts[1] == "summary" {
			writeJSON(w, http.StatusOK, buildGraphSummary(graph))
			return
		}
		if len(parts) == 2 && parts[1] == "metrics" {
			writeJSON(w, http.StatusOK, buildGraphMetrics(graph))
			return
		}
		if len(parts) == 2 && parts[1] == "architecture-tasks" {
			switch r.Method {
			case http.MethodGet:
				path := strings.TrimSpace(r.URL.Query().Get("path"))
				if path == "" {
					path = defaultArchitectureTasksPathForGraph(graphRoot, graphID)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					if os.IsNotExist(err) {
						focusNodeID := strings.TrimSpace(r.URL.Query().Get("focus_node_id"))
						report := buildArchitectureTaskReport(graph, focusNodeID)
						includeGraphData := parseBoolDefault(r.URL.Query().Get("include_graph_data"), false)
						resp := map[string]any{
							"graph_id": graphID,
							"path":     "",
							"report":   report,
						}
						if includeGraphData {
							resp["focused_subgraph"] = buildFocusedSubgraph(graph, fmt.Sprint(report["focus_node_id"]))
						}
						writeJSON(w, http.StatusOK, resp)
						return
					}
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				payload := map[string]any{}
				if err := json.Unmarshal(data, &payload); err != nil {
					writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode architecture task report: %v", err))
					return
				}
				resp := map[string]any{
					"graph_id": graphID,
					"path":     path,
					"report":   payload,
				}
				subgraphPath := strings.TrimSpace(r.URL.Query().Get("subgraph_path"))
				if subgraphPath == "" {
					subgraphPath = defaultArchitectureFocusedSubgraphPathForGraph(graphRoot, graphID)
				}
				if data, err := os.ReadFile(subgraphPath); err == nil {
					subgraph := map[string]any{}
					if json.Unmarshal(data, &subgraph) == nil {
						resp["focused_subgraph_path"] = subgraphPath
						resp["focused_subgraph"] = subgraph
					}
				}
				writeJSON(w, http.StatusOK, resp)
				return
			case http.MethodPost:
				authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, graphTenant(graph), false, true, graphRoot)
				if !ok {
					return
				}
				req := architectureTaskAssessRequest{}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
					writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
					return
				}
				report := buildArchitectureTaskReport(graph, strings.TrimSpace(req.FocusNodeID))
				outPath := strings.TrimSpace(req.OutPath)
				if outPath == "" {
					outPath = defaultArchitectureTasksPathForGraph(graphRoot, graphID)
				}
				raw, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					writeError(w, http.StatusInternalServerError, fmt.Sprintf("marshal architecture task report: %v", err))
					return
				}
				if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
					writeError(w, http.StatusInternalServerError, fmt.Sprintf("create architecture task report dir: %v", err))
					return
				}
				if err := os.WriteFile(outPath, raw, 0o644); err != nil {
					writeError(w, http.StatusInternalServerError, fmt.Sprintf("write architecture task report: %v", err))
					return
				}
				resp := map[string]any{
					"graph_id": graphID,
					"path":     outPath,
					"report":   report,
				}
				if req.ExportSubgraph || strings.TrimSpace(req.SubgraphOutPath) != "" {
					subgraph := buildFocusedSubgraph(graph, fmt.Sprint(report["focus_node_id"]))
					subgraphPath := strings.TrimSpace(req.SubgraphOutPath)
					if subgraphPath == "" {
						subgraphPath = defaultArchitectureFocusedSubgraphPathForGraph(graphRoot, graphID)
					}
					rawSubgraph, err := json.MarshalIndent(subgraph, "", "  ")
					if err != nil {
						writeError(w, http.StatusInternalServerError, fmt.Sprintf("marshal focused subgraph: %v", err))
						return
					}
					if err := os.MkdirAll(filepath.Dir(subgraphPath), 0o755); err != nil {
						writeError(w, http.StatusInternalServerError, fmt.Sprintf("create focused subgraph dir: %v", err))
						return
					}
					if err := os.WriteFile(subgraphPath, rawSubgraph, 0o644); err != nil {
						writeError(w, http.StatusInternalServerError, fmt.Sprintf("write focused subgraph: %v", err))
						return
					}
					resp["focused_subgraph_path"] = subgraphPath
					if req.IncludeGraphData {
						resp["focused_subgraph"] = subgraph
					}
				}
				recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "architecture_tasks_assessed", map[string]any{
					"graph_id": graphID,
					"path":     outPath,
				})
				writeJSON(w, http.StatusOK, resp)
				return
			default:
				w.Header().Set("Allow", "GET, POST")
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
		}
		if len(parts) == 2 && parts[1] == "merge-quality" {
			if r.Method == http.MethodPost {
				authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, graphTenant(graph), false, true, graphRoot)
				if !ok {
					return
				}
				req := graphMergeQualityAssessRequest{}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
					writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
					return
				}
				indexPath := strings.TrimSpace(req.IndexPath)
				if indexPath == "" {
					indexPath = filepath.Join(graphRoot, "index.json")
				}
				outPath := strings.TrimSpace(req.OutPath)
				if outPath == "" {
					outPath = defaultMergeQualityPathForGraph(graphRoot, graphID)
				}
				result, err := graphpkg.Assess(r.Context(), graphpkg.AssessRequest{
					GraphPath:       graphPathFromRoot(graphRoot, graphID),
					IndexPath:       indexPath,
					OutPath:         outPath,
					ExpectLinksPath: strings.TrimSpace(req.ExpectLinksPath),
					FailOnGate:      req.FailOnGate,
				})
				if err != nil {
					recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "deny", "merge_quality_assess_failed", map[string]any{"error": err.Error(), "graph_id": graphID, "out_path": outPath})
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				reportData, err := os.ReadFile(result.ReportPath)
				if err != nil {
					writeError(w, http.StatusInternalServerError, fmt.Sprintf("read merge quality report: %v", err))
					return
				}
				var payload map[string]any
				if err := json.Unmarshal(reportData, &payload); err != nil {
					writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode merge quality report: %v", err))
					return
				}
				recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "merge_quality_assess_succeeded", map[string]any{"graph_id": result.GraphID, "report_path": result.ReportPath, "passed": result.Passed})
				writeJSON(w, http.StatusOK, map[string]any{
					"graph_id":   result.GraphID,
					"graph_path": result.GraphPath,
					"path":       result.ReportPath,
					"passed":     result.Passed,
					"report":     payload,
				})
				return
			}
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET, POST")
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			path := strings.TrimSpace(r.URL.Query().Get("path"))
			if path == "" {
				path = defaultMergeQualityPathForGraph(graphRoot, graphID)
			}
			data, err := os.ReadFile(path)
			if err != nil && os.IsNotExist(err) && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
				// Backward compatibility: fallback to legacy shared path.
				path = defaultMergeQualityPath(graphRoot)
				data, err = os.ReadFile(path)
			}
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "merge quality report not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode merge quality report: %v", err))
				return
			}
			reportGraphID := strings.TrimSpace(fmt.Sprint(payload["graph_id"]))
			if reportGraphID != "" && reportGraphID != graphID {
				writeError(w, http.StatusNotFound, "merge quality report for graph not found")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"graph_id": graphID,
				"path":     path,
				"report":   payload,
			})
			return
		}
		writeJSON(w, http.StatusOK, graph)
	}
}

func handleGraphAt(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		at, err := parseTimeTravelAt(r.URL.Query().Get("at"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))

		index, err := loadGraphIndex(graphRoot)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph index not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		summary, ok := selectGraphAt(index, at, mode)
		if !ok {
			writeError(w, http.StatusNotFound, "no graph available for requested time")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, summaryTenant(summary), false, false, graphRoot)
		if !ok {
			return
		}

		graph, err := loadGraphByPath(summary.Path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		filters, err := parseGraphFilters(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid confidence_min")
			return
		}
		if hasGraphFilter(filters) {
			graph = filterGraph(graph, filters)
		}
		graph = applyStrictPublishPolicy(graph, parsePublishPolicy(r))
		includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
		graph = security.RedactGraph(graph, authCtx, includeSensitive)
		graph = annotateGraphFreshness(graph, time.Now().UTC(), parseMaxAgeHours(r.URL.Query().Get("max_age_hours")))
		pagination, err := parseQueryPagination(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		graph = applyQueryPagination(graph, pagination)
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, graph)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"graph":   graph,
			"explain": buildGraphExplain(graph),
		})
	}
}

func authorizeRequest(w http.ResponseWriter, r *http.Request, action security.Action, resourceTenant string, sensitive bool, mutating bool, graphRoot string) (security.Context, bool) {
	ctx, err := security.ContextFromHeaders(r.Header)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		recordAudit(graphRoot, security.Context{}, r, action, "deny", "missing_auth_context", map[string]any{
			"error": err.Error(),
		})
		return security.Context{}, false
	}
	decision := security.Authorize(ctx, security.Request{
		Action:         action,
		ResourceTenant: normalizeTenant(resourceTenant),
		Method:         r.Method,
		Path:           r.URL.Path,
		Sensitive:      sensitive,
		Mutating:       mutating,
	})
	if !decision.Allow {
		writeError(w, http.StatusForbidden, decision.Reason)
		recordAudit(graphRoot, ctx, r, action, "deny", decision.Reason, nil)
		return security.Context{}, false
	}
	recordAudit(graphRoot, ctx, r, action, "allow", decision.Reason, nil)
	return ctx, true
}

func normalizeTenant(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	return v
}

func graphTenant(graph graphschema.Graph) string {
	return normalizeTenant(graph.Meta.TenantID)
}

func summaryTenant(s graphschema.Summary) string {
	return normalizeTenant(s.TenantID)
}

func recordAudit(graphRoot string, ctx security.Context, r *http.Request, action security.Action, decision string, reason string, metadata map[string]any) {
	_ = audit.AppendEvent(filepath.Dir(graphRoot), audit.Event{
		Timestamp: time.Now().UTC(),
		Action:    string(action),
		TenantID:  normalizeTenant(ctx.TenantID),
		Principal: strings.TrimSpace(ctx.Principal),
		Method:    r.Method,
		Path:      r.URL.Path,
		Decision:  decision,
		Reason:    reason,
		Metadata:  metadata,
	})
}

func loadGraph(graphRoot string, graphID string) (graphschema.Graph, error) {
	graphPath := filepath.Join(graphRoot, graphID, "graph.json")
	return loadGraphByPath(graphPath)
}

func loadGraphByPath(graphPath string) (graphschema.Graph, error) {
	data, err := os.ReadFile(graphPath)
	if err != nil {
		return graphschema.Graph{}, err
	}
	var graph graphschema.Graph
	if err := json.Unmarshal(data, &graph); err != nil {
		return graphschema.Graph{}, fmt.Errorf("decode graph: %w", err)
	}
	return graph, nil
}

func loadGraphIndex(graphRoot string) (graphschema.Index, error) {
	indexPath := filepath.Join(graphRoot, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return graphschema.Index{}, err
	}
	var index graphschema.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return graphschema.Index{}, fmt.Errorf("decode graph index: %w", err)
	}
	return index, nil
}

func parseTimeTravelAt(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
		ts := time.Unix(unix, 0).UTC()
		return &ts, nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("invalid at timestamp: expected RFC3339 or unix seconds")
	}
	u := ts.UTC()
	return &u, nil
}

func parseTimeTravelArg(raw string, argName string) (*time.Time, error) {
	ts, err := parseTimeTravelAt(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s timestamp: expected RFC3339 or unix seconds", argName)
	}
	return ts, nil
}

func resolveCompareGraphIDs(graphRoot string, req graphCompareRequest) (string, string, error) {
	fromAt, err := parseTimeTravelArg(req.FromAt, "from_at")
	if err != nil {
		return "", "", err
	}
	toAt, err := parseTimeTravelArg(req.ToAt, "to_at")
	if err != nil {
		return "", "", err
	}
	if fromAt == nil || toAt == nil {
		return "", "", errors.New("from_graph_id and to_graph_id are required (or provide from_at and to_at)")
	}
	index, err := loadGraphIndex(graphRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", os.ErrNotExist
		}
		return "", "", fmt.Errorf("load graph index: %w", err)
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	fromSummary, ok := selectGraphAt(index, fromAt, mode)
	if !ok {
		return "", "", errCompareFromAtNotFound
	}
	toSummary, ok := selectGraphAt(index, toAt, mode)
	if !ok {
		return "", "", errCompareToAtNotFound
	}
	return fromSummary.GraphID, toSummary.GraphID, nil
}

func selectGraphAt(index graphschema.Index, at *time.Time, mode string) (graphschema.Summary, bool) {
	candidates := make([]graphschema.Summary, 0, len(index.Graphs))
	for _, s := range index.Graphs {
		if mode != "" && strings.ToLower(strings.TrimSpace(s.Mode)) != mode {
			continue
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 0 {
		return graphschema.Summary{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].GeneratedAt.After(candidates[j].GeneratedAt)
	})
	if at == nil {
		return candidates[0], true
	}
	for _, s := range candidates {
		if !s.GeneratedAt.After(*at) {
			return s, true
		}
	}
	return graphschema.Summary{}, false
}

func parseMaxAgeHours(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 24
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 24
	}
	return v
}

func annotateGraphFreshness(graph graphschema.Graph, evaluatedAt time.Time, maxAgeHours int) graphschema.Graph {
	if maxAgeHours <= 0 {
		maxAgeHours = 24
	}
	age := int64(0)
	if !graph.GeneratedAt.IsZero() && evaluatedAt.After(graph.GeneratedAt) {
		age = int64(evaluatedAt.Sub(graph.GeneratedAt).Seconds())
	}
	graph.Meta.Freshness = graphschema.GraphFreshness{
		EvaluatedAt: evaluatedAt.UTC(),
		MaxAgeHours: maxAgeHours,
		AgeSeconds:  age,
		IsStale:     age > int64(maxAgeHours*3600),
	}
	return graph
}

func parseGraphFilters(r *http.Request) (graphFilters, error) {
	confidenceMin, err := parseFloatDefault(r.URL.Query().Get("confidence_min"), 0)
	if err != nil {
		return graphFilters{}, err
	}
	return graphFilters{
		IncludeInferred: parseBoolDefault(r.URL.Query().Get("include_inferred"), false),
		EdgeTypeFilter:  strings.TrimSpace(r.URL.Query().Get("edge_types")),
		TypeFilter:      strings.TrimSpace(r.URL.Query().Get("type")),
		ServiceFilter:   strings.TrimSpace(r.URL.Query().Get("service")),
		RepoFilter:      strings.TrimSpace(r.URL.Query().Get("repo")),
		NodeFilter:      strings.TrimSpace(r.URL.Query().Get("node")),
		SectionFilter:   strings.TrimSpace(r.URL.Query().Get("section")),
		ClassFilter:     strings.TrimSpace(r.URL.Query().Get("class")),
		ConfidenceMin:   confidenceMin,
		Verification:    firstNonEmptyTrimmed(r.URL.Query().Get("verification_state"), r.URL.Query().Get("verification_status")),
		AdapterID:       strings.TrimSpace(r.URL.Query().Get("adapter_id")),
		ProvVersion:     strings.TrimSpace(r.URL.Query().Get("provenance_version")),
		ConflictStatus:  strings.TrimSpace(r.URL.Query().Get("conflict_status")),
		Environment:     strings.TrimSpace(r.URL.Query().Get("environment")),
		QueryText:       strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))),
	}, nil
}

func parseQueryPagination(r *http.Request) (queryPagination, error) {
	out := queryPagination{}
	if q := strings.TrimSpace(r.URL.Query().Get("node_limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			return queryPagination{}, errors.New("invalid node_limit")
		}
		out.NodeLimit = n
	}
	if q := strings.TrimSpace(r.URL.Query().Get("edge_limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			return queryPagination{}, errors.New("invalid edge_limit")
		}
		out.EdgeLimit = n
	}
	return out, nil
}

func hasQueryPagination(p queryPagination) bool {
	return p.NodeLimit > 0 || p.EdgeLimit > 0
}

func applyQueryPagination(graph graphschema.Graph, p queryPagination) graphschema.Graph {
	if !hasQueryPagination(p) {
		return graph
	}

	totalNodes := len(graph.Nodes)
	totalEdges := len(graph.Edges)
	nodes := graph.Nodes
	nodesTruncated := false
	if p.NodeLimit > 0 && len(nodes) > p.NodeLimit {
		nodes = append([]graphschema.Node(nil), nodes[:p.NodeLimit]...)
		nodesTruncated = true
	}

	allowedNodeIDs := map[string]struct{}{}
	for _, n := range nodes {
		allowedNodeIDs[n.ID] = struct{}{}
	}

	edges := make([]graphschema.Edge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		if len(allowedNodeIDs) > 0 {
			if _, ok := allowedNodeIDs[e.SourceID]; !ok {
				continue
			}
			if _, ok := allowedNodeIDs[e.TargetID]; !ok {
				continue
			}
		}
		edges = append(edges, e)
	}
	edgesTruncated := len(edges) < len(graph.Edges)
	if p.EdgeLimit > 0 && len(edges) > p.EdgeLimit {
		edges = append([]graphschema.Edge(nil), edges[:p.EdgeLimit]...)
		edgesTruncated = true
	}

	graph.Nodes = nodes
	graph.Edges = edges
	graph.Stats = recomputeGraphStats(nodes, edges)
	graph.Meta.QueryPagination = graphschema.QueryPagination{
		NodeLimit:      p.NodeLimit,
		EdgeLimit:      p.EdgeLimit,
		TotalNodes:     totalNodes,
		TotalEdges:     totalEdges,
		ReturnedNodes:  len(nodes),
		ReturnedEdges:  len(edges),
		NodesTruncated: nodesTruncated,
		EdgesTruncated: edgesTruncated,
	}
	return graph
}

func parsePublishPolicy(r *http.Request) publishPolicy {
	return publishPolicy{
		IncludeDisputed: parseBoolDefault(r.URL.Query().Get("include_disputed"), false),
	}
}

func hasGraphFilter(f graphFilters) bool {
	return !f.IncludeInferred || f.EdgeTypeFilter != "" || f.TypeFilter != "" || f.ServiceFilter != "" || f.RepoFilter != "" || f.NodeFilter != "" || f.SectionFilter != "" || f.ClassFilter != "" || f.ConfidenceMin > 0 || f.Verification != "" || f.AdapterID != "" || f.ProvVersion != "" || f.ConflictStatus != "" || f.Environment != "" || f.QueryText != ""
}

func filterGraph(graph graphschema.Graph, f graphFilters) graphschema.Graph {
	edges := filterGraphEdges(graph, f)
	serviceRepo := map[string]string{}
	for _, s := range graph.Meta.Services {
		serviceRepo[s.ID] = s.RepoPath
	}

	// If there is no explicit node-scoping filter and edge filtering produced no edges,
	// preserve nodes so node-only graphs remain visible in the UI.
	hasNodeScope := f.TypeFilter != "" || f.ServiceFilter != "" || f.RepoFilter != "" || f.NodeFilter != "" || f.SectionFilter != "" || f.ClassFilter != "" || f.Verification != "" || f.AdapterID != "" || f.ProvVersion != "" || f.ConflictStatus != "" || f.Environment != "" || f.QueryText != ""
	if !hasNodeScope && len(edges) == 0 {
		graph.Edges = edges
		graph.Stats = recomputeGraphStats(graph.Nodes, edges)
		return graph
	}

	includeNodes := map[string]struct{}{}
	for _, e := range edges {
		includeNodes[e.SourceID] = struct{}{}
		includeNodes[e.TargetID] = struct{}{}
	}
	if hasNodeScope {
		for _, n := range graph.Nodes {
			if nodeMatchesFilters(n, serviceRepo, f) {
				includeNodes[n.ID] = struct{}{}
			}
		}
	}
	for _, n := range graph.Nodes {
		if f.ServiceFilter != "" && n.ServiceID == f.ServiceFilter {
			includeNodes[n.ID] = struct{}{}
		}
		if f.RepoFilter != "" && serviceRepo[n.ServiceID] == f.RepoFilter {
			includeNodes[n.ID] = struct{}{}
		}
		if f.NodeFilter != "" && n.ID == f.NodeFilter {
			includeNodes[n.ID] = struct{}{}
		}
		if f.TypeFilter != "" && equalsFoldTrimmed(n.Type, f.TypeFilter) {
			includeNodes[n.ID] = struct{}{}
		}
	}

	nodes := make([]graphschema.Node, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if _, ok := includeNodes[n.ID]; !ok {
			continue
		}
		if !nodeMatchesFilters(n, serviceRepo, f) {
			continue
		}
		nodes = append(nodes, n)
	}
	nodeSet := map[string]struct{}{}
	for _, n := range nodes {
		nodeSet[n.ID] = struct{}{}
	}
	keptEdges := make([]graphschema.Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := nodeSet[e.SourceID]; !ok {
			continue
		}
		if _, ok := nodeSet[e.TargetID]; !ok {
			continue
		}
		keptEdges = append(keptEdges, e)
	}

	graph.Nodes = nodes
	graph.Edges = keptEdges
	graph.Stats = recomputeGraphStats(nodes, keptEdges)
	return graph
}

func filterGraphEdges(graph graphschema.Graph, f graphFilters) []graphschema.Edge {
	allowedTypes := map[string]struct{}{}
	if f.EdgeTypeFilter != "" {
		for _, t := range strings.Split(f.EdgeTypeFilter, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				allowedTypes[t] = struct{}{}
			}
		}
	}

	nodeService := map[string]string{}
	for _, n := range graph.Nodes {
		nodeService[n.ID] = n.ServiceID
	}
	serviceRepo := map[string]string{}
	for _, s := range graph.Meta.Services {
		serviceRepo[s.ID] = s.RepoPath
	}

	out := make([]graphschema.Edge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		if !f.IncludeInferred && e.Inferred {
			continue
		}
		if len(allowedTypes) > 0 {
			if _, ok := allowedTypes[e.Type]; !ok {
				continue
			}
		}
		if f.TypeFilter != "" && !equalsFoldTrimmed(e.Type, f.TypeFilter) {
			continue
		}
		if f.ServiceFilter != "" {
			if nodeService[e.SourceID] != f.ServiceFilter && nodeService[e.TargetID] != f.ServiceFilter {
				continue
			}
		}
		if f.RepoFilter != "" {
			sourceRepo := serviceRepo[nodeService[e.SourceID]]
			targetRepo := serviceRepo[nodeService[e.TargetID]]
			if sourceRepo != f.RepoFilter && targetRepo != f.RepoFilter {
				continue
			}
		}
		if e.Confidence < f.ConfidenceMin {
			continue
		}
		if f.NodeFilter != "" && e.SourceID != f.NodeFilter && e.TargetID != f.NodeFilter {
			continue
		}
		if f.SectionFilter != "" && !equalsFoldTrimmed(e.Section, f.SectionFilter) {
			continue
		}
		if f.ClassFilter != "" && !equalsFoldTrimmed(e.Class, f.ClassFilter) {
			continue
		}
		if f.Verification != "" {
			v := effectiveVerificationState(e.VerificationState, e.Attributes, e.Inferred)
			if !equalsFoldTrimmed(v, f.Verification) {
				continue
			}
		}
		if f.AdapterID != "" {
			adapter := firstNonEmptyTrimmed(
				attrString(e.Attributes, "adapter_id"),
				attrString(e.Attributes, "provenance_adapter_id"),
			)
			if !equalsFoldTrimmed(adapter, f.AdapterID) {
				continue
			}
		}
		if f.ProvVersion != "" {
			version := firstNonEmptyTrimmed(
				attrString(e.Attributes, "provenance_version"),
				attrString(e.Attributes, "adapter_version"),
				attrString(e.Attributes, "rulepack_version"),
			)
			if !equalsFoldTrimmed(version, f.ProvVersion) {
				continue
			}
		}
		if f.QueryText != "" {
			blob := strings.ToLower(e.ID + " " + e.Type + " " + fmt.Sprint(e.Attributes))
			if !strings.Contains(blob, f.QueryText) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

func nodeMatchesFilters(n graphschema.Node, serviceRepo map[string]string, f graphFilters) bool {
	_ = serviceRepo
	if f.TypeFilter != "" && !equalsFoldTrimmed(n.Type, f.TypeFilter) {
		return false
	}
	if f.SectionFilter != "" && !equalsFoldTrimmed(n.Section, f.SectionFilter) {
		return false
	}
	if f.ClassFilter != "" && !equalsFoldTrimmed(n.Class, f.ClassFilter) {
		return false
	}
	if f.Verification != "" {
		v := effectiveVerificationState(n.VerificationState, n.Attributes, n.Inferred)
		if !equalsFoldTrimmed(v, f.Verification) {
			return false
		}
	}
	if f.AdapterID != "" {
		adapter := firstNonEmptyTrimmed(
			attrString(n.Attributes, "adapter_id"),
			attrString(n.Attributes, "provenance_adapter_id"),
		)
		if !equalsFoldTrimmed(adapter, f.AdapterID) {
			return false
		}
	}
	if f.ProvVersion != "" {
		version := firstNonEmptyTrimmed(
			attrString(n.Attributes, "provenance_version"),
			attrString(n.Attributes, "adapter_version"),
			attrString(n.Attributes, "rulepack_version"),
		)
		if !equalsFoldTrimmed(version, f.ProvVersion) {
			return false
		}
	}
	if f.ConflictStatus != "" {
		if n.Type != "conflict" {
			return false
		}
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(n.Attributes["status"])))
		if status != strings.ToLower(f.ConflictStatus) {
			return false
		}
	}
	if f.Environment != "" {
		env := strings.ToLower(strings.TrimSpace(fmt.Sprint(n.Attributes["environment"])))
		if env != strings.ToLower(f.Environment) {
			return false
		}
	}
	if f.QueryText != "" {
		blob := strings.ToLower(n.ID + " " + n.Type + " " + n.Label + " " + fmt.Sprint(n.Attributes))
		if !strings.Contains(blob, f.QueryText) {
			return false
		}
	}
	return true
}

func applyStrictPublishPolicy(graph graphschema.Graph, policy publishPolicy) graphschema.Graph {
	keepNode := map[string]struct{}{}
	for _, n := range graph.Nodes {
		if shouldKeepByPublishPolicy(n.Section, n.VerificationState, n.Attributes, n.Inferred, policy) {
			keepNode[n.ID] = struct{}{}
		}
	}

	nodes := make([]graphschema.Node, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if _, ok := keepNode[n.ID]; !ok {
			continue
		}
		nodes = append(nodes, n)
	}

	edges := make([]graphschema.Edge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		if _, ok := keepNode[e.SourceID]; !ok {
			continue
		}
		if _, ok := keepNode[e.TargetID]; !ok {
			continue
		}
		if !shouldKeepByPublishPolicy(e.Section, e.VerificationState, e.Attributes, e.Inferred, policy) {
			continue
		}
		edges = append(edges, e)
	}

	graph.Nodes = nodes
	graph.Edges = edges
	graph.Stats = recomputeGraphStats(nodes, edges)
	return graph
}

func shouldKeepByPublishPolicy(section string, verificationState string, attrs map[string]any, inferred bool, policy publishPolicy) bool {
	sec := strings.ToLower(strings.TrimSpace(section))
	if sec != "exposure" && sec != "dependencies" {
		return true
	}
	state := effectiveVerificationState(verificationState, attrs, inferred)
	if state == "verified" {
		return true
	}
	if state == "disputed" && policy.IncludeDisputed {
		return true
	}
	return false
}

func effectiveVerificationState(field string, attrs map[string]any, inferred bool) string {
	if inferred {
		return "inferred"
	}
	if v := strings.ToLower(strings.TrimSpace(field)); v != "" {
		return v
	}
	if attrs != nil {
		if raw, ok := attrs["verification_state"]; ok {
			if v := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw))); v != "" {
				return v
			}
		}
		if raw, ok := attrs["verification_status"]; ok {
			if v := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw))); v != "" {
				return v
			}
		}
		if raw, ok := attrs["status"]; ok {
			if v := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw))); v != "" {
				return v
			}
		}
	}
	return "verified"
}

func buildGraphExplain(graph graphschema.Graph) map[string]any {
	nodeExplain := make([]map[string]any, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeExplain = append(nodeExplain, map[string]any{
			"id":                  n.ID,
			"type":                n.Type,
			"label":               n.Label,
			"service_id":          n.ServiceID,
			"section":             n.Section,
			"class":               n.Class,
			"verification_state":  n.VerificationState,
			"confidence":          n.Confidence,
			"inferred":            n.Inferred,
			"verification_status": n.Attributes["verification_status"],
			"environment":         n.Attributes["environment"],
			"status":              n.Attributes["status"],
		})
	}
	edgeExplain := make([]map[string]any, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		edgeExplain = append(edgeExplain, map[string]any{
			"id":                 e.ID,
			"type":               e.Type,
			"source_id":          e.SourceID,
			"target_id":          e.TargetID,
			"section":            e.Section,
			"class":              e.Class,
			"verification_state": e.VerificationState,
			"confidence":         e.Confidence,
			"inferred":           e.Inferred,
			"attributes":         e.Attributes,
			"evidence_refs":      e.EvidenceRefs,
		})
	}
	return map[string]any{
		"meta":  graph.Meta,
		"nodes": nodeExplain,
		"edges": edgeExplain,
	}
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func equalsFoldTrimmed(a string, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func attrString(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	for k, v := range attrs {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(key)) {
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return ""
}

func recomputeGraphStats(nodes []graphschema.Node, edges []graphschema.Edge) graphschema.GraphStats {
	byNode := map[string]int{}
	byEdge := map[string]int{}
	for _, n := range nodes {
		byNode[n.Type]++
	}
	for _, e := range edges {
		byEdge[e.Type]++
	}
	return graphschema.GraphStats{
		NodeCount: len(nodes),
		EdgeCount: len(edges),
		ByNode:    byNode,
		ByEdge:    byEdge,
	}
}

func buildGraphMetrics(graph graphschema.Graph) graphMetrics {
	outgoing := map[string]int{}
	incoming := map[string]int{}
	labels := map[string]string{}
	edgeTypeCounts := map[string]int{}

	for _, n := range graph.Nodes {
		labels[n.ID] = n.Label
	}
	for _, e := range graph.Edges {
		outgoing[e.SourceID]++
		incoming[e.TargetID]++
		edgeTypeCounts[e.Type]++
	}

	return graphMetrics{
		GraphID:         graph.GraphID,
		NodeCount:       len(graph.Nodes),
		EdgeCount:       len(graph.Edges),
		EdgeTypeCounts:  edgeTypeCounts,
		TopCallers:      topMetricItems(outgoing, labels, 5),
		TopDependencies: topMetricItems(incoming, labels, 5),
	}
}

func buildGraphSummary(graph graphschema.Graph) map[string]any {
	serviceCount := 0
	for _, n := range graph.Nodes {
		if n.Type == "service" {
			serviceCount++
		}
	}
	density := 0.0
	nodeCount := len(graph.Nodes)
	if nodeCount > 1 {
		maxEdges := float64(nodeCount * (nodeCount - 1))
		density = float64(len(graph.Edges)) / maxEdges
	}
	return map[string]any{
		"graph_id":      graph.GraphID,
		"mode":          graph.Mode,
		"generated_at":  graph.GeneratedAt,
		"tenant_id":     graph.Meta.TenantID,
		"node_count":    len(graph.Nodes),
		"edge_count":    len(graph.Edges),
		"service_count": serviceCount,
		"density":       density,
		"by_node_type":  graph.Stats.ByNode,
		"by_edge_type":  graph.Stats.ByEdge,
		"freshness":     graph.Meta.Freshness,
	}
}

func topMetricItems(counts map[string]int, labels map[string]string, limit int) []metricItem {
	items := make([]metricItem, 0, len(counts))
	for nodeID, count := range counts {
		label := labels[nodeID]
		if label == "" {
			label = nodeID
		}
		items = append(items, metricItem{
			NodeID: nodeID,
			Label:  label,
			Count:  count,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].NodeID < items[j].NodeID
		}
		return items[i].Count > items[j].Count
	})
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func buildGraphCompare(from graphschema.Graph, to graphschema.Graph) graphCompare {
	fromNodes := map[string]graphschema.Node{}
	toNodes := map[string]graphschema.Node{}
	for _, n := range from.Nodes {
		fromNodes[n.ID] = n
	}
	for _, n := range to.Nodes {
		toNodes[n.ID] = n
	}

	fromEdges := map[string]graphschema.Edge{}
	toEdges := map[string]graphschema.Edge{}
	for _, e := range from.Edges {
		fromEdges[e.ID] = e
	}
	for _, e := range to.Edges {
		toEdges[e.ID] = e
	}

	addedNodes := make([]nodeCompareItem, 0)
	removedNodes := make([]nodeCompareItem, 0)
	changedNodes := make([]nodeChangeItem, 0)
	for id, n := range toNodes {
		if _, ok := fromNodes[id]; !ok {
			addedNodes = append(addedNodes, nodeCompareItem{
				ID:        n.ID,
				Type:      n.Type,
				Label:     n.Label,
				ServiceID: n.ServiceID,
			})
		}
	}
	for id, n := range fromNodes {
		if _, ok := toNodes[id]; !ok {
			removedNodes = append(removedNodes, nodeCompareItem{
				ID:        n.ID,
				Type:      n.Type,
				Label:     n.Label,
				ServiceID: n.ServiceID,
			})
		}
	}
	for id, fromNode := range fromNodes {
		toNode, ok := toNodes[id]
		if !ok {
			continue
		}
		keys := diffNodeKeys(fromNode, toNode)
		if len(keys) > 0 {
			changedNodes = append(changedNodes, nodeChangeItem{
				ID:        id,
				Label:     toNode.Label,
				ServiceID: toNode.ServiceID,
				Keys:      keys,
				Before: map[string]any{
					"type":       fromNode.Type,
					"label":      fromNode.Label,
					"service_id": fromNode.ServiceID,
					"confidence": fromNode.Confidence,
					"inferred":   fromNode.Inferred,
					"attributes": fromNode.Attributes,
				},
				After: map[string]any{
					"type":       toNode.Type,
					"label":      toNode.Label,
					"service_id": toNode.ServiceID,
					"confidence": toNode.Confidence,
					"inferred":   toNode.Inferred,
					"attributes": toNode.Attributes,
				},
			})
		}
	}

	addedEdges := make([]edgeCompareItem, 0)
	removedEdges := make([]edgeCompareItem, 0)
	changedEdges := make([]edgeChangeItem, 0)
	for id, e := range toEdges {
		if _, ok := fromEdges[id]; !ok {
			addedEdges = append(addedEdges, edgeCompareItem{
				ID:         e.ID,
				Type:       e.Type,
				SourceID:   e.SourceID,
				TargetID:   e.TargetID,
				Confidence: e.Confidence,
			})
		}
	}
	for id, e := range fromEdges {
		if _, ok := toEdges[id]; !ok {
			removedEdges = append(removedEdges, edgeCompareItem{
				ID:         e.ID,
				Type:       e.Type,
				SourceID:   e.SourceID,
				TargetID:   e.TargetID,
				Confidence: e.Confidence,
			})
		}
	}
	for id, fromEdge := range fromEdges {
		toEdge, ok := toEdges[id]
		if !ok {
			continue
		}
		keys := diffEdgeKeys(fromEdge, toEdge)
		if len(keys) > 0 {
			changedEdges = append(changedEdges, edgeChangeItem{
				ID:       id,
				Type:     toEdge.Type,
				SourceID: toEdge.SourceID,
				TargetID: toEdge.TargetID,
				Keys:     keys,
				Before: map[string]any{
					"type":       fromEdge.Type,
					"source_id":  fromEdge.SourceID,
					"target_id":  fromEdge.TargetID,
					"confidence": fromEdge.Confidence,
					"inferred":   fromEdge.Inferred,
					"attributes": fromEdge.Attributes,
				},
				After: map[string]any{
					"type":       toEdge.Type,
					"source_id":  toEdge.SourceID,
					"target_id":  toEdge.TargetID,
					"confidence": toEdge.Confidence,
					"inferred":   toEdge.Inferred,
					"attributes": toEdge.Attributes,
				},
			})
		}
	}

	sort.Slice(addedNodes, func(i, j int) bool { return addedNodes[i].ID < addedNodes[j].ID })
	sort.Slice(removedNodes, func(i, j int) bool { return removedNodes[i].ID < removedNodes[j].ID })
	sort.Slice(changedNodes, func(i, j int) bool { return changedNodes[i].ID < changedNodes[j].ID })
	sort.Slice(addedEdges, func(i, j int) bool { return addedEdges[i].ID < addedEdges[j].ID })
	sort.Slice(removedEdges, func(i, j int) bool { return removedEdges[i].ID < removedEdges[j].ID })
	sort.Slice(changedEdges, func(i, j int) bool { return changedEdges[i].ID < changedEdges[j].ID })

	return graphCompare{
		FromGraphID: from.GraphID,
		ToGraphID:   to.GraphID,
		FromCounts: map[string]int{
			"nodes": len(from.Nodes),
			"edges": len(from.Edges),
		},
		ToCounts: map[string]int{
			"nodes": len(to.Nodes),
			"edges": len(to.Edges),
		},
		AddedNodes:   addedNodes,
		RemovedNodes: removedNodes,
		ChangedNodes: changedNodes,
		AddedEdges:   addedEdges,
		RemovedEdges: removedEdges,
		ChangedEdges: changedEdges,
	}
}

func buildCompareImpact(compare graphCompare) compareImpact {
	nodeSet := map[string]struct{}{}
	edgeSet := map[string]struct{}{}
	reasons := map[string][]string{
		"added_nodes":    {},
		"removed_nodes":  {},
		"changed_nodes":  {},
		"added_edges":    {},
		"removed_edges":  {},
		"changed_edges":  {},
		"edge_endpoints": {},
	}

	for _, n := range compare.AddedNodes {
		nodeSet[n.ID] = struct{}{}
		reasons["added_nodes"] = append(reasons["added_nodes"], n.ID)
	}
	for _, n := range compare.RemovedNodes {
		nodeSet[n.ID] = struct{}{}
		reasons["removed_nodes"] = append(reasons["removed_nodes"], n.ID)
	}
	for _, n := range compare.ChangedNodes {
		nodeSet[n.ID] = struct{}{}
		reasons["changed_nodes"] = append(reasons["changed_nodes"], n.ID)
	}
	for _, e := range compare.AddedEdges {
		edgeSet[e.ID] = struct{}{}
		nodeSet[e.SourceID] = struct{}{}
		nodeSet[e.TargetID] = struct{}{}
		reasons["added_edges"] = append(reasons["added_edges"], e.ID)
		reasons["edge_endpoints"] = append(reasons["edge_endpoints"], e.SourceID, e.TargetID)
	}
	for _, e := range compare.RemovedEdges {
		edgeSet[e.ID] = struct{}{}
		nodeSet[e.SourceID] = struct{}{}
		nodeSet[e.TargetID] = struct{}{}
		reasons["removed_edges"] = append(reasons["removed_edges"], e.ID)
		reasons["edge_endpoints"] = append(reasons["edge_endpoints"], e.SourceID, e.TargetID)
	}
	for _, e := range compare.ChangedEdges {
		edgeSet[e.ID] = struct{}{}
		nodeSet[e.SourceID] = struct{}{}
		nodeSet[e.TargetID] = struct{}{}
		reasons["changed_edges"] = append(reasons["changed_edges"], e.ID)
		reasons["edge_endpoints"] = append(reasons["edge_endpoints"], e.SourceID, e.TargetID)
	}

	nodeIDs := sortedSetKeys(nodeSet)
	edgeIDs := sortedSetKeys(edgeSet)
	for key := range reasons {
		reasons[key] = dedupeSorted(reasons[key])
	}

	return compareImpact{
		CompareID:       compare.CompareID,
		FromGraphID:     compare.FromGraphID,
		ToGraphID:       compare.ToGraphID,
		ImpactedNodeIDs: nodeIDs,
		ImpactedEdgeIDs: edgeIDs,
		Counts: map[string]int{
			"nodes": len(nodeIDs),
			"edges": len(edgeIDs),
		},
		Reasons: reasons,
	}
}

func sortedSetKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupeSorted(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	sort.Strings(values)
	out := make([]string, 0, len(values))
	last := ""
	for i, v := range values {
		if i == 0 || v != last {
			out = append(out, v)
			last = v
		}
	}
	return out
}

func listCompareHistory(graphRoot string, r *http.Request, w http.ResponseWriter) {
	indexPath := filepath.Join(graphRoot, "compare", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, compareIndex{Compares: []compareSummary{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var index compareIndex
	if err := json.Unmarshal(data, &index); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode compare index: %v", err))
		return
	}
	if authCtx, err := security.ContextFromHeaders(r.Header); err == nil && !authCtx.HasRole("platform_admin") {
		filtered := make([]compareSummary, 0, len(index.Compares))
		for _, c := range index.Compares {
			if normalizeTenant(c.TenantID) == normalizeTenant(authCtx.TenantID) {
				filtered = append(filtered, c)
			}
		}
		index.Compares = filtered
	}
	fromGraphFilter := strings.TrimSpace(r.URL.Query().Get("from_graph_id"))
	toGraphFilter := strings.TrimSpace(r.URL.Query().Get("to_graph_id"))
	fromTs, err := parseTimeTravelAt(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid from timestamp")
		return
	}
	toTs, err := parseTimeTravelAt(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid to timestamp")
		return
	}
	if fromTs != nil && toTs != nil && fromTs.After(*toTs) {
		writeError(w, http.StatusBadRequest, "from timestamp must be <= to timestamp")
		return
	}
	if fromGraphFilter != "" || toGraphFilter != "" || fromTs != nil || toTs != nil {
		filtered := make([]compareSummary, 0, len(index.Compares))
		for _, c := range index.Compares {
			if fromGraphFilter != "" && c.FromGraphID != fromGraphFilter {
				continue
			}
			if toGraphFilter != "" && c.ToGraphID != toGraphFilter {
				continue
			}
			if fromTs != nil && c.GeneratedAt.Before(*fromTs) {
				continue
			}
			if toTs != nil && c.GeneratedAt.After(*toTs) {
				continue
			}
			filtered = append(filtered, c)
		}
		index.Compares = filtered
	}
	// Optional cap to avoid sending very large history lists.
	limit := 0
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	start := 0
	if before != "" {
		found := -1
		for i, c := range index.Compares {
			if c.CompareID == before {
				found = i
				break
			}
		}
		if found == -1 {
			writeError(w, http.StatusBadRequest, "invalid before cursor")
			return
		}
		start = found + 1
	}
	if start > len(index.Compares) {
		start = len(index.Compares)
	}
	comps := index.Compares[start:]
	nextBefore := ""
	if limit > 0 && len(comps) > limit {
		comps = comps[:limit]
		nextBefore = comps[len(comps)-1].CompareID
	}
	writeJSON(w, http.StatusOK, compareIndex{
		Compares:   comps,
		NextBefore: nextBefore,
	})
}

func persistCompareResult(graphRoot string, result *graphCompare) error {
	if result == nil {
		return fmt.Errorf("nil compare result")
	}
	now := time.Now().UTC()
	if result.CompareID == "" {
		result.CompareID = fmt.Sprintf("%d", now.UnixNano())
	}
	result.GeneratedAt = now

	compareDir := filepath.Join(graphRoot, "compare", result.CompareID)
	if err := os.MkdirAll(compareDir, 0o755); err != nil {
		return fmt.Errorf("create compare dir: %w", err)
	}
	comparePath := filepath.Join(compareDir, "compare.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal compare result: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(comparePath, data, 0o644); err != nil {
		return fmt.Errorf("write compare result: %w", err)
	}

	indexPath := filepath.Join(graphRoot, "compare", "index.json")
	idx := compareIndex{Compares: []compareSummary{}}
	if indexData, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(indexData, &idx)
	}
	filtered := make([]compareSummary, 0, len(idx.Compares)+1)
	for _, c := range idx.Compares {
		if c.CompareID != result.CompareID {
			filtered = append(filtered, c)
		}
	}
	filtered = append(filtered, compareSummary{
		CompareID:    result.CompareID,
		GeneratedAt:  result.GeneratedAt,
		TenantID:     normalizeTenant(result.TenantID),
		FromGraphID:  result.FromGraphID,
		ToGraphID:    result.ToGraphID,
		AddedNodes:   len(result.AddedNodes),
		RemovedNodes: len(result.RemovedNodes),
		ChangedNodes: len(result.ChangedNodes),
		AddedEdges:   len(result.AddedEdges),
		RemovedEdges: len(result.RemovedEdges),
		ChangedEdges: len(result.ChangedEdges),
		Path:         comparePath,
	})
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].GeneratedAt.After(filtered[j].GeneratedAt)
	})
	idx.Compares = filtered

	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal compare index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return fmt.Errorf("create compare index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write compare index: %w", err)
	}
	return nil
}

func loadCompareResult(graphRoot string, compareID string) (graphCompare, error) {
	comparePath := filepath.Join(graphRoot, "compare", compareID, "compare.json")
	data, err := os.ReadFile(comparePath)
	if err != nil {
		return graphCompare{}, err
	}
	var payload graphCompare
	if err := json.Unmarshal(data, &payload); err != nil {
		return graphCompare{}, fmt.Errorf("decode compare result: %w", err)
	}
	return payload, nil
}

func adjudicationIndexPath(graphRoot string, graphID string) string {
	return filepath.Join(graphRoot, "adjudication", strings.TrimSpace(graphID), "index.json")
}

func loadAdjudicationIndex(graphRoot string, graphID string) (adjudicationIndex, error) {
	path := adjudicationIndexPath(graphRoot, graphID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return adjudicationIndex{
				GraphID:       strings.TrimSpace(graphID),
				Adjudications: []adjudicationRecord{},
			}, nil
		}
		return adjudicationIndex{}, err
	}
	var idx adjudicationIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return adjudicationIndex{}, fmt.Errorf("decode adjudication index: %w", err)
	}
	if strings.TrimSpace(idx.GraphID) == "" {
		idx.GraphID = strings.TrimSpace(graphID)
	}
	if idx.Adjudications == nil {
		idx.Adjudications = []adjudicationRecord{}
	}
	return idx, nil
}

func persistAdjudicationRecord(graphRoot string, graphID string, record adjudicationRecord) error {
	idx, err := loadAdjudicationIndex(graphRoot, graphID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if strings.TrimSpace(record.ID) == "" {
		record.ID = fmt.Sprintf("adj:%d", now.UnixNano())
	}
	filtered := make([]adjudicationRecord, 0, len(idx.Adjudications)+1)
	for _, item := range idx.Adjudications {
		if item.TargetID == record.TargetID && item.TargetKind == record.TargetKind {
			continue
		}
		filtered = append(filtered, item)
	}
	filtered = append(filtered, record)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	idx.GraphID = strings.TrimSpace(graphID)
	idx.GeneratedAt = now
	idx.Adjudications = filtered

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal adjudication index: %w", err)
	}
	data = append(data, '\n')
	path := adjudicationIndexPath(graphRoot, graphID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create adjudication dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write adjudication index: %w", err)
	}
	return nil
}

func deleteCompareResult(graphRoot string, compareID string) error {
	comparePath := filepath.Join(graphRoot, "compare", compareID, "compare.json")
	if _, err := os.Stat(comparePath); err != nil {
		return err
	}
	compareDir := filepath.Dir(comparePath)
	if err := os.RemoveAll(compareDir); err != nil {
		return fmt.Errorf("remove compare dir: %w", err)
	}

	indexPath := filepath.Join(graphRoot, "compare", "index.json")
	idx := compareIndex{Compares: []compareSummary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(data, &idx)
	}
	filtered := make([]compareSummary, 0, len(idx.Compares))
	for _, c := range idx.Compares {
		if c.CompareID != compareID {
			filtered = append(filtered, c)
		}
	}
	idx.Compares = filtered
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal compare index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write compare index: %w", err)
	}
	return nil
}

func pruneCompareHistory(graphRoot string, keepLatest int) (int, error) {
	indexPath := filepath.Join(graphRoot, "compare", "index.json")
	idx := compareIndex{Compares: []compareSummary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		if err := json.Unmarshal(data, &idx); err != nil {
			return 0, fmt.Errorf("decode compare index: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	if keepLatest >= len(idx.Compares) {
		return 0, nil
	}

	toDelete := idx.Compares[keepLatest:]
	deleted := 0
	for _, c := range toDelete {
		if c.Path == "" {
			continue
		}
		if err := os.RemoveAll(filepath.Dir(c.Path)); err == nil {
			deleted++
		}
	}
	idx.Compares = idx.Compares[:keepLatest]
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return deleted, fmt.Errorf("marshal compare index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return deleted, fmt.Errorf("create compare index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return deleted, fmt.Errorf("write compare index: %w", err)
	}
	return deleted, nil
}

func incrementalPlanIndexPath(graphRoot string) string {
	return filepath.Join(graphRoot, "incremental", "index.json")
}

func loadIncrementalPlanIndex(graphRoot string) (incrementalPlanIndex, error) {
	path := incrementalPlanIndexPath(graphRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return incrementalPlanIndex{Plans: []incrementalPlanSummary{}}, nil
		}
		return incrementalPlanIndex{}, err
	}
	idx := incrementalPlanIndex{Plans: []incrementalPlanSummary{}}
	if err := json.Unmarshal(data, &idx); err != nil {
		return incrementalPlanIndex{}, fmt.Errorf("decode incremental index: %w", err)
	}
	return idx, nil
}

func listIncrementalPlans(graphRoot string, r *http.Request, w http.ResponseWriter) {
	index, err := loadIncrementalPlanIndex(graphRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	index.Plans = filterIncrementalPlansByTenant(r, index.Plans)
	graphID := strings.TrimSpace(r.URL.Query().Get("graph_id"))
	if graphID != "" {
		filtered := make([]incrementalPlanSummary, 0, len(index.Plans))
		for _, p := range index.Plans {
			if strings.EqualFold(strings.TrimSpace(p.GraphID), graphID) {
				filtered = append(filtered, p)
			}
		}
		index.Plans = filtered
	}
	fromTs, err := parseTimeTravelAt(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid from timestamp")
		return
	}
	toTs, err := parseTimeTravelAt(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid to timestamp")
		return
	}
	if fromTs != nil && toTs != nil && fromTs.After(*toTs) {
		writeError(w, http.StatusBadRequest, "from timestamp must be <= to timestamp")
		return
	}
	if fromTs != nil || toTs != nil {
		filtered := make([]incrementalPlanSummary, 0, len(index.Plans))
		for _, p := range index.Plans {
			if fromTs != nil && p.GeneratedAt.Before(*fromTs) {
				continue
			}
			if toTs != nil && p.GeneratedAt.After(*toTs) {
				continue
			}
			filtered = append(filtered, p)
		}
		index.Plans = filtered
	}
	limit := 0
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	start := 0
	if before != "" {
		found := -1
		for i, item := range index.Plans {
			if item.PlanID == before {
				found = i
				break
			}
		}
		if found == -1 {
			writeError(w, http.StatusBadRequest, "invalid before cursor")
			return
		}
		start = found + 1
	}
	if start > len(index.Plans) {
		start = len(index.Plans)
	}
	plans := index.Plans[start:]
	nextBefore := ""
	if limit > 0 && len(plans) > limit {
		plans = plans[:limit]
		nextBefore = plans[len(plans)-1].PlanID
	}
	writeJSON(w, http.StatusOK, incrementalPlanIndex{Plans: plans, NextBefore: nextBefore})
}

func filterIncrementalPlansByTenant(r *http.Request, plans []incrementalPlanSummary) []incrementalPlanSummary {
	authCtx, err := security.ContextFromHeaders(r.Header)
	if err != nil || authCtx.HasRole("platform_admin") {
		return plans
	}
	tenantID := normalizeTenant(authCtx.TenantID)
	filtered := make([]incrementalPlanSummary, 0, len(plans))
	for _, p := range plans {
		if normalizeTenant(p.TenantID) == tenantID {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func persistIncrementalPlan(graphRoot string, plan incrementalPlan) error {
	if strings.TrimSpace(plan.PlanID) == "" {
		plan.PlanID = fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	if plan.GeneratedAt.IsZero() {
		plan.GeneratedAt = time.Now().UTC()
	}
	planDir := filepath.Join(graphRoot, "incremental", plan.PlanID)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return fmt.Errorf("create incremental plan dir: %w", err)
	}
	path := filepath.Join(planDir, "plan.json")
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal incremental plan: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write incremental plan: %w", err)
	}

	idx, err := loadIncrementalPlanIndex(graphRoot)
	if err != nil {
		return err
	}
	filtered := make([]incrementalPlanSummary, 0, len(idx.Plans)+1)
	for _, item := range idx.Plans {
		if item.PlanID != plan.PlanID {
			filtered = append(filtered, item)
		}
	}
	filtered = append(filtered, incrementalPlanSummary{
		PlanID:        plan.PlanID,
		GeneratedAt:   plan.GeneratedAt,
		TenantID:      normalizeTenant(plan.TenantID),
		GraphID:       plan.GraphID,
		ChangedFiles:  len(plan.ChangedFiles),
		SeedNodes:     len(plan.SeedNodes),
		ImpactedNodes: len(plan.ImpactedNodeIDs),
		ImpactedEdges: len(plan.ImpactedEdgeIDs),
		Path:          path,
	})
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].GeneratedAt.After(filtered[j].GeneratedAt)
	})
	idx.Plans = filtered
	idxData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal incremental index: %w", err)
	}
	idxData = append(idxData, '\n')
	if err := os.MkdirAll(filepath.Dir(incrementalPlanIndexPath(graphRoot)), 0o755); err != nil {
		return fmt.Errorf("create incremental index dir: %w", err)
	}
	if err := os.WriteFile(incrementalPlanIndexPath(graphRoot), idxData, 0o644); err != nil {
		return fmt.Errorf("write incremental index: %w", err)
	}
	return nil
}

func loadIncrementalPlan(graphRoot string, planID string) (incrementalPlan, error) {
	path := filepath.Join(graphRoot, "incremental", planID, "plan.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return incrementalPlan{}, err
	}
	var plan incrementalPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return incrementalPlan{}, fmt.Errorf("decode incremental plan: %w", err)
	}
	return plan, nil
}

func loadRuntimeReconcileIndex(graphRoot string) (runtimeReconcileIndex, error) {
	indexPath := filepath.Join(graphRoot, "runtime", "reconcile", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}, nil
		}
		return runtimeReconcileIndex{}, err
	}
	index := runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}
	if err := json.Unmarshal(data, &index); err != nil {
		return runtimeReconcileIndex{}, fmt.Errorf("decode runtime reconcile index: %w", err)
	}
	return index, nil
}

func filterRuntimeReconcileRunsByTenant(r *http.Request, runs []runtimeReconcileSummary) []runtimeReconcileSummary {
	authCtx, err := security.ContextFromHeaders(r.Header)
	if err != nil || authCtx.HasRole("platform_admin") {
		return runs
	}
	tenantID := normalizeTenant(authCtx.TenantID)
	filtered := make([]runtimeReconcileSummary, 0, len(runs))
	for _, run := range runs {
		if normalizeTenant(run.TenantID) == tenantID {
			filtered = append(filtered, run)
		}
	}
	return filtered
}

func listRuntimeReconcileHistory(graphRoot string, r *http.Request, w http.ResponseWriter) {
	index, err := loadRuntimeReconcileIndex(graphRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	index.Runs = filterRuntimeReconcileRunsByTenant(r, index.Runs)
	limit := 0
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	start := 0
	if before != "" {
		found := -1
		for i, item := range index.Runs {
			if item.ReconcileID == before {
				found = i
				break
			}
		}
		if found == -1 {
			writeError(w, http.StatusBadRequest, "invalid before cursor")
			return
		}
		start = found + 1
	}
	if start > len(index.Runs) {
		start = len(index.Runs)
	}
	runs := index.Runs[start:]
	nextBefore := ""
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
		nextBefore = runs[len(runs)-1].ReconcileID
	}
	writeJSON(w, http.StatusOK, runtimeReconcileIndex{Runs: runs, NextBefore: nextBefore})
}

func persistRuntimeReconcileResult(graphRoot string, tenantID string, req contracts.RuntimeReconciliationRequest, result contracts.RuntimeReconciliationResult) (string, time.Time, error) {
	now := time.Now().UTC()
	recID := fmt.Sprintf("%d", now.UnixNano())
	baseDir := filepath.Join(graphRoot, "runtime", "reconcile", recID)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", time.Time{}, fmt.Errorf("create runtime reconcile dir: %w", err)
	}
	payload := runtimeReconcileRecord{
		ReconcileID: recID,
		GeneratedAt: now,
		TenantID:    normalizeTenant(tenantID),
		Request:     req,
		Result:      result,
	}
	outPath := filepath.Join(baseDir, "result.json")
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshal runtime reconcile result: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return "", time.Time{}, fmt.Errorf("write runtime reconcile result: %w", err)
	}

	indexPath := filepath.Join(graphRoot, "runtime", "reconcile", "index.json")
	idx := runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}
	if indexData, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(indexData, &idx)
	}
	idx.Runs = append(idx.Runs, runtimeReconcileSummary{
		ReconcileID:  recID,
		GeneratedAt:  now,
		TenantID:     normalizeTenant(tenantID),
		GraphID:      strings.TrimSpace(req.GraphID),
		Claims:       len(req.Claims),
		Observations: len(req.Observations),
		Confirmed:    len(result.Confirmed),
		Contradicted: len(result.Contradicted),
		Unmapped:     len(result.Unmapped),
		NeedsReview:  len(result.NeedsReview),
		Path:         outPath,
	})
	sort.Slice(idx.Runs, func(i, j int) bool {
		return idx.Runs[i].GeneratedAt.After(idx.Runs[j].GeneratedAt)
	})
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshal runtime reconcile index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return "", time.Time{}, fmt.Errorf("create runtime reconcile index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return "", time.Time{}, fmt.Errorf("write runtime reconcile index: %w", err)
	}
	return recID, now, nil
}

func loadRuntimeReconcileResult(graphRoot string, reconcileID string) (runtimeReconcileRecord, error) {
	path := filepath.Join(graphRoot, "runtime", "reconcile", reconcileID, "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return runtimeReconcileRecord{}, err
	}
	var payload runtimeReconcileRecord
	if err := json.Unmarshal(data, &payload); err != nil {
		return runtimeReconcileRecord{}, fmt.Errorf("decode runtime reconcile result: %w", err)
	}
	return payload, nil
}

func verifyRunIndexPath(graphRoot string) string {
	return filepath.Join(graphRoot, "verify", "runs", "index.json")
}

func loadVerifyRunIndex(graphRoot string) (verifyRunIndex, error) {
	path := verifyRunIndexPath(graphRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return verifyRunIndex{Runs: []verifyRunSummary{}}, nil
		}
		return verifyRunIndex{}, err
	}
	index := verifyRunIndex{Runs: []verifyRunSummary{}}
	if err := json.Unmarshal(data, &index); err != nil {
		return verifyRunIndex{}, fmt.Errorf("decode verify run index: %w", err)
	}
	return index, nil
}

func persistVerifyRun(graphRoot string, run verifyRunSummary) error {
	index, err := loadVerifyRunIndex(graphRoot)
	if err != nil {
		return err
	}
	filtered := make([]verifyRunSummary, 0, len(index.Runs)+1)
	for _, item := range index.Runs {
		if item.RunID != run.RunID {
			filtered = append(filtered, item)
		}
	}
	filtered = append(filtered, run)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].GeneratedAt.After(filtered[j].GeneratedAt)
	})
	index.Runs = filtered
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal verify run index: %w", err)
	}
	data = append(data, '\n')
	path := verifyRunIndexPath(graphRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create verify run dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write verify run index: %w", err)
	}
	return nil
}

func loadVerifyRunByID(graphRoot string, runID string) (verifyRunSummary, error) {
	index, err := loadVerifyRunIndex(graphRoot)
	if err != nil {
		return verifyRunSummary{}, err
	}
	for _, item := range index.Runs {
		if item.RunID == runID {
			return item, nil
		}
	}
	return verifyRunSummary{}, os.ErrNotExist
}

func filterVerifyRunsByTenant(r *http.Request, runs []verifyRunSummary) []verifyRunSummary {
	authCtx, err := security.ContextFromHeaders(r.Header)
	if err != nil || authCtx.HasRole("platform_admin") {
		return runs
	}
	tenantID := normalizeTenant(authCtx.TenantID)
	filtered := make([]verifyRunSummary, 0, len(runs))
	for _, run := range runs {
		if normalizeTenant(run.TenantID) == tenantID {
			filtered = append(filtered, run)
		}
	}
	return filtered
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode json %s: %w", path, err)
	}
	return payload, nil
}

func compareRuntimeReconcileRuns(from runtimeReconcileRecord, to runtimeReconcileRecord) runtimeReconcileCompare {
	confirmedAdded, confirmedRemoved := diffStringSets(from.Result.Confirmed, to.Result.Confirmed)
	contradictedAdded, contradictedRemoved := diffStringSets(from.Result.Contradicted, to.Result.Contradicted)
	unmappedAdded, unmappedRemoved := diffStringSets(from.Result.Unmapped, to.Result.Unmapped)
	needsReviewAdded, needsReviewRemoved := diffStringSets(from.Result.NeedsReview, to.Result.NeedsReview)
	return runtimeReconcileCompare{
		FromReconcileID:     from.ReconcileID,
		ToReconcileID:       to.ReconcileID,
		FromGraphID:         from.Result.GraphID,
		ToGraphID:           to.Result.GraphID,
		ConfirmedAdded:      confirmedAdded,
		ConfirmedRemoved:    confirmedRemoved,
		ContradictedAdded:   contradictedAdded,
		ContradictedRemoved: contradictedRemoved,
		UnmappedAdded:       unmappedAdded,
		UnmappedRemoved:     unmappedRemoved,
		NeedsReviewAdded:    needsReviewAdded,
		NeedsReviewRemoved:  needsReviewRemoved,
	}
}

func diffStringSets(from []string, to []string) ([]string, []string) {
	fromSet := map[string]struct{}{}
	toSet := map[string]struct{}{}
	for _, v := range from {
		v = strings.TrimSpace(v)
		if v != "" {
			fromSet[v] = struct{}{}
		}
	}
	for _, v := range to {
		v = strings.TrimSpace(v)
		if v != "" {
			toSet[v] = struct{}{}
		}
	}
	added := make([]string, 0)
	removed := make([]string, 0)
	for v := range toSet {
		if _, ok := fromSet[v]; !ok {
			added = append(added, v)
		}
	}
	for v := range fromSet {
		if _, ok := toSet[v]; !ok {
			removed = append(removed, v)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func deleteRuntimeReconcileResult(graphRoot string, reconcileID string) error {
	resultPath := filepath.Join(graphRoot, "runtime", "reconcile", reconcileID, "result.json")
	if _, err := os.Stat(resultPath); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Dir(resultPath)); err != nil {
		return fmt.Errorf("remove runtime reconcile dir: %w", err)
	}
	indexPath := filepath.Join(graphRoot, "runtime", "reconcile", "index.json")
	idx := runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(data, &idx)
	}
	filtered := make([]runtimeReconcileSummary, 0, len(idx.Runs))
	for _, item := range idx.Runs {
		if item.ReconcileID != reconcileID {
			filtered = append(filtered, item)
		}
	}
	idx.Runs = filtered
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime reconcile index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write runtime reconcile index: %w", err)
	}
	return nil
}

func pruneRuntimeReconcileHistory(graphRoot string, keepLatest int, tenantScope string) (int, error) {
	indexPath := filepath.Join(graphRoot, "runtime", "reconcile", "index.json")
	idx := runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		if err := json.Unmarshal(data, &idx); err != nil {
			return 0, fmt.Errorf("decode runtime reconcile index: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	tenantScope = strings.TrimSpace(tenantScope)
	tenantScopeNorm := ""
	if tenantScope != "" {
		tenantScopeNorm = normalizeTenant(tenantScope)
	}
	var target []runtimeReconcileSummary
	var passthrough []runtimeReconcileSummary
	if tenantScopeNorm == "" {
		target = idx.Runs
	} else {
		target = make([]runtimeReconcileSummary, 0, len(idx.Runs))
		passthrough = make([]runtimeReconcileSummary, 0, len(idx.Runs))
		for _, item := range idx.Runs {
			if normalizeTenant(item.TenantID) == tenantScopeNorm {
				target = append(target, item)
			} else {
				passthrough = append(passthrough, item)
			}
		}
	}
	if keepLatest >= len(target) {
		return 0, nil
	}
	toDelete := target[keepLatest:]
	deleted := 0
	for _, item := range toDelete {
		if item.Path == "" {
			continue
		}
		if err := os.RemoveAll(filepath.Dir(item.Path)); err == nil {
			deleted++
		}
	}
	if tenantScopeNorm == "" {
		idx.Runs = target[:keepLatest]
	} else {
		idx.Runs = append(passthrough, target[:keepLatest]...)
		sort.Slice(idx.Runs, func(i, j int) bool {
			return idx.Runs[i].GeneratedAt.After(idx.Runs[j].GeneratedAt)
		})
	}
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return deleted, fmt.Errorf("marshal runtime reconcile index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return deleted, fmt.Errorf("create runtime reconcile index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return deleted, fmt.Errorf("write runtime reconcile index: %w", err)
	}
	return deleted, nil
}

func handleAuditList(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot)
		if !ok {
			return
		}
		limit := 200
		if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "invalid limit")
				return
			}
			limit = n
		}
		tenant := normalizeTenant(authCtx.TenantID)
		if authCtx.HasRole("platform_admin") {
			tenant = ""
		}
		events, err := audit.ListEvents(filepath.Dir(graphRoot), tenant, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"count":  len(events),
			"events": events,
		})
	}
}

func handleAuditExport(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionAuditExport, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		var req struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Encrypt  bool   `json:"encrypt"`
			TenantID string `json:"tenant_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		var fromPtr *time.Time
		if strings.TrimSpace(req.From) != "" {
			ts, err := time.Parse(time.RFC3339, strings.TrimSpace(req.From))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid from timestamp")
				return
			}
			u := ts.UTC()
			fromPtr = &u
		}
		var toPtr *time.Time
		if strings.TrimSpace(req.To) != "" {
			ts, err := time.Parse(time.RFC3339, strings.TrimSpace(req.To))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid to timestamp")
				return
			}
			u := ts.UTC()
			toPtr = &u
		}
		if fromPtr != nil && toPtr != nil && fromPtr.After(*toPtr) {
			writeError(w, http.StatusBadRequest, "from timestamp must be <= to timestamp")
			return
		}
		requestedTenant := normalizeTenant(req.TenantID)
		resourceTenant := normalizeTenant(authCtx.TenantID)
		if requestedTenant != "" {
			if authCtx.HasRole("platform_admin") {
				resourceTenant = requestedTenant
			} else if requestedTenant != resourceTenant {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
		} else if authCtx.HasRole("platform_admin") {
			resourceTenant = ""
		}
		keyB64 := strings.TrimSpace(os.Getenv("DIFFMIND_AUDIT_EXPORT_KEY_B64"))
		keyID := strings.TrimSpace(os.Getenv("DIFFMIND_KMS_KEY_ID"))
		if req.Encrypt {
			if keyB64 == "" {
				writeError(w, http.StatusBadRequest, "DIFFMIND_AUDIT_EXPORT_KEY_B64 is required when encrypt=true")
				return
			}
			keyRaw, err := base64.StdEncoding.DecodeString(keyB64)
			if err != nil || len(keyRaw) != 32 {
				writeError(w, http.StatusBadRequest, "DIFFMIND_AUDIT_EXPORT_KEY_B64 must be base64-encoded 32-byte key when encrypt=true")
				return
			}
			if keyID == "" {
				writeError(w, http.StatusBadRequest, "DIFFMIND_KMS_KEY_ID is required when encrypt=true")
				return
			}
		}
		result, err := audit.ExportEvents(filepath.Dir(graphRoot), audit.ExportRequest{
			From:     fromPtr,
			To:       toPtr,
			TenantID: resourceTenant,
			Encrypt:  req.Encrypt,
			KeyB64:   keyB64,
			KeyID:    keyID,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionAuditExport, "allow", "audit_exported", map[string]any{
			"path":      result.Path,
			"encrypted": result.Encrypted,
			"count":     result.Count,
		})
		writeJSON(w, http.StatusOK, result)
	}
}

func handleAuditExportVerify(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot)
		if !ok {
			return
		}
		var req struct {
			ManifestPath string `json:"manifest_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		manifestPath := strings.TrimSpace(req.ManifestPath)
		if manifestPath == "" {
			writeError(w, http.StatusBadRequest, "manifest_path is required")
			return
		}
		result, err := audit.VerifyExportManifest(manifestPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "manifest not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Non-platform users can only verify manifests for their tenant scope.
		if !authCtx.HasRole("platform_admin") {
			manifestData, mErr := os.ReadFile(manifestPath)
			if mErr != nil {
				writeError(w, http.StatusInternalServerError, mErr.Error())
				return
			}
			var manifest audit.ExportManifest
			if umErr := json.Unmarshal(manifestData, &manifest); umErr != nil {
				writeError(w, http.StatusInternalServerError, umErr.Error())
				return
			}
			if normalizeTenant(manifest.TenantID) != normalizeTenant(authCtx.TenantID) {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleAuditEvidenceBundle(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			authCtx, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot)
			if !ok {
				return
			}
			auditRoot := filepath.Dir(graphRoot)
			tenantScope, err := resolveEvidenceTenantScope(r, authCtx)
			if err != nil {
				if strings.Contains(err.Error(), "forbidden") {
					writeError(w, http.StatusForbidden, strings.TrimPrefix(err.Error(), "forbidden: "))
					return
				}
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			path := strings.TrimSpace(r.URL.Query().Get("path"))
			if path != "" {
				envelope, bundle, checksumValid, err := readEvidenceBundle(path)
				if err != nil {
					if os.IsNotExist(err) {
						writeError(w, http.StatusNotFound, "evidence bundle not found")
						return
					}
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				bundleTenant := normalizeTenant(fmt.Sprint(bundle["tenant_scope"]))
				if tenantScope != "" && bundleTenant != tenantScope {
					writeError(w, http.StatusForbidden, "tenant_mismatch")
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"path":            path,
					"generated_at":    envelope.GeneratedAt,
					"tenant_scope":    envelope.TenantScope,
					"checksum_sha256": envelope.ChecksumSHA256,
					"checksum_valid":  checksumValid,
					"bundle":          bundle,
				})
				return
			}
			limit := 20
			if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
				n, err := strconv.Atoi(q)
				if err != nil || n <= 0 {
					writeError(w, http.StatusBadRequest, "invalid limit")
					return
				}
				limit = n
			}
			list, err := listEvidenceBundles(auditRoot, tenantScope, limit)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"count":        len(list),
				"bundles":      list,
				"tenant_scope": tenantScope,
			})
			return
		case http.MethodPost:
			// continue below
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		authCtx, ok := authorizeRequest(w, r, security.ActionAuditExport, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		var req struct {
			TenantID   string `json:"tenant_id"`
			AllTenants bool   `json:"all_tenants"`
			RetainDays int    `json:"retain_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		tenantScope, err := resolveEvidenceTenantScopeValues(normalizeTenant(authCtx.TenantID), authCtx.HasRole("platform_admin"), normalizeTenant(req.TenantID), req.AllTenants)
		if err != nil {
			writeError(w, http.StatusForbidden, strings.TrimPrefix(err.Error(), "forbidden: "))
			return
		}
		if req.RetainDays <= 0 {
			req.RetainDays = 30
		}

		auditRoot := filepath.Dir(graphRoot)
		payload, err := buildEvidenceBundlePayload(auditRoot, tenantScope, req.RetainDays)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		outDir := filepath.Join(auditRoot, "audit", "evidence")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		outPath := filepath.Join(outDir, fmt.Sprintf("security-evidence-bundle-%d.json", time.Now().UTC().UnixNano()))
		checksum, err := computeBundleChecksum(payload)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		envelope := evidenceBundleEnvelope{
			GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
			TenantScope:    tenantScope,
			ChecksumSHA256: checksum,
			Bundle:         payload,
		}
		envelopeData, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		envelopeData = append(envelopeData, '\n')
		if err := os.WriteFile(outPath, envelopeData, 0o644); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":            outPath,
			"checksum_sha256": envelope.ChecksumSHA256,
			"bundle":          payload,
		})
	}
}

func handleAuditIntegrity(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot)
		if !ok {
			return
		}
		requestedTenant := normalizeTenant(r.URL.Query().Get("tenant_id"))
		tenantScope := normalizeTenant(authCtx.TenantID)
		if authCtx.HasRole("platform_admin") {
			if parseBoolDefault(r.URL.Query().Get("all_tenants"), false) {
				tenantScope = ""
			} else if requestedTenant != "" {
				tenantScope = requestedTenant
			}
		} else {
			if requestedTenant != "" && requestedTenant != tenantScope {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
			if parseBoolDefault(r.URL.Query().Get("all_tenants"), false) {
				writeError(w, http.StatusForbidden, "all_tenants requires platform_admin")
				return
			}
		}
		result, err := audit.VerifyEvents(filepath.Dir(graphRoot), audit.VerifyRequest{
			TenantID:     tenantScope,
			EnforceChain: tenantScope == "",
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleAuditRetention(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionAuditPrune, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		var req struct {
			RetainDays int    `json:"retain_days"`
			TenantID   string `json:"tenant_id"`
			AllTenants bool   `json:"all_tenants"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		if req.RetainDays <= 0 {
			writeError(w, http.StatusBadRequest, "retain_days must be > 0")
			return
		}
		tenantScope := normalizeTenant(authCtx.TenantID)
		requestedTenant := normalizeTenant(req.TenantID)
		if authCtx.HasRole("platform_admin") {
			if req.AllTenants {
				tenantScope = ""
			} else if requestedTenant != "" {
				tenantScope = requestedTenant
			}
		} else {
			if requestedTenant != "" && requestedTenant != tenantScope {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
			if req.AllTenants {
				writeError(w, http.StatusForbidden, "all_tenants requires platform_admin")
				return
			}
		}
		cutoff := time.Now().UTC().Add(-time.Duration(req.RetainDays) * 24 * time.Hour)
		deleted, err := audit.PruneEvents(filepath.Dir(graphRoot), tenantScope, cutoff)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionAuditPrune, "allow", "audit_pruned", map[string]any{
			"deleted":      deleted,
			"retain_days":  req.RetainDays,
			"tenant_scope": tenantScope,
			"all_tenants":  req.AllTenants,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"deleted":      deleted,
			"retain_days":  req.RetainDays,
			"tenant_scope": tenantScope,
			"all_tenants":  req.AllTenants,
		})
	}
}

func latestExportManifestPath(auditRoot string) (string, bool) {
	exportDir := filepath.Join(auditRoot, "audit", "exports")
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		return "", false
	}
	best := ""
	bestName := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".manifest.json") {
			continue
		}
		if best == "" || name > bestName {
			best = filepath.Join(exportDir, name)
			bestName = name
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

func formatOptionalTime(ts *time.Time) any {
	if ts == nil {
		return nil
	}
	return ts.UTC().Format(time.RFC3339)
}

type evidenceBundleEnvelope struct {
	GeneratedAt    string         `json:"generated_at"`
	TenantScope    string         `json:"tenant_scope"`
	ChecksumSHA256 string         `json:"checksum_sha256"`
	Bundle         map[string]any `json:"bundle"`
}

func resolveEvidenceTenantScope(r *http.Request, authCtx security.Context) (string, error) {
	requestedTenant := normalizeTenant(r.URL.Query().Get("tenant_id"))
	allTenants := parseBoolDefault(r.URL.Query().Get("all_tenants"), false)
	return resolveEvidenceTenantScopeValues(normalizeTenant(authCtx.TenantID), authCtx.HasRole("platform_admin"), requestedTenant, allTenants)
}

func resolveEvidenceTenantScopeValues(authTenant string, isPlatformAdmin bool, requestedTenant string, allTenants bool) (string, error) {
	tenantScope := authTenant
	if isPlatformAdmin {
		if allTenants {
			return "", nil
		}
		if requestedTenant != "" {
			return requestedTenant, nil
		}
		return tenantScope, nil
	}
	if allTenants {
		return "", errors.New("forbidden: all_tenants requires platform_admin")
	}
	if requestedTenant != "" && requestedTenant != tenantScope {
		return "", errors.New("forbidden: tenant_mismatch")
	}
	return tenantScope, nil
}

func buildEvidenceBundlePayload(auditRoot string, tenantScope string, retainDays int) (map[string]any, error) {
	integrity, err := audit.VerifyEvents(auditRoot, audit.VerifyRequest{
		TenantID:     tenantScope,
		EnforceChain: tenantScope == "",
	})
	if err != nil {
		return nil, err
	}
	events, err := audit.ListEvents(auditRoot, tenantScope, 100000)
	if err != nil {
		return nil, err
	}
	var oldest *time.Time
	var newest *time.Time
	retentionCutoff := time.Now().UTC().Add(-time.Duration(retainDays) * 24 * time.Hour)
	retentionDeletePreview := 0
	for _, e := range events {
		ts := e.Timestamp.UTC()
		if oldest == nil || ts.Before(*oldest) {
			copyTs := ts
			oldest = &copyTs
		}
		if newest == nil || ts.After(*newest) {
			copyTs := ts
			newest = &copyTs
		}
		if ts.Before(retentionCutoff) {
			retentionDeletePreview++
		}
	}

	latestManifestPath := ""
	latestManifestGeneratedAt := ""
	exportVerification := map[string]any{}
	if latest, ok := latestExportManifestPath(auditRoot); ok {
		verifyRes, vErr := audit.VerifyExportManifest(latest)
		if vErr == nil {
			exportVerification = map[string]any{
				"valid":           verifyRes.Valid,
				"signed":          verifyRes.Signed,
				"signature_valid": verifyRes.SignatureValid,
				"issues":          verifyRes.Issues,
			}
			manifestData, mErr := os.ReadFile(latest)
			if mErr == nil {
				var m audit.ExportManifest
				if umErr := json.Unmarshal(manifestData, &m); umErr == nil {
					manifestTenant := normalizeTenant(m.TenantID)
					if tenantScope == "" || manifestTenant == tenantScope {
						latestManifestPath = latest
						latestManifestGeneratedAt = m.GeneratedAt.UTC().Format(time.RFC3339)
					}
				}
			}
		}
	}

	return map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"tenant_scope": tenantScope,
		"integrity":    integrity,
		"events": map[string]any{
			"count":  len(events),
			"oldest": formatOptionalTime(oldest),
			"newest": formatOptionalTime(newest),
			"retention_preview": map[string]any{
				"retain_days":              retainDays,
				"cutoff":                   retentionCutoff.Format(time.RFC3339),
				"estimated_events_deleted": retentionDeletePreview,
			},
		},
		"latest_export": map[string]any{
			"manifest_path": latestManifestPath,
			"generated_at":  latestManifestGeneratedAt,
			"verification":  exportVerification,
		},
	}, nil
}

func readEvidenceBundle(path string) (evidenceBundleEnvelope, map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return evidenceBundleEnvelope{}, nil, false, err
	}
	var envelope evidenceBundleEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return evidenceBundleEnvelope{}, nil, false, fmt.Errorf("decode evidence bundle: %w", err)
	}
	if envelope.Bundle == nil {
		return evidenceBundleEnvelope{}, nil, false, errors.New("invalid evidence bundle: missing bundle")
	}
	checksum, err := computeBundleChecksum(envelope.Bundle)
	if err != nil {
		return evidenceBundleEnvelope{}, nil, false, err
	}
	checksumValid := strings.EqualFold(envelope.ChecksumSHA256, checksum)
	return envelope, envelope.Bundle, checksumValid, nil
}

func listEvidenceBundles(auditRoot string, tenantScope string, limit int) ([]map[string]any, error) {
	evidenceDir := filepath.Join(auditRoot, "audit", "evidence")
	entries, err := os.ReadDir(evidenceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(evidenceDir, entry.Name())
		envelope, bundle, checksumValid, err := readEvidenceBundle(path)
		if err != nil {
			continue
		}
		bundleTenant := normalizeTenant(fmt.Sprint(bundle["tenant_scope"]))
		if tenantScope != "" && bundleTenant != tenantScope {
			continue
		}
		items = append(items, map[string]any{
			"path":            path,
			"generated_at":    envelope.GeneratedAt,
			"tenant_scope":    envelope.TenantScope,
			"checksum_sha256": envelope.ChecksumSHA256,
			"checksum_valid":  checksumValid,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["path"]) > fmt.Sprint(items[j]["path"])
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func computeBundleChecksum(bundle map[string]any) (string, error) {
	raw, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	normalized := map[string]any{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func defaultTemplateCatalogPath() string {
	return filepath.Join("docs", "m15_query_templates.json")
}

func defaultGraphQueryTemplateCatalogPath() string {
	return filepath.Join("docs", "m11_query_templates.json")
}

func defaultQuestionCatalogPath() string {
	return filepath.Join("docs", "m17_question_catalog.json")
}

func resolveReadablePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	if filepath.IsAbs(path) {
		return path
	}
	candidates := []string{
		path,
		filepath.Join("..", path),
		filepath.Join("..", "..", path),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return path
}

func loadProductTemplates(path string) (productTemplateFile, error) {
	path = resolveReadablePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return productTemplateFile{}, err
	}
	var payload productTemplateFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return productTemplateFile{}, fmt.Errorf("decode product templates: %w", err)
	}
	return payload, nil
}

func loadProductQuestions(path string) (productQuestionFile, error) {
	path = resolveReadablePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return productQuestionFile{}, err
	}
	var payload productQuestionFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return productQuestionFile{}, fmt.Errorf("decode product question catalog: %w", err)
	}
	return payload, nil
}

func loadQueryTemplates(path string) (queryTemplateFile, error) {
	path = resolveReadablePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return queryTemplateFile{}, err
	}
	var payload queryTemplateFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return queryTemplateFile{}, fmt.Errorf("decode query templates: %w", err)
	}
	return payload, nil
}

func findQueryTemplateByID(catalog queryTemplateFile, templateID string) *queryTemplate {
	for i := range catalog.Templates {
		if strings.TrimSpace(catalog.Templates[i].ID) == templateID {
			return &catalog.Templates[i]
		}
	}
	return nil
}

func findTemplateByID(catalog productTemplateFile, templateID string) *productTemplate {
	for i := range catalog.Templates {
		if strings.TrimSpace(catalog.Templates[i].ID) == templateID {
			return &catalog.Templates[i]
		}
	}
	return nil
}

func findTemplateByEndpoint(catalog productTemplateFile, endpoint string) *productTemplate {
	needle := normalizeTemplatePath(strings.TrimSpace(endpoint))
	for i := range catalog.Templates {
		path := normalizeTemplatePath(strings.TrimSpace(catalog.Templates[i].Path))
		if path == "" {
			continue
		}
		if matchCatalogPath(path, needle) {
			return &catalog.Templates[i]
		}
	}
	return nil
}

func productContractForPath(path string) (productPathContract, bool) {
	path = strings.TrimSpace(path)
	switch {
	case strings.HasPrefix(path, "/products/pr-review"):
		return productPathContract{Product: "pr-review", Method: http.MethodPost}, true
	case strings.HasPrefix(path, "/products/docs/"):
		return productPathContract{Product: "docs", Method: http.MethodGet}, true
	case strings.HasPrefix(path, "/products/runtime/"):
		return productPathContract{Product: "runtime", Method: http.MethodGet}, true
	case strings.HasPrefix(path, "/products/topology/"):
		return productPathContract{Product: "topology", Method: http.MethodGet}, true
	case strings.HasPrefix(path, "/products/company/"):
		return productPathContract{Product: "company", Method: http.MethodGet}, true
	case strings.HasPrefix(path, "/products/trust/"):
		return productPathContract{Product: "trust", Method: http.MethodGet}, true
	case strings.HasPrefix(path, "/products/architecture/"):
		return productPathContract{Product: "architecture", Method: http.MethodGet}, true
	case strings.HasPrefix(path, "/products/mapper/"):
		return productPathContract{Product: "mapper", Method: http.MethodGet}, true
	case strings.HasPrefix(path, "/products/governance/"):
		return productPathContract{Product: "governance", Method: http.MethodGet}, true
	default:
		return productPathContract{}, false
	}
}

func productHandlerForPath(graphRoot string, path string) (http.Handler, bool) {
	switch {
	case strings.HasPrefix(path, "/products/pr-review"):
		return handleProductPRReview(graphRoot), true
	case strings.HasPrefix(path, "/products/docs/"):
		return handleProductDocs(graphRoot), true
	case strings.HasPrefix(path, "/products/runtime/"):
		return handleProductRuntime(graphRoot), true
	case strings.HasPrefix(path, "/products/topology/"):
		return handleProductTopology(graphRoot), true
	case strings.HasPrefix(path, "/products/company/"):
		return handleProductCompany(graphRoot), true
	case strings.HasPrefix(path, "/products/trust/"):
		return handleProductTrust(graphRoot), true
	case strings.HasPrefix(path, "/products/architecture/"):
		return handleProductArchitecture(graphRoot), true
	case strings.HasPrefix(path, "/products/mapper/"):
		return handleProductMapper(graphRoot), true
	case strings.HasPrefix(path, "/products/governance/"):
		return handleProductGovernance(graphRoot), true
	default:
		return nil, false
	}
}

func executeProductTemplate(graphRoot string, authHeaders http.Header, templateID string, templatePath string, vars map[string]any, dryRun bool) (int, map[string]any) {
	resolvedTemplatePath := resolveReadablePath(templatePath)
	catalog, err := loadProductTemplates(resolvedTemplatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return http.StatusNotFound, map[string]any{"error": "product templates not found"}
		}
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	tmpl := findTemplateByID(catalog, templateID)
	if tmpl == nil {
		return http.StatusNotFound, map[string]any{"error": "template not found"}
	}
	missingVars := missingTemplateVars(vars, templateRequiredVars(*tmpl))
	if len(missingVars) > 0 {
		return http.StatusBadRequest, map[string]any{
			"error":        "missing template vars",
			"missing_vars": missingVars,
		}
	}
	method := strings.ToUpper(strings.TrimSpace(tmpl.Method))
	if method == "" {
		method = http.MethodGet
	}
	targetPath := interpolateTemplateString(strings.TrimSpace(tmpl.Path), vars)
	if targetPath == "" {
		return http.StatusBadRequest, map[string]any{"error": "template path resolves to empty"}
	}
	if !strings.HasPrefix(targetPath, "/products/") || strings.HasPrefix(targetPath, "/products/templates") || strings.HasPrefix(targetPath, "/products/questions") {
		return http.StatusBadRequest, map[string]any{"error": "template path is not executable"}
	}
	contract, mapped := productContractForPath(targetPath)
	if !mapped {
		return http.StatusBadRequest, map[string]any{"error": "template path is not mapped to a product handler"}
	}
	if method != contract.Method {
		return http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("template method must be %s for path", contract.Method)}
	}
	queryVals := url.Values{}
	for k, v := range tmpl.Query {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		value := interpolateTemplateAny(v, vars)
		if value == nil {
			continue
		}
		queryVals.Set(key, fmt.Sprint(value))
	}
	resolvedPath := targetPath
	if encoded := queryVals.Encode(); encoded != "" {
		sep := "?"
		if strings.Contains(resolvedPath, "?") {
			sep = "&"
		}
		resolvedPath += sep + encoded
	}
	bodyPayload := interpolateTemplateAny(tmpl.Payload, vars)
	var bodyReader io.Reader
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		if bodyPayload == nil {
			bodyPayload = map[string]any{}
		}
		b, err := json.Marshal(bodyPayload)
		if err != nil {
			return http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("encode resolved payload: %v", err)}
		}
		bodyReader = bytes.NewReader(b)
	}
	resp := map[string]any{
		"template_id":   templateID,
		"template_path": resolvedTemplatePath,
		"method":        method,
		"path":          targetPath,
		"query":         queryVals,
	}
	if bodyPayload != nil {
		resp["payload"] = bodyPayload
	}
	if dryRun {
		resp["dry_run"] = true
		resp["resolved_path"] = resolvedPath
		resp["status"] = http.StatusOK
		return http.StatusOK, resp
	}
	execReq := httptest.NewRequest(method, resolvedPath, bodyReader)
	for k, values := range authHeaders {
		if len(values) == 0 {
			continue
		}
		execReq.Header[k] = append([]string(nil), values...)
	}
	if bodyReader != nil {
		execReq.Header.Set("Content-Type", "application/json")
	}
	h, ok := productHandlerForPath(graphRoot, targetPath)
	if !ok {
		return http.StatusBadRequest, map[string]any{"error": "template path is not mapped to a product handler"}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, execReq)
	var result any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		result = map[string]any{"raw": rec.Body.String()}
	}
	resp["status"] = rec.Code
	resp["result"] = result
	if rec.Code >= 400 {
		resp["error"] = result
	}
	return rec.Code, resp
}

func executeGraphQueryRequest(graphRoot string, authHeaders http.Header, req queryExecuteRequest) (int, map[string]any) {
	graphID := strings.TrimSpace(req.GraphID)
	if graphID == "" {
		return http.StatusBadRequest, map[string]any{"error": "graph_id is required"}
	}
	graph, err := loadGraph(graphRoot, graphID)
	if err != nil {
		if os.IsNotExist(err) {
			return http.StatusNotFound, map[string]any{"error": "graph not found"}
		}
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	authCtx, err := security.ContextFromHeaders(authHeaders)
	if err != nil {
		return http.StatusUnauthorized, map[string]any{"error": err.Error()}
	}
	decision := security.Authorize(authCtx, security.Request{
		Action:         security.ActionQueryGraph,
		ResourceTenant: graphTenant(graph),
		Method:         http.MethodPost,
		Path:           "/query/execute",
		Sensitive:      req.IncludeSensitive,
		Mutating:       false,
	})
	if !decision.Allow {
		status := http.StatusForbidden
		if decision.Reason == "missing_auth_context" {
			status = http.StatusUnauthorized
		}
		return status, map[string]any{"error": decision.Reason}
	}
	filters := graphFilters{
		IncludeInferred: req.IncludeInferred,
		EdgeTypeFilter:  strings.TrimSpace(req.EdgeType),
		TypeFilter:      strings.TrimSpace(req.Type),
		ServiceFilter:   strings.TrimSpace(req.ServiceID),
		RepoFilter:      strings.TrimSpace(req.RepoPath),
		NodeFilter:      strings.TrimSpace(req.NodeID),
		SectionFilter:   strings.TrimSpace(req.Section),
		ClassFilter:     strings.TrimSpace(req.Class),
		ConfidenceMin:   req.ConfidenceMin,
		Verification:    strings.TrimSpace(req.Verification),
		AdapterID:       strings.TrimSpace(req.AdapterID),
		ProvVersion:     strings.TrimSpace(req.ProvVersion),
		ConflictStatus:  strings.TrimSpace(req.ConflictStatus),
		Environment:     strings.TrimSpace(req.Environment),
		QueryText:       strings.ToLower(strings.TrimSpace(req.QueryText)),
	}
	if hasGraphFilter(filters) {
		graph = filterGraph(graph, filters)
	}
	graph = applyStrictPublishPolicy(graph, publishPolicy{IncludeDisputed: req.IncludeDisputed})
	graph = security.RedactGraph(graph, authCtx, req.IncludeSensitive)
	graph = annotateGraphFreshness(graph, time.Now().UTC(), req.MaxAgeHours)
	graph = applyQueryPagination(graph, queryPagination{NodeLimit: req.NodeLimit, EdgeLimit: req.EdgeLimit})
	if req.Explain {
		return http.StatusOK, map[string]any{
			"graph":   graph,
			"explain": buildGraphExplain(graph),
		}
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	return http.StatusOK, out
}

func executeQueryTemplate(graphRoot string, authHeaders http.Header, templateID string, templatePath string, vars map[string]any, dryRun bool) (int, map[string]any) {
	resolvedTemplatePath := resolveReadablePath(templatePath)
	catalog, err := loadQueryTemplates(resolvedTemplatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return http.StatusNotFound, map[string]any{"error": "query templates not found"}
		}
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	tmpl := findQueryTemplateByID(catalog, templateID)
	if tmpl == nil {
		return http.StatusNotFound, map[string]any{"error": "query template not found"}
	}
	method := strings.ToUpper(strings.TrimSpace(tmpl.Method))
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost {
		return http.StatusBadRequest, map[string]any{"error": "query template method must be POST"}
	}
	if strings.TrimSpace(tmpl.Path) != "/query/execute" {
		return http.StatusBadRequest, map[string]any{"error": "query template path must target /query/execute"}
	}
	missingVars := missingTemplateVars(vars, queryTemplateRequiredVars(*tmpl))
	if len(missingVars) > 0 {
		return http.StatusBadRequest, map[string]any{
			"error":        "missing template vars",
			"missing_vars": missingVars,
		}
	}
	payloadAny := interpolateTemplateAny(tmpl.Payload, vars)
	if payloadAny == nil {
		payloadAny = map[string]any{}
	}
	queryReqRaw, err := json.Marshal(payloadAny)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("encode query template payload: %v", err)}
	}
	queryReq := queryExecuteRequest{}
	if err := json.Unmarshal(queryReqRaw, &queryReq); err != nil {
		return http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("decode query template payload: %v", err)}
	}
	resp := map[string]any{
		"template_id":   templateID,
		"template_path": resolvedTemplatePath,
		"method":        method,
		"path":          "/query/execute",
		"payload":       payloadAny,
	}
	if dryRun {
		resp["dry_run"] = true
		resp["status"] = http.StatusOK
		return http.StatusOK, resp
	}
	status, result := executeGraphQueryRequest(graphRoot, authHeaders, queryReq)
	resp["status"] = status
	resp["result"] = result
	if status >= 400 {
		resp["error"] = result
	}
	return status, resp
}

func handleQueryTemplates(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		templatePath := strings.TrimSpace(r.URL.Query().Get("path"))
		if templatePath == "" {
			templatePath = defaultGraphQueryTemplateCatalogPath()
		}
		resolvedPath := resolveReadablePath(templatePath)
		payload, err := loadQueryTemplates(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "query templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":      resolvedPath,
			"count":     len(payload.Templates),
			"templates": payload.Templates,
		})
	}
}

func handleQueryTemplateValidate(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		templatePath := strings.TrimSpace(r.URL.Query().Get("path"))
		if templatePath == "" {
			templatePath = defaultGraphQueryTemplateCatalogPath()
		}
		resolvedPath := resolveReadablePath(templatePath)
		payload, err := loadQueryTemplates(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "query templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		errorsOut := make([]string, 0)
		seen := map[string]struct{}{}
		for i, tmpl := range payload.Templates {
			id := strings.TrimSpace(tmpl.ID)
			if id == "" {
				errorsOut = append(errorsOut, fmt.Sprintf("template[%d] has empty id", i))
			} else {
				if _, ok := seen[id]; ok {
					errorsOut = append(errorsOut, fmt.Sprintf("duplicate template id: %s", id))
				}
				seen[id] = struct{}{}
			}
			if strings.ToUpper(strings.TrimSpace(tmpl.Method)) != http.MethodPost {
				errorsOut = append(errorsOut, fmt.Sprintf("template %q method must be POST", id))
			}
			if strings.TrimSpace(tmpl.Path) != "/query/execute" {
				errorsOut = append(errorsOut, fmt.Sprintf("template %q path must be /query/execute", id))
			}
			requiredVars := queryTemplateRequiredVars(tmpl)
			sampleVars := map[string]any{}
			for _, name := range requiredVars {
				sampleVars[name] = "sample"
			}
			resolvedPayload := interpolateTemplateAny(tmpl.Payload, sampleVars)
			queryReqRaw, err := json.Marshal(resolvedPayload)
			if err != nil {
				errorsOut = append(errorsOut, fmt.Sprintf("template %q payload marshal failed: %v", id, err))
				continue
			}
			queryReq := queryExecuteRequest{}
			if err := json.Unmarshal(queryReqRaw, &queryReq); err != nil {
				errorsOut = append(errorsOut, fmt.Sprintf("template %q payload decode failed: %v", id, err))
				continue
			}
			if strings.TrimSpace(queryReq.GraphID) == "" {
				errorsOut = append(errorsOut, fmt.Sprintf("template %q payload must resolve non-empty graph_id", id))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":        resolvedPath,
			"count":       len(payload.Templates),
			"valid":       len(errorsOut) == 0,
			"error_count": len(errorsOut),
			"errors":      errorsOut,
		})
	}
}

func handleQueryExecute(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		req := queryExecuteRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		status, payload := executeGraphQueryRequest(graphRoot, r.Header, req)
		writeJSON(w, status, payload)
	}
}

func handleQueryTemplateExecute(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		req := queryTemplateExecuteRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		if strings.TrimSpace(req.TemplateID) == "" {
			writeError(w, http.StatusBadRequest, "template_id is required")
			return
		}
		templatePath := strings.TrimSpace(req.TemplatePath)
		if templatePath == "" {
			templatePath = defaultGraphQueryTemplateCatalogPath()
		}
		status, payload := executeQueryTemplate(graphRoot, r.Header, strings.TrimSpace(req.TemplateID), templatePath, req.Vars, req.DryRun)
		writeJSON(w, status, payload)
	}
}

func handleProductTemplates(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		templatePath := strings.TrimSpace(r.URL.Query().Get("path"))
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		resolvedPath := resolveReadablePath(templatePath)
		payload, err := loadProductTemplates(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":      resolvedPath,
			"count":     len(payload.Templates),
			"templates": payload.Templates,
		})
	}
}

func handleProductTemplateValidate(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		templatePath := strings.TrimSpace(r.URL.Query().Get("path"))
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		resolvedTemplatePath := resolveReadablePath(templatePath)
		catalogPath := strings.TrimSpace(r.URL.Query().Get("catalog_path"))
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		resolvedCatalogPath := resolveReadablePath(catalogPath)

		templates, err := loadProductTemplates(resolvedTemplatePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		questions, err := loadProductQuestions(resolvedCatalogPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		type validationIssue struct {
			Severity   string `json:"severity"`
			TemplateID string `json:"template_id,omitempty"`
			QuestionID string `json:"question_id,omitempty"`
			Field      string `json:"field"`
			Message    string `json:"message"`
		}
		issues := make([]validationIssue, 0)
		templateIDs := map[string]int{}
		questionMappedTemplateIDs := map[string]struct{}{}
		coveredQuestions := 0

		for idx, tmpl := range templates.Templates {
			tid := strings.TrimSpace(tmpl.ID)
			if tid == "" {
				issues = append(issues, validationIssue{
					Severity: "error",
					Field:    fmt.Sprintf("templates[%d].id", idx),
					Message:  "template id is required",
				})
			} else {
				templateIDs[tid]++
			}
			method := strings.ToUpper(strings.TrimSpace(tmpl.Method))
			if method == "" {
				method = http.MethodGet
			}
			switch method {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				issues = append(issues, validationIssue{
					Severity:   "error",
					TemplateID: tid,
					Field:      "method",
					Message:    fmt.Sprintf("unsupported method %q", method),
				})
			}

			path := strings.TrimSpace(tmpl.Path)
			if path == "" {
				issues = append(issues, validationIssue{
					Severity:   "error",
					TemplateID: tid,
					Field:      "path",
					Message:    "template path is required",
				})
			} else if !isExecutableProductPath(path) {
				issues = append(issues, validationIssue{
					Severity:   "error",
					TemplateID: tid,
					Field:      "path",
					Message:    "template path is not executable product endpoint",
				})
			} else if contract, ok := productContractForPath(path); !ok {
				issues = append(issues, validationIssue{
					Severity:   "error",
					TemplateID: tid,
					Field:      "path",
					Message:    "template path is not mapped to a product handler",
				})
			} else {
				if method != contract.Method {
					issues = append(issues, validationIssue{
						Severity:   "error",
						TemplateID: tid,
						Field:      "method",
						Message:    fmt.Sprintf("method must be %s for path", contract.Method),
					})
				}
				product := strings.TrimSpace(tmpl.Product)
				if product == "" {
					issues = append(issues, validationIssue{
						Severity:   "error",
						TemplateID: tid,
						Field:      "product",
						Message:    "template product is required",
					})
				} else if product != contract.Product {
					issues = append(issues, validationIssue{
						Severity:   "error",
						TemplateID: tid,
						Field:      "product",
						Message:    fmt.Sprintf("product must be %q for path", contract.Product),
					})
				}
			}
		}
		for tid, count := range templateIDs {
			if count > 1 {
				issues = append(issues, validationIssue{
					Severity:   "error",
					TemplateID: tid,
					Field:      "id",
					Message:    "duplicate template id",
				})
			}
		}

		for _, q := range questions.Questions {
			qid := strings.TrimSpace(q.ID)
			endpoint := strings.TrimSpace(q.Endpoint)
			if qid == "" {
				issues = append(issues, validationIssue{
					Severity: "error",
					Field:    "questions[].id",
					Message:  "question id is required",
				})
				continue
			}
			if endpoint == "" {
				issues = append(issues, validationIssue{
					Severity:   "error",
					QuestionID: qid,
					Field:      "endpoint",
					Message:    "question endpoint is required",
				})
				continue
			}
			mapped := findTemplateByEndpoint(templates, endpoint)
			if mapped == nil {
				issues = append(issues, validationIssue{
					Severity:   "error",
					QuestionID: qid,
					Field:      "endpoint",
					Message:    "no template mapped to question endpoint",
				})
				continue
			}
			missingVars := missingTemplateVarsForQuestionEndpoint(*mapped, endpoint)
			if len(missingVars) > 0 {
				issues = append(issues, validationIssue{
					Severity:   "error",
					QuestionID: qid,
					Field:      "endpoint",
					Message:    fmt.Sprintf("mapped template %q is missing vars required by question endpoint: %s", strings.TrimSpace(mapped.ID), strings.Join(missingVars, ", ")),
				})
				continue
			}
			coveredQuestions++
			questionMappedTemplateIDs[strings.TrimSpace(mapped.ID)] = struct{}{}
		}

		orphanTemplates := make([]string, 0)
		for _, tmpl := range templates.Templates {
			tid := strings.TrimSpace(tmpl.ID)
			if tid == "" {
				continue
			}
			if _, ok := questionMappedTemplateIDs[tid]; ok {
				continue
			}
			orphanTemplates = append(orphanTemplates, tid)
		}
		sort.Strings(orphanTemplates)
		for _, tid := range orphanTemplates {
			issues = append(issues, validationIssue{
				Severity:   "warn",
				TemplateID: tid,
				Field:      "mapping",
				Message:    "template is not mapped to any question endpoint",
			})
		}

		errorCount := 0
		warnCount := 0
		for _, issue := range issues {
			switch strings.ToLower(strings.TrimSpace(issue.Severity)) {
			case "error":
				errorCount++
			case "warn":
				warnCount++
			}
		}
		coverage := 1.0
		if len(questions.Questions) > 0 {
			coverage = float64(coveredQuestions) / float64(len(questions.Questions))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"template_path":          resolvedTemplatePath,
			"catalog_path":           resolvedCatalogPath,
			"templates_total":        len(templates.Templates),
			"questions_total":        len(questions.Questions),
			"questions_covered":      coveredQuestions,
			"coverage_ratio":         coverage,
			"orphan_templates":       orphanTemplates,
			"issues":                 issues,
			"error_count":            errorCount,
			"warn_count":             warnCount,
			"valid":                  errorCount == 0,
			"templates_with_orphans": len(orphanTemplates),
		})
	}
}

func isExecutableProductPath(path string) bool {
	path = strings.TrimSpace(path)
	return strings.HasPrefix(path, "/products/") &&
		!strings.HasPrefix(path, "/products/templates") &&
		!strings.HasPrefix(path, "/products/questions")
}

func handleProductTemplateExecute(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		req := productTemplateExecuteRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		req.TemplateID = strings.TrimSpace(req.TemplateID)
		if req.TemplateID == "" {
			writeError(w, http.StatusBadRequest, "template_id is required")
			return
		}
		templatePath := strings.TrimSpace(req.TemplatePath)
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		status, resp := executeProductTemplate(graphRoot, r.Header, req.TemplateID, templatePath, req.Vars, req.DryRun)
		writeJSON(w, status, resp)
	}
}

func handleProductQuestions(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		catalogPath := strings.TrimSpace(r.URL.Query().Get("catalog_path"))
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		resolvedPath := resolveReadablePath(catalogPath)
		payload, err := loadProductQuestions(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":      resolvedPath,
			"count":     len(payload.Questions),
			"questions": payload.Questions,
		})
	}
}

func handleProductQuestionExecute(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		req := productQuestionExecuteRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		req.QuestionID = strings.TrimSpace(req.QuestionID)
		if req.QuestionID == "" {
			writeError(w, http.StatusBadRequest, "question_id is required")
			return
		}
		catalogPath := strings.TrimSpace(req.CatalogPath)
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		resolvedCatalogPath := resolveReadablePath(catalogPath)
		questions, err := loadProductQuestions(resolvedCatalogPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var selectedEndpoint string
		for _, q := range questions.Questions {
			if strings.TrimSpace(q.ID) == req.QuestionID {
				selectedEndpoint = strings.TrimSpace(q.Endpoint)
				break
			}
		}
		if selectedEndpoint == "" {
			writeError(w, http.StatusNotFound, "question not found")
			return
		}
		templatePath := strings.TrimSpace(req.TemplatePath)
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		templates, err := loadProductTemplates(templatePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		template := findTemplateByEndpoint(templates, selectedEndpoint)
		if template == nil {
			writeError(w, http.StatusNotFound, "no template mapped to question endpoint")
			return
		}
		status, resp := executeProductTemplate(graphRoot, r.Header, strings.TrimSpace(template.ID), templatePath, req.Vars, false)
		resp["question_id"] = req.QuestionID
		resp["question_endpoint"] = selectedEndpoint
		resp["catalog_path"] = resolvedCatalogPath
		writeJSON(w, status, resp)
	}
}

func handleProductQuestionRun(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		req := productQuestionRunRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		catalogPath := strings.TrimSpace(req.CatalogPath)
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		templatePath := strings.TrimSpace(req.TemplatePath)
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		resolvedCatalogPath := resolveReadablePath(catalogPath)
		resolvedTemplatePath := resolveReadablePath(templatePath)

		questions, err := loadProductQuestions(resolvedCatalogPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		templates, err := loadProductTemplates(resolvedTemplatePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		selected := map[string]struct{}{}
		if len(req.QuestionIDs) > 0 {
			for _, id := range req.QuestionIDs {
				id = strings.TrimSpace(id)
				if id != "" {
					selected[id] = struct{}{}
				}
			}
		}

		results := make([]map[string]any, 0, len(questions.Questions))
		success := 0
		failed := 0
		for _, q := range questions.Questions {
			qid := strings.TrimSpace(q.ID)
			if len(selected) > 0 {
				if _, ok := selected[qid]; !ok {
					continue
				}
			}
			endpoint := strings.TrimSpace(q.Endpoint)
			mapped := findTemplateByEndpoint(templates, endpoint)
			item := map[string]any{
				"question_id":       qid,
				"question":          strings.TrimSpace(q.Question),
				"question_endpoint": endpoint,
			}
			if mapped == nil {
				failed++
				item["status"] = http.StatusNotFound
				item["error"] = "no template mapped to question endpoint"
				results = append(results, item)
				continue
			}
			item["template_id"] = strings.TrimSpace(mapped.ID)
			vars := map[string]any{}
			for k, v := range req.Vars {
				vars[k] = v
			}
			if qVars, ok := req.VarsByQuestion[qid]; ok {
				for k, v := range qVars {
					vars[k] = v
				}
			}
			status, resp := executeProductTemplate(graphRoot, r.Header, strings.TrimSpace(mapped.ID), resolvedTemplatePath, vars, false)
			item["status"] = status
			item["response"] = resp
			if status >= 200 && status < 300 {
				success++
			} else {
				failed++
			}
			results = append(results, item)
		}
		total := len(results)
		writeJSON(w, http.StatusOK, map[string]any{
			"catalog_path":   resolvedCatalogPath,
			"template_path":  resolvedTemplatePath,
			"total":          total,
			"succeeded":      success,
			"failed":         failed,
			"overall_passed": failed == 0 && total > 0,
			"results":        results,
		})
	}
}

func handleProductQuestionCoverage(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		catalogPath := strings.TrimSpace(r.URL.Query().Get("catalog_path"))
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		templatePath := strings.TrimSpace(r.URL.Query().Get("template_path"))
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		resolvedCatalogPath := resolveReadablePath(catalogPath)
		resolvedTemplatePath := resolveReadablePath(templatePath)
		questions, err := loadProductQuestions(resolvedCatalogPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		templates, err := loadProductTemplates(resolvedTemplatePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		type item struct {
			QuestionID       string `json:"question_id"`
			Question         string `json:"question"`
			Endpoint         string `json:"endpoint"`
			Covered          bool   `json:"covered"`
			MappedTemplateID string `json:"mapped_template_id,omitempty"`
			ContractValid    bool   `json:"contract_valid,omitempty"`
			ContractError    string `json:"contract_error,omitempty"`
		}
		items := make([]item, 0, len(questions.Questions))
		coveredCount := 0
		contractValidCount := 0
		for _, q := range questions.Questions {
			endpoint := strings.TrimSpace(q.Endpoint)
			mapped := findTemplateByEndpoint(templates, endpoint)
			entry := item{
				QuestionID: strings.TrimSpace(q.ID),
				Question:   strings.TrimSpace(q.Question),
				Endpoint:   endpoint,
			}
			if mapped != nil {
				entry.Covered = true
				entry.MappedTemplateID = strings.TrimSpace(mapped.ID)
				coveredCount++
				missingVars := missingTemplateVarsForQuestionEndpoint(*mapped, endpoint)
				entry.ContractValid = len(missingVars) == 0
				if entry.ContractValid {
					contractValidCount++
				} else {
					entry.ContractError = fmt.Sprintf("mapped template is missing vars required by question endpoint: %s", strings.Join(missingVars, ", "))
				}
			}
			items = append(items, entry)
		}
		coverage := 1.0
		if len(items) > 0 {
			coverage = float64(coveredCount) / float64(len(items))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"catalog_path":           resolvedCatalogPath,
			"template_path":          resolvedTemplatePath,
			"total":                  len(items),
			"covered":                coveredCount,
			"coverage_ratio":         coverage,
			"contract_valid_covered": contractValidCount,
			"items":                  items,
		})
	}
}

func extractTemplatePlaceholderVars(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for i := 0; i < len(input); i++ {
		start := strings.Index(input[i:], "${")
		if start < 0 {
			break
		}
		start += i + 2
		endRel := strings.Index(input[start:], "}")
		if endRel < 0 {
			break
		}
		end := start + endRel
		name := strings.TrimSpace(input[start:end])
		if name != "" {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				out = append(out, name)
			}
		}
		i = end
	}
	sort.Strings(out)
	return out
}

func placeholderVarSetFromAny(value any, out map[string]struct{}) {
	switch v := value.(type) {
	case string:
		for _, name := range extractTemplatePlaceholderVars(v) {
			out[name] = struct{}{}
		}
	case []any:
		for _, item := range v {
			placeholderVarSetFromAny(item, out)
		}
	case map[string]any:
		for _, item := range v {
			placeholderVarSetFromAny(item, out)
		}
	}
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func templateRequiredVars(tmpl productTemplate) []string {
	set := map[string]struct{}{}
	placeholderVarSetFromAny(tmpl.Path, set)
	for _, v := range tmpl.Query {
		placeholderVarSetFromAny(v, set)
	}
	placeholderVarSetFromAny(tmpl.Payload, set)
	return sortedKeys(set)
}

func queryTemplateRequiredVars(tmpl queryTemplate) []string {
	set := map[string]struct{}{}
	placeholderVarSetFromAny(tmpl.Path, set)
	for _, v := range tmpl.Query {
		placeholderVarSetFromAny(v, set)
	}
	placeholderVarSetFromAny(tmpl.Payload, set)
	return sortedKeys(set)
}

func requiredVarsFromQuestionEndpoint(endpoint string) []string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil
	}
	set := map[string]struct{}{}
	normalized := strings.ReplaceAll(endpoint, "{", "${")
	for _, name := range extractTemplatePlaceholderVars(normalized) {
		set[name] = struct{}{}
	}
	return sortedKeys(set)
}

func missingTemplateVarsForQuestionEndpoint(tmpl productTemplate, endpoint string) []string {
	templateVars := map[string]struct{}{}
	for _, name := range templateRequiredVars(tmpl) {
		templateVars[name] = struct{}{}
	}
	missing := make([]string, 0)
	for _, name := range requiredVarsFromQuestionEndpoint(endpoint) {
		if _, ok := templateVars[name]; ok {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return missing
}

func missingTemplateVars(vars map[string]any, required []string) []string {
	if len(required) == 0 {
		return nil
	}
	missing := make([]string, 0)
	for _, name := range required {
		value, ok := vars[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if strings.TrimSpace(fmt.Sprint(value)) == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func interpolateTemplateString(input string, vars map[string]any) string {
	out := input
	for key, value := range vars {
		token := "${" + strings.TrimSpace(key) + "}"
		out = strings.ReplaceAll(out, token, fmt.Sprint(value))
	}
	return out
}

func interpolateTemplateAny(value any, vars map[string]any) any {
	switch v := value.(type) {
	case string:
		return interpolateTemplateString(v, vars)
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, interpolateTemplateAny(item, vars))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = interpolateTemplateAny(item, vars)
		}
		return out
	default:
		return value
	}
}

func normalizeTemplatePath(path string) string {
	if path == "" {
		return ""
	}
	out := strings.TrimSpace(path)
	if idx := strings.Index(out, "?"); idx >= 0 {
		out = out[:idx]
	}
	out = strings.ReplaceAll(out, "{graph_id}", "${graph_id}")
	out = strings.ReplaceAll(out, "{service_id}", "${service_id}")
	out = strings.ReplaceAll(out, "$${graph_id}", "${graph_id}")
	out = strings.ReplaceAll(out, "$${service_id}", "${service_id}")
	out = strings.TrimRight(out, "/")
	if out == "" {
		return "/"
	}
	return out
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

func handleProductPRReview(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req productPRReviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		req.GraphID = strings.TrimSpace(req.GraphID)
		if req.GraphID == "" {
			writeError(w, http.StatusBadRequest, "graph_id is required")
			return
		}
		if req.MaxFindings <= 0 {
			req.MaxFindings = 50
		}
		graph, err := loadGraph(graphRoot, req.GraphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), true, false, graphRoot)
		if !ok {
			return
		}
		includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
		graph = security.RedactGraph(graph, authCtx, includeSensitive)

		changedSet := map[string]struct{}{}
		for _, n := range req.ChangedNodes {
			n = strings.TrimSpace(n)
			if n != "" {
				changedSet[n] = struct{}{}
			}
		}
		findings := make([]productFinding, 0)
		for _, e := range graph.Edges {
			if len(changedSet) > 0 {
				_, src := changedSet[e.SourceID]
				_, dst := changedSet[e.TargetID]
				if !src && !dst {
					continue
				}
			}
			if status := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Attributes["verification_status"]))); status == "rejected" {
				findings = append(findings, productFinding{
					Severity: "high",
					Title:    "Rejected relation involved in change scope",
					EdgeID:   e.ID,
					Details: map[string]any{
						"type":      e.Type,
						"source_id": e.SourceID,
						"target_id": e.TargetID,
					},
				})
			}
			if e.Confidence < 0.70 {
				findings = append(findings, productFinding{
					Severity: "medium",
					Title:    "Low-confidence relation touched by PR",
					EdgeID:   e.ID,
					Details: map[string]any{
						"type":       e.Type,
						"confidence": e.Confidence,
					},
				})
			}
		}
		for _, n := range graph.Nodes {
			if len(changedSet) > 0 {
				if _, ok := changedSet[n.ID]; !ok {
					continue
				}
			}
			if strings.EqualFold(strings.TrimSpace(fmt.Sprint(n.Attributes["verification_status"])), "needs_review") {
				findings = append(findings, productFinding{
					Severity: "medium",
					Title:    "Changed entity requires verification review",
					NodeID:   n.ID,
					Details: map[string]any{
						"type":   n.Type,
						"label":  n.Label,
						"status": n.Attributes["verification_status"],
					},
				})
			}
		}
		sort.Slice(findings, func(i, j int) bool {
			if findings[i].Severity == findings[j].Severity {
				return findings[i].Title < findings[j].Title
			}
			return findings[i].Severity > findings[j].Severity
		})
		if len(findings) > req.MaxFindings {
			findings = findings[:req.MaxFindings]
		}

		resp := productPRReviewResponse{
			GraphID:  graph.GraphID,
			Findings: findings,
			Summary: map[string]int{
				"high":   countSeverity(findings, "high"),
				"medium": countSeverity(findings, "medium"),
				"low":    countSeverity(findings, "low"),
			},
			Generated: time.Now().UTC(),
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph_id":       graph.GraphID,
				"changed_nodes":  req.ChangedNodes,
				"findings":       findings,
			},
		})
	}
}

func handleProductDocs(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/docs/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
		graph = security.RedactGraph(graph, authCtx, includeSensitive)

		serviceFilter := strings.TrimSpace(r.URL.Query().Get("service"))
		if serviceFilter != "" {
			graph = filterGraph(graph, graphFilters{ServiceFilter: serviceFilter, IncludeInferred: true})
		}
		doc := map[string]any{
			"graph_id":   graph.GraphID,
			"service":    serviceFilter,
			"generated":  time.Now().UTC(),
			"overview":   buildDocsOverview(graph),
			"operations": buildDocsOperations(graph),
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, doc)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": doc,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph":          graph,
			},
		})
	}
}

func handleProductRuntime(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/runtime/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		serviceFilter := strings.TrimSpace(r.URL.Query().Get("service"))
		if serviceFilter != "" {
			graph = filterGraph(graph, graphFilters{ServiceFilter: serviceFilter, IncludeInferred: true})
		}
		report := buildRuntimeIntelligenceReport(graph)
		resp := map[string]any{
			"graph_id":   graph.GraphID,
			"service":    serviceFilter,
			"generated":  time.Now().UTC(),
			"runtime":    report,
			"operations": buildDocsOperations(graph),
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph":          graph,
				"focus":          "runtime_build_deploy_ci",
			},
		})
	}
}

func handleProductTopology(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/topology/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		serviceFilter := strings.TrimSpace(r.URL.Query().Get("service"))
		if serviceFilter != "" {
			graph = filterGraph(graph, graphFilters{ServiceFilter: serviceFilter, IncludeInferred: true})
		}
		report := buildTopologyReport(graph)
		resp := map[string]any{
			"graph_id":  graph.GraphID,
			"service":   serviceFilter,
			"generated": time.Now().UTC(),
			"topology":  report,
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph":          graph,
				"focus":          "internal_external_topology",
			},
		})
	}
}

func handleProductCompany(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/company/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		report := buildCompanyIdentityReport(graph)
		resp := map[string]any{
			"graph_id":  graph.GraphID,
			"generated": time.Now().UTC(),
			"company":   report,
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph":          graph,
				"focus":          "cross_repo_canonical_identity",
			},
		})
	}
}

func handleProductTrust(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/trust/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		report := buildTrustReport(graphRoot, graphID, graph)
		resp := map[string]any{
			"graph_id":  graph.GraphID,
			"generated": time.Now().UTC(),
			"trust":     report,
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph":          graph,
				"focus":          "confidence_conflict_adjudication",
			},
		})
	}
}

func handleProductArchitecture(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/architecture/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		serviceFilter := strings.TrimSpace(r.URL.Query().Get("service"))
		if serviceFilter != "" {
			graph = filterGraph(graph, graphFilters{ServiceFilter: serviceFilter, IncludeInferred: true})
		}
		focusNodeID := strings.TrimSpace(r.URL.Query().Get("focus_node_id"))
		report := buildArchitectureTaskReport(graph, focusNodeID)
		subgraph := buildFocusedSubgraph(graph, fmt.Sprint(report["focus_node_id"]))
		resp := map[string]any{
			"graph_id":           graph.GraphID,
			"service":            serviceFilter,
			"generated":          time.Now().UTC(),
			"architecture":       report,
			"focused_subgraph":   subgraph,
			"focused_node_id":    report["focus_node_id"],
			"focused_node_label": report["focus_node_label"],
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph":          graph,
				"focus":          "architecture_task_traces",
			},
		})
	}
}

func handleProductMapper(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/mapper/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		serviceID := strings.TrimSpace(r.URL.Query().Get("service"))
		if serviceID == "" {
			writeError(w, http.StatusBadRequest, "service query param is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		subgraph := buildServiceImpactSubgraph(graph, serviceID)
		resp := map[string]any{
			"graph_id":      graph.GraphID,
			"service":       serviceID,
			"impact_graph":  subgraph,
			"impact_counts": map[string]int{"nodes": len(subgraph.Nodes), "edges": len(subgraph.Edges)},
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"selection":      "service impact subgraph by service_id",
			},
		})
	}
}

func handleProductGovernance(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/governance/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), true, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		report := map[string]any{
			"graph_id":                  graph.GraphID,
			"generated_at":              time.Now().UTC(),
			"risk_posture":              buildGovernancePosture(graph),
			"open_conflicts":            countNodesByType(graph.Nodes, "conflict"),
			"sensitive_surfaces":        countNodesByType(graph.Nodes, "sensitive_surface"),
			"verification_needs_review": countNodesByAttr(graph.Nodes, "verification_status", "needs_review"),
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, report)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": report,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph_stats":    graph.Stats,
			},
		})
	}
}

func (m *runtimeMetrics) record(route string, status int, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rm, ok := m.routes[route]
	if !ok {
		rm = &routeMetrics{Route: route}
		m.routes[route] = rm
	}
	rm.Requests++
	if status >= 500 {
		rm.Errors++
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		rm.AuthFailures++
	}
	rm.LastStatus = status
	rm.LastAccessedAt = time.Now().UTC()
	ms := float64(d.Milliseconds())
	if ms <= 0 {
		ms = float64(d.Microseconds()) / 1000.0
	}
	rm.TotalDurationMS += ms
	// Exponential approximation for p95 that is stable in-memory.
	if rm.P95DurationMS == 0 || ms > rm.P95DurationMS {
		rm.P95DurationMS = ms
	} else {
		rm.P95DurationMS = (rm.P95DurationMS*0.95 + ms*0.05)
	}
	success := rm.Requests - rm.Errors - rm.AuthFailures
	if rm.Requests > 0 {
		rm.AvailabilityRate = (float64(success) / float64(rm.Requests)) * 100
	}
}

func (m *runtimeMetrics) snapshot() []routeMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]routeMetrics, 0, len(m.routes))
	for _, r := range m.routes {
		copyR := *r
		out = append(out, copyR)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}

func handleOpsMetrics(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		snap := apiMetrics.snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"generated_at": time.Now().UTC(),
			"routes":       snap,
		})
	}
}

func handleOpsSLO(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		writeJSON(w, http.StatusOK, computeSLOPayload(graphRoot, r))
	}
}

func handleOpsSLOEvaluate(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionOperateOps, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		req := opsSLOEvaluateRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		payload := computeSLOPayload(graphRoot, r)
		sloPassed := parseBoolDefault(fmt.Sprint(payload["slo_passed"]), false)
		incidentCreated := false
		incidentID := ""
		if !sloPassed || req.ForceIncident {
			reason := strings.TrimSpace(req.Reason)
			if reason == "" {
				reason = "slo_breach_detected"
				if req.ForceIncident && sloPassed {
					reason = "manual_slo_incident_drill"
				}
			}
			record := opsIncidentRecord{
				IncidentID: fmt.Sprintf("opsinc:%d", time.Now().UTC().UnixNano()),
				CreatedAt:  time.Now().UTC(),
				TenantID:   normalizeTenant(authCtx.TenantID),
				Status:     "open",
				Reason:     reason,
				SLOPassed:  sloPassed,
				SLOPayload: payload,
			}
			if err := persistOpsIncident(graphRoot, record); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			incidentCreated = true
			incidentID = record.IncidentID
			recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "allow", "ops_slo_incident_created", map[string]any{
				"incident_id": incidentID,
				"slo_passed":  sloPassed,
				"reason":      reason,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"slo":              payload,
			"incident_created": incidentCreated,
			"incident_id":      incidentID,
		})
	}
}

func handleOpsIncidents(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		index, err := loadOpsIncidentIndex(graphRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		index.Incidents = filterOpsIncidentsByTenant(r, index.Incidents)
		statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
		if statusFilter != "" {
			filtered := make([]opsIncidentSummary, 0, len(index.Incidents))
			for _, item := range index.Incidents {
				if strings.EqualFold(item.Status, statusFilter) {
					filtered = append(filtered, item)
				}
			}
			index.Incidents = filtered
		}
		limit := 0
		if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "invalid limit")
				return
			}
			limit = n
		}
		before := strings.TrimSpace(r.URL.Query().Get("before"))
		start := 0
		if before != "" {
			found := -1
			for i, item := range index.Incidents {
				if item.IncidentID == before {
					found = i
					break
				}
			}
			if found == -1 {
				writeError(w, http.StatusBadRequest, "invalid before cursor")
				return
			}
			start = found + 1
		}
		if start > len(index.Incidents) {
			start = len(index.Incidents)
		}
		items := index.Incidents[start:]
		nextBefore := ""
		if limit > 0 && len(items) > limit {
			items = items[:limit]
			nextBefore = items[len(items)-1].IncidentID
		}
		writeJSON(w, http.StatusOK, opsIncidentIndex{Incidents: items, NextBefore: nextBefore})
	}
}

func handleOpsIncidentByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		incidentID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/ops/incidents/"))
		incidentID = strings.Trim(incidentID, "/")
		if incidentID == "" {
			writeError(w, http.StatusBadRequest, "incident id is required")
			return
		}
		incident, err := loadOpsIncident(graphRoot, incidentID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "incident not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, err := security.ContextFromHeaders(r.Header)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if !authCtx.HasRole("platform_admin") && normalizeTenant(incident.TenantID) != normalizeTenant(authCtx.TenantID) {
			writeError(w, http.StatusForbidden, "tenant_mismatch")
			return
		}
		writeJSON(w, http.StatusOK, incident)
	}
}

func handleOpsUITelemetry(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := opsUITelemetryPath(graphRoot)
		switch r.Method {
		case http.MethodPost:
			authCtx, ok := authorizeRequest(w, r, security.ActionOperateOps, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
			if !ok {
				return
			}
			event := opsUITelemetryEvent{}
			if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
				return
			}
			event.SessionID = strings.TrimSpace(event.SessionID)
			event.EventType = strings.TrimSpace(event.EventType)
			if event.SessionID == "" {
				writeError(w, http.StatusBadRequest, "session_id is required")
				return
			}
			if event.EventType == "" {
				writeError(w, http.StatusBadRequest, "event_type is required")
				return
			}
			event.TaskID = strings.TrimSpace(event.TaskID)
			event.Status = strings.TrimSpace(event.Status)
			event.TenantID = normalizeTenant(strings.TrimSpace(event.TenantID))
			if event.TenantID == "" {
				event.TenantID = normalizeTenant(authCtx.TenantID)
			}
			if event.Principal == "" {
				event.Principal = strings.TrimSpace(authCtx.Principal)
			}
			if strings.TrimSpace(event.TimestampUTC) == "" {
				event.TimestampUTC = time.Now().UTC().Format(time.RFC3339Nano)
			}
			if event.Metadata == nil {
				event.Metadata = map[string]any{}
			}
			if err := appendOpsUITelemetryEvent(path, event); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":   true,
				"path": path,
			})
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
				return
			}
			events, err := loadOpsUITelemetryEvents(path)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, opsUITelemetryResponse{
				Path:    path,
				Events:  events,
				Summary: summarizeOpsUITelemetry(events),
			})
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

func computeSLOPayload(graphRoot string, r *http.Request) map[string]any {
	routes := apiMetrics.snapshot()
	total := int64(0)
	success := int64(0)
	p95 := 0.0
	for _, rm := range routes {
		critical := strings.HasPrefix(rm.Route, "/graphs") || strings.HasPrefix(rm.Route, "/products")
		if !critical {
			continue
		}
		total += rm.Requests
		success += rm.Requests - rm.Errors - rm.AuthFailures
		if rm.P95DurationMS > p95 {
			p95 = rm.P95DurationMS
		}
	}
	adherence := 100.0
	if total > 0 {
		adherence = (float64(success) / float64(total)) * 100
	}
	runtimeSLO := map[string]any{
		"total_runs":                   0,
		"decision_total":               0,
		"confirmed":                    0,
		"contradicted":                 0,
		"needs_review":                 0,
		"runtime_only_unmapped":        0,
		"confirmed_rate":               0.0,
		"contradicted_rate":            0.0,
		"needs_review_rate":            0.0,
		"runtime_only_unmapped_rate":   0.0,
		"target_confirmed_rate":        0.9,
		"runtime_quality_passed":       true,
		"runtime_quality_reason":       "no_runtime_reconciliation_runs",
		"runtime_quality_gate_enabled": true,
	}
	runtimePassed := true
	if authCtx, err := security.ContextFromHeaders(r.Header); err == nil {
		if index, err := loadRuntimeReconcileIndex(graphRoot); err == nil {
			runs := index.Runs
			if !authCtx.HasRole("platform_admin") {
				runs = filterRuntimeReconcileRunsByTenant(r, runs)
			}
			totalRuns := len(runs)
			totalConfirmed := 0
			totalContradicted := 0
			totalNeedsReview := 0
			totalUnmapped := 0
			for _, run := range runs {
				totalConfirmed += run.Confirmed
				totalContradicted += run.Contradicted
				totalNeedsReview += run.NeedsReview
				totalUnmapped += run.Unmapped
			}
			decisionTotal := totalConfirmed + totalContradicted + totalNeedsReview + totalUnmapped
			confirmedRate := 0.0
			contradictedRate := 0.0
			needsReviewRate := 0.0
			unmappedRate := 0.0
			if decisionTotal > 0 {
				confirmedRate = float64(totalConfirmed) / float64(decisionTotal)
				contradictedRate = float64(totalContradicted) / float64(decisionTotal)
				needsReviewRate = float64(totalNeedsReview) / float64(decisionTotal)
				unmappedRate = float64(totalUnmapped) / float64(decisionTotal)
			}
			runtimePassed = totalRuns == 0 || confirmedRate >= 0.9
			reason := "no_runtime_reconciliation_runs"
			if totalRuns > 0 {
				if runtimePassed {
					reason = "confirmed_rate_meets_target"
				} else {
					reason = "confirmed_rate_below_target"
				}
			}
			runtimeSLO = map[string]any{
				"total_runs":                   totalRuns,
				"decision_total":               decisionTotal,
				"confirmed":                    totalConfirmed,
				"contradicted":                 totalContradicted,
				"needs_review":                 totalNeedsReview,
				"runtime_only_unmapped":        totalUnmapped,
				"confirmed_rate":               confirmedRate,
				"contradicted_rate":            contradictedRate,
				"needs_review_rate":            needsReviewRate,
				"runtime_only_unmapped_rate":   unmappedRate,
				"target_confirmed_rate":        0.9,
				"runtime_quality_passed":       runtimePassed,
				"runtime_quality_reason":       reason,
				"runtime_quality_gate_enabled": true,
			}
		}
	}
	return map[string]any{
		"generated_at":            time.Now().UTC(),
		"critical_requests":       total,
		"critical_successes":      success,
		"slo_adherence":           adherence,
		"slo_target":              99.9,
		"slo_passed":              adherence >= 99.9 && runtimePassed,
		"critical_latency_p95_ms": p95,
		"slo_checks": map[string]any{
			"api_availability_passed": adherence >= 99.9,
			"runtime_quality_passed":  runtimePassed,
		},
		"runtime_reconciliation": runtimeSLO,
		"routes":                 routes,
	}
}

func opsUITelemetryPath(graphRoot string) string {
	return filepath.Join(defaultOutDirFromGraphRoot(graphRoot), "ops", "ui_telemetry_events.jsonl")
}

func appendOpsUITelemetryEvent(path string, event opsUITelemetryEvent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create ops ui telemetry dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open ops ui telemetry file: %w", err)
	}
	defer file.Close()
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal ops ui telemetry event: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append ops ui telemetry event: %w", err)
	}
	return nil
}

func loadOpsUITelemetryEvents(path string) ([]opsUITelemetryEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []opsUITelemetryEvent{}, nil
		}
		return nil, fmt.Errorf("read ops ui telemetry events: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	events := make([]opsUITelemetryEvent, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		event := opsUITelemetryEvent{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("decode ops ui telemetry event: %w", err)
		}
		events = append(events, event)
	}
	return events, nil
}

func summarizeOpsUITelemetry(events []opsUITelemetryEvent) opsUITelemetrySummary {
	summary := opsUITelemetrySummary{
		ByEventType:       map[string]int{},
		ByTask:            map[string]int{},
		AvgDurationByTask: map[string]float64{},
	}
	sessionSet := map[string]struct{}{}
	durationTotals := map[string]float64{}
	durationCounts := map[string]int{}
	for _, event := range events {
		summary.TotalEvents++
		sessionID := strings.TrimSpace(event.SessionID)
		if sessionID != "" {
			sessionSet[sessionID] = struct{}{}
		}
		eventType := strings.TrimSpace(event.EventType)
		if eventType != "" {
			summary.ByEventType[eventType]++
		}
		taskID := strings.TrimSpace(event.TaskID)
		if taskID != "" {
			summary.ByTask[taskID]++
			if event.DurationMS > 0 {
				durationTotals[taskID] += float64(event.DurationMS)
				durationCounts[taskID]++
			}
		}
		if event.DeadEnd {
			summary.DeadEndEvents++
		}
	}
	summary.TotalSessions = len(sessionSet)
	for taskID, total := range durationTotals {
		count := durationCounts[taskID]
		if count <= 0 {
			continue
		}
		summary.AvgDurationByTask[taskID] = total / float64(count)
	}
	return summary
}

func opsIncidentIndexPath(graphRoot string) string {
	return filepath.Join(graphRoot, "ops", "incidents", "index.json")
}

func loadOpsIncidentIndex(graphRoot string) (opsIncidentIndex, error) {
	path := opsIncidentIndexPath(graphRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return opsIncidentIndex{Incidents: []opsIncidentSummary{}}, nil
		}
		return opsIncidentIndex{}, err
	}
	idx := opsIncidentIndex{Incidents: []opsIncidentSummary{}}
	if err := json.Unmarshal(data, &idx); err != nil {
		return opsIncidentIndex{}, fmt.Errorf("decode ops incident index: %w", err)
	}
	return idx, nil
}

func persistOpsIncident(graphRoot string, incident opsIncidentRecord) error {
	if strings.TrimSpace(incident.IncidentID) == "" {
		incident.IncidentID = fmt.Sprintf("opsinc:%d", time.Now().UTC().UnixNano())
	}
	if incident.CreatedAt.IsZero() {
		incident.CreatedAt = time.Now().UTC()
	}
	incidentDir := filepath.Join(graphRoot, "ops", "incidents", incident.IncidentID)
	if err := os.MkdirAll(incidentDir, 0o755); err != nil {
		return fmt.Errorf("create ops incident dir: %w", err)
	}
	path := filepath.Join(incidentDir, "incident.json")
	data, err := json.MarshalIndent(incident, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ops incident: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write ops incident: %w", err)
	}
	idx, err := loadOpsIncidentIndex(graphRoot)
	if err != nil {
		return err
	}
	filtered := make([]opsIncidentSummary, 0, len(idx.Incidents)+1)
	for _, item := range idx.Incidents {
		if item.IncidentID != incident.IncidentID {
			filtered = append(filtered, item)
		}
	}
	filtered = append(filtered, opsIncidentSummary{
		IncidentID: incident.IncidentID,
		CreatedAt:  incident.CreatedAt,
		TenantID:   normalizeTenant(incident.TenantID),
		Status:     incident.Status,
		Reason:     incident.Reason,
		SLOPassed:  incident.SLOPassed,
		Path:       path,
	})
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	idx.Incidents = filtered
	idxData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ops incident index: %w", err)
	}
	idxData = append(idxData, '\n')
	if err := os.MkdirAll(filepath.Dir(opsIncidentIndexPath(graphRoot)), 0o755); err != nil {
		return fmt.Errorf("create ops incident index dir: %w", err)
	}
	if err := os.WriteFile(opsIncidentIndexPath(graphRoot), idxData, 0o644); err != nil {
		return fmt.Errorf("write ops incident index: %w", err)
	}
	return nil
}

func filterOpsIncidentsByTenant(r *http.Request, incidents []opsIncidentSummary) []opsIncidentSummary {
	authCtx, err := security.ContextFromHeaders(r.Header)
	if err != nil || authCtx.HasRole("platform_admin") {
		return incidents
	}
	tenantID := normalizeTenant(authCtx.TenantID)
	filtered := make([]opsIncidentSummary, 0, len(incidents))
	for _, item := range incidents {
		if normalizeTenant(item.TenantID) == tenantID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func loadOpsIncident(graphRoot string, incidentID string) (opsIncidentRecord, error) {
	path := filepath.Join(graphRoot, "ops", "incidents", incidentID, "incident.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return opsIncidentRecord{}, err
	}
	var payload opsIncidentRecord
	if err := json.Unmarshal(data, &payload); err != nil {
		return opsIncidentRecord{}, fmt.Errorf("decode ops incident: %w", err)
	}
	return payload, nil
}

func defaultOpsPaths(graphRoot string) (string, string, string) {
	root := finalArtifactsRoot(graphRoot)
	return root,
		filepath.Join(root, "quality", "report.json"),
		filepath.Join(root, "ops")
}

func handleOpsRolloutPolicy(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = filepath.Join("docs", "m16_rollout_policy.json")
		}
		resolvedPath := resolveReadablePath(path)
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "rollout policy not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var payload any
		if err := json.Unmarshal(data, &payload); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode rollout policy: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":   resolvedPath,
			"policy": payload,
		})
	}
}

func handleOpsBackup(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionOperateOps, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		req := opsBackupRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		defaultSourceRoot, _, _ := defaultOpsPaths(graphRoot)
		sourceRoot := strings.TrimSpace(req.SourceRoot)
		if sourceRoot == "" {
			sourceRoot = defaultSourceRoot
		}
		outPath := strings.TrimSpace(req.OutPath)
		if outPath == "" {
			outPath = filepath.Join(os.TempDir(), fmt.Sprintf("diffmind-ops-backup-%d.tar.gz", time.Now().UTC().UnixNano()))
		}
		if err := opspkg.Run(r.Context(), []string{"backup", "--source", sourceRoot, "--out", outPath}); err != nil {
			recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "deny", "ops_backup_failed", map[string]any{"error": err.Error(), "source_root": sourceRoot, "out_path": outPath})
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sizeBytes := int64(0)
		if st, err := os.Stat(outPath); err == nil {
			sizeBytes = st.Size()
		}
		recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "allow", "ops_backup_succeeded", map[string]any{"source_root": sourceRoot, "out_path": outPath, "size_bytes": sizeBytes})
		writeJSON(w, http.StatusOK, map[string]any{
			"source_root":  sourceRoot,
			"archive_path": outPath,
			"size_bytes":   sizeBytes,
		})
	}
}

func handleOpsRestore(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionOperateOps, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		req := opsRestoreRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		_, _, defaultOpsDir := defaultOpsPaths(graphRoot)
		archivePath := strings.TrimSpace(req.ArchivePath)
		if archivePath == "" {
			writeError(w, http.StatusBadRequest, "archive_path is required")
			return
		}
		targetRoot := strings.TrimSpace(req.TargetRoot)
		if targetRoot == "" {
			targetRoot = filepath.Join(defaultOpsDir, fmt.Sprintf("restore-%d", time.Now().UTC().UnixNano()))
		}
		if err := opspkg.Run(r.Context(), []string{"restore", "--archive", archivePath, "--target", targetRoot}); err != nil {
			recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "deny", "ops_restore_failed", map[string]any{"error": err.Error(), "archive_path": archivePath, "target_root": targetRoot})
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "allow", "ops_restore_succeeded", map[string]any{"archive_path": archivePath, "target_root": targetRoot})
		writeJSON(w, http.StatusOK, map[string]any{
			"archive_path": archivePath,
			"target_root":  targetRoot,
		})
	}
}

func handleOpsRollout(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionOperateOps, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		req := opsRolloutRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		component := strings.TrimSpace(req.Component)
		if component == "" {
			component = "extractor"
		}
		candidate := strings.TrimSpace(req.Candidate)
		if candidate == "" {
			writeError(w, http.StatusBadRequest, "candidate is required")
			return
		}
		current := strings.TrimSpace(req.Current)
		if current == "" {
			current = "unknown"
		}
		_, _, defaultOpsDir := defaultOpsPaths(graphRoot)
		outPath := strings.TrimSpace(req.OutPath)
		if outPath == "" {
			outPath = filepath.Join(defaultOpsDir, "rollout_plan.json")
		}
		if err := opspkg.Run(r.Context(), []string{"rollout", "--component", component, "--candidate", candidate, "--current", current, "--out", outPath}); err != nil {
			recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "deny", "ops_rollout_failed", map[string]any{"error": err.Error(), "out_path": outPath})
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read rollout plan: %v", err))
			return
		}
		var plan any
		if err := json.Unmarshal(data, &plan); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode rollout plan: %v", err))
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "allow", "ops_rollout_succeeded", map[string]any{"component": component, "candidate": candidate, "current": current, "out_path": outPath})
		writeJSON(w, http.StatusOK, map[string]any{
			"component": component,
			"candidate": candidate,
			"current":   current,
			"out_path":  outPath,
			"plan":      plan,
		})
	}
}

func handleOpsDrill(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionOperateOps, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		req := opsDrillRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		defaultSourceRoot, defaultQualityPath, defaultOpsDir := defaultOpsPaths(graphRoot)
		sourceRoot := strings.TrimSpace(req.SourceRoot)
		if sourceRoot == "" {
			sourceRoot = defaultSourceRoot
		}
		qualityPath := strings.TrimSpace(req.QualityPath)
		if qualityPath == "" {
			qualityPath = defaultQualityPath
		}
		drillOutDir := strings.TrimSpace(req.DrillOutDir)
		if drillOutDir == "" {
			drillOutDir = filepath.Join(defaultOpsDir, "drills", fmt.Sprintf("%d", time.Now().UTC().UnixNano()))
		}
		if err := os.MkdirAll(drillOutDir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("prepare drill dir: %v", err))
			return
		}
		archivePath := strings.TrimSpace(req.ArchivePath)
		if archivePath == "" {
			archivePath = filepath.Join(os.TempDir(), fmt.Sprintf("diffmind-ops-drill-backup-%d.tar.gz", time.Now().UTC().UnixNano()))
		}
		restoreTarget := strings.TrimSpace(req.RestoreTarget)
		if restoreTarget == "" {
			restoreTarget = filepath.Join(drillOutDir, "restore")
		}
		rolloutPath := strings.TrimSpace(req.RolloutPath)
		if rolloutPath == "" {
			rolloutPath = filepath.Join(drillOutDir, "rollout_plan.json")
		}
		sloOutPath := strings.TrimSpace(req.SLOOutPath)
		if sloOutPath == "" {
			sloOutPath = filepath.Join(drillOutDir, "slo_report.json")
		}

		backupErr := opspkg.Run(r.Context(), []string{"backup", "--source", sourceRoot, "--out", archivePath})
		restoreErr := error(nil)
		rolloutErr := error(nil)
		sloErr := error(nil)
		if backupErr == nil {
			restoreErr = opspkg.Run(r.Context(), []string{"restore", "--archive", archivePath, "--target", restoreTarget})
		}
		if restoreErr == nil {
			rolloutErr = opspkg.Run(r.Context(), []string{"rollout", "--component", "extractor", "--candidate", "vnext", "--current", "vcurrent", "--out", rolloutPath})
		}
		if rolloutErr == nil {
			sloErr = opspkg.Run(r.Context(), []string{"slo", "--audit-root", sourceRoot, "--quality", qualityPath, "--out", sloOutPath})
		}
		passed := backupErr == nil && restoreErr == nil && rolloutErr == nil && sloErr == nil
		resp := map[string]any{
			"passed":         passed,
			"source_root":    sourceRoot,
			"quality_path":   qualityPath,
			"drill_out_dir":  drillOutDir,
			"archive_path":   archivePath,
			"restore_target": restoreTarget,
			"rollout_path":   rolloutPath,
			"slo_out_path":   sloOutPath,
			"backup_error":   errorString(backupErr),
			"restore_error":  errorString(restoreErr),
			"rollout_error":  errorString(rolloutErr),
			"slo_error":      errorString(sloErr),
		}
		if !passed {
			recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "deny", "ops_drill_failed", resp)
			writeJSON(w, http.StatusOK, resp)
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionOperateOps, "allow", "ops_drill_succeeded", map[string]any{"drill_out_dir": drillOutDir})
		writeJSON(w, http.StatusOK, resp)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func finalArtifactsRoot(graphRoot string) string {
	root := strings.TrimSpace(graphRoot)
	if root == "" {
		return ".diffmind"
	}
	if strings.EqualFold(filepath.Base(root), "graph") {
		return filepath.Dir(root)
	}
	return root
}

func defaultFinalGatePaths(graphRoot string) (string, string, string, string, string, string, string, string) {
	root := finalArtifactsRoot(graphRoot)
	return filepath.Join(root, "quality", "gate_result.json"),
		filepath.Join(root, "graph", "merge_quality_report.json"),
		filepath.Join(root, "ops", "slo_report.json"),
		filepath.Join("docs", "m15_query_templates.json"),
		filepath.Join("docs", "m17_question_catalog.json"),
		filepath.Join(root, "graph", "index.json"),
		filepath.Join(root, "final", "readiness_report.json"),
		filepath.Join(root, "final", "gate_decision.md")
}

func defaultFinalCloseoutPaths(graphRoot string) (string, string, string, string, string, string, string, string, string, string, string, string, string, string, string) {
	qualityGatePath, mergeQualityPath, sloPath, templatesPath, catalogPath, graphIndexPath, outReportPath, outDecisionPath := defaultFinalGatePaths(graphRoot)
	root := finalArtifactsRoot(graphRoot)
	return qualityGatePath,
		mergeQualityPath,
		sloPath,
		templatesPath,
		catalogPath,
		graphIndexPath,
		filepath.Join(root, "graph", "contract_report.json"),
		filepath.Join(root, "quality", "report.json"),
		filepath.Join(root, "corpus", "report.json"),
		filepath.Join("docs", "graph_performance_baseline.md"),
		root,
		root,
		filepath.Join(root, "final", "drills"),
		outReportPath,
		outDecisionPath
}

func defaultFinalCloseoutArtifactPaths(graphRoot string) (string, string, string, string, string) {
	root := finalArtifactsRoot(graphRoot)
	return filepath.Join(root, "final", "milestone_closure_report.json"),
		filepath.Join(root, "final", "benchmark_evidence_report.json"),
		filepath.Join(root, "final", "security_validation_report.json"),
		filepath.Join(root, "final", "operations_drill_report.json"),
		filepath.Join(root, "final", "closure_rules_report.json")
}

func defaultQualityPaths(graphRoot string) (string, string, string, string, string, string, string) {
	root := finalArtifactsRoot(graphRoot)
	return filepath.Join(root, "corpus", "report.json"),
		filepath.Join("corpus", "golden", "summary.json"),
		filepath.Join(root, "graph", "merge_quality_report.json"),
		filepath.Join(root, "graph", "index.json"),
		filepath.Join(root, "quality", "report.json"),
		filepath.Join(root, "quality", "dashboard.md"),
		filepath.Join(root, "quality", "triage.md")
}

func defaultMergeQualityPath(graphRoot string) string {
	_, mergeQualityPath, _, _, _, _, _, _ := defaultFinalGatePaths(graphRoot)
	return mergeQualityPath
}

func defaultMergeQualityPathForGraph(graphRoot string, graphID string) string {
	root := strings.TrimSpace(graphRoot)
	if root == "" {
		root = filepath.Join(".diffmind", "graph")
	}
	return filepath.Join(root, strings.TrimSpace(graphID), "merge_quality_report.json")
}

func defaultArchitectureTasksPathForGraph(graphRoot string, graphID string) string {
	root := strings.TrimSpace(graphRoot)
	if root == "" {
		root = filepath.Join(".diffmind", "graph")
	}
	return filepath.Join(root, strings.TrimSpace(graphID), "architecture_tasks.json")
}

func defaultArchitectureFocusedSubgraphPathForGraph(graphRoot string, graphID string) string {
	root := strings.TrimSpace(graphRoot)
	if root == "" {
		root = filepath.Join(".diffmind", "graph")
	}
	return filepath.Join(root, strings.TrimSpace(graphID), "focused_subgraph.json")
}

func graphPathFromRoot(graphRoot string, graphID string) string {
	return filepath.Join(strings.TrimSpace(graphRoot), strings.TrimSpace(graphID), "graph.json")
}

func handleGraphMergeQuality(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			path := strings.TrimSpace(r.URL.Query().Get("path"))
			if path == "" {
				path = defaultMergeQualityPath(graphRoot)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "merge quality report not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode merge quality report: %v", err))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"path":   path,
				"report": payload,
			})
		case http.MethodPost:
			authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			req := graphMergeQualityAssessRequest{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
				return
			}
			indexPath := strings.TrimSpace(req.IndexPath)
			if indexPath == "" {
				indexPath = filepath.Join(graphRoot, "index.json")
			}
			outPath := strings.TrimSpace(req.OutPath)
			if outPath == "" {
				outPath = defaultMergeQualityPath(graphRoot)
			}
			result, err := graphpkg.Assess(r.Context(), graphpkg.AssessRequest{
				GraphPath:       strings.TrimSpace(req.GraphPath),
				IndexPath:       indexPath,
				OutPath:         outPath,
				ExpectLinksPath: strings.TrimSpace(req.ExpectLinksPath),
				FailOnGate:      req.FailOnGate,
			})
			if err != nil {
				recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "deny", "merge_quality_assess_failed", map[string]any{"error": err.Error(), "out_path": outPath})
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			reportData, err := os.ReadFile(result.ReportPath)
			if err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("read merge quality report: %v", err))
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(reportData, &payload); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode merge quality report: %v", err))
				return
			}
			recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "merge_quality_assess_succeeded", map[string]any{"graph_id": result.GraphID, "report_path": result.ReportPath, "passed": result.Passed})
			writeJSON(w, http.StatusOK, map[string]any{
				"graph_id":   result.GraphID,
				"graph_path": result.GraphPath,
				"path":       result.ReportPath,
				"passed":     result.Passed,
				"report":     payload,
			})
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

func handleQualityReport(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		_, _, _, _, defaultPath, _, _ := defaultQualityPaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "quality report not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var payload any
		if err := json.Unmarshal(data, &payload); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode quality report: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":   path,
			"report": payload,
		})
	}
}

func handleQualityDashboard(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		_, _, _, _, _, defaultPath, _ := defaultQualityPaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "quality dashboard not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":    path,
			"content": string(data),
		})
	}
}

func handleQualityTriage(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		_, _, _, _, _, _, defaultPath := defaultQualityPaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "quality triage not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":    path,
			"content": string(data),
		})
	}
}

func handleQualityEvaluate(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
		if !ok {
			return
		}
		req := qualityEvaluateRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		corpusPath, goldenPath, mergeQualityPath, graphIndexPath, outPath, dashboardPath, triagePath := defaultQualityPaths(graphRoot)
		if v := strings.TrimSpace(req.CorpusPath); v != "" {
			corpusPath = v
		}
		if v := strings.TrimSpace(req.GoldenPath); v != "" {
			goldenPath = v
		}
		if v := strings.TrimSpace(req.MergeQualityPath); v != "" {
			mergeQualityPath = v
		}
		if v := strings.TrimSpace(req.GraphIndexPath); v != "" {
			graphIndexPath = v
		}
		if v := strings.TrimSpace(req.OutPath); v != "" {
			outPath = v
		}
		if v := strings.TrimSpace(req.DashboardPath); v != "" {
			dashboardPath = v
		}
		if v := strings.TrimSpace(req.TriagePath); v != "" {
			triagePath = v
		}
		args := []string{
			"evaluate",
			"--corpus", corpusPath,
			"--golden", goldenPath,
			"--merge-quality", mergeQualityPath,
			"--graph-index", graphIndexPath,
			"--out", outPath,
			"--dashboard", dashboardPath,
			"--triage", triagePath,
		}
		if v := strings.TrimSpace(req.ExpectedLinksPath); v != "" {
			args = append(args, "--merge-quality-expect-links", v)
		}
		mergeQualityAuto := true
		if req.MergeQualityAuto != nil {
			mergeQualityAuto = *req.MergeQualityAuto
		}
		args = append(args, fmt.Sprintf("--merge-quality-auto=%t", mergeQualityAuto))
		if err := quality.Run(r.Context(), args); err != nil {
			recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "deny", "quality_evaluate_failed", map[string]any{"error": err.Error(), "out_path": outPath})
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		reportData, err := os.ReadFile(outPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read quality report: %v", err))
			return
		}
		dashboardData, err := os.ReadFile(dashboardPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read quality dashboard: %v", err))
			return
		}
		triageData, err := os.ReadFile(triagePath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read quality triage: %v", err))
			return
		}
		var payload any
		if err := json.Unmarshal(reportData, &payload); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode quality report: %v", err))
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "quality_evaluate_succeeded", map[string]any{"report_path": outPath, "dashboard_path": dashboardPath, "triage_path": triagePath})
		writeJSON(w, http.StatusOK, map[string]any{
			"report_path":    outPath,
			"dashboard_path": dashboardPath,
			"triage_path":    triagePath,
			"report":         payload,
			"dashboard":      string(dashboardData),
			"triage":         string(triageData),
		})
	}
}

func handleQualityGate(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			path := strings.TrimSpace(r.URL.Query().Get("path"))
			if path == "" {
				path, _, _, _, _, _, _, _ = defaultFinalGatePaths(graphRoot)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "quality gate result not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			var payload any
			if err := json.Unmarshal(data, &payload); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode quality gate result: %v", err))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"path":   path,
				"result": payload,
			})
		case http.MethodPost:
			authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			req := qualityGateRequest{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
				return
			}
			_, _, _, _, defaultReportPath, _, _ := defaultQualityPaths(graphRoot)
			reportPath := defaultReportPath
			if v := strings.TrimSpace(req.ReportPath); v != "" {
				reportPath = v
			}
			policyPath := strings.TrimSpace(req.PolicyPath)
			if policyPath == "" {
				policyPath = filepath.Join("quality", "policy.json")
			}
			policyPath = resolveReadablePath(policyPath)
			outPath := strings.TrimSpace(req.OutPath)
			if outPath == "" {
				outPath, _, _, _, _, _, _, _ = defaultFinalGatePaths(graphRoot)
			}
			runErr := quality.Run(r.Context(), []string{
				"gate",
				"--report", reportPath,
				"--policy", policyPath,
				"--out", outPath,
			})
			data, readErr := os.ReadFile(outPath)
			if readErr != nil {
				if runErr != nil {
					recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "deny", "quality_gate_failed", map[string]any{"error": runErr.Error(), "out_path": outPath})
					writeError(w, http.StatusBadRequest, runErr.Error())
					return
				}
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("read quality gate result: %v", readErr))
				return
			}
			var payload any
			if err := json.Unmarshal(data, &payload); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode quality gate result: %v", err))
				return
			}
			resp := map[string]any{
				"report_path": reportPath,
				"policy_path": policyPath,
				"out_path":    outPath,
				"result":      payload,
			}
			if runErr != nil {
				recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "deny", "quality_gate_failed", map[string]any{"error": runErr.Error(), "out_path": outPath})
				resp["overall_passed"] = false
				resp["gate_error"] = runErr.Error()
				writeJSON(w, http.StatusOK, resp)
				return
			}
			recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "quality_gate_passed", map[string]any{"out_path": outPath})
			resp["overall_passed"] = true
			writeJSON(w, http.StatusOK, resp)
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

func handleFinalGateAttest(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditExport, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot); !ok {
			return
		}
		qualityPath, mergeQualityPath, sloPath, templatesPath, catalogPath, graphIndexPath, outReportPath, outDecisionPath := defaultFinalGatePaths(graphRoot)
		req := finalGateAttestRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		if v := strings.TrimSpace(req.QualityGatePath); v != "" {
			qualityPath = v
		}
		if v := strings.TrimSpace(req.MergeQualityPath); v != "" {
			mergeQualityPath = v
		}
		mergeQualityExpectPath := strings.TrimSpace(req.MergeQualityExpectPath)
		if v := strings.TrimSpace(req.SLOPath); v != "" {
			sloPath = v
		}
		if v := strings.TrimSpace(req.TemplatesPath); v != "" {
			templatesPath = v
		}
		if v := strings.TrimSpace(req.CatalogPath); v != "" {
			catalogPath = v
		}
		if v := strings.TrimSpace(req.GraphIndexPath); v != "" {
			graphIndexPath = v
		}
		if v := strings.TrimSpace(req.OutReportPath); v != "" {
			outReportPath = v
		}
		if v := strings.TrimSpace(req.OutDecisionPath); v != "" {
			outDecisionPath = v
		}
		args := []string{
			"attest",
			"--quality-gate", qualityPath,
			"--merge-quality", mergeQualityPath,
			"--slo", sloPath,
			"--templates", templatesPath,
			"--catalog", catalogPath,
			"--graph-index", graphIndexPath,
			"--out-report", outReportPath,
			"--out-decision", outDecisionPath,
		}
		if mergeQualityExpectPath != "" {
			args = append(args, "--merge-quality-expect-links", mergeQualityExpectPath)
		}
		if len(req.Signers) > 0 {
			signers := make([]string, 0, len(req.Signers))
			for _, s := range req.Signers {
				s = strings.TrimSpace(s)
				if s != "" {
					signers = append(signers, s)
				}
			}
			if len(signers) > 0 {
				args = append(args, "--signers", strings.Join(signers, ","))
			}
		}
		runErr := finalgate.Run(r.Context(), args)
		resp := map[string]any{
			"quality_gate_path":               qualityPath,
			"merge_quality_path":              mergeQualityPath,
			"merge_quality_expect_links_path": mergeQualityExpectPath,
			"slo_path":                        sloPath,
			"templates_path":                  templatesPath,
			"catalog_path":                    catalogPath,
			"graph_index_path":                graphIndexPath,
			"report_path":                     outReportPath,
			"decision_path":                   outDecisionPath,
		}
		if reportData, err := os.ReadFile(outReportPath); err == nil {
			var reportPayload any
			if json.Unmarshal(reportData, &reportPayload) == nil {
				resp["readiness_report"] = reportPayload
			}
		}
		if decisionData, err := os.ReadFile(outDecisionPath); err == nil {
			resp["gate_decision_markdown"] = string(decisionData)
		}
		if runErr != nil {
			resp["attest_error"] = runErr.Error()
			resp["overall_passed"] = false
			if _, ok := resp["readiness_report"]; ok {
				writeJSON(w, http.StatusOK, resp)
				return
			}
			writeError(w, http.StatusInternalServerError, runErr.Error())
			return
		}
		resp["overall_passed"] = true
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleFinalCloseout(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditExport, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot); !ok {
			return
		}
		qualityGatePath, mergeQualityPath, sloPath, templatesPath, catalogPath, graphIndexPath, contractReportPath, qualityReportPath, corpusReportPath, performancePolicyPath, auditRoot, drillSource, drillOutDir, outReportPath, outDecisionPath := defaultFinalCloseoutPaths(graphRoot)
		outMilestonesPath, outBenchmarkPath, outSecurityPath, outOpsPath, outClosureRulesPath := defaultFinalCloseoutArtifactPaths(graphRoot)

		req := finalGateCloseoutRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		if v := strings.TrimSpace(req.QualityGatePath); v != "" {
			qualityGatePath = v
		}
		if v := strings.TrimSpace(req.MergeQualityPath); v != "" {
			mergeQualityPath = v
		}
		mergeQualityExpectPath := strings.TrimSpace(req.MergeQualityExpectPath)
		if v := strings.TrimSpace(req.SLOPath); v != "" {
			sloPath = v
		}
		if v := strings.TrimSpace(req.TemplatesPath); v != "" {
			templatesPath = v
		}
		if v := strings.TrimSpace(req.CatalogPath); v != "" {
			catalogPath = v
		}
		if v := strings.TrimSpace(req.GraphIndexPath); v != "" {
			graphIndexPath = v
		}
		if v := strings.TrimSpace(req.ContractReportPath); v != "" {
			contractReportPath = v
		}
		if v := strings.TrimSpace(req.QualityReportPath); v != "" {
			qualityReportPath = v
		}
		if v := strings.TrimSpace(req.CorpusReportPath); v != "" {
			corpusReportPath = v
		}
		if v := strings.TrimSpace(req.PerformancePolicyPath); v != "" {
			performancePolicyPath = v
		}
		if v := strings.TrimSpace(req.AuditRoot); v != "" {
			auditRoot = v
		}
		if v := strings.TrimSpace(req.DrillSource); v != "" {
			drillSource = v
		}
		if v := strings.TrimSpace(req.DrillOutDir); v != "" {
			drillOutDir = v
		}
		if v := strings.TrimSpace(req.OutReportPath); v != "" {
			outReportPath = v
		}
		if v := strings.TrimSpace(req.OutDecisionPath); v != "" {
			outDecisionPath = v
		}
		if v := strings.TrimSpace(req.OutMilestonesPath); v != "" {
			outMilestonesPath = v
		}
		if v := strings.TrimSpace(req.OutBenchmarkPath); v != "" {
			outBenchmarkPath = v
		}
		if v := strings.TrimSpace(req.OutSecurityPath); v != "" {
			outSecurityPath = v
		}
		if v := strings.TrimSpace(req.OutOpsPath); v != "" {
			outOpsPath = v
		}
		if v := strings.TrimSpace(req.OutClosureRulesPath); v != "" {
			outClosureRulesPath = v
		}

		args := []string{
			"closeout",
			"--quality-gate", qualityGatePath,
			"--merge-quality", mergeQualityPath,
			"--slo", sloPath,
			"--templates", templatesPath,
			"--catalog", catalogPath,
			"--graph-index", graphIndexPath,
			"--contract-report", contractReportPath,
			"--quality-report", qualityReportPath,
			"--corpus-report", corpusReportPath,
			"--performance-policy", performancePolicyPath,
			"--audit-root", auditRoot,
			"--drill-source", drillSource,
			"--drill-out", drillOutDir,
			"--out-report", outReportPath,
			"--out-decision", outDecisionPath,
			"--out-milestones", outMilestonesPath,
			"--out-benchmark", outBenchmarkPath,
			"--out-security", outSecurityPath,
			"--out-ops", outOpsPath,
			"--out-closure-rules", outClosureRulesPath,
		}
		if mergeQualityExpectPath != "" {
			args = append(args, "--merge-quality-expect-links", mergeQualityExpectPath)
		}
		if len(req.Signers) > 0 {
			signers := make([]string, 0, len(req.Signers))
			for _, s := range req.Signers {
				s = strings.TrimSpace(s)
				if s != "" {
					signers = append(signers, s)
				}
			}
			if len(signers) > 0 {
				args = append(args, "--signers", strings.Join(signers, ","))
			}
		}
		runErr := finalgate.Run(r.Context(), args)
		resp := map[string]any{
			"quality_gate_path":               qualityGatePath,
			"merge_quality_path":              mergeQualityPath,
			"merge_quality_expect_links_path": mergeQualityExpectPath,
			"slo_path":                        sloPath,
			"templates_path":                  templatesPath,
			"catalog_path":                    catalogPath,
			"graph_index_path":                graphIndexPath,
			"contract_report_path":            contractReportPath,
			"quality_report_path":             qualityReportPath,
			"corpus_report_path":              corpusReportPath,
			"performance_policy_path":         performancePolicyPath,
			"audit_root":                      auditRoot,
			"drill_source":                    drillSource,
			"drill_out_dir":                   drillOutDir,
			"report_path":                     outReportPath,
			"decision_path":                   outDecisionPath,
			"milestones_path":                 outMilestonesPath,
			"benchmark_path":                  outBenchmarkPath,
			"security_path":                   outSecurityPath,
			"ops_path":                        outOpsPath,
			"closure_rules_path":              outClosureRulesPath,
		}
		if reportData, err := os.ReadFile(outReportPath); err == nil {
			var payload any
			if json.Unmarshal(reportData, &payload) == nil {
				resp["readiness_report"] = payload
			}
		}
		if decisionData, err := os.ReadFile(outDecisionPath); err == nil {
			resp["gate_decision_markdown"] = string(decisionData)
		}
		if data, err := os.ReadFile(outMilestonesPath); err == nil {
			var payload any
			if json.Unmarshal(data, &payload) == nil {
				resp["milestone_closure_report"] = payload
			}
		}
		if data, err := os.ReadFile(outBenchmarkPath); err == nil {
			var payload any
			if json.Unmarshal(data, &payload) == nil {
				resp["benchmark_evidence_report"] = payload
			}
		}
		if data, err := os.ReadFile(outSecurityPath); err == nil {
			var payload any
			if json.Unmarshal(data, &payload) == nil {
				resp["security_validation_report"] = payload
			}
		}
		if data, err := os.ReadFile(outOpsPath); err == nil {
			var payload any
			if json.Unmarshal(data, &payload) == nil {
				resp["operations_drill_report"] = payload
			}
		}
		if data, err := os.ReadFile(outClosureRulesPath); err == nil {
			var payload any
			if json.Unmarshal(data, &payload) == nil {
				resp["closure_rules_report"] = payload
			}
		}
		if runErr != nil {
			resp["closeout_error"] = runErr.Error()
			resp["overall_passed"] = false
			if _, ok := resp["readiness_report"]; ok {
				writeJSON(w, http.StatusOK, resp)
				return
			}
			writeError(w, http.StatusInternalServerError, runErr.Error())
			return
		}
		resp["overall_passed"] = true
		writeJSON(w, http.StatusOK, resp)
	}
}

func readFinalJSONReport(w http.ResponseWriter, path string, notFoundMsg string, decodeMsg string) (any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, notFoundMsg)
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("%s: %v", decodeMsg, err))
		return nil, false
	}
	return payload, true
}

func handleFinalReadiness(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		_, _, _, _, _, _, defaultPath, _ := defaultFinalGatePaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "final readiness report not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var payload any
		if err := json.Unmarshal(data, &payload); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode readiness report: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":   path,
			"report": payload,
		})
	}
}

func handleFinalDecision(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		_, _, _, _, _, _, _, defaultPath := defaultFinalGatePaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "final gate decision not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":    path,
			"content": string(data),
		})
	}
}

func handleFinalMilestones(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		defaultPath, _, _, _, _ := defaultFinalCloseoutArtifactPaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		payload, ok := readFinalJSONReport(w, path, "final milestone closure report not found", "decode final milestone closure report")
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "report": payload})
	}
}

func handleFinalBenchmark(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		_, defaultPath, _, _, _ := defaultFinalCloseoutArtifactPaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		payload, ok := readFinalJSONReport(w, path, "final benchmark evidence report not found", "decode final benchmark evidence report")
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "report": payload})
	}
}

func handleFinalSecurity(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		_, _, defaultPath, _, _ := defaultFinalCloseoutArtifactPaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		payload, ok := readFinalJSONReport(w, path, "final security validation report not found", "decode final security validation report")
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "report": payload})
	}
}

func handleFinalOps(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		_, _, _, defaultPath, _ := defaultFinalCloseoutArtifactPaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		payload, ok := readFinalJSONReport(w, path, "final operations drill report not found", "decode final operations drill report")
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "report": payload})
	}
}

func handleFinalClosureRules(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		_, _, _, _, defaultPath := defaultFinalCloseoutArtifactPaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		payload, ok := readFinalJSONReport(w, path, "final closure rules report not found", "decode final closure rules report")
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "report": payload})
	}
}

func handleRuntimePlan(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		writeJSON(w, http.StatusOK, runtimepkg.DefaultPlan())
	}
}

func parseRuntimeReportTimeParam(raw string, name string) (time.Time, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("invalid %s (expected RFC3339)", name)
	}
	return ts.UTC(), true, nil
}

func handleRuntimeReconcileReport(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		from, hasFrom, err := parseRuntimeReportTimeParam(r.URL.Query().Get("from"), "from")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		to, hasTo, err := parseRuntimeReportTimeParam(r.URL.Query().Get("to"), "to")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if hasFrom && hasTo && from.After(to) {
			writeError(w, http.StatusBadRequest, "from must be <= to")
			return
		}
		graphFilter := strings.TrimSpace(r.URL.Query().Get("graph_id"))

		index, err := loadRuntimeReconcileIndex(graphRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		filtered := filterRuntimeReconcileRunsByTenant(r, index.Runs)
		if graphFilter != "" {
			next := make([]runtimeReconcileSummary, 0, len(filtered))
			for _, run := range filtered {
				if strings.TrimSpace(run.GraphID) == graphFilter {
					next = append(next, run)
				}
			}
			filtered = next
		}
		if hasFrom || hasTo {
			next := make([]runtimeReconcileSummary, 0, len(filtered))
			for _, run := range filtered {
				ts := run.GeneratedAt.UTC()
				if hasFrom && ts.Before(from) {
					continue
				}
				if hasTo && ts.After(to) {
					continue
				}
				next = append(next, run)
			}
			filtered = next
		}

		authCtx, authErr := security.ContextFromHeaders(r.Header)
		report := runtimeReconcileReport{
			GeneratedAt: time.Now().UTC(),
			GraphID:     graphFilter,
			TopGraphs:   []runtimeReconcileGraphSummary{},
		}
		if authErr == nil && !authCtx.HasRole("platform_admin") {
			report.TenantID = normalizeTenant(authCtx.TenantID)
		}
		if hasFrom {
			report.From = from.Format(time.RFC3339)
		}
		if hasTo {
			report.To = to.Format(time.RFC3339)
		}
		report.TotalRuns = len(filtered)
		if len(filtered) > 0 {
			latest := filtered[0]
			report.LatestRun = &latest
		}
		graphs := map[string]*runtimeReconcileGraphSummary{}
		for _, run := range filtered {
			report.TotalClaims += run.Claims
			report.TotalObservations += run.Observations
			report.TotalConfirmed += run.Confirmed
			report.TotalContradicted += run.Contradicted
			report.TotalUnmapped += run.Unmapped
			report.TotalNeedsReview += run.NeedsReview

			stat, ok := graphs[run.GraphID]
			if !ok {
				stat = &runtimeReconcileGraphSummary{GraphID: run.GraphID}
				graphs[run.GraphID] = stat
			}
			stat.Runs++
			stat.Claims += run.Claims
			stat.Observations += run.Observations
			stat.Confirmed += run.Confirmed
			stat.Contradicted += run.Contradicted
			stat.Unmapped += run.Unmapped
			stat.NeedsReview += run.NeedsReview
			if run.GeneratedAt.After(stat.LastRunAt) {
				stat.LastRunAt = run.GeneratedAt
			}
		}
		decisionTotal := report.TotalConfirmed + report.TotalContradicted + report.TotalNeedsReview + report.TotalUnmapped
		if decisionTotal > 0 {
			report.ConfirmedRate = float64(report.TotalConfirmed) / float64(decisionTotal)
			report.ContradictedRate = float64(report.TotalContradicted) / float64(decisionTotal)
			report.NeedsReviewRate = float64(report.TotalNeedsReview) / float64(decisionTotal)
			report.UnmappedRate = float64(report.TotalUnmapped) / float64(decisionTotal)
		}
		report.TopGraphs = make([]runtimeReconcileGraphSummary, 0, len(graphs))
		for _, stat := range graphs {
			report.TopGraphs = append(report.TopGraphs, *stat)
		}
		sort.Slice(report.TopGraphs, func(i, j int) bool {
			if report.TopGraphs[i].Runs == report.TopGraphs[j].Runs {
				return report.TopGraphs[i].GraphID < report.TopGraphs[j].GraphID
			}
			return report.TopGraphs[i].Runs > report.TopGraphs[j].Runs
		})
		if len(report.TopGraphs) > 5 {
			report.TopGraphs = report.TopGraphs[:5]
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleRuntimeReconcile(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			listRuntimeReconcileHistory(graphRoot, r, w)
			return
		case http.MethodPost:
			req := contracts.RuntimeReconciliationRequest{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
				return
			}
			if strings.TrimSpace(req.GraphID) == "" {
				writeError(w, http.StatusBadRequest, "graph_id is required")
				return
			}
			resourceTenant := normalizeTenant(req.TenantID)
			if req.TenantID == "" {
				resourceTenant = normalizeTenant(r.Header.Get("X-DiffMind-Tenant"))
			}
			if _, ok := authorizeRequest(w, r, security.ActionRuntimeRun, resourceTenant, false, true, graphRoot); !ok {
				return
			}
			result, err := runtimepkg.Reconcile(r.Context(), req)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recID, generatedAt, err := persistRuntimeReconcileResult(graphRoot, normalizeTenant(resourceTenant), req, result)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"reconcile_id":          recID,
				"generated_at":          generatedAt,
				"tenant_id":             normalizeTenant(resourceTenant),
				"graph_id":              result.GraphID,
				"confirmed":             result.Confirmed,
				"contradicted":          result.Contradicted,
				"runtime_only_unmapped": result.Unmapped,
				"needs_review":          result.NeedsReview,
			})
			return
		case http.MethodDelete:
			authCtx, ok := authorizeRequest(w, r, security.ActionRuntimeRun, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			keepLatest := 20
			if q := strings.TrimSpace(r.URL.Query().Get("keep_latest")); q != "" {
				n, err := strconv.Atoi(q)
				if err != nil || n < 0 {
					writeError(w, http.StatusBadRequest, "invalid keep_latest")
					return
				}
				keepLatest = n
			}
			scopeTenant := ""
			if !authCtx.HasRole("platform_admin") {
				scopeTenant = normalizeTenant(authCtx.TenantID)
			}
			deleted, err := pruneRuntimeReconcileHistory(graphRoot, keepLatest, scopeTenant)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"deleted":     deleted,
				"keep_latest": keepLatest,
			})
			return
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}
}

func handleRuntimeReconcileByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/runtime/reconcile/"))
		recID = strings.Trim(recID, "/")
		if recID == "" {
			writeError(w, http.StatusBadRequest, "reconcile id is required")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			payload, err := loadRuntimeReconcileResult(graphRoot, recID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "runtime reconcile result not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			authCtx, err := security.ContextFromHeaders(r.Header)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}
			if !authCtx.HasRole("platform_admin") && normalizeTenant(payload.TenantID) != normalizeTenant(authCtx.TenantID) {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
			writeJSON(w, http.StatusOK, payload)
			return
		case http.MethodDelete:
			authCtx, ok := authorizeRequest(w, r, security.ActionRuntimeRun, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			payload, err := loadRuntimeReconcileResult(graphRoot, recID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "runtime reconcile result not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !authCtx.HasRole("platform_admin") && normalizeTenant(payload.TenantID) != normalizeTenant(authCtx.TenantID) {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
			if err := deleteRuntimeReconcileResult(graphRoot, recID); err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "runtime reconcile result not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"deleted": recID})
			return
		default:
			w.Header().Set("Allow", "GET, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}
}

func handleRuntimeReconcileCompare(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		fromID := strings.TrimSpace(r.URL.Query().Get("from"))
		toID := strings.TrimSpace(r.URL.Query().Get("to"))
		if fromID == "" || toID == "" {
			writeError(w, http.StatusBadRequest, "from and to query params are required")
			return
		}
		fromRec, err := loadRuntimeReconcileResult(graphRoot, fromID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "from runtime reconcile result not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		toRec, err := loadRuntimeReconcileResult(graphRoot, toID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "to runtime reconcile result not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, err := security.ContextFromHeaders(r.Header)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if !authCtx.HasRole("platform_admin") {
			if normalizeTenant(fromRec.TenantID) != normalizeTenant(authCtx.TenantID) || normalizeTenant(toRec.TenantID) != normalizeTenant(authCtx.TenantID) {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
		}
		payload := compareRuntimeReconcileRuns(fromRec, toRec)
		writeJSON(w, http.StatusOK, payload)
	}
}

func handleRuntimeClaims(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/runtime/claims/")
		graphID := strings.TrimSpace(strings.Trim(rest, "/"))
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, graphTenant(graph), false, false, graphRoot); !ok {
			return
		}

		policy := parsePublishPolicy(r)
		sections := runtimeClaimSections(r.URL.Query().Get("sections"))
		includeNodes := parseBoolDefault(r.URL.Query().Get("include_nodes"), true)
		includeEdges := parseBoolDefault(r.URL.Query().Get("include_edges"), true)
		claims := buildRuntimeClaims(graphID, graph, sections, includeNodes, includeEdges, policy)

		writeJSON(w, http.StatusOK, map[string]any{
			"graph_id": graphID,
			"count":    len(claims),
			"claims":   claims,
		})
	}
}

func handleVerifyRun(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
		if !ok {
			return
		}
		req := verifyRunRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		inBundle := strings.TrimSpace(req.InBundle)
		if inBundle == "" {
			writeError(w, http.StatusBadRequest, "in_bundle is required")
			return
		}
		if _, err := os.Stat(inBundle); err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "in_bundle not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		runID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
		outDir := strings.TrimSpace(req.OutDir)
		if outDir == "" {
			outDir = filepath.Join(graphRoot, "verify", "runs", runID)
		}
		tenantID := normalizeTenant(authCtx.TenantID)
		if authCtx.HasRole("platform_admin") && strings.TrimSpace(req.TenantID) != "" {
			tenantID = normalizeTenant(req.TenantID)
		}
		graphID := strings.TrimSpace(req.GraphID)
		args := []string{"--in", inBundle, "--out", outDir}
		if strings.TrimSpace(req.OutBundle) != "" {
			args = append(args, "--out-bundle", strings.TrimSpace(req.OutBundle))
		}
		if strings.TrimSpace(req.ReviewQueuePath) != "" {
			args = append(args, "--review-queue", strings.TrimSpace(req.ReviewQueuePath))
		}
		if req.PromoteThreshold != nil {
			args = append(args, "--promote-threshold", fmt.Sprintf("%.6f", *req.PromoteThreshold))
		}
		if req.DisputeThreshold != nil {
			args = append(args, "--dispute-threshold", fmt.Sprintf("%.6f", *req.DisputeThreshold))
		}
		if req.StrictEvidence != nil {
			args = append(args, fmt.Sprintf("--strict-evidence=%t", *req.StrictEvidence))
		}
		if req.TwoPass != nil {
			args = append(args, fmt.Sprintf("--two-pass=%t", *req.TwoPass))
		}
		if err := verifier.Run(r.Context(), args); err != nil {
			recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "deny", "verify_run_failed", map[string]any{"error": err.Error(), "in_bundle": inBundle})
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		reportPath := filepath.Join(outDir, "verify", "report.json")
		queuePath := filepath.Join(outDir, "verify", "review_queue.json")
		outBundle := filepath.Join(outDir, "bundle", "intelligence_bundle.json")
		if strings.TrimSpace(req.OutBundle) != "" {
			outBundle = strings.TrimSpace(req.OutBundle)
		}
		if strings.TrimSpace(req.ReviewQueuePath) != "" {
			queuePath = strings.TrimSpace(req.ReviewQueuePath)
		}
		reportData, err := os.ReadFile(reportPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read verify report: %v", err))
			return
		}
		var report verifier.Report
		if err := json.Unmarshal(reportData, &report); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode verify report: %v", err))
			return
		}
		summary := verifyRunSummary{
			RunID:         runID,
			GeneratedAt:   time.Now().UTC(),
			TenantID:      tenantID,
			GraphID:       graphID,
			SnapshotID:    report.SnapshotID,
			InBundle:      inBundle,
			OutDir:        outDir,
			OutBundle:     outBundle,
			ReportPath:    reportPath,
			QueuePath:     queuePath,
			Verified:      report.VerifiedCount,
			NeedsReview:   report.NeedsReviewCount,
			Disputed:      report.DisputedCount,
			QueueItems:    report.ReviewQueueItems,
			LowConfidence: report.UnresolvedLowConfidence,
		}
		if err := persistVerifyRun(graphRoot, summary); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "verify_run_succeeded", map[string]any{
			"run_id":             runID,
			"tenant_id":          tenantID,
			"graph_id":           graphID,
			"review_queue_items": report.ReviewQueueItems,
			"disputed":           report.DisputedCount,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"run":    summary,
			"report": report,
		})
	}
}

func handleVerifyRuns(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		index, err := loadVerifyRunIndex(graphRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		index.Runs = filterVerifyRunsByTenant(r, index.Runs)
		graphID := strings.TrimSpace(r.URL.Query().Get("graph_id"))
		if graphID != "" {
			filtered := make([]verifyRunSummary, 0, len(index.Runs))
			for _, run := range index.Runs {
				if strings.EqualFold(strings.TrimSpace(run.GraphID), graphID) {
					filtered = append(filtered, run)
				}
			}
			index.Runs = filtered
		}
		limit := 0
		if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "invalid limit")
				return
			}
			limit = n
		}
		before := strings.TrimSpace(r.URL.Query().Get("before"))
		start := 0
		if before != "" {
			found := -1
			for i, item := range index.Runs {
				if item.RunID == before {
					found = i
					break
				}
			}
			if found == -1 {
				writeError(w, http.StatusBadRequest, "invalid before cursor")
				return
			}
			start = found + 1
		}
		if start > len(index.Runs) {
			start = len(index.Runs)
		}
		runs := index.Runs[start:]
		nextBefore := ""
		if limit > 0 && len(runs) > limit {
			runs = runs[:limit]
			nextBefore = runs[len(runs)-1].RunID
		}
		writeJSON(w, http.StatusOK, verifyRunIndex{Runs: runs, NextBefore: nextBefore})
	}
}

func handleVerifyRunByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/verify/runs/")
		rest = strings.TrimSpace(strings.Trim(rest, "/"))
		if rest == "" {
			writeError(w, http.StatusBadRequest, "run id is required")
			return
		}
		parts := strings.Split(rest, "/")
		runID := strings.TrimSpace(parts[0])
		action := ""
		if len(parts) > 1 {
			action = strings.ToLower(strings.TrimSpace(strings.Join(parts[1:], "/")))
		}
		run, err := loadVerifyRunByID(graphRoot, runID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "verify run not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, err := security.ContextFromHeaders(r.Header)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if !authCtx.HasRole("platform_admin") && normalizeTenant(run.TenantID) != normalizeTenant(authCtx.TenantID) {
			writeError(w, http.StatusForbidden, "tenant_mismatch")
			return
		}
		switch r.Method {
		case http.MethodGet:
		default:
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		switch action {
		case "":
			writeJSON(w, http.StatusOK, run)
			return
		case "report":
			payload, err := readJSONMap(run.ReportPath)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "verify report not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"run": run, "report": payload})
			return
		case "queue":
			payload, err := readJSONMap(run.QueuePath)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "review queue not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"run": run, "review_queue": payload})
			return
		default:
			writeError(w, http.StatusNotFound, "verify run subresource not found")
			return
		}
	}
}

func runtimeClaimSections(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		out["exposure"] = struct{}{}
		out["dependencies"] = struct{}{}
		return out
	}
	for _, p := range strings.Split(raw, ",") {
		s := strings.ToLower(strings.TrimSpace(p))
		if s == "" {
			continue
		}
		out[s] = struct{}{}
	}
	if len(out) == 0 {
		out["exposure"] = struct{}{}
		out["dependencies"] = struct{}{}
	}
	return out
}

func buildRuntimeClaims(graphID string, graph graphschema.Graph, sections map[string]struct{}, includeNodes bool, includeEdges bool, policy publishPolicy) []contracts.RuntimeClaim {
	claims := make([]contracts.RuntimeClaim, 0)
	if includeNodes {
		for _, n := range graph.Nodes {
			sec := strings.ToLower(strings.TrimSpace(n.Section))
			if _, ok := sections[sec]; !ok {
				continue
			}
			if !shouldKeepByPublishPolicy(n.Section, n.VerificationState, n.Attributes, n.Inferred, policy) {
				continue
			}
			claims = append(claims, contracts.RuntimeClaim{
				GraphID: graphID,
				NodeID:  n.ID,
				Class:   n.Class,
				Section: n.Section,
				Metadata: map[string]string{
					"type":               n.Type,
					"label":              n.Label,
					"service_id":         n.ServiceID,
					"verification_state": effectiveVerificationState(n.VerificationState, n.Attributes, n.Inferred),
				},
			})
		}
	}
	if includeEdges {
		for _, e := range graph.Edges {
			sec := strings.ToLower(strings.TrimSpace(e.Section))
			if _, ok := sections[sec]; !ok {
				continue
			}
			if !shouldKeepByPublishPolicy(e.Section, e.VerificationState, e.Attributes, e.Inferred, policy) {
				continue
			}
			claims = append(claims, contracts.RuntimeClaim{
				GraphID:  graphID,
				EdgeID:   e.ID,
				Class:    e.Class,
				Section:  e.Section,
				Evidence: evidenceRefStrings(e.EvidenceRefs),
				Metadata: map[string]string{
					"type":               e.Type,
					"source_id":          e.SourceID,
					"target_id":          e.TargetID,
					"verification_state": effectiveVerificationState(e.VerificationState, e.Attributes, e.Inferred),
				},
			})
		}
	}
	sort.Slice(claims, func(i, j int) bool {
		ik := strings.TrimSpace(claims[i].EdgeID)
		if ik == "" {
			ik = strings.TrimSpace(claims[i].NodeID)
		}
		jk := strings.TrimSpace(claims[j].EdgeID)
		if jk == "" {
			jk = strings.TrimSpace(claims[j].NodeID)
		}
		return ik < jk
	})
	return claims
}

func evidenceRefStrings(refs []graphschema.EvidenceRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if path := strings.TrimSpace(ref.FilePath); path != "" {
			if ref.StartLine > 0 {
				out = append(out, fmt.Sprintf("%s:%d", path, ref.StartLine))
			} else {
				out = append(out, path)
			}
			continue
		}
		if id := strings.TrimSpace(ref.EvidenceID); id != "" {
			out = append(out, "evidence:"+id)
		}
	}
	return out
}

func countSeverity(findings []productFinding, severity string) int {
	n := 0
	for _, f := range findings {
		if strings.EqualFold(f.Severity, severity) {
			n++
		}
	}
	return n
}

func buildDocsOverview(graph graphschema.Graph) map[string]any {
	return map[string]any{
		"services":        countNodesByType(graph.Nodes, "service"),
		"endpoints":       countNodesByType(graph.Nodes, "endpoint"),
		"queues":          countNodesByType(graph.Nodes, "queue"),
		"databases":       countNodesByType(graph.Nodes, "database"),
		"dependencies":    countNodesByType(graph.Nodes, "dependency"),
		"deployments":     countNodesByType(graph.Nodes, "deployment"),
		"infra_resources": countNodesByType(graph.Nodes, "infra_resource"),
	}
}

func buildRuntimeIntelligenceReport(graph graphschema.Graph) map[string]any {
	return map[string]any{
		"runtime_units":                 countNodesByType(graph.Nodes, "runtime_unit"),
		"pipeline_steps":                countNodesByType(graph.Nodes, "pipeline_step"),
		"build_artifacts":               countNodesByType(graph.Nodes, "build_artifact"),
		"deployments":                   countNodesByType(graph.Nodes, "deployment"),
		"infra_resources":               countNodesByType(graph.Nodes, "infra_resource"),
		"service_runtime_edges":         countEdgesByType(graph.Edges, "service_has_runtime_unit"),
		"service_pipeline_edges":        countEdgesByType(graph.Edges, "service_built_by_pipeline_step"),
		"pipeline_artifact_edges":       countEdgesByType(graph.Edges, "pipeline_step_produces_artifact"),
		"artifact_deployment_edges":     countEdgesByType(graph.Edges, "artifact_deployed_via"),
		"runtime_config_edges":          countEdgesByType(graph.Edges, "runtime_unit_reads_config"),
		"runtime_dependency_call_edges": countEdgesByType(graph.Edges, "runtime_unit_calls_dependency"),
	}
}

func buildTopologyReport(graph graphschema.Graph) map[string]any {
	serviceIDs := map[string]struct{}{}
	for _, n := range graph.Nodes {
		if strings.EqualFold(n.Type, "service") {
			serviceIDs[n.ID] = struct{}{}
		}
	}
	fanOut := map[string]int{}
	fanIn := map[string]int{}
	internalServiceCalls := 0
	externalDependencyEdges := 0
	for _, e := range graph.Edges {
		switch e.Type {
		case "service_calls_service":
			if _, ok := serviceIDs[e.SourceID]; ok {
				if _, ok := serviceIDs[e.TargetID]; ok {
					internalServiceCalls++
					fanOut[e.SourceID]++
					fanIn[e.TargetID]++
				}
			}
		case "service_calls_dependency", "service_depends_on_dependency", "service_publishes_queue", "service_reads_db", "service_writes_db":
			externalDependencyEdges++
		}
	}
	serviceNeighbors := map[string]map[string]struct{}{}
	for _, e := range graph.Edges {
		if e.Type != "service_calls_service" {
			continue
		}
		if _, ok := serviceIDs[e.SourceID]; !ok {
			continue
		}
		if _, ok := serviceIDs[e.TargetID]; !ok {
			continue
		}
		if _, ok := serviceNeighbors[e.SourceID]; !ok {
			serviceNeighbors[e.SourceID] = map[string]struct{}{}
		}
		serviceNeighbors[e.SourceID][e.TargetID] = struct{}{}
	}
	cycles := estimateServiceCycleCount(serviceNeighbors)
	isolatedServices := 0
	for id := range serviceIDs {
		if fanIn[id] == 0 && fanOut[id] == 0 {
			isolatedServices++
		}
	}
	return map[string]any{
		"services":                    len(serviceIDs),
		"dependency_nodes":            countNodesByType(graph.Nodes, "dependency"),
		"queue_nodes":                 countNodesByType(graph.Nodes, "queue"),
		"database_nodes":              countNodesByType(graph.Nodes, "database"),
		"internal_service_call_edges": internalServiceCalls,
		"external_dependency_edges":   externalDependencyEdges,
		"isolated_services":           isolatedServices,
		"estimated_service_cycles":    cycles,
		"top_fan_out":                 topMetricItems(fanOut, nodeLabels(graph.Nodes), 10),
		"top_fan_in":                  topMetricItems(fanIn, nodeLabels(graph.Nodes), 10),
	}
}

func estimateServiceCycleCount(adj map[string]map[string]struct{}) int {
	type colorState uint8
	const (
		colorWhite colorState = iota
		colorGray
		colorBlack
	)
	color := map[string]colorState{}
	for src := range adj {
		color[src] = colorWhite
		for dst := range adj[src] {
			if _, ok := color[dst]; !ok {
				color[dst] = colorWhite
			}
		}
	}
	cycles := 0
	var visit func(string)
	visit = func(node string) {
		color[node] = colorGray
		for next := range adj[node] {
			switch color[next] {
			case colorWhite:
				visit(next)
			case colorGray:
				cycles++
			}
		}
		color[node] = colorBlack
	}
	for node, state := range color {
		if state == colorWhite {
			visit(node)
		}
	}
	return cycles
}

func buildCompanyIdentityReport(graph graphschema.Graph) map[string]any {
	repos := map[string]struct{}{}
	for _, svc := range graph.Meta.Services {
		repo := strings.TrimSpace(svc.RepoPath)
		if repo != "" {
			repos[repo] = struct{}{}
		}
	}
	crossRepoCalls := 0
	for _, e := range graph.Edges {
		if e.Type != "service_calls_service" {
			continue
		}
		srcRepo := strings.TrimSpace(fmt.Sprint(e.Attributes["source_repo_path"]))
		dstRepo := strings.TrimSpace(fmt.Sprint(e.Attributes["target_repo_path"]))
		if srcRepo != "" && dstRepo != "" && !strings.EqualFold(srcRepo, dstRepo) {
			crossRepoCalls++
		}
	}
	return map[string]any{
		"services":                        countNodesByType(graph.Nodes, "service"),
		"repositories":                    len(repos),
		"canonical_service_nodes":         countNodesByType(graph.Nodes, "canonical_service"),
		"canonical_queue_nodes":           countNodesByType(graph.Nodes, "canonical_queue"),
		"canonical_database_nodes":        countNodesByType(graph.Nodes, "canonical_database"),
		"canonical_api_host_nodes":        countNodesByType(graph.Nodes, "canonical_api_host"),
		"service_alias_edges":             countEdgesByType(graph.Edges, "service_alias_of_canonical_service"),
		"queue_alias_edges":               countEdgesByType(graph.Edges, "queue_alias_of_canonical_queue"),
		"database_alias_edges":            countEdgesByType(graph.Edges, "database_alias_of_canonical_database"),
		"api_host_alias_edges":            countEdgesByType(graph.Edges, "service_alias_of_canonical_api_host"),
		"cross_repo_service_call_edges":   crossRepoCalls,
		"top_canonical_service_clusters":  topCanonicalMembers(graph.Nodes, "canonical_service", 10),
		"top_canonical_queue_clusters":    topCanonicalMembers(graph.Nodes, "canonical_queue", 10),
		"top_canonical_database_clusters": topCanonicalMembers(graph.Nodes, "canonical_database", 10),
		"top_canonical_api_host_clusters": topCanonicalMembers(graph.Nodes, "canonical_api_host", 10),
	}
}

func buildTrustReport(graphRoot string, graphID string, graph graphschema.Graph) map[string]any {
	nodeStates := map[string]int{}
	edgeStates := map[string]int{}
	lowConfidenceNodes := 0
	lowConfidenceEdges := 0
	nodeConfidenceSum := 0.0
	edgeConfidenceSum := 0.0
	nodeBuckets := map[string]int{
		"0.00-0.49": 0,
		"0.50-0.74": 0,
		"0.75-0.89": 0,
		"0.90-1.00": 0,
	}
	edgeBuckets := map[string]int{
		"0.00-0.49": 0,
		"0.50-0.74": 0,
		"0.75-0.89": 0,
		"0.90-1.00": 0,
	}
	for _, n := range graph.Nodes {
		state := effectiveVerificationState(n.VerificationState, n.Attributes, n.Inferred)
		nodeStates[state]++
		nodeConfidenceSum += n.Confidence
		if n.Confidence < 0.75 {
			lowConfidenceNodes++
		}
		nodeBuckets[confidenceBucket(n.Confidence)]++
	}
	for _, e := range graph.Edges {
		state := effectiveVerificationState(e.VerificationState, e.Attributes, e.Inferred)
		edgeStates[state]++
		edgeConfidenceSum += e.Confidence
		if e.Confidence < 0.75 {
			lowConfidenceEdges++
		}
		edgeBuckets[confidenceBucket(e.Confidence)]++
	}
	adjIndex, err := loadAdjudicationIndex(graphRoot, graphID)
	if err != nil {
		adjIndex = adjudicationIndex{GraphID: graphID, Adjudications: []adjudicationRecord{}}
	}
	conflictStore := buildConflictStore(graph, graphID)
	return map[string]any{
		"nodes": map[string]any{
			"total":                     len(graph.Nodes),
			"verification_state_counts": nodeStates,
			"low_confidence_count":      lowConfidenceNodes,
			"confidence_avg":            averageConfidence(nodeConfidenceSum, len(graph.Nodes)),
			"confidence_buckets":        nodeBuckets,
		},
		"edges": map[string]any{
			"total":                     len(graph.Edges),
			"verification_state_counts": edgeStates,
			"low_confidence_count":      lowConfidenceEdges,
			"confidence_avg":            averageConfidence(edgeConfidenceSum, len(graph.Edges)),
			"confidence_buckets":        edgeBuckets,
		},
		"conflicts": map[string]any{
			"open":     conflictStore["open"],
			"resolved": conflictStore["resolved"],
			"total":    conflictStore["total"],
			"by_type":  conflictStore["by_type"],
		},
		"adjudications": buildAdjudicationSummary(adjIndex),
	}
}

func buildArchitectureTaskReport(graph graphschema.Graph, focusNodeID string) map[string]any {
	nodesByID := map[string]graphschema.Node{}
	serviceNodeByServiceID := map[string]string{}
	for _, n := range graph.Nodes {
		nodesByID[n.ID] = n
		if n.Type == "service" {
			sid := strings.TrimSpace(n.ServiceID)
			if sid == "" {
				sid = strings.TrimSpace(strings.TrimPrefix(n.ID, "svc:"))
			}
			if sid != "" {
				serviceNodeByServiceID[sid] = n.ID
			}
		}
	}
	focusNodeID = strings.TrimSpace(focusNodeID)
	if focusNodeID == "" || nodesByID[focusNodeID].ID == "" {
		focusNodeID = selectArchitectureFocusNodeID(graph.Nodes)
	}
	focusNodeLabel := ""
	if node, ok := nodesByID[focusNodeID]; ok {
		focusNodeLabel = strings.TrimSpace(node.Label)
	}

	exposureIDs := make([]string, 0)
	endpointIDs := make([]string, 0)
	schedulerIDs := make([]string, 0)
	dependencyIDs := make([]string, 0)
	queueIDs := make([]string, 0)
	for _, n := range graph.Nodes {
		section := strings.ToLower(strings.TrimSpace(n.Section))
		if section == "exposure" {
			exposureIDs = append(exposureIDs, n.ID)
		}
		if section == "dependencies" {
			dependencyIDs = append(dependencyIDs, n.ID)
		}
		t := strings.ToLower(strings.TrimSpace(n.Type))
		if t == "endpoint" && section == "exposure" {
			endpointIDs = append(endpointIDs, n.ID)
		}
		if t == "queue" || t == "topic" {
			queueIDs = append(queueIDs, n.ID)
		}
		classLower := strings.ToLower(strings.TrimSpace(n.Class))
		labelLower := strings.ToLower(strings.TrimSpace(n.Label))
		if section == "exposure" && (strings.Contains(classLower, "scheduler") || strings.Contains(labelLower, "cron") || strings.Contains(labelLower, "schedule")) {
			schedulerIDs = append(schedulerIDs, n.ID)
		}
	}
	sort.Strings(exposureIDs)
	sort.Strings(endpointIDs)
	sort.Strings(schedulerIDs)
	sort.Strings(dependencyIDs)
	sort.Strings(queueIDs)

	endpointServiceNodes := serviceNodesForIDs(nodesByID, serviceNodeByServiceID, endpointIDs)
	schedulerServiceNodes := serviceNodesForIDs(nodesByID, serviceNodeByServiceID, schedulerIDs)
	queueNodeSet := map[string]struct{}{}
	for _, id := range queueIDs {
		queueNodeSet[id] = struct{}{}
	}
	topologyEdgeTypes := map[string]struct{}{
		"service_calls_service":   {},
		"service_calls_endpoint":  {},
		"service_publishes_queue": {},
		"service_reads_db":        {},
		"service_writes_db":       {},
	}
	queueEdgeTypes := map[string]struct{}{
		"service_publishes_queue":   {},
		"queue_delivers_to_service": {},
	}
	endpointDependenciesConnected := hasEdgeConnectedToSet(graph.Edges, endpointServiceNodes, topologyEdgeTypes)
	schedulerDependenciesConnected := hasEdgeConnectedToSet(graph.Edges, schedulerServiceNodes, topologyEdgeTypes)
	queueConnected := hasEdgeConnectedToSet(graph.Edges, queueNodeSet, queueEdgeTypes)
	focusedSubgraph := buildFocusedSubgraph(graph, focusNodeID)
	focusedNodeCount := 0
	focusedEdgeCount := 0
	if nodes, ok := focusedSubgraph["nodes"].([]graphschema.Node); ok {
		focusedNodeCount = len(nodes)
	}
	if edges, ok := focusedSubgraph["edges"].([]graphschema.Edge); ok {
		focusedEdgeCount = len(edges)
	}
	focusApplicable := strings.TrimSpace(focusNodeID) != ""
	focusPassed := focusApplicable && focusedNodeCount > 0

	tasks := map[string]map[string]any{
		"find_exposures": {
			"applicable": true,
			"passed":     len(exposureIDs) > 0,
			"expected":   "at least one exposure node exists",
			"observed": map[string]any{
				"exposure_count": len(exposureIDs),
			},
		},
		"trace_endpoint_to_dependencies": {
			"applicable": len(endpointIDs) > 0 && len(dependencyIDs) > 0,
			"passed":     (len(endpointIDs) == 0 || len(dependencyIDs) == 0) || endpointDependenciesConnected,
			"expected":   "endpoint-exposing service can be traced to dependency-bearing edges",
			"observed": map[string]any{
				"endpoint_count":         len(endpointIDs),
				"dependency_count":       len(dependencyIDs),
				"endpoint_service_count": len(endpointServiceNodes),
			},
		},
		"identify_queue_consumers_publishers": {
			"applicable": len(queueIDs) > 0,
			"passed":     len(queueIDs) == 0 || queueConnected,
			"expected":   "queue/topic nodes have producer or consumer edges",
			"observed": map[string]any{
				"queue_node_count": len(queueIDs),
			},
		},
		"trace_scheduler_trigger_paths": {
			"applicable": len(schedulerIDs) > 0 && len(dependencyIDs) > 0,
			"passed":     (len(schedulerIDs) == 0 || len(dependencyIDs) == 0) || schedulerDependenciesConnected,
			"expected":   "scheduler-exposing service can be traced to dependency-bearing edges",
			"observed": map[string]any{
				"scheduler_count":         len(schedulerIDs),
				"dependency_count":        len(dependencyIDs),
				"scheduler_service_count": len(schedulerServiceNodes),
			},
		},
		"export_focused_subgraph": {
			"applicable": focusApplicable,
			"passed":     focusPassed,
			"expected":   "focused subgraph export target node exists and artifact is produced",
			"observed": map[string]any{
				"focus_node_id":    focusNodeID,
				"focus_node_label": focusNodeLabel,
				"subgraph_nodes":   focusedNodeCount,
				"subgraph_edges":   focusedEdgeCount,
			},
		},
	}

	totalTasks := len(tasks)
	applicableTasks := 0
	passedTasks := 0
	for _, task := range tasks {
		applicable := parseBoolAny(task["applicable"])
		passed := parseBoolAny(task["passed"])
		if applicable {
			applicableTasks++
			if passed {
				passedTasks++
			}
		}
	}
	passRate := 1.0
	if applicableTasks > 0 {
		passRate = float64(passedTasks) / float64(applicableTasks)
	}
	return map[string]any{
		"generated_at_utc": time.Now().UTC(),
		"focus_node_id":    focusNodeID,
		"focus_node_label": focusNodeLabel,
		"tasks":            tasks,
		"summary": map[string]any{
			"total_tasks":      totalTasks,
			"applicable_tasks": applicableTasks,
			"passed_tasks":     passedTasks,
			"pass_rate":        passRate,
		},
	}
}

func parseBoolAny(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}

func selectArchitectureFocusNodeID(nodes []graphschema.Node) string {
	firstExposure := ""
	firstService := ""
	for _, n := range nodes {
		section := strings.ToLower(strings.TrimSpace(n.Section))
		if section == "exposure" && strings.EqualFold(n.Type, "endpoint") {
			return n.ID
		}
		if firstExposure == "" && section == "exposure" {
			firstExposure = n.ID
		}
		if firstService == "" && strings.EqualFold(n.Type, "service") {
			firstService = n.ID
		}
	}
	if firstExposure != "" {
		return firstExposure
	}
	return firstService
}

func serviceNodesForIDs(nodesByID map[string]graphschema.Node, serviceNodeByServiceID map[string]string, ids []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		n, ok := nodesByID[id]
		if !ok {
			continue
		}
		sid := strings.TrimSpace(n.ServiceID)
		if sid == "" {
			continue
		}
		if serviceNodeID, ok := serviceNodeByServiceID[sid]; ok {
			out[serviceNodeID] = struct{}{}
		}
	}
	return out
}

func hasEdgeConnectedToSet(edges []graphschema.Edge, nodeIDs map[string]struct{}, allowedTypes map[string]struct{}) bool {
	if len(nodeIDs) == 0 || len(allowedTypes) == 0 {
		return false
	}
	for _, e := range edges {
		if _, ok := allowedTypes[e.Type]; !ok {
			continue
		}
		if _, ok := nodeIDs[e.SourceID]; ok {
			return true
		}
		if _, ok := nodeIDs[e.TargetID]; ok {
			return true
		}
	}
	return false
}

func buildFocusedSubgraph(graph graphschema.Graph, focusNodeID string) map[string]any {
	focusNodeID = strings.TrimSpace(focusNodeID)
	if focusNodeID == "" {
		return map[string]any{
			"graph_id":      graph.GraphID,
			"focus_node_id": "",
			"nodes":         []graphschema.Node{},
			"edges":         []graphschema.Edge{},
			"meta": map[string]any{
				"node_count":       0,
				"edge_count":       0,
				"exported_at_utc":  time.Now().UTC(),
				"focus_node_found": false,
			},
		}
	}
	nodeByID := map[string]graphschema.Node{}
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}
	if _, ok := nodeByID[focusNodeID]; !ok {
		return map[string]any{
			"graph_id":      graph.GraphID,
			"focus_node_id": focusNodeID,
			"nodes":         []graphschema.Node{},
			"edges":         []graphschema.Edge{},
			"meta": map[string]any{
				"node_count":       0,
				"edge_count":       0,
				"exported_at_utc":  time.Now().UTC(),
				"focus_node_found": false,
			},
		}
	}
	keep := map[string]struct{}{focusNodeID: {}}
	edges := make([]graphschema.Edge, 0)
	for _, e := range graph.Edges {
		if e.SourceID == focusNodeID || e.TargetID == focusNodeID {
			keep[e.SourceID] = struct{}{}
			keep[e.TargetID] = struct{}{}
			edges = append(edges, e)
		}
	}
	nodes := make([]graphschema.Node, 0, len(keep))
	for _, n := range graph.Nodes {
		if _, ok := keep[n.ID]; ok {
			nodes = append(nodes, n)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return map[string]any{
		"graph_id":      graph.GraphID,
		"focus_node_id": focusNodeID,
		"nodes":         nodes,
		"edges":         edges,
		"meta": map[string]any{
			"node_count":       len(nodes),
			"edge_count":       len(edges),
			"exported_at_utc":  time.Now().UTC(),
			"focus_node_found": true,
		},
	}
}

func buildConflictStore(graph graphschema.Graph, graphID string) map[string]any {
	nodeByID := map[string]graphschema.Node{}
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}
	conflicts := make([]map[string]any, 0)
	byType := map[string]int{}
	open := 0
	resolved := 0
	for _, n := range graph.Nodes {
		if n.Type != "conflict" {
			continue
		}
		status := conflictStatusFromAttrs(n.Attributes)
		if status == "resolved" {
			resolved++
		} else {
			open++
		}
		conflictType := strings.TrimSpace(fmt.Sprint(n.Attributes["conflict_type"]))
		if conflictType == "" {
			conflictType = "generic"
		}
		byType[conflictType]++
		relatedServices := map[string]struct{}{}
		for _, e := range graph.Edges {
			if e.TargetID != n.ID || e.Type != "service_has_conflict" {
				continue
			}
			srcNode, ok := nodeByID[e.SourceID]
			if !ok {
				continue
			}
			if sid := strings.TrimSpace(srcNode.ServiceID); sid != "" {
				relatedServices[sid] = struct{}{}
			}
		}
		services := make([]string, 0, len(relatedServices))
		for sid := range relatedServices {
			services = append(services, sid)
		}
		sort.Strings(services)
		conflicts = append(conflicts, map[string]any{
			"id":               n.ID,
			"label":            n.Label,
			"service_id":       n.ServiceID,
			"status":           status,
			"conflict_type":    conflictType,
			"verification":     effectiveVerificationState(n.VerificationState, n.Attributes, n.Inferred),
			"confidence":       n.Confidence,
			"related_services": services,
			"attributes":       n.Attributes,
		})
	}
	sort.Slice(conflicts, func(i, j int) bool {
		left := strings.TrimSpace(fmt.Sprint(conflicts[i]["id"]))
		right := strings.TrimSpace(fmt.Sprint(conflicts[j]["id"]))
		return left < right
	})
	return map[string]any{
		"graph_id":  graphID,
		"total":     len(conflicts),
		"open":      open,
		"resolved":  resolved,
		"by_type":   byType,
		"conflicts": conflicts,
	}
}

func buildAdjudicationSummary(index adjudicationIndex) map[string]any {
	decisions := map[string]int{}
	targetKinds := map[string]int{}
	byActor := map[string]int{}
	var lastUpdated time.Time
	for _, rec := range index.Adjudications {
		decisions[rec.Decision]++
		targetKinds[rec.TargetKind]++
		actor := strings.TrimSpace(rec.Actor)
		if actor == "" {
			actor = "unknown"
		}
		byActor[actor]++
		if rec.UpdatedAt.After(lastUpdated) {
			lastUpdated = rec.UpdatedAt
		}
	}
	return map[string]any{
		"graph_id":       index.GraphID,
		"total":          len(index.Adjudications),
		"by_decision":    decisions,
		"by_target_kind": targetKinds,
		"by_actor":       byActor,
		"last_updated":   lastUpdated,
	}
}

func buildAdjudicationRecord(graph graphschema.Graph, graphID string, tenantID string, req adjudicationRequest, authCtx security.Context) (adjudicationRecord, error) {
	targetID := strings.TrimSpace(req.TargetID)
	if targetID == "" {
		return adjudicationRecord{}, fmt.Errorf("target_id is required")
	}
	decision := normalizeAdjudicationDecision(req.Decision)
	if decision == "" {
		return adjudicationRecord{}, fmt.Errorf("decision must be one of verified|needs_review|disputed|resolved")
	}
	nodeByID := map[string]graphschema.Node{}
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}
	edgeByID := map[string]graphschema.Edge{}
	for _, e := range graph.Edges {
		edgeByID[e.ID] = e
	}
	targetKind := strings.ToLower(strings.TrimSpace(req.TargetKind))
	if targetKind == "" {
		if _, ok := nodeByID[targetID]; ok {
			targetKind = "node"
		} else if _, ok := edgeByID[targetID]; ok {
			targetKind = "edge"
		}
	}
	now := time.Now().UTC()
	record := adjudicationRecord{
		ID:         fmt.Sprintf("adj:%d", now.UnixNano()),
		GraphID:    graphID,
		TenantID:   normalizeTenant(tenantID),
		TargetID:   targetID,
		TargetKind: targetKind,
		Decision:   decision,
		Reason:     strings.TrimSpace(req.Reason),
		Source:     firstNonEmptyTrimmed(req.Source, "manual"),
		Actor:      firstNonEmptyTrimmed(req.Actor, authCtx.Principal),
		Attributes: req.Attributes,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	switch targetKind {
	case "node", "":
		n, ok := nodeByID[targetID]
		if !ok {
			return adjudicationRecord{}, fmt.Errorf("target node not found")
		}
		record.TargetKind = "node"
		record.TargetType = n.Type
		record.ServiceID = n.ServiceID
		record.ConfidenceBefore = n.Confidence
		record.VerificationBefore = effectiveVerificationState(n.VerificationState, n.Attributes, n.Inferred)
	case "edge":
		e, ok := edgeByID[targetID]
		if !ok {
			return adjudicationRecord{}, fmt.Errorf("target edge not found")
		}
		record.TargetType = e.Type
		record.ConfidenceBefore = e.Confidence
		record.VerificationBefore = effectiveVerificationState(e.VerificationState, e.Attributes, e.Inferred)
	default:
		return adjudicationRecord{}, fmt.Errorf("target_kind must be node or edge")
	}
	record.VerificationAfter = decision
	record.ConfidenceAfter = record.ConfidenceBefore
	if req.Confidence != nil {
		if *req.Confidence < 0 || *req.Confidence > 1 {
			return adjudicationRecord{}, fmt.Errorf("confidence must be between 0 and 1")
		}
		record.ConfidenceAfter = *req.Confidence
	}
	return record, nil
}

func normalizeAdjudicationDecision(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "verified":
		return "verified"
	case "needs_review":
		return "needs_review"
	case "disputed":
		return "disputed"
	case "resolved":
		return "resolved"
	default:
		return ""
	}
}

func conflictStatusFromAttrs(attrs map[string]any) string {
	if attrs == nil {
		return "open"
	}
	for _, key := range []string{"status", "conflict_status"} {
		if raw, ok := attrs[key]; ok {
			v := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
			if v == "resolved" || v == "open" {
				return v
			}
		}
	}
	return "open"
}

func confidenceBucket(conf float64) string {
	switch {
	case conf < 0.50:
		return "0.00-0.49"
	case conf < 0.75:
		return "0.50-0.74"
	case conf < 0.90:
		return "0.75-0.89"
	default:
		return "0.90-1.00"
	}
}

func averageConfidence(sum float64, count int) float64 {
	if count <= 0 {
		return 0
	}
	return sum / float64(count)
}

func topCanonicalMembers(nodes []graphschema.Node, nodeType string, limit int) []map[string]any {
	type member struct {
		id    string
		label string
		count int
	}
	list := make([]member, 0)
	for _, n := range nodes {
		if n.Type != nodeType {
			continue
		}
		count := 0
		if raw, ok := n.Attributes["member_count"]; ok {
			switch v := raw.(type) {
			case float64:
				count = int(v)
			case int:
				count = v
			case int64:
				count = int(v)
			case json.Number:
				if parsed, err := v.Int64(); err == nil {
					count = int(parsed)
				}
			case string:
				if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					count = parsed
				}
			}
		}
		if count == 0 {
			count = 1
		}
		list = append(list, member{id: n.ID, label: n.Label, count: count})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count == list[j].count {
			return list[i].label < list[j].label
		}
		return list[i].count > list[j].count
	})
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		out = append(out, map[string]any{
			"node_id":        it.id,
			"label":          it.label,
			"member_count":   it.count,
			"canonical_type": nodeType,
		})
	}
	return out
}

func buildDocsOperations(graph graphschema.Graph) map[string]any {
	return map[string]any{
		"top_callers":      topMetricItems(countOutgoing(graph.Edges), nodeLabels(graph.Nodes), 10),
		"top_dependencies": topMetricItems(countIncoming(graph.Edges), nodeLabels(graph.Nodes), 10),
		"edge_types":       graph.Stats.ByEdge,
	}
}

func buildServiceImpactSubgraph(graph graphschema.Graph, serviceID string) graphschema.Graph {
	includeNodes := map[string]struct{}{}
	for _, n := range graph.Nodes {
		if n.ServiceID == serviceID || n.ID == serviceID {
			includeNodes[n.ID] = struct{}{}
		}
	}
	changed := true
	for changed {
		changed = false
		for _, e := range graph.Edges {
			_, src := includeNodes[e.SourceID]
			_, dst := includeNodes[e.TargetID]
			if src || dst {
				if _, ok := includeNodes[e.SourceID]; !ok {
					includeNodes[e.SourceID] = struct{}{}
					changed = true
				}
				if _, ok := includeNodes[e.TargetID]; !ok {
					includeNodes[e.TargetID] = struct{}{}
					changed = true
				}
			}
		}
	}
	nodes := make([]graphschema.Node, 0)
	for _, n := range graph.Nodes {
		if _, ok := includeNodes[n.ID]; ok {
			nodes = append(nodes, n)
		}
	}
	edges := make([]graphschema.Edge, 0)
	for _, e := range graph.Edges {
		_, src := includeNodes[e.SourceID]
		_, dst := includeNodes[e.TargetID]
		if src && dst {
			edges = append(edges, e)
		}
	}
	graph.Nodes = nodes
	graph.Edges = edges
	graph.Stats = recomputeGraphStats(nodes, edges)
	return graph
}

func buildIncrementalPlan(graph graphschema.Graph, req incrementalPlanRequest) incrementalPlan {
	changedSet := map[string]struct{}{}
	changedFiles := make([]string, 0, len(req.ChangedFiles))
	for _, item := range req.ChangedFiles {
		normalized := normalizeChangedPath(item)
		if normalized == "" {
			continue
		}
		if _, ok := changedSet[normalized]; ok {
			continue
		}
		changedSet[normalized] = struct{}{}
		changedFiles = append(changedFiles, normalized)
	}
	sort.Strings(changedFiles)
	nodeByID := map[string]graphschema.Node{}
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}
	seedNodes := map[string]struct{}{}
	impactedByReason := map[string][]string{
		"node_attributes": {},
		"edge_evidence":   {},
	}
	impactedEdges := map[string]struct{}{}
	for _, n := range graph.Nodes {
		if nodeReferencesChangedFile(n, changedSet) {
			seedNodes[n.ID] = struct{}{}
			impactedByReason["node_attributes"] = append(impactedByReason["node_attributes"], n.ID)
		}
	}
	for _, e := range graph.Edges {
		if edgeReferencesChangedFile(e, changedSet) {
			impactedEdges[e.ID] = struct{}{}
			seedNodes[e.SourceID] = struct{}{}
			seedNodes[e.TargetID] = struct{}{}
			impactedByReason["edge_evidence"] = append(impactedByReason["edge_evidence"], e.ID)
		}
	}
	seeds := make([]string, 0, len(seedNodes))
	for id := range seedNodes {
		if _, ok := nodeByID[id]; ok {
			seeds = append(seeds, id)
		}
	}
	sort.Strings(seeds)
	for reason := range impactedByReason {
		sort.Strings(impactedByReason[reason])
	}

	seedSet := map[string]struct{}{}
	for _, id := range seeds {
		seedSet[id] = struct{}{}
	}
	subgraph := buildNeighborhoodSubgraph(graph, seedSet, req.Hops)
	impactedNodeIDs := make([]string, 0, len(subgraph.Nodes))
	for _, n := range subgraph.Nodes {
		impactedNodeIDs = append(impactedNodeIDs, n.ID)
	}
	sort.Strings(impactedNodeIDs)
	impactedEdgeIDs := make([]string, 0, len(subgraph.Edges))
	for _, e := range subgraph.Edges {
		impactedEdgeIDs = append(impactedEdgeIDs, e.ID)
	}
	sort.Strings(impactedEdgeIDs)

	action := map[string]any{
		"mode":                         "targeted_incremental",
		"rebuild_scope":                "impacted_subgraph",
		"changed_files_count":          len(changedFiles),
		"impacted_nodes_count":         len(impactedNodeIDs),
		"impacted_edges_count":         len(impactedEdgeIDs),
		"suggested_confidence_recheck": len(impactedNodeIDs) > 0 || len(impactedEdgeIDs) > 0,
	}

	return incrementalPlan{
		PlanID:            fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
		GeneratedAt:       time.Now().UTC(),
		GraphID:           req.GraphID,
		ChangedFiles:      changedFiles,
		Hops:              req.Hops,
		SeedNodes:         seeds,
		ImpactedNodeIDs:   impactedNodeIDs,
		ImpactedEdgeIDs:   impactedEdgeIDs,
		ImpactedByReason:  impactedByReason,
		ImpactGraph:       subgraph,
		RecommendedAction: action,
	}
}

func normalizeChangedPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")
	return strings.ToLower(path)
}

func normalizeChangedFiles(changedFiles []string) []string {
	set := map[string]struct{}{}
	out := make([]string, 0, len(changedFiles))
	for _, item := range changedFiles {
		normalized := normalizeChangedPath(item)
		if normalized == "" {
			continue
		}
		if _, ok := set[normalized]; ok {
			continue
		}
		set[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func nodeReferencesChangedFile(n graphschema.Node, changed map[string]struct{}) bool {
	if len(changed) == 0 {
		return false
	}
	candidates := []string{
		attrString(n.Attributes, "file_path"),
		attrString(n.Attributes, "source_file"),
		attrString(n.Attributes, "path"),
	}
	for _, candidate := range candidates {
		normalized := normalizeChangedPath(candidate)
		if normalized == "" {
			continue
		}
		for changedPath := range changed {
			if normalized == changedPath || strings.HasSuffix(normalized, "/"+changedPath) || strings.HasSuffix(changedPath, "/"+normalized) {
				return true
			}
		}
	}
	return false
}

func edgeReferencesChangedFile(e graphschema.Edge, changed map[string]struct{}) bool {
	if len(changed) == 0 {
		return false
	}
	for _, ref := range e.EvidenceRefs {
		normalized := normalizeChangedPath(ref.FilePath)
		if normalized == "" {
			continue
		}
		for changedPath := range changed {
			if normalized == changedPath || strings.HasSuffix(normalized, "/"+changedPath) || strings.HasSuffix(changedPath, "/"+normalized) {
				return true
			}
		}
	}
	return false
}

func buildNeighborhoodSubgraph(graph graphschema.Graph, seeds map[string]struct{}, hops int) graphschema.Graph {
	if hops < 0 {
		hops = 0
	}
	if len(seeds) == 0 {
		graph.Nodes = []graphschema.Node{}
		graph.Edges = []graphschema.Edge{}
		graph.Stats = recomputeGraphStats(graph.Nodes, graph.Edges)
		return graph
	}
	includeNodes := map[string]struct{}{}
	frontier := map[string]struct{}{}
	for id := range seeds {
		includeNodes[id] = struct{}{}
		frontier[id] = struct{}{}
	}
	for i := 0; i < hops; i++ {
		next := map[string]struct{}{}
		for _, e := range graph.Edges {
			_, src := frontier[e.SourceID]
			_, dst := frontier[e.TargetID]
			if !src && !dst {
				continue
			}
			if _, ok := includeNodes[e.SourceID]; !ok {
				includeNodes[e.SourceID] = struct{}{}
				next[e.SourceID] = struct{}{}
			}
			if _, ok := includeNodes[e.TargetID]; !ok {
				includeNodes[e.TargetID] = struct{}{}
				next[e.TargetID] = struct{}{}
			}
		}
		frontier = next
		if len(frontier) == 0 {
			break
		}
	}

	nodes := make([]graphschema.Node, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if _, ok := includeNodes[n.ID]; ok {
			nodes = append(nodes, n)
		}
	}
	edges := make([]graphschema.Edge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		_, src := includeNodes[e.SourceID]
		_, dst := includeNodes[e.TargetID]
		if src && dst {
			edges = append(edges, e)
		}
	}
	graph.Nodes = nodes
	graph.Edges = edges
	graph.Stats = recomputeGraphStats(nodes, edges)
	return graph
}

func buildGovernancePosture(graph graphschema.Graph) string {
	highRisk := countNodesByType(graph.Nodes, "dependency_risk")
	openConflicts := countNodesByType(graph.Nodes, "conflict")
	needsReview := countNodesByAttr(graph.Nodes, "verification_status", "needs_review")
	if highRisk+openConflicts+needsReview > 20 {
		return "high_risk"
	}
	if highRisk+openConflicts+needsReview > 5 {
		return "medium_risk"
	}
	return "low_risk"
}

func countNodesByType(nodes []graphschema.Node, typ string) int {
	n := 0
	for _, node := range nodes {
		if node.Type == typ {
			n++
		}
	}
	return n
}

func countEdgesByType(edges []graphschema.Edge, typ string) int {
	n := 0
	for _, edge := range edges {
		if edge.Type == typ {
			n++
		}
	}
	return n
}

func countNodesByAttr(nodes []graphschema.Node, key string, value string) int {
	n := 0
	for _, node := range nodes {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(node.Attributes[key])), value) {
			n++
		}
	}
	return n
}

func nodeLabels(nodes []graphschema.Node) map[string]string {
	out := map[string]string{}
	for _, n := range nodes {
		out[n.ID] = n.Label
	}
	return out
}

func countOutgoing(edges []graphschema.Edge) map[string]int {
	out := map[string]int{}
	for _, e := range edges {
		out[e.SourceID]++
	}
	return out
}

func countIncoming(edges []graphschema.Edge) map[string]int {
	out := map[string]int{}
	for _, e := range edges {
		out[e.TargetID]++
	}
	return out
}

func diffNodeKeys(from graphschema.Node, to graphschema.Node) []string {
	keys := make([]string, 0, 6)
	if from.Type != to.Type {
		keys = append(keys, "type")
	}
	if from.Label != to.Label {
		keys = append(keys, "label")
	}
	if from.ServiceID != to.ServiceID {
		keys = append(keys, "service_id")
	}
	if from.Confidence != to.Confidence {
		keys = append(keys, "confidence")
	}
	if from.Inferred != to.Inferred {
		keys = append(keys, "inferred")
	}
	if !jsonEqual(from.Attributes, to.Attributes) {
		keys = append(keys, "attributes")
	}
	return keys
}

func diffEdgeKeys(from graphschema.Edge, to graphschema.Edge) []string {
	keys := make([]string, 0, 7)
	if from.Type != to.Type {
		keys = append(keys, "type")
	}
	if from.SourceID != to.SourceID {
		keys = append(keys, "source_id")
	}
	if from.TargetID != to.TargetID {
		keys = append(keys, "target_id")
	}
	if from.Confidence != to.Confidence {
		keys = append(keys, "confidence")
	}
	if from.Inferred != to.Inferred {
		keys = append(keys, "inferred")
	}
	if !jsonEqual(from.Attributes, to.Attributes) {
		keys = append(keys, "attributes")
	}
	return keys
}

func jsonEqual(a any, b any) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aj) == string(bj)
}

func parseBoolDefault(v string, d bool) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return d
	}
	return v == "1" || v == "true" || v == "yes"
}

func parseFloatDefault(v string, d float64) (float64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return d, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": message,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
