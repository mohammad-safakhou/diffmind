package eval

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func exp(typ, name string, det bool, details map[string]any) ExpectedEntity {
	return ExpectedEntity{Type: typ, Name: name, Deterministic: det, Details: details}
}

func httpExp(method, path string) model.Exposure {
	return model.Exposure{BaseEntity: model.BaseEntity{
		ID: method + path, Type: "http_route", Name: method + " " + path,
		Details: map[string]any{"method": method, "path": path},
	}}
}

func dbDep(id, table, op, platform string) model.Dependency {
	return model.Dependency{BaseEntity: model.BaseEntity{
		ID: id, Type: "db_operation", Name: op + " " + table, Platform: platform,
		Details: map[string]any{"table": table, "operation": op},
	}}
}

func TestScoreExactMatch(t *testing.T) {
	ext := Extracted{Exposures: []model.Exposure{httpExp("GET", "/orders")}}
	set := ExpectedSet{Exposures: []ExpectedEntity{exp("http_route", "GET /orders", true, map[string]any{"method": "GET", "path": "/orders"})}}
	rep := ScoreAll(ext, set, ModeCheap)
	if rep.Overall.TP != 1 || rep.Overall.FP != 0 || rep.Overall.FN != 0 {
		t.Fatalf("want 1/0/0, got %d/%d/%d", rep.Overall.TP, rep.Overall.FP, rep.Overall.FN)
	}
	if rep.Overall.F1 != 1 {
		t.Fatalf("want F1=1, got %v", rep.Overall.F1)
	}
}

func TestScorePhrasingCollapsesToTP(t *testing.T) {
	// extracted: plural table, SQL verb, path without leading slash.
	ext := Extracted{
		Exposures:    []model.Exposure{{BaseEntity: model.BaseEntity{ID: "1", Type: "http_route", Name: "GET orders", Details: map[string]any{"method": "get", "path": "orders"}}}},
		Dependencies: []model.Dependency{dbDep("d1", "orders", "SELECT", "postgres")},
	}
	set := ExpectedSet{
		Exposures:    []ExpectedEntity{exp("http_route", "", true, map[string]any{"method": "GET", "path": "/orders"})},
		Dependencies: []ExpectedEntity{{Type: "db_operation", Platform: "postgres", Deterministic: true, Details: map[string]any{"table": "order", "operation": "read"}}},
	}
	rep := ScoreAll(ext, set, ModeCheap)
	if rep.Overall.TP != 2 || rep.Overall.FP != 0 || rep.Overall.FN != 0 {
		t.Fatalf("phrasing variants should collapse to 2 TP, got %d/%d/%d (%+v)", rep.Overall.TP, rep.Overall.FP, rep.Overall.FN, rep.Objectives)
	}
}

func TestScoreFalsePositiveAndNegative(t *testing.T) {
	ext := Extracted{Exposures: []model.Exposure{httpExp("GET", "/a")}}
	set := ExpectedSet{Exposures: []ExpectedEntity{exp("http_route", "GET /b", true, map[string]any{"method": "GET", "path": "/b"})}}
	rep := ScoreAll(ext, set, ModeCheap)
	if rep.Overall.TP != 0 || rep.Overall.FP != 1 || rep.Overall.FN != 1 {
		t.Fatalf("want 0/1/1, got %d/%d/%d", rep.Overall.TP, rep.Overall.FP, rep.Overall.FN)
	}
	var hr ObjectiveScore
	for _, o := range rep.Objectives {
		if o.Objective == "http_route" {
			hr = o
		}
	}
	if len(hr.FalsePositives) != 1 || hr.FalsePositives[0].Name != "GET /a" {
		t.Fatalf("FP should name the offending item, got %+v", hr.FalsePositives)
	}
	if len(hr.FalseNegatives) != 1 {
		t.Fatalf("expected one FN, got %+v", hr.FalseNegatives)
	}
}

func TestScoreEmptyNoNaN(t *testing.T) {
	rep := ScoreAll(Extracted{}, ExpectedSet{}, ModeCheap)
	if rep.Overall.Precision != 1 || rep.Overall.Recall != 1 {
		t.Fatalf("empty-vs-empty should be P=R=1, got P=%v R=%v", rep.Overall.Precision, rep.Overall.Recall)
	}
}

func TestScoreMultiPlatformDBStaysTwoTP(t *testing.T) {
	ext := Extracted{Dependencies: []model.Dependency{
		dbDep("d1", "orders", "read", "postgres"),
		dbDep("d2", "orders", "read", "dynamodb"),
	}}
	set := ExpectedSet{Dependencies: []ExpectedEntity{
		{Type: "db_operation", Platform: "postgres", Deterministic: true, Details: map[string]any{"table": "orders", "operation": "read"}},
		{Type: "db_operation", Platform: "dynamodb", Deterministic: true, Details: map[string]any{"table": "orders", "operation": "read"}},
	}}
	rep := ScoreAll(ext, set, ModeCheap)
	if rep.Overall.TP != 2 || rep.Overall.FP != 0 || rep.Overall.FN != 0 {
		t.Fatalf("distinct datastores should be 2 TP, got %d/%d/%d", rep.Overall.TP, rep.Overall.FP, rep.Overall.FN)
	}
}

func TestScoreConnectionTranslation(t *testing.T) {
	e := httpExp("POST", "/orders")
	d := dbDep("dep1", "order", "write", "postgres")
	ext := Extracted{
		Exposures:    []model.Exposure{e},
		Dependencies: []model.Dependency{d},
		Connections: []model.Connection{
			{ID: "c1", FromExposureID: e.ID, ToDependencyID: d.ID, FromType: "http_route", ToType: "db_operation"},
			{ID: "c2", FromExposureID: "ghost", ToDependencyID: d.ID}, // unresolved endpoint -> FP
		},
	}
	set := ExpectedSet{
		Connections: []ExpectedConnection{{
			From:          ExpectedEntity{Type: "http_route", Details: map[string]any{"method": "POST", "path": "/orders"}},
			To:            ExpectedEntity{Type: "db_operation", Platform: "postgres", Details: map[string]any{"table": "order", "operation": "write"}},
			Deterministic: true,
		}},
	}
	rep := ScoreAll(ext, set, ModeCheap)
	var conn ObjectiveScore
	for _, o := range rep.Objectives {
		if o.Objective == "connections" {
			conn = o
		}
	}
	if conn.TP != 1 || conn.FP != 1 || conn.FN != 0 {
		t.Fatalf("want conn 1/1/0, got %d/%d/%d (%+v)", conn.TP, conn.FP, conn.FN, conn)
	}
}

func TestScoreCheapModeExcludesNonDeterministic(t *testing.T) {
	// extracted floor found nothing; expected has one det + one non-det item.
	set := ExpectedSet{Dependencies: []ExpectedEntity{
		{Type: "db_operation", Deterministic: true, Details: map[string]any{"table": "a", "operation": "read"}},
		{Type: "db_operation", Deterministic: false, Details: map[string]any{"table": "b", "operation": "read"}},
	}}
	rep := ScoreAll(Extracted{}, set, ModeCheap)
	// only the deterministic item is in scope → exactly 1 FN.
	if rep.Overall.FN != 1 {
		t.Fatalf("cheap mode should only count deterministic items, want 1 FN got %d", rep.Overall.FN)
	}
	full := ScoreAll(Extracted{}, set, ModeFull)
	if full.Overall.FN != 2 {
		t.Fatalf("full mode should count both, want 2 FN got %d", full.Overall.FN)
	}
}
