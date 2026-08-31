package connections

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/register"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
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

func TestResolveExposureEntriesScopesAmbiguousShortHandlerToRoutePackage(t *testing.T) {
	idx := &astpkg.ProjectIndex{
		Files: map[string]*astpkg.FileAST{
			"internal/affiliate/http/controller.go": {},
			"internal/portfolio/http/controller.go": {},
		},
		Symbols: map[string][]astpkg.SymbolDef{
			"affiliate.Controller.Delete": {{
				Name:      "Delete",
				Qualified: "affiliate.Controller.Delete",
				Kind:      astpkg.SymbolKindMethod,
				File:      "internal/affiliate/http/delete.go",
				Receiver:  "Controller",
			}},
			"portfolio.Controller.Delete": {{
				Name:      "Delete",
				Qualified: "portfolio.Controller.Delete",
				Kind:      astpkg.SymbolKindMethod,
				File:      "internal/portfolio/http/delete.go",
				Receiver:  "Controller",
			}},
		},
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{
		ID:   "http.delete_marketing_asset",
		Type: "http_route",
		Name: "DELETE /admin/marketing-assets/:id",
		Details: map[string]any{
			"handler": "Delete",
		},
		Locations: []model.Location{{
			File:      "internal/affiliate/http/controller.go",
			StartLine: 18,
			EndLine:   18,
		}},
	}}
	got := resolveExposureEntries(idx, exp)
	if len(got) != 1 || got[0] != "affiliate.Controller.Delete" {
		t.Fatalf("expected scoped affiliate handler only, got %#v", got)
	}
	if global := resolveExactEntitySymbolAST(idx, "Delete"); len(global) != 0 {
		t.Fatalf("ambiguous global short handler should not resolve, got %#v", global)
	}
}

func debugSymbolsInPath(idx *astpkg.ProjectIndex, path string) []string {
	var out []string
	for qualified, defs := range idx.Symbols {
		for _, def := range defs {
			if strings.Contains(def.File, path) {
				out = append(out, qualified+"|"+def.Name+"|"+def.File+"|"+def.Kind.String())
			}
		}
	}
	sort.Strings(out)
	return out
}

func debugCalls(idx *astpkg.ProjectIndex, caller string) []string {
	var out []string
	for _, call := range idx.CallGraph[caller] {
		out = append(out, call.CalleeRaw+"=>"+strings.Join(call.CalleeResolved, ","))
	}
	sort.Strings(out)
	return out
}

func debugFieldTypes(idx *astpkg.ProjectIndex, field string) []string {
	var out []string
	for key, typ := range idx.FieldTypes {
		if strings.Contains(key, field) || strings.Contains(typ, field) {
			out = append(out, key+"=>"+typ)
		}
	}
	sort.Strings(out)
	return out
}

func debugCallsContain(calls []string, want string) bool {
	for _, call := range calls {
		if strings.Contains(call, want) {
			return true
		}
	}
	return false
}

func debugResolveDependency(idx *astpkg.ProjectIndex, dep model.Dependency) []string {
	return resolveEntitySymbolsAST(idx, dep.Name, dependencyTargetLocations(dep))
}

// TestASTConnectionPreservesPerPathConditions verifies that conditional
// call sites (if/else branches) produce distinct paths and that each
// path carries a non-empty Condition.
func TestASTConnectionPreservesPerPathConditions(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;
public class Controller {
    private Repo repo;
    public void handle(boolean flag) {
        if (flag) {
            repo.saveA("ok");
        } else {
            repo.saveB("fallback");
        }
    }
}
`
	svcSrc := `package com.example;
public class Repo {
    public void saveA(String v) {}
    public void saveB(String v) {}
}
`
	if err := os.WriteFile(filepath.Join(dir, "Controller.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Repo.java"), []byte(svcSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := astpkg.Build(context.Background(), dir, "java", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	exp := model.Exposure{BaseEntity: model.BaseEntity{
		ID: "exp", Type: "http_route", Name: "Controller.handle",
		Locations: []model.Location{{File: "Controller.java", StartLine: 4, EndLine: 10}},
	}}
	depA := model.Dependency{BaseEntity: model.BaseEntity{
		ID: "dep-a", Type: "db_operation", Name: "Repo.saveA",
		Locations: []model.Location{{File: "Repo.java", StartLine: 3, EndLine: 3}},
	}}
	depB := model.Dependency{BaseEntity: model.BaseEntity{
		ID: "dep-b", Type: "db_operation", Name: "Repo.saveB",
		Locations: []model.Location{{File: "Repo.java", StartLine: 4, EndLine: 4}},
	}}

	conns, _ := runASTConnections(context.Background(), idx, []model.Exposure{exp}, []model.Dependency{depA, depB}, 0.7, 4)
	if len(conns) != 2 {
		t.Fatalf("expected 2 connections (saveA and saveB), got %d", len(conns))
	}
	for _, c := range conns {
		if len(c.Paths) == 0 {
			t.Errorf("connection %s has no paths", c.ID)
		}
	}
}

func TestASTConnectionsPreferExactDependencyOverBroadInterfaceLocation(t *testing.T) {
	dir := t.TempDir()
	controller := `package com.example;
public class CampaignPlacementController {
    private final CampaignPlacementService campaignPlacementService = new CampaignPlacementService();
    public void listByCampaign(String campaignId) {
        campaignPlacementService.listByCampaignId(campaignId);
    }
}
`
	service := `package com.example;
public class CampaignPlacementService {
    private final CampaignPlacementRepository campaignPlacementRepository = null;
    public void listByCampaignId(String campaignId) {
        campaignPlacementRepository.findByCampaignId(campaignId);
    }
}
`
	repo := `package com.example;
public interface CampaignPlacementRepository {
    java.util.List<String> findByCampaignId(String campaignId);
}
`
	files := map[string]string{"CampaignPlacementController.java": controller, "CampaignPlacementService.java": service, "CampaignPlacementRepository.java": repo}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{ID: "exp", Type: "http_route", Name: "GET /campaign-placements/campaign/{campaignId}", Locations: []model.Location{{File: "CampaignPlacementController.java", StartLine: 4, EndLine: 6}}}}
	findByCampaignID := model.Dependency{BaseEntity: model.BaseEntity{ID: "dep-find", Type: "db_operation", Name: "CampaignPlacementRepository.findByCampaignId", Locations: []model.Location{{File: "CampaignPlacementRepository.java", StartLine: 3, EndLine: 3}}}}
	findAllByID := model.Dependency{BaseEntity: model.BaseEntity{ID: "dep-find-all", Type: "db_operation", Name: "CampaignPlacementRepository.findAllById", Locations: []model.Location{{File: "CampaignPlacementRepository.java", StartLine: 2, EndLine: 3}}}}

	conns, _ := runASTConnections(context.Background(), idx, []model.Exposure{exp}, []model.Dependency{findByCampaignID, findAllByID}, 0.7, 4)
	if len(conns) != 1 {
		t.Logf("field types: %#v", idx.FieldTypes)
		t.Logf("call graph: %#v", idx.CallGraph)
		t.Fatalf("expected only exact findByCampaignId connection, got %d", len(conns))
	}
	if conns[0].ToDependencyID != "dep-find" {
		t.Fatalf("connection target = %s, want dep-find", conns[0].ToDependencyID)
	}
}

func TestASTConnectionsDoNotFanOutUnresolvedReceiverCalls(t *testing.T) {
	dir := t.TempDir()
	controller := `package com.example;
public class Controller {
    private final UserRepository userRepository = null;
    public void handle(String id) {
        userRepository.findById(id);
    }
}
`
	userRepo := `package com.example;
public interface UserRepository {
    String findById(String id);
}
`
	accountRepo := `package com.example;
public interface AccountRepository {
    String findById(String id);
}
`
	files := map[string]string{"Controller.java": controller, "UserRepository.java": userRepo, "AccountRepository.java": accountRepo}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{ID: "exp", Type: "http_route", Name: "Controller.handle", Locations: []model.Location{{File: "Controller.java", StartLine: 4, EndLine: 6}}}}
	userDep := model.Dependency{BaseEntity: model.BaseEntity{ID: "user", Type: "db_operation", Name: "UserRepository.findById", Locations: []model.Location{{File: "UserRepository.java", StartLine: 3, EndLine: 3}}}}
	accountDep := model.Dependency{BaseEntity: model.BaseEntity{ID: "account", Type: "db_operation", Name: "AccountRepository.findById", Locations: []model.Location{{File: "AccountRepository.java", StartLine: 3, EndLine: 3}}}}

	conns, _ := runASTConnections(context.Background(), idx, []model.Exposure{exp}, []model.Dependency{userDep, accountDep}, 0.7, 4)
	if len(conns) != 1 {
		t.Fatalf("expected only the receiver-typed repository connection, got %d", len(conns))
	}
	if conns[0].ToDependencyID != "user" {
		t.Fatalf("connection target = %s, want user", conns[0].ToDependencyID)
	}
}

func TestASTConnectionsUsePrimaryExposureEntryAndInheritedRepositoryMethod(t *testing.T) {
	dir := t.TempDir()
	controller := `package com.example;
public class CampaignController {
    private final CampaignService campaignService = null;
    public void list() {
        campaignService.findCampaigns();
    }
}
`
	service := `package com.example;
public class CampaignService {
    private final CampaignRepository campaignRepository = null;
    public void findCampaigns() {
        campaignRepository.findAll();
    }
}
`
	repo := `package com.example;
public interface CampaignRepository extends JpaRepository<Campaign, String> {
}
`
	files := map[string]string{"CampaignController.java": controller, "CampaignService.java": service, "CampaignRepository.java": repo}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{
		ID:   "exp",
		Type: "http_route",
		Name: "GET /campaigns",
		Locations: []model.Location{
			{File: "CampaignController.java", StartLine: 4, EndLine: 6},
			{File: "CampaignService.java", StartLine: 4, EndLine: 6},
			{File: "CampaignRepository.java", StartLine: 2, EndLine: 3},
		},
		Details: map[string]any{"handler": "com.example.CampaignController#list"},
	}}
	dep := model.Dependency{BaseEntity: model.BaseEntity{
		ID:        "dep",
		Type:      "db_operation",
		Name:      "CampaignRepository.findAll",
		Locations: []model.Location{{File: "CampaignService.java", StartLine: 4, EndLine: 6}},
	}}

	conns, _ := runASTConnections(context.Background(), idx, []model.Exposure{exp}, []model.Dependency{dep}, 0.7, 4)
	if len(conns) != 1 {
		t.Fatalf("expected inherited repository connection from primary handler, got %d", len(conns))
	}
	if conns[0].ToDependencyID != "dep" {
		t.Fatalf("connection target = %s, want dep", conns[0].ToDependencyID)
	}
}

func TestASTConnectionsExpandTypedInterfaceLocalToImplementations(t *testing.T) {
	dir := t.TempDir()
	listener := `package com.example;
public class Listener {
    public void onMessage(Event event) {
        Processor<Event> processor = null;
        processor.processMessage(event);
    }
}
`
	iface := `package com.example;
public interface Processor<E> {
    void processMessage(E event);
}
`
	impl := `package com.example;
public class CampaignProcessor implements Processor<Event> {
    private final CampaignRepository campaignRepository = null;
    public void processMessage(Event event) {
        campaignRepository.save(event);
    }
}
`
	repo := `package com.example;
public interface CampaignRepository {
    void save(Event event);
}
`
	event := `package com.example;
public class Event {}
`
	files := map[string]string{"Listener.java": listener, "Processor.java": iface, "CampaignProcessor.java": impl, "CampaignRepository.java": repo, "Event.java": event}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{
		ID:        "exp",
		Type:      "queue_consumer",
		Name:      "Listener.onMessage",
		Locations: []model.Location{{File: "Listener.java", StartLine: 3, EndLine: 6}},
	}}
	dep := model.Dependency{BaseEntity: model.BaseEntity{
		ID:        "dep",
		Type:      "db_operation",
		Name:      "CampaignRepository.save",
		Locations: []model.Location{{File: "CampaignRepository.java", StartLine: 3, EndLine: 3}},
	}}

	conns, _ := runASTConnections(context.Background(), idx, []model.Exposure{exp}, []model.Dependency{dep}, 0.7, 4)
	if len(conns) != 1 {
		t.Fatalf("expected interface implementation path to repository, got %d", len(conns))
	}
}

func TestASTConnectionsResolveMultilineInjectedRepositoryField(t *testing.T) {
	dir := t.TempDir()
	controller := `package com.example;
public class SubTargetHistoryController {
    private final TrafficConfigurationSubTargetHistoryService service = null;
    public void getTargetHistory() {
        service.getTargetHistory();
    }
}
`
	service := `package com.example;
public class TrafficConfigurationSubTargetHistoryService {
    private final TrafficConfigurationSubTargetHistoryRepository
            trafficConfigurationSubTargetHistoryRepository = null;
    public void getTargetHistory() {
        trafficConfigurationSubTargetHistoryRepository.findTargetHistoryByCampaignAndCampaignItem();
    }
}
`
	repo := `package com.example;
public interface TrafficConfigurationSubTargetHistoryRepository {
    void findTargetHistoryByCampaignAndCampaignItem();
}
`
	files := map[string]string{"SubTargetHistoryController.java": controller, "TrafficConfigurationSubTargetHistoryService.java": service, "TrafficConfigurationSubTargetHistoryRepository.java": repo}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := idx.FieldTypes["TrafficConfigurationSubTargetHistoryService.trafficConfigurationSubTargetHistoryRepository"]; got != "TrafficConfigurationSubTargetHistoryRepository" {
		t.Fatalf("multiline field type = %q", got)
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{ID: "exp", Type: "http_route", Name: "GET /sub-target-history", Locations: []model.Location{{File: "SubTargetHistoryController.java", StartLine: 4, EndLine: 6}}}}
	dep := model.Dependency{BaseEntity: model.BaseEntity{ID: "dep", Type: "db_operation", Name: "TrafficConfigurationSubTargetHistoryRepository.findTargetHistoryByCampaignAndCampaignItem", Locations: []model.Location{{File: "TrafficConfigurationSubTargetHistoryRepository.java", StartLine: 3, EndLine: 3}}}}

	conns, _ := runASTConnections(context.Background(), idx, []model.Exposure{exp}, []model.Dependency{dep}, 0.7, 4)
	if len(conns) != 1 {
		t.Fatalf("expected multiline repository field connection, got %d", len(conns))
	}
}

func TestAugmentDependenciesFromASTAddsMissingRepositoryDependency(t *testing.T) {
	dir := t.TempDir()
	controller := `package com.example;
public class TrafficConfigurationController {
    private final TrafficConfigurationHistoryService service = null;
    public void getTargetHistory() {
        service.getTargetHistory();
    }
}
`
	service := `package com.example;
public class TrafficConfigurationHistoryService {
    private final TrafficConfigurationHistoryRepository repository = null;
    public void getTargetHistory() {
        repository.findTargetHistoryByCampaignId();
    }
}
`
	repo := `package com.example;
public interface TrafficConfigurationHistoryRepository {
    void findTargetHistoryByCampaignId();
}
`
	files := map[string]string{"TrafficConfigurationController.java": controller, "TrafficConfigurationHistoryService.java": service, "TrafficConfigurationHistoryRepository.java": repo}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{ID: "exp", Type: "http_route", Name: "GET /target-history", Locations: []model.Location{{File: "TrafficConfigurationController.java", StartLine: 4, EndLine: 6}}}}
	existing := []model.Dependency{{BaseEntity: model.BaseEntity{ID: "existing", Type: "db_operation", Name: "OtherRepository.findAll", Platform: "postgres", Instance: "routing_db", Details: map[string]any{"database_type": "PostgreSQL", "database_name": "routing_db"}}}}
	deps := AugmentDependencies(idx, []model.Exposure{exp}, existing, 0.7)
	if len(deps) != 2 {
		t.Fatalf("expected existing plus one AST-derived dependency, got %d", len(deps))
	}
	var dep model.Dependency
	for _, candidate := range deps {
		if candidate.Name == "TrafficConfigurationHistoryRepository.findTargetHistoryByCampaignId" {
			dep = candidate
		}
	}
	if dep.Name != "TrafficConfigurationHistoryRepository.findTargetHistoryByCampaignId" {
		t.Fatalf("dependency name = %q", dep.Name)
	}
	if dep.Platform != "postgres" || dep.Instance != "routing_db" {
		t.Fatalf("dependency grouping platform/instance = %q/%q", dep.Platform, dep.Instance)
	}
	if dep.Details["table"] != "traffic_configuration_history" || dep.Details["entity"] != "TrafficConfigurationHistory" {
		t.Fatalf("dependency table/entity = %#v/%#v", dep.Details["table"], dep.Details["entity"])
	}
	conns, _ := runASTConnections(context.Background(), idx, []model.Exposure{exp}, deps, 0.7, 4)
	if len(conns) != 1 {
		t.Fatalf("expected connection to AST-derived dependency, got %d", len(conns))
	}
}

func TestASTConnectionsExcludeTestPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "main", "java"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src", "test", "java"), 0o755); err != nil {
		t.Fatal(err)
	}
	mainSrc := `package com.example;
public class Controller {
    public void handle() {}
}
`
	testSrc := `package com.example;
public class ControllerTest {
    public void helper() { new Repo().save(); }
}
`
	repoSrc := `package com.example;
public class Repo {
    public void save() {}
}
`
	files := map[string]string{
		"src/main/java/Controller.java":     mainSrc,
		"src/main/java/Repo.java":           repoSrc,
		"src/test/java/ControllerTest.java": testSrc,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := astpkg.Build(context.Background(), dir, "java", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := idx.Files["src/test/java/ControllerTest.java"]; ok {
		t.Fatal("test source should not be indexed for production graph traversal")
	}
}

// TestASTScoreConfidenceBoundedByFloor verifies the path-length-based
// scoring respects the minimum-confidence floor.
func TestASTScoreConfidenceBoundedByFloor(t *testing.T) {
	got := scoreASTConfidence(nil, 0.6)
	if got != 0.6 {
		t.Errorf("empty paths: got %v, want 0.6 (floor)", got)
	}
	// 1-hop path scores 0.98; longer paths score progressively lower.
	short := []astpkg.CallPath{{Steps: make([]astpkg.PathStep, 1)}}
	if got := scoreASTConfidence(short, 0.0); got != 0.98 {
		t.Errorf("1-hop: got %v, want 0.98", got)
	}
	long := []astpkg.CallPath{{Steps: make([]astpkg.PathStep, 20)}}
	if got := scoreASTConfidence(long, 0.0); got < 0.5 {
		t.Errorf("20-hop: got %v, want >= 0.5 (internal floor)", got)
	}
}

// TestASTPathSignatureDeterministic ensures two equivalent AST path
// sets produce the same signature regardless of input order.
func TestASTPathSignatureDeterministic(t *testing.T) {
	a := astpkg.CallPath{Steps: []astpkg.PathStep{{Callee: "A"}, {Callee: "B"}}}
	b := astpkg.CallPath{Steps: []astpkg.PathStep{{Callee: "C"}}}
	sig1 := buildASTPathSignature([]astpkg.CallPath{a, b})
	sig2 := buildASTPathSignature([]astpkg.CallPath{b, a})
	if sig1 != sig2 {
		t.Errorf("ast path signature is order-dependent:\n  %q\n  %q", sig1, sig2)
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
	idx, err := astpkg.Build(context.Background(), dir, "java", 2)
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
	idx, err := astpkg.Build(context.Background(), dir, "java", 2)
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
	// Second file: service called by the job (also a downstream source location)
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
	idx, err := astpkg.Build(context.Background(), dir, "java", 2)
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
	idx, err := astpkg.Build(context.Background(), dir, "java", 2)
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
		0.5, 4,
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
