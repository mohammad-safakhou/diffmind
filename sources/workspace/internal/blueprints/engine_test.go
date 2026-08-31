package blueprints

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..", "testdata")
}

func TestLoadBlueprintsFromDirs(t *testing.T) {
	wd, _ := os.Getwd()
	bpDir := filepath.Join(wd, "..", "..", "blueprints")

	bps, err := LoadBlueprintsFromDirs([]string{bpDir})
	if err != nil {
		t.Fatalf("failed to load blueprints: %v", err)
	}

	if len(bps) == 0 {
		t.Fatal("expected at least one blueprint to be loaded")
	}

	// Check that we loaded specific blueprints.
	found := make(map[string]bool)
	for _, bp := range bps {
		found[bp.Name] = true
	}
	if !found["helm-values-identity"] {
		t.Error("expected helm-values-identity blueprint")
	}
}

func TestMatches_ServiceRepo(t *testing.T) {
	td := testdataDir(t)
	orderRepoPath := filepath.Join(td, "sample_service_repos", "order-service")

	bp := &Blueprint{
		AppliesTo: AppliesTo{
			Kind: "service_repo",
			Match: MatchConfig{
				HasPath: ".example",
			},
		},
	}

	if !Matches(bp, orderRepoPath, "service_repo") {
		t.Error("expected blueprint to match order-service (has .example directory)")
	}
}

func TestMatches_WrongKind(t *testing.T) {
	td := testdataDir(t)
	orderRepoPath := filepath.Join(td, "sample_service_repos", "order-service")

	bp := &Blueprint{
		AppliesTo: AppliesTo{
			Kind: "infra_repo",
			Match: MatchConfig{
				HasPath: ".example",
			},
		},
	}

	if Matches(bp, orderRepoPath, "service_repo") {
		t.Error("expected blueprint NOT to match (kind is infra_repo, repo is service_repo)")
	}
}

func TestMatches_AnyKind(t *testing.T) {
	td := testdataDir(t)
	orderRepoPath := filepath.Join(td, "sample_service_repos", "order-service")

	bp := &Blueprint{
		AppliesTo: AppliesTo{
			Kind: "any",
			Match: MatchConfig{
				HasPath: ".example",
			},
		},
	}

	if !Matches(bp, orderRepoPath, "service_repo") {
		t.Error("expected 'any' kind to match service_repo")
	}
}

func TestMatches_MissingPath(t *testing.T) {
	td := testdataDir(t)
	orderRepoPath := filepath.Join(td, "sample_service_repos", "order-service")

	bp := &Blueprint{
		AppliesTo: AppliesTo{
			Kind: "service_repo",
			Match: MatchConfig{
				HasPath: "nonexistent-dir",
			},
		},
	}

	if Matches(bp, orderRepoPath, "service_repo") {
		t.Error("expected blueprint NOT to match (path doesn't exist)")
	}
}

func TestEngine_FieldPathExtraction(t *testing.T) {
	td := testdataDir(t)
	repoPath := filepath.Join(td, "sample_service_repos", "order-service")

	log := util.NewLogger(util.LevelInfo)
	engine := NewEngine(log)

	bp := &Blueprint{
		Name: "test-bp",
		Extractions: []Extraction{
			{
				Name:     "identity",
				Source:   ExtractionSource{Glob: ".example/prod/values.yaml"},
				Strategy: "field_path",
				Extract: []ExtractField{
					{Field: "iamRole", MapsTo: "iam_role"},
					{Field: "ingress.hosts", MapsTo: "dns_aliases"},
				},
			},
		},
	}

	results := engine.Run(bp, repoPath)
	if len(results) == 0 {
		t.Fatal("expected extraction results")
	}

	// Check extracted values.
	r := results[0]
	if r.Values["iam_role"] != "order-service-prod" {
		t.Errorf("expected iam_role 'order-service-prod', got %v", r.Values["iam_role"])
	}

	aliases := r.Values["dns_aliases"]
	if aliases == nil {
		t.Fatal("expected dns_aliases to be extracted")
	}
}

func TestToIdentity(t *testing.T) {
	results := []ExtractionResult{
		{
			BlueprintName:  "test",
			ExtractionName: "identity",
			Values: map[string]any{
				"service_name": "order-service",
				"iam_role":     "order-service-prod",
				"dns_aliases":  []any{"order-service.example.global", "orders.internal"},
			},
		},
	}

	identity := ToIdentity("order-svc", "/repos/order-service", results)
	if identity.ServiceName != "order-service" {
		t.Errorf("expected service_name to be overridden to 'order-service', got %s", identity.ServiceName)
	}

	if len(identity.Aliases) < 3 {
		t.Errorf("expected at least 3 aliases (1 iam_role + 2 dns), got %d", len(identity.Aliases))
	}

	// Check for iam_role alias.
	foundIAM := false
	for _, a := range identity.Aliases {
		if a.Kind == "iam_role" && a.Value == "order-service-prod" {
			foundIAM = true
		}
	}
	if !foundIAM {
		t.Error("expected iam_role alias")
	}
}

func TestToIdentityParsesJSONStringArraysAndSkipsEmptyArrays(t *testing.T) {
	results := []ExtractionResult{
		{
			BlueprintName:  "test",
			ExtractionName: "identity",
			Values: map[string]any{
				"dns_aliases":         `["ranking-service.example.global"]`,
				"queue_identifiers":   `[]`,
				"database_connection": `["routingdb-rds.example.global:5432/routing"]`,
			},
		},
	}

	identity := ToIdentity("ranking-service", "/repos/ranking-service", results)
	if len(identity.Aliases) != 1 || identity.Aliases[0].Value != "ranking-service.example.global" {
		t.Fatalf("aliases = %#v", identity.Aliases)
	}
	if len(identity.Resources) != 1 || identity.Resources[0].Identifier != "routingdb-rds.example.global:5432/routing" {
		t.Fatalf("resources = %#v", identity.Resources)
	}
}

func TestResolveGlob(t *testing.T) {
	td := testdataDir(t)
	repoPath := filepath.Join(td, "sample_service_repos", "order-service")

	matches, err := ResolveGlob(repoPath, ".example/*/values.yaml")
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if len(matches) == 0 {
		t.Error("expected at least one match for .example/*/values.yaml")
	}
}
