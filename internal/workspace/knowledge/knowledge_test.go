package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

func validPack() *Pack {
	return &Pack{
		APIVersion: APIVersion, Kind: Kind, ID: "example.identity", Name: "Example identity",
		Version: "1.2.3", License: "Apache-2.0", Compatibility: ">=0.1.0", Priority: 10,
		AppliesTo: AppliesTo{Kind: "service_repo", Match: MatchConfig{HasFile: "service.yaml"}},
		Extractions: []Extraction{{
			Name: "identity", Source: ExtractionSource{Glob: "service.yaml"}, Strategy: "field_path",
			Extract: []ExtractField{{Field: "name", MapsTo: "service_name"}},
		}},
	}
}

func writePack(t *testing.T, dir string, pack *Pack) string {
	t.Helper()
	body, err := MarshalYAML(pack)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidatePackStrictAndExecutable(t *testing.T) {
	body, _ := MarshalYAML(validPack())
	if _, errs := ValidatePack(body, ".yaml"); len(errs) != 0 {
		t.Fatalf("valid pack rejected: %v", errs)
	}
	cases := []struct {
		name, replace, with, field string
	}{
		{"unknown field", "priority: 10", "priority: 10\nmystery: value", ""},
		{"invalid semver", "1.2.3", "one", "version"},
		{"path traversal", "service.yaml", "../secret.yaml", "applies_to.match.has_file"},
		{"unknown mapping", "maps_to: service_name", "maps_to: team", "maps_to"},
		{"invalid compatibility", "'>=0.1.0'", "'banana'", "compatibility"},
		{"incompatible runtime", "'>=0.1.0'", "'>=9.0.0'", "compatibility"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bad := []byte(strings.Replace(string(body), test.replace, test.with, 1))
			_, errs := ValidatePack(bad, ".yaml")
			if len(errs) == 0 {
				t.Fatal("expected validation failure")
			}
			if test.field != "" && !strings.Contains(FormatValidationErrors(errs), test.field) {
				t.Fatalf("errors %q do not identify %s", FormatValidationErrors(errs), test.field)
			}
		})
	}
	regexPack := validPack()
	regexPack.Extractions[0].Strategy = "regex"
	regexPack.Extractions[0].Extract[0].Field = ""
	regexPack.Extractions[0].Extract[0].Pattern = "["
	regexBody, _ := MarshalYAML(regexPack)
	if _, errs := ValidatePack(regexBody, ".yaml"); len(errs) == 0 || !strings.Contains(FormatValidationErrors(errs), "pattern") {
		t.Fatalf("invalid regex was not rejected: %v", errs)
	}
}

func TestLoadPacksRecursiveDeterministicAndNoSilentFailure(t *testing.T) {
	root := t.TempDir()
	first := validPack()
	first.ID = "z-pack"
	first.Priority = 1
	second := validPack()
	second.ID = "a-pack"
	second.Priority = 20
	writePack(t, filepath.Join(root, "nested", "z"), first)
	writePack(t, filepath.Join(root, "a"), second)
	packs, err := LoadPacksFromDirs([]string{filepath.Join(root, "missing"), root})
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 || packs[0].ID != "a-pack" || packs[1].ID != "z-pack" {
		t.Fatalf("packs are not priority ordered: %+v", packs)
	}
	if err := os.WriteFile(filepath.Join(root, "broken", "pack.yaml"), []byte("not: [yaml"), 0o644); err == nil {
		t.Fatal("expected missing parent write to fail")
	}
	if err := os.MkdirAll(filepath.Join(root, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken", "pack.yaml"), []byte("not: [yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPacksFromDirs([]string{root}); err == nil {
		t.Fatal("malformed manifests must not be silently skipped")
	}
}

func TestMatcherEngineIgnoreAndRelativeEvidence(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "service.yaml"), []byte("name: checkout\nhosts:\n  - checkout.internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "generated", "service.yaml"), []byte("name: wrong\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := validPack()
	pack.Extractions[0].Source.Glob = "**/service.yaml"
	pack.Extractions[0].Extract = append(pack.Extractions[0].Extract, ExtractField{Field: "hosts", MapsTo: "dns_aliases"})
	pack.Ignore = []string{"generated/**"}
	if !Matches(pack, repo, "service_repo") || Matches(pack, repo, "infra_repo") {
		t.Fatal("repository matcher returned an incorrect result")
	}
	results := NewEngine(util.NewLogger(util.LevelInfo)).Run(pack, repo)
	if len(results) != 1 || results[0].SourceFile != "service.yaml" {
		t.Fatalf("unexpected evidence: %+v", results)
	}
	identity, err := ToIdentity("fallback", repo, results)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ServiceName != "checkout" || len(identity.Aliases) != 1 || identity.Aliases[0].Value != "checkout.internal" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestRegexExtraction(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "Dockerfile"), []byte("LABEL service=payments\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := validPack()
	pack.AppliesTo.Match = MatchConfig{HasFile: "Dockerfile"}
	pack.Extractions = []Extraction{{
		Name: "label", Source: ExtractionSource{Glob: "Dockerfile"}, Strategy: "regex",
		Extract: []ExtractField{{Pattern: `service=([a-z]+)`, MapsTo: "service_name"}},
	}}
	results := NewEngine(util.NewLogger(util.LevelInfo)).Run(pack, repo)
	identity, err := ToIdentity("fallback", repo, results)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ServiceName != "payments" {
		t.Fatalf("service name = %q", identity.ServiceName)
	}
}

func TestIdentityPrecedenceConflictAndDeduplication(t *testing.T) {
	results := []ExtractionResult{
		{PackID: "low", PackPriority: 1, Values: map[string]any{"service_name": "low", "dns_aliases": []any{"same", "same"}}},
		{PackID: "high", PackPriority: 10, Values: map[string]any{"service_name": "high", "dns_aliases": "same"}},
	}
	identity, err := ToIdentity("fallback", "/repo", results)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ServiceName != "high" || len(identity.Aliases) != 1 {
		t.Fatalf("precedence/deduplication failed: %+v", identity)
	}
	_, err = ToIdentity("fallback", "/repo", []ExtractionResult{
		{PackID: "a", PackPriority: 5, Values: map[string]any{"service_name": "one"}},
		{PackID: "b", PackPriority: 5, Values: map[string]any{"service_name": "two"}},
	})
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected explicit equal-priority conflict, got %v", err)
	}
	_, err = ToIdentity("fallback", "/repo", []ExtractionResult{
		{PackID: "a", PackPriority: 5, Values: map[string]any{"metadata.owner": "team-a"}},
		{PackID: "b", PackPriority: 5, Values: map[string]any{"metadata.owner": "team-b"}},
	})
	if err == nil || !strings.Contains(err.Error(), "metadata.owner") {
		t.Fatalf("expected metadata conflict, got %v", err)
	}
}

func TestResolutionRuleValidationAndOrdering(t *testing.T) {
	low := validPack()
	low.ID, low.Priority = "low", 1
	low.ResolutionRules = []ResolutionRule{{Name: "low", TargetPattern: "target", TargetService: "service"}}
	high := validPack()
	high.ID, high.Priority = "high", 10
	high.ResolutionRules = []ResolutionRule{{Name: "high", TargetPattern: "target", TargetService: "service", Confidence: 0.9}}
	rules := ResolutionRules([]*Pack{low, high})
	if len(rules) != 2 || rules[0].PackID != "high" || rules[1].Confidence != 0.98 {
		t.Fatalf("unexpected rule ordering/defaults: %+v", rules)
	}
	high.ResolutionRules[0].TargetPattern = "["
	body, _ := MarshalYAML(high)
	if _, errs := ValidatePack(body, ".yaml"); len(errs) == 0 || !strings.Contains(FormatValidationErrors(errs), "target_pattern") {
		t.Fatalf("invalid resolution pattern accepted: %v", errs)
	}
}

func TestServiceOverrideHasHighestPrecedence(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".diffmind"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `api_version: diffmind.dev/v1alpha1
kind: ServiceIdentity
service_name: canonical
aliases:
  - kind: dns
    value: canonical.internal
metadata:
  owner: platform
`
	if err := os.WriteFile(filepath.Join(repo, ".diffmind", "service.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	override, err := LoadServiceOverride(repo)
	if err != nil {
		t.Fatal(err)
	}
	identity := model.ServiceIdentity{ServiceName: "derived", Aliases: []model.IdentityAlias{{Kind: "dns", Value: "derived"}}}
	ApplyServiceOverride(&identity, override)
	if identity.ServiceName != "canonical" || identity.Aliases[0].Value != "canonical.internal" || identity.Metadata["owner"] != "platform" {
		t.Fatalf("override not applied: %+v", identity)
	}
}

func TestOfficialPacksLintAndFixtures(t *testing.T) {
	projectRoot := filepath.Join("..", "..", "..")
	packs, err := LoadPacksFromDirs([]string{filepath.Join(projectRoot, "packs")})
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) == 0 {
		t.Fatal("no official packs")
	}
	for _, pack := range packs {
		if len(pack.Tests) == 0 {
			t.Errorf("%s has no tests", pack.ID)
			continue
		}
		for _, result := range RunTests(pack) {
			if !result.Passed {
				t.Errorf("%s/%s: %s", pack.ID, result.Name, result.Error)
			}
		}
	}
}
