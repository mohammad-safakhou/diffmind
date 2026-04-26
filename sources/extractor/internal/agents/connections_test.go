package agents

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func TestAssembleConnectionsRespectsClosedSetAndConfidence(t *testing.T) {
	expByID := map[string]model.Exposure{
		"exp1": {BaseEntity: model.BaseEntity{ID: "exp1", Type: "http_route", Name: "GET /"}},
	}
	depByID := map[string]model.Dependency{
		"dep1": {BaseEntity: model.BaseEntity{ID: "dep1", Type: "db_operation", Name: "users_select"}},
	}
	raw := []llmConnection{
		{FromExposureID: "exp1", ToDependencyID: "dep1", Summary: "kept", Confidence: 0.9},
		{FromExposureID: "exp1", ToDependencyID: "dep_unknown", Summary: "orphan", Confidence: 0.9},
		{FromExposureID: "exp1", ToDependencyID: "dep1", Summary: "low_conf", Confidence: 0.2},
	}
	conns, unresolved := assembleConnections(expByID, depByID, raw, 0.7)
	if len(conns) != 1 {
		t.Fatalf("expected 1 surviving connection, got %d", len(conns))
	}
	if conns[0].Summary != "kept" {
		t.Fatalf("unexpected connection summary: %s", conns[0].Summary)
	}
	// Orphan dep produces unmatched_reference; low_conf is silently filtered.
	found := false
	for _, u := range unresolved {
		if u.ReasonCode == "unmatched_reference" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unmatched_reference unresolved item; got %+v", unresolved)
	}
}

func TestAssembleConnectionsDeduplicates(t *testing.T) {
	expByID := map[string]model.Exposure{"e": {BaseEntity: model.BaseEntity{ID: "e", Type: "http_route"}}}
	depByID := map[string]model.Dependency{"d": {BaseEntity: model.BaseEntity{ID: "d", Type: "db_operation"}}}
	raw := []llmConnection{
		{FromExposureID: "e", ToDependencyID: "d", Summary: "s", Confidence: 0.9, PathSignature: "sig"},
		{FromExposureID: "e", ToDependencyID: "d", Summary: "s2", Confidence: 0.9, PathSignature: "sig"},
	}
	conns, _ := assembleConnections(expByID, depByID, raw, 0.7)
	if len(conns) != 1 {
		t.Fatalf("expected connections to be deduped by path signature, got %d", len(conns))
	}
}
