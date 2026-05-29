package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/ast/framework"
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

// ── AST-based connection tests ────────────────────────────────────────────────

// TestResolveEntitySymbolsASTMethodPreferred verifies that when a location
// points to a specific method line, that method is returned (not the class).
func TestResolveEntitySymbolsASTMethodPreferred(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;
public class UserController {
    public void getUser(String id) {
        // method body
    }
    public void createUser(Object user) {
        // another method
    }
}
`
	if err := os.WriteFile(filepath.Join(dir, "UserController.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 2, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Line 3 is inside getUser (1-based). Should resolve to getUser, not class.
	locs := []model.Location{{File: "UserController.java", StartLine: 3, EndLine: 5}}
	syms := resolveEntitySymbolsAST(idx, "UserController", locs)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	// Must find the method, not just the class
	foundMethod := false
	for _, s := range syms {
		if s == "UserController.getUser" {
			foundMethod = true
		}
	}
	if !foundMethod {
		t.Errorf("expected UserController.getUser in results; got: %v", syms)
	}
}

// TestResolveEntitySymbolsASTClassExpansion verifies that when a location
// points to the class declaration line (e.g. line 1), all class methods
// are returned as entry points.
func TestResolveEntitySymbolsASTClassExpansion(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;
public class Scheduler {
    public void runDaily() {}
    public void runWeekly() {}
}
`
	if err := os.WriteFile(filepath.Join(dir, "Scheduler.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 2, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Line 2 is the class declaration. Should expand to all methods.
	locs := []model.Location{{File: "Scheduler.java", StartLine: 2, EndLine: 5}}
	syms := resolveEntitySymbolsAST(idx, "Scheduler", locs)
	if len(syms) == 0 {
		t.Fatal("expected symbols from class expansion, got none")
	}
	foundClass := false
	foundRunDaily := false
	for _, s := range syms {
		if s == "Scheduler" {
			foundClass = true
		}
		if s == "Scheduler.runDaily" {
			foundRunDaily = true
		}
	}
	if !foundClass {
		t.Errorf("expected class itself in expansion; got: %v", syms)
	}
	if !foundRunDaily {
		t.Errorf("expected Scheduler.runDaily in expansion; got: %v", syms)
	}
}

// TestResolveEntitySymbolsASTMultiLocation verifies that ALL locations are
// tried, not just the first. When the first location hits a class and a later
// location hits a specific method, both should be in the result set.
func TestResolveEntitySymbolsASTMultiLocation(t *testing.T) {
	dir := t.TempDir()
	// First file: job class (entry point)
	jobSrc := `package com.example;
public class DeliveryJob {
    private final DeliveryService service;
    public void run(String[] args) {
        service.deliver();
    }
}
`
	// Second file: service called by the job (also a location provided by LLM)
	svcSrc := `package com.example;
public class DeliveryService {
    public void deliver() {
        // writes to database
    }
}
`
	if err := os.WriteFile(filepath.Join(dir, "DeliveryJob.java"), []byte(jobSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DeliveryService.java"), []byte(svcSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 2, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Two locations: first is job class at line 2, second is service method at line 3.
	locs := []model.Location{
		{File: "DeliveryJob.java", StartLine: 2, EndLine: 6},
		{File: "DeliveryService.java", StartLine: 3, EndLine: 5},
	}
	syms := resolveEntitySymbolsAST(idx, "DeliveryJob", locs)
	if len(syms) == 0 {
		t.Fatal("expected symbols from multi-location resolution, got none")
	}
	// Should include DeliveryJob.run (from class expansion) AND DeliveryService.deliver (from loc 2)
	hasJobMethod := false
	hasSvcMethod := false
	for _, s := range syms {
		if s == "DeliveryJob.run" {
			hasJobMethod = true
		}
		if s == "DeliveryService.deliver" {
			hasSvcMethod = true
		}
	}
	if !hasJobMethod {
		t.Errorf("expected DeliveryJob.run in multi-location results; got: %v", syms)
	}
	if !hasSvcMethod {
		t.Errorf("expected DeliveryService.deliver in multi-location results; got: %v", syms)
	}
}

// TestSelfReferentialConnectionsFiltered verifies that when an exposure's
// class and a dependency's class are the same (e.g. a queue listener that
// is simultaneously recorded as a dependency), no intra-class connections
// are produced.
func TestSelfReferentialConnectionsFiltered(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;
public class EventListener {
    private final EventService service;
    public void onMessage(String msg) {
        processMessage(msg);
    }
    private void processMessage(String msg) {
        service.handle(msg);
    }
}
`
	svcSrc := `package com.example;
public class EventService {
    public void handle(String msg) {}
}
`
	if err := os.WriteFile(filepath.Join(dir, "EventListener.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "EventService.java"), []byte(svcSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 2, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Exposure: EventListener at line 1 (class declaration)
	exposure := model.Exposure{
		BaseEntity: model.BaseEntity{
			ID: "exp-listener", Type: "queue_consumer", Name: "EventListener",
			Locations: []model.Location{{File: "EventListener.java", StartLine: 2, EndLine: 9}},
		},
	}
	// Dependency: EventListener (same class! self-referential)
	selfDep := model.Dependency{
		BaseEntity: model.BaseEntity{
			ID: "dep-self", Type: "sqs_queue_consumer", Name: "EventListener",
			Locations: []model.Location{{File: "EventListener.java", StartLine: 2, EndLine: 9}},
		},
	}
	// Dependency: EventService (external, should produce connections)
	externalDep := model.Dependency{
		BaseEntity: model.BaseEntity{
			ID: "dep-svc", Type: "db_operation", Name: "EventService.handle",
			Locations: []model.Location{{File: "EventService.java", StartLine: 3, EndLine: 3}},
		},
	}

	conns, _ := runASTConnections(
		context.Background(), idx,
		[]model.Exposure{exposure},
		[]model.Dependency{selfDep, externalDep},
		0.5, 4, nil,
	)

	// Should have connection to EventService, not to EventListener itself.
	selfConns := 0
	externalConns := 0
	for _, c := range conns {
		if c.ToDependencyID == "dep-self" {
			selfConns++
		}
		if c.ToDependencyID == "dep-svc" {
			externalConns++
		}
	}
	if selfConns > 0 {
		t.Errorf("expected no self-referential connections (same class), got %d", selfConns)
	}
	if externalConns == 0 {
		t.Errorf("expected connection to external EventService, got none")
	}
}
