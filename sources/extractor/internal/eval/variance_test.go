package eval

import (
	"math"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func httpRoute(id, name string) model.Exposure {
	return model.Exposure{BaseEntity: model.BaseEntity{ID: id, Type: "http_route", Name: name}}
}

func outHTTP(id, name string) model.Dependency {
	return model.Dependency{BaseEntity: model.BaseEntity{ID: id, Type: "outbound_http", Name: name}}
}

func findObj(rep VarianceReport, obj string) (ObjectiveVariance, bool) {
	for _, o := range rep.Objectives {
		if o.Objective == obj {
			return o, true
		}
	}
	return ObjectiveVariance{}, false
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Identical runs → perfect reproducibility: stdev 0, core==union, ratios 1.0.
func TestVarianceIdenticalRuns(t *testing.T) {
	run := Extracted{Exposures: []model.Exposure{
		httpRoute("e1", "GET /a"), httpRoute("e2", "GET /b"),
	}}
	rep := Variance([]Extracted{run, run}, []string{"r1", "r2"})
	o, ok := findObj(rep, "http_route")
	if !ok {
		t.Fatalf("http_route objective missing: %+v", rep)
	}
	if o.Mean != 2 || o.Stdev != 0 || o.Min != 2 || o.Max != 2 {
		t.Errorf("counts stats wrong: %+v", o)
	}
	if o.CoreKeys != 2 || o.UnionKeys != 2 || !approx(o.CoreUnion, 1.0) {
		t.Errorf("core/union wrong: core=%d union=%d cu=%.3f", o.CoreKeys, o.UnionKeys, o.CoreUnion)
	}
	if !approx(o.JaccardMean, 1.0) {
		t.Errorf("jaccard want 1.0 got %.3f", o.JaccardMean)
	}
}

// One item differs between runs → core/union and Jaccard both 1/3.
func TestVarianceDriftBetweenRuns(t *testing.T) {
	run1 := Extracted{Exposures: []model.Exposure{httpRoute("e1", "GET /a"), httpRoute("e2", "GET /b")}}
	run2 := Extracted{Exposures: []model.Exposure{httpRoute("e1", "GET /a"), httpRoute("e3", "GET /c")}}
	rep := Variance([]Extracted{run1, run2}, []string{"r1", "r2"})
	o, _ := findObj(rep, "http_route")
	// counts are [2,2] (stable count, unstable identity).
	if o.Mean != 2 || o.Stdev != 0 {
		t.Errorf("counts stats wrong: %+v", o)
	}
	// keys: {/a,/b} vs {/a,/c} → core {/a}=1, union {/a,/b,/c}=3.
	if o.CoreKeys != 1 || o.UnionKeys != 3 || !approx(o.CoreUnion, 1.0/3.0) {
		t.Errorf("core/union wrong: core=%d union=%d cu=%.3f", o.CoreKeys, o.UnionKeys, o.CoreUnion)
	}
	if !approx(o.JaccardMean, 1.0/3.0) {
		t.Errorf("jaccard want 0.333 got %.3f", o.JaccardMean)
	}
}

// A run that finds nothing for a type must show count 0 and drag core/union down.
func TestVarianceMissingInOneRun(t *testing.T) {
	run1 := Extracted{Exposures: []model.Exposure{httpRoute("e1", "GET /a")}}
	run2 := Extracted{} // found no http_route at all
	rep := Variance([]Extracted{run1, run2}, nil)
	o, ok := findObj(rep, "http_route")
	if !ok {
		t.Fatal("http_route objective missing")
	}
	if o.Min != 0 || o.Max != 1 {
		t.Errorf("expected min 0 max 1, got %+v", o)
	}
	if o.CoreKeys != 0 || o.UnionKeys != 1 || !approx(o.CoreUnion, 0.0) {
		t.Errorf("core/union should be 0/1: core=%d union=%d", o.CoreKeys, o.UnionKeys)
	}
	if !approx(o.JaccardMean, 0.0) {
		t.Errorf("jaccard want 0 got %.3f", o.JaccardMean)
	}
}

// Connections are measured as a first-class objective via endpoint-pair keys.
func TestVarianceConnections(t *testing.T) {
	mk := func() Extracted {
		return Extracted{
			Exposures:    []model.Exposure{httpRoute("e1", "GET /a")},
			Dependencies: []model.Dependency{outHTTP("d1", "GET /downstream")},
			Connections: []model.Connection{
				{ID: "c1", FromExposureID: "e1", ToDependencyID: "d1"},
			},
		}
	}
	rep := Variance([]Extracted{mk(), mk()}, []string{"r1", "r2"})
	o, ok := findObj(rep, "connections")
	if !ok {
		t.Fatalf("connections objective missing: %+v", rep)
	}
	if o.CoreKeys != 1 || o.UnionKeys != 1 || !approx(o.CoreUnion, 1.0) {
		t.Errorf("connection stability wrong: %+v", o)
	}
}

// An unresolved connection endpoint must key to <unresolved>, not silently
// match a resolved one (mirrors the scorer).
func TestVarianceConnectionUnresolvedEndpoint(t *testing.T) {
	resolved := Extracted{
		Exposures:    []model.Exposure{httpRoute("e1", "GET /a")},
		Dependencies: []model.Dependency{outHTTP("d1", "GET /x")},
		Connections:  []model.Connection{{ID: "c1", FromExposureID: "e1", ToDependencyID: "d1"}},
	}
	dangling := Extracted{
		Exposures:   []model.Exposure{httpRoute("e1", "GET /a")},
		Connections: []model.Connection{{ID: "c1", FromExposureID: "e1", ToDependencyID: "missing"}},
	}
	rep := Variance([]Extracted{resolved, dangling}, nil)
	o, _ := findObj(rep, "connections")
	// "e1=>d1" vs "e1=><unresolved>" share nothing → core 0, union 2.
	if o.CoreKeys != 0 || o.UnionKeys != 2 {
		t.Errorf("expected core 0 union 2, got %+v", o)
	}
}

// Fewer than two runs has no variance to measure → ratios default to 1.0.
func TestVarianceSingleRun(t *testing.T) {
	rep := Variance([]Extracted{{Exposures: []model.Exposure{httpRoute("e1", "GET /a")}}}, []string{"r1"})
	o, _ := findObj(rep, "http_route")
	if !approx(o.JaccardMean, 1.0) || !approx(o.CoreUnion, 1.0) {
		t.Errorf("single run should be trivially stable, got %+v", o)
	}
}

func TestIntStats(t *testing.T) {
	mean, stdev, mn, mx := intStats([]int{2, 4, 6})
	if mean != 4 || mn != 2 || mx != 6 {
		t.Errorf("mean/min/max wrong: %.2f %d %d", mean, mn, mx)
	}
	// population stdev of {2,4,6} = sqrt(8/3) ≈ 1.632993
	if !approx(stdev, math.Sqrt(8.0/3.0)) {
		t.Errorf("stdev wrong: %.6f", stdev)
	}
}
