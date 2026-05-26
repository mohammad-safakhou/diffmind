package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/scip"
)

// TestShallowFallbackMatchesByRepository verifies the no-SCIP path:
// when scipIndex is nil, the connections stage degrades to a name
// matcher that pairs an exposure to a dependency whose repository or
// name appears in the exposure's details.
func TestShallowFallbackMatchesByRepository(t *testing.T) {
	exp := model.Exposure{
		BaseEntity: model.BaseEntity{
			ID:   "exp-1",
			Type: "http_route",
			Name: "DELETE /campaign-items/{id}",
			Details: map[string]any{
				"db_operations": []any{
					map[string]any{
						"repository": "CampaignItemRepository",
						"method":     "findByIdOrThrow(String id)",
					},
				},
			},
		},
	}
	dep := model.Dependency{
		BaseEntity: model.BaseEntity{
			ID:   "dep-1",
			Type: "db_operation",
			Name: "CampaignItemRepository.findByIdOrThrow",
			Details: map[string]any{
				"repository": "CampaignItemRepository",
				"class":      "CampaignItemRepository",
			},
		},
	}

	conns, _ := buildShallowConnections(
		[]model.Exposure{exp}, []model.Dependency{dep}, 0.6,
	)
	if len(conns) != 1 {
		t.Fatalf("expected 1 shallow connection, got %d", len(conns))
	}
	c := conns[0]
	if c.FromExposureID != "exp-1" || c.ToDependencyID != "dep-1" {
		t.Errorf("unexpected from/to ids: %+v", c)
	}
	if len(c.Paths) != 0 {
		t.Errorf("shallow connection must have no paths, got %d", len(c.Paths))
	}
	if c.Confidence != 0.6 {
		t.Errorf("expected confidence at floor (0.6), got %v", c.Confidence)
	}
	if c.Evidence == nil || c.Evidence[0].Source != "shallow" {
		t.Errorf("expected evidence Source=shallow, got %+v", c.Evidence)
	}
}

// TestShallowFallbackMatchesByExposureDependencyList covers the second
// shallow strategy: exposures whose `dependencies` detail explicitly
// lists a service/repo name.
func TestShallowFallbackMatchesByExposureDependencyList(t *testing.T) {
	exp := model.Exposure{
		BaseEntity: model.BaseEntity{
			ID:   "exp-2",
			Type: "scheduled_job",
			Name: "SuperBannerScheduler",
			Details: map[string]any{
				"dependencies": []any{
					map[string]any{"name": "AwsSnsNotificationClient", "type": "AWS SNS Component"},
				},
			},
		},
	}
	dep := model.Dependency{
		BaseEntity: model.BaseEntity{
			ID:   "dep-2",
			Type: "queue_publish",
			Name: "AwsSnsNotificationClient",
		},
	}
	conns, _ := buildShallowConnections(
		[]model.Exposure{exp}, []model.Dependency{dep}, 0.5,
	)
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(conns))
	}
}

// TestShallowFallbackEmitsNoUnresolvedForNoMatch confirms a clean
// no-match doesn't produce an UnresolvedItem (the run still completes
// successfully, just with fewer connections).
func TestShallowFallbackNoMatchProducesEmptyResult(t *testing.T) {
	exp := model.Exposure{
		BaseEntity: model.BaseEntity{ID: "e", Type: "http_route", Name: "GET /x"},
	}
	dep := model.Dependency{
		BaseEntity: model.BaseEntity{ID: "d", Type: "db_operation", Name: "Other"},
	}
	conns, unr := buildShallowConnections(
		[]model.Exposure{exp}, []model.Dependency{dep}, 0.5,
	)
	if len(conns) != 0 {
		t.Errorf("expected 0 connections for no match, got %d", len(conns))
	}
	if len(unr) != 0 {
		t.Errorf("expected 0 unresolved items, got %d", len(unr))
	}
}

// TestLastIdentTrimsScipSymbol verifies the helper that produces
// human-readable identifiers for connection summaries.
func TestLastIdentTrimsScipSymbol(t *testing.T) {
	cases := map[string]string{
		"scip-java maven com.ex/Foo#findByIdOrThrow().":     "findByIdOrThrow",
		"scip-typescript pkg ex/svc.ts/UserService#find().": "find",
		"scip-go pkg ex/repo.User#Get().":                   "Get",
		"":                                                  "",
		"local-1":                                           "local-1",
	}
	for sym, want := range cases {
		if got := lastIdent(sym); got != want {
			t.Errorf("lastIdent(%q) = %q, want %q", sym, got, want)
		}
	}
}

func TestBuildConnectionPreservesPerPathConditions(t *testing.T) {
	snap := t.TempDir()
	src := `class Controller {
  void handle(boolean flag) {
    if (flag) {
      repo.saveA();
    } else {
      repo.saveB();
    }
  }
}`
	if err := os.WriteFile(filepath.Join(snap, "Controller.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	exp := model.Exposure{BaseEntity: model.BaseEntity{ID: "exp", Type: "http_route", Name: "handle"}}
	dep := model.Dependency{BaseEntity: model.BaseEntity{ID: "dep", Type: "db_operation", Name: "Repo.save"}}
	paths := []scip.Path{
		{EntrySymbol: "Ctrl#handle().", TargetSymbol: "Repo#saveA().", Steps: []scip.CallSite{{CallerSymbol: "Ctrl#handle().", CalleeSymbol: "Repo#saveA().", At: scip.Location{File: "Controller.java", StartLine: 3}}}},
		{EntrySymbol: "Ctrl#handle().", TargetSymbol: "Repo#saveB().", Steps: []scip.CallSite{{CallerSymbol: "Ctrl#handle().", CalleeSymbol: "Repo#saveB().", At: scip.Location{File: "Controller.java", StartLine: 5}}}},
	}

	conn := buildConnection(exp, dep, paths, scip.NewConditionExtractor(snap), 0.7)
	if len(conn.Paths) != 2 {
		t.Fatalf("expected both branch paths to be preserved, got %d", len(conn.Paths))
	}
	for _, p := range conn.Paths {
		if p.Condition.Kind == "" {
			t.Fatalf("path %s lost conditional evidence: %+v", p.ID, p)
		}
	}
}

// TestScoreConfidenceBoundedByFloor verifies the path-length-based
// scoring respects the minimum-confidence floor.
func TestScoreConfidenceBoundedByFloor(t *testing.T) {
	got := scoreConfidence(nil, 0.6)
	if got != 0.6 {
		t.Errorf("empty paths: got %v, want 0.6 (floor)", got)
	}
	// 1-hop path scores 0.95; longer paths get progressively lower
	// scores but stay above the internal 0.5 hard floor.
	short := []scip.Path{{Steps: make([]scip.CallSite, 1)}}
	if got := scoreConfidence(short, 0.0); got != 0.95 {
		t.Errorf("1-hop: got %v, want 0.95", got)
	}
	long := []scip.Path{{Steps: make([]scip.CallSite, 20)}}
	if got := scoreConfidence(long, 0.0); got < 0.5 {
		t.Errorf("20-hop: got %v, want >= 0.5 (internal floor)", got)
	}
}

// TestBuildPathSignatureDeterministic ensures two equivalent path
// sets produce the same signature regardless of input order. Path
// signatures feed the StableID-derived connection IDs; mismatched
// signatures across runs would scramble the dashboard's connection
// timeline.
func TestBuildPathSignatureDeterministic(t *testing.T) {
	a := scip.Path{EntrySymbol: "X", Steps: []scip.CallSite{{CalleeSymbol: "A"}, {CalleeSymbol: "B"}}}
	b := scip.Path{EntrySymbol: "X", Steps: []scip.CallSite{{CalleeSymbol: "C"}}}
	sig1 := buildPathSignature([]scip.Path{a, b})
	sig2 := buildPathSignature([]scip.Path{b, a})
	if sig1 != sig2 {
		t.Errorf("path signature is order-dependent:\n  %q\n  %q", sig1, sig2)
	}
}
