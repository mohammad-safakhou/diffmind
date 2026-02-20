package verifier

import "time"

const verifierVersion = "v1"
const verifierID = "verifier.rule.v1"

type Options struct {
	InBundle         string
	OutDir           string
	OutBundle        string
	ReviewQueuePath  string
	PromoteThreshold float64
	DisputeThreshold float64
	StrictEvidence   bool
	TwoPass          bool
}

type Report struct {
	GeneratedAt                 time.Time `json:"generated_at"`
	SnapshotID                  string    `json:"snapshot_id"`
	VerifierID                  string    `json:"verifier_id"`
	VerifierVersion             string    `json:"verifier_version"`
	InputEntities               int       `json:"input_entities"`
	OutputEntities              int       `json:"output_entities"`
	VerifiedCount               int       `json:"verified_count"`
	NeedsReviewCount            int       `json:"needs_review_count"`
	DisputedCount               int       `json:"disputed_count"`
	DecisionEntitiesAdded       int       `json:"decision_entities_added"`
	ReviewQueueItems            int       `json:"review_queue_items"`
	MissingEvidenceCritical     int       `json:"missing_evidence_critical"`
	HypothesisCandidates        int       `json:"hypothesis_candidates"`
	ContradictionDisputes       int       `json:"contradiction_disputes"`
	UnresolvedLowConfidence     int       `json:"unresolved_low_confidence"`
	UnresolvedLowConfidenceRate float64   `json:"unresolved_low_confidence_rate"`
}

type ReviewQueueItem struct {
	EntityID      string   `json:"entity_id"`
	EntityType    string   `json:"entity_type"`
	NaturalKey    string   `json:"natural_key"`
	Status        string   `json:"status"`
	Reason        string   `json:"reason"`
	Confidence    float64  `json:"confidence"`
	EvidenceIDs   []string `json:"evidence_ids"`
	FactIDs       []string `json:"fact_ids"`
	Section       string   `json:"section,omitempty"`
	Class         string   `json:"class,omitempty"`
	Priority      string   `json:"priority"`
	CreatedAtUTC  string   `json:"created_at_utc"`
	VerifierID    string   `json:"verifier_id"`
	SourceAdapter string   `json:"source_adapter,omitempty"`
}
