package eval

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func fcFind(objs []FloorCoverage, name string) (FloorCoverage, bool) {
	for _, o := range objs {
		if o.Objective == name {
			return o, true
		}
	}
	return FloorCoverage{}, false
}

// The floor covers a subset of what the LLM found.
func TestFloorCoveragePartial(t *testing.T) {
	floor := Extracted{Exposures: []model.Exposure{httpRoute("e1", "GET /a")}}
	llm := Extracted{Exposures: []model.Exposure{httpRoute("e1", "GET /a"), httpRoute("e2", "GET /b")}}
	objs := computeFloorCoverage(floor, llm)
	o, ok := fcFind(objs, "http_route")
	if !ok {
		t.Fatalf("http_route missing: %+v", objs)
	}
	if o.FloorKeys != 1 || o.LLMKeys != 2 || o.Covered != 1 || o.FloorOnly != 0 {
		t.Errorf("counts wrong: %+v", o)
	}
	if !o.Applicable || !approx(o.Coverage, 0.5) {
		t.Errorf("coverage want 0.5, got applicable=%v cov=%.3f", o.Applicable, o.Coverage)
	}
}

// A floor key the LLM run did not produce is reported as floor_only (a candidate
// floor false positive or an LLM miss) and does not inflate coverage.
func TestFloorCoverageFloorOnly(t *testing.T) {
	floor := Extracted{Exposures: []model.Exposure{httpRoute("e1", "GET /a"), httpRoute("e2", "GET /z")}}
	llm := Extracted{Exposures: []model.Exposure{httpRoute("e1", "GET /a")}}
	o, _ := fcFind(computeFloorCoverage(floor, llm), "http_route")
	if o.FloorKeys != 2 || o.LLMKeys != 1 || o.Covered != 1 || o.FloorOnly != 1 {
		t.Errorf("counts wrong: %+v", o)
	}
	if !approx(o.Coverage, 1.0) {
		t.Errorf("coverage want 1.0 (all LLM keys covered), got %.3f", o.Coverage)
	}
}

// When the LLM run found nothing of a type, coverage is N/A (never 1.0), even if
// the floor produced items for it.
func TestFloorCoverageLLMEmptyIsNA(t *testing.T) {
	floor := Extracted{Dependencies: []model.Dependency{outHTTP("d1", "GET /x")}}
	llm := Extracted{} // LLM produced no outbound_http
	o, ok := fcFind(computeFloorCoverage(floor, llm), "outbound_http")
	if !ok {
		t.Fatalf("outbound_http missing: %+v", computeFloorCoverage(floor, llm))
	}
	if o.Applicable {
		t.Errorf("expected N/A (not applicable) when LLM keys == 0, got %+v", o)
	}
	if o.FloorOnly != 1 {
		t.Errorf("expected floor_only=1, got %+v", o)
	}
}

// Connections coverage is measured first-class via endpoint-pair keys.
func TestFloorCoverageConnections(t *testing.T) {
	conn := []model.Connection{{ID: "c1", FromExposureID: "e1", ToDependencyID: "d1"}}
	base := func() Extracted {
		return Extracted{
			Exposures:    []model.Exposure{httpRoute("e1", "GET /a")},
			Dependencies: []model.Dependency{outHTTP("d1", "GET /x")},
		}
	}
	floor := base()
	floor.Connections = conn
	llm := base()
	llm.Connections = conn
	o, ok := fcFind(computeFloorCoverage(floor, llm), "connections")
	if !ok {
		t.Fatalf("connections missing")
	}
	if o.Covered != 1 || o.LLMKeys != 1 || !approx(o.Coverage, 1.0) {
		t.Errorf("connection coverage wrong: %+v", o)
	}
}
