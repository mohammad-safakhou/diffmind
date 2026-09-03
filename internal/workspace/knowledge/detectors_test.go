package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func detectorPack() *Pack {
	pack := validPack()
	pack.Extractions = nil
	pack.AppliesTo.Match = MatchConfig{}
	pack.Detectors = []Detector{{Name: "clients", Type: "outbound_http", Source: ExtractionSource{Glob: "**/clients.yaml"}, Field: "clients.*.url"}}
	return pack
}

func detectorFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectorFieldPathsEvidenceAndDeterminism(t *testing.T) {
	repo := t.TempDir()
	detectorFile(t, repo, "clients.yaml", "clients:\n  first:\n    url: https://catalog/items\n  second:\n    url: https://billing/invoices\n---\nclients:\n  third:\n    url: https://status.example.test\n")
	pack := detectorPack()
	first, err := Detect(context.Background(), pack, repo, "gateway")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Detect(context.Background(), pack, repo, "gateway")
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("not deterministic: %v", err)
	}
	if len(first.Dependencies) != 3 {
		t.Fatalf("detections: %+v", first)
	}
	for i, line := range []int{3, 5, 9} {
		dep := first.Dependencies[i]
		if dep.ID == "" || dep.Locations[0].File != "clients.yaml" || dep.Locations[0].StartLine != line || !strings.Contains(dep.Evidence[0].Source, pack.ID+"@"+pack.Version) {
			t.Fatalf("missing evidence: %+v", dep)
		}
		if _, ok := dep.Details["method"]; ok {
			t.Fatal("must not invent HTTP methods from base URLs")
		}
	}
	pack.Detectors[0].Source.Glob = "clients.json"
	detectorFile(t, repo, "clients.json", "{\"clients\": [{\"url\": \"https://catalog\"}, {\"url\": \"https://billing\"}]}")
	got, err := Detect(context.Background(), pack, repo, "gateway")
	if err != nil || len(got.Dependencies) != 2 {
		t.Fatalf("JSON arrays: %+v %v", got, err)
	}
	pack.Detectors[0].Field = "clients.0.url"
	got, err = Detect(context.Background(), pack, repo, "gateway")
	if err != nil || len(got.Dependencies) != 1 {
		t.Fatalf("numeric index: %+v %v", got, err)
	}
}

func TestDetectorSkipsUnsafeTargetsWithoutLeakingThem(t *testing.T) {
	repo := t.TempDir()
	pack := detectorPack()
	pack.Detectors[0].Field = "targets"
	detectorFile(t, repo, "clients.yaml", "targets:\n  - '${PRIVATE_URL}'\n  - 'https://user:PRIVATE_SECRET@example.test'\n  - 'https://example.test?token=PRIVATE_SECRET'\n  - 'https://example.test#PRIVATE_SECRET'\n  - '/relative/path'\n  - 'file:///PRIVATE_FILE'\n  - 'https://safe.example.test'\n")
	got, err := Detect(context.Background(), pack, repo, "gateway")
	if err != nil || len(got.Dependencies) != 1 || len(got.Skipped) != 6 {
		t.Fatalf("unsafe detection: %+v %v", got, err)
	}
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), "PRIVATE") {
		t.Fatalf("sensitive contents leaked: %s", body)
	}
}

func TestDetectorRegexAndGlobIgnores(t *testing.T) {
	repo := t.TempDir()
	pack := detectorPack()
	pack.Detectors[0] = Detector{Name: "wrapper", Type: "outbound_rpc", Strategy: "regex", Source: ExtractionSource{Glob: "src/**/client.conf"}, Pattern: `(?m)^rpc=(?P<target>[a-z.-]+)$`}
	pack.Ignore = []string{"**/vendor/**"}
	for _, path := range []string{"src/client.conf", "src/nested/client.conf", "src/vendor/client.conf", "vendor/src/client.conf", ".git/src/client.conf"} {
		detectorFile(t, repo, path, "# rpc=ignored\nrpc=billing\n")
	}
	got, err := Detect(context.Background(), pack, repo, "gateway")
	if err != nil || len(got.Dependencies) != 2 {
		t.Fatalf("glob/ignore: %+v %v", got, err)
	}
	for _, dep := range got.Dependencies {
		if dep.Locations[0].StartLine != 2 {
			t.Fatalf("regex line: %+v", dep)
		}
	}
}

func TestDetectorFailsClosed(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"malformed", "clients: ["},
		{"duplicate key", "clients: {}\nclients: {}\n"},
		{"number target", "clients: {one: {url: 123}}"},
		{"object target", "clients: {one: {url: {host: catalog}}}"},
		{"alias target", "url: &u https://catalog\nclients: {one: {url: *u}}"},
		{"oversize", strings.Repeat(" ", maxDetectorFileBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			detectorFile(t, repo, "clients.yaml", tc.body)
			if _, err := Detect(context.Background(), detectorPack(), repo, "gateway"); err == nil {
				t.Fatal("expected explicit error")
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Detect(ctx, detectorPack(), t.TempDir(), "gateway"); err != context.Canceled {
			t.Fatalf("cancel: %v", err)
		}
	})
	t.Run("symlink input", func(t *testing.T) {
		repo, outside := t.TempDir(), t.TempDir()
		detectorFile(t, outside, "private", "clients: {one: {url: https://private}}")
		if err := os.Symlink(filepath.Join(outside, "private"), filepath.Join(repo, "clients.yaml")); err != nil {
			t.Fatal(err)
		}
		if _, err := Detect(context.Background(), detectorPack(), repo, "gateway"); err == nil {
			t.Fatal("symlink accepted")
		}
	})
}

func TestDetectorValidation(t *testing.T) {
	mutations := []struct {
		name   string
		change func(*Pack)
	}{
		{"bad type", func(p *Pack) { p.Detectors[0].Type = "execute" }},
		{"traversal", func(p *Pack) { p.Detectors[0].Source.Glob = "../private" }},
		{"windows traversal", func(p *Pack) { p.Detectors[0].Source.Glob = `..\private` }},
		{"invalid glob", func(p *Pack) { p.Detectors[0].Source.Glob = "[bad" }},
		{"empty field", func(p *Pack) { p.Detectors[0].Field = "" }},
		{"empty component", func(p *Pack) { p.Detectors[0].Field = "a..b" }},
		{"unknown strategy", func(p *Pack) { p.Detectors[0].Strategy = "shell" }},
		{"unnamed capture", func(p *Pack) {
			p.Detectors[0].Strategy = "regex"
			p.Detectors[0].Field = ""
			p.Detectors[0].Pattern = "(target)"
		}},
		{"bad regex", func(p *Pack) { p.Detectors[0].Strategy = "regex"; p.Detectors[0].Pattern = "[" }},
		{"duplicate name", func(p *Pack) { p.Detectors = append(p.Detectors, p.Detectors[0]) }},
		{"infra detection", func(p *Pack) { p.AppliesTo.Kind = "infra_repo" }},
		{"unsafe graph fixture", func(p *Pack) {
			p.GraphTests = []GraphTest{{Name: "test", Repositories: []GraphTestRepository{{Name: "gateway", Fixture: "../private"}}}}
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			pack := detectorPack()
			tc.change(pack)
			body, _ := MarshalYAML(pack)
			if _, errs := ValidatePack(body, ".yaml"); len(errs) == 0 {
				t.Fatal("accepted invalid rule")
			}
		})
	}
	pack := detectorPack()
	body, _ := MarshalYAML(pack)
	if _, errs := ValidatePack(body, ".yaml"); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestFixtureConfinementAndExactAssertions(t *testing.T) {
	root := t.TempDir()
	pack := detectorPack()
	pack.SourcePath = filepath.Join(root, "pack.yaml")
	detectorFile(t, root, "testdata/clients.yaml", "clients: {one: {url: https://catalog}}")
	pack.Tests = []TestCase{{Name: "exact", Fixture: "testdata", Expected: ExpectedIdentity{ServiceName: "gateway"}}}
	if got := RunTests(pack); len(got) != 1 || got[0].Passed {
		t.Fatalf("unexpected dependency not caught: %+v", got)
	}
	pack.Tests[0].Dependencies = []ExpectedDetection{{Type: "outbound_http", Target: "https://catalog", File: "clients.yaml", Line: 1}}
	if got := RunTests(pack); !got[0].Passed {
		t.Fatal(got[0].Error)
	}
	if _, err := FixturePath(pack, "../outside"); err == nil {
		t.Fatal("traversal accepted")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := FixturePath(pack, "linked"); err == nil {
		t.Fatal("symlink fixture accepted")
	}
}
