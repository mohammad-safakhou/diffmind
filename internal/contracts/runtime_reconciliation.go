package contracts

import "context"

// RuntimeObservation represents a runtime signal candidate for reconciliation.
type RuntimeObservation struct {
	SourceSystem string            `json:"source_system"`
	SignalType   string            `json:"signal_type"`
	ObservedAt   string            `json:"observed_at"`
	ServiceID    string            `json:"service_id,omitempty"`
	TargetID     string            `json:"target_id,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

// RuntimeClaim references a graph claim that may be confirmed or contradicted by runtime signals.
type RuntimeClaim struct {
	GraphID  string            `json:"graph_id"`
	NodeID   string            `json:"node_id,omitempty"`
	EdgeID   string            `json:"edge_id,omitempty"`
	Class    string            `json:"class,omitempty"`
	Section  string            `json:"section,omitempty"`
	Evidence []string          `json:"evidence_refs,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// RuntimeReconciliationRequest is the input envelope for phase-2 runtime reconciliation.
type RuntimeReconciliationRequest struct {
	TenantID      string               `json:"tenant_id,omitempty"`
	GraphID       string               `json:"graph_id"`
	Observations  []RuntimeObservation `json:"observations"`
	Claims        []RuntimeClaim       `json:"claims"`
	PublishPolicy string               `json:"publish_policy,omitempty"`
}

// RuntimeReconciliationPlan documents phase-2 runtime ingestion readiness.
type RuntimeReconciliationPlan struct {
	ContractVersion string   `json:"contract_version"`
	Phase           string   `json:"phase"`
	Enabled         bool     `json:"enabled"`
	PublishBlocking bool     `json:"publish_blocking"`
	InputSignals    []string `json:"input_signals"`
	MatchStrategy   string   `json:"match_strategy"`
	OutputStates    []string `json:"output_states"`
}

// RuntimeReconciliationResult summarizes reconciliation output for graph-level consumers.
type RuntimeReconciliationResult struct {
	GraphID      string   `json:"graph_id"`
	Confirmed    []string `json:"confirmed,omitempty"`
	Contradicted []string `json:"contradicted,omitempty"`
	Unmapped     []string `json:"runtime_only_unmapped,omitempty"`
	NeedsReview  []string `json:"needs_review,omitempty"`
}

// RuntimeReconciliationModule defines the phase-2 plug-in point for runtime truth integration.
// Current pipeline stages can depend on this interface without enabling publish-time blocking behavior.
type RuntimeReconciliationModule interface {
	Reconcile(context.Context, RuntimeReconciliationRequest) (RuntimeReconciliationResult, error)
}
