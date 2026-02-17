package verifier

import "time"

const verifierVersion = "v1"
const verifierID = "verifier.rule.v1"

type Options struct {
	InBundle         string
	OutDir           string
	OutBundle        string
	PromoteThreshold float64
	DisputeThreshold float64
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
	UnresolvedLowConfidence     int       `json:"unresolved_low_confidence"`
	UnresolvedLowConfidenceRate float64   `json:"unresolved_low_confidence_rate"`
}
