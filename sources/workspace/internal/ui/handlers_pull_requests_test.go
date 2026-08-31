package ui

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/store"
)

func TestAnalyzeCodebaseImpactFindsHighRiskSurfaces(t *testing.T) {
	pull := githubPull{Additions: 640, Deletions: 180, ChangedFiles: 5, Commits: 4}
	files := []githubPullFile{
		{Filename: "api/openapi.yaml", Additions: 80, Deletions: 30, Patch: "- /v1/orders\n+ /v2/orders"},
		{Filename: "db/migrations/004_drop_legacy.sql", Additions: 10, Deletions: 20, Patch: "+ALTER TABLE orders DROP COLUMN legacy_id"},
		{Filename: "internal/security/authorizer.go", Additions: 110, Deletions: 40},
		{Filename: "go.mod", Additions: 4, Deletions: 2},
		{Filename: "internal/orders/service.go", Additions: 436, Deletions: 88, Patch: "-func PublicOrder() {}"},
	}

	impact := analyzeCodebaseImpact(pull, files, false)
	if impact.RiskLevel != "high" && impact.RiskLevel != "critical" {
		t.Fatalf("risk level = %q (%d), want high or critical", impact.RiskLevel, impact.RiskScore)
	}
	for _, want := range []string{"api", "data", "security", "dependencies", "code"} {
		if !hasChangeCategory(impact.Categories, want) {
			t.Errorf("missing category %q in %+v", want, impact.Categories)
		}
	}
	for _, want := range []string{"contract_change", "destructive_schema", "security_boundary", "public_surface_removal"} {
		if !hasSemanticSignal(impact.Signals, want) {
			t.Errorf("missing signal %q in %+v", want, impact.Signals)
		}
	}
	if !containsExactString(impact.RiskReasons, "production code changed without test-file changes") {
		t.Fatalf("missing no-tests risk reason: %+v", impact.RiskReasons)
	}
}

func TestAnalyzeCodebaseImpactKeepsTestOnlyChangeLowRisk(t *testing.T) {
	pull := githubPull{Additions: 42, Deletions: 8, ChangedFiles: 2, Commits: 1}
	impact := analyzeCodebaseImpact(pull, []githubPullFile{
		{Filename: "internal/orders/service_test.go", Additions: 30, Deletions: 5},
		{Filename: "tests/orders.spec.js", Additions: 12, Deletions: 3},
	}, false)
	if impact.RiskLevel != "low" {
		t.Fatalf("risk level = %q (%d), want low", impact.RiskLevel, impact.RiskScore)
	}
	if len(impact.Categories) != 1 || impact.Categories[0].ID != "tests" {
		t.Fatalf("categories = %+v, want tests only", impact.Categories)
	}
}

func TestGraphServiceForRepoUsesStableIdentityThenPathThenName(t *testing.T) {
	graph := &archgraph.ArchGraph{Services: []*archgraph.ServiceNode{
		{Name: "orders-service", RepoID: "repo-orders", RepoPath: "/repos/orders"},
		{Name: "billing_api", RepoPath: "/repos/billing"},
	}}
	cases := []struct {
		name string
		repo store.Repo
		want string
	}{
		{name: "repo id", repo: store.Repo{ID: "repo-orders", Name: "different", Path: "/elsewhere"}, want: "orders-service"},
		{name: "path", repo: store.Repo{ID: "unknown", Name: "different", Path: "/repos/billing"}, want: "billing_api"},
		{name: "normalized name", repo: store.Repo{Name: "billing-api", Path: "/elsewhere"}, want: "billing_api"},
		{name: "not represented", repo: store.Repo{Name: "search-index"}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := graphServiceForRepo(graph, tc.repo); got != tc.want {
				t.Fatalf("graphServiceForRepo() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyChangedFile(t *testing.T) {
	cases := map[string]string{
		"proto/orders.proto":                     "api",
		"db/migrations/001_orders.sql":           "data",
		"deploy/terraform/main.tf":               "infrastructure",
		"internal/auth/permissions.go":           "security",
		".example/config/production/secrets.yaml": "security",
		"package-lock.json":                      "dependencies",
		"config/application-production.yaml":     "configuration",
		"internal/orders/service_test.go":        "tests",
		"docs/operations.md":                     "documentation",
		"internal/orders/service.go":             "code",
	}
	for path, want := range cases {
		if got := classifyChangedFile(path); got != want {
			t.Errorf("classifyChangedFile(%q) = %q, want %q", path, got, want)
		}
	}
}

func hasChangeCategory(categories []changeCategory, id string) bool {
	for _, category := range categories {
		if category.ID == id {
			return true
		}
	}
	return false
}

func hasSemanticSignal(signals []semanticSignal, kind string) bool {
	for _, signal := range signals {
		if signal.Kind == kind {
			return true
		}
	}
	return false
}

func containsExactString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
