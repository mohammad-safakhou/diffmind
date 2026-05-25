package recipe

import (
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/langdetect"
)

// TestGenerateProducesExpectedBases checks that a multi-language
// fact set produces one base BuildJob per language with the
// correct image tag.
func TestGenerateProducesExpectedBases(t *testing.T) {
	plan, err := Generate([]langdetect.Fact{
		{Language: langdetect.LangJava, Version: "17", BuildTool: "maven"},
		{Language: langdetect.LangTypeScript, Version: "20"},
		{Language: langdetect.LangGo, Version: "1.22"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(plan.Base) != 3 {
		t.Fatalf("Base jobs = %d, want 3", len(plan.Base))
	}
	wantTags := map[string]bool{
		"diffmind-base-java:17":       false,
		"diffmind-base-typescript:20": false,
		"diffmind-base-go:1.22":       false,
	}
	for _, j := range plan.Base {
		if _, ok := wantTags[j.Tag]; !ok {
			t.Errorf("unexpected base tag %q", j.Tag)
			continue
		}
		wantTags[j.Tag] = true
		if j.Kind != "base" {
			t.Errorf("%s kind = %q, want base", j.Tag, j.Kind)
		}
		if j.Dockerfile == "" {
			t.Errorf("%s empty Dockerfile", j.Tag)
		}
	}
	for tag, found := range wantTags {
		if !found {
			t.Errorf("missing base tag %q", tag)
		}
	}
}

// TestCompositeTagIsHumanReadable checks the readable-tag rule:
// "lang+version" entries joined by "_", sorted by language.
func TestCompositeTagIsHumanReadable(t *testing.T) {
	plan, err := Generate([]langdetect.Fact{
		{Language: langdetect.LangTypeScript, Version: "20"},
		{Language: langdetect.LangJava, Version: "21"},
		{Language: langdetect.LangPython, Version: "3.12"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := "diffmind-indexer:java21_python3.12_typescript20"
	if plan.Composite.Tag != want {
		t.Errorf("Composite.Tag = %q, want %q", plan.Composite.Tag, want)
	}
}

// TestGenerateFallsBackToDefault confirms the resolver picks the
// language's Default version when none is specified, and flags
// UsedFallback so the orchestrator can surface a warning.
func TestGenerateFallsBackToDefault(t *testing.T) {
	plan, err := Generate([]langdetect.Fact{
		{Language: langdetect.LangPython}, // no version
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(plan.Resolved) != 1 {
		t.Fatalf("Resolved = %d, want 1", len(plan.Resolved))
	}
	if !plan.Resolved[0].UsedFallback {
		t.Errorf("UsedFallback = false, want true")
	}
	if plan.Resolved[0].ResolvedVersion != "3.12" {
		t.Errorf("ResolvedVersion = %q, want 3.12 (Python default)", plan.Resolved[0].ResolvedVersion)
	}
}

// TestGenerateSkipsUnsupportedLanguage is the contract that an
// unknown language doesn't make Generate explode — it just
// produces a plan with the supported subset.
func TestGenerateSkipsUnsupportedLanguage(t *testing.T) {
	plan, err := Generate([]langdetect.Fact{
		{Language: langdetect.LangJava, Version: "21"},
		{Language: langdetect.LangScala, Version: "3.3"}, // unsupported
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(plan.Base) != 1 {
		t.Fatalf("Base = %d, want 1", len(plan.Base))
	}
	if plan.Composite.Tag != "diffmind-indexer:java21" {
		t.Errorf("Composite.Tag = %q, want diffmind-indexer:java21", plan.Composite.Tag)
	}
}

// TestGenerateErrorsWhenNothingSupported documents the (rare)
// case where every detected language is unsupported — the
// orchestrator should treat this as a fail-soft for the index
// stage, falling back to the shallow matcher.
func TestGenerateErrorsWhenNothingSupported(t *testing.T) {
	_, err := Generate([]langdetect.Fact{
		{Language: langdetect.LangScala, Version: "3"},
	})
	if err == nil {
		t.Fatalf("expected error for unsupported-only input")
	}
}

// TestPickVersionMatching exercises the resolver's matching
// algorithm: exact > major-minor > major > default.
func TestPickVersionMatching(t *testing.T) {
	spec := languageSpec{Versions: []string{"11", "17", "21"}, Default: "21"}
	cases := map[string]string{
		"21":     "21",
		"17":     "17",
		"17.0.1": "17", // major-minor prefix? Java numbering matches major-only
		"":       "21",
		"99":     "21", // unknown → default
	}
	for in, want := range cases {
		got := pickVersion(spec, in)
		if got != want {
			t.Errorf("pickVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCompositeDockerfileIncludesWrapper ensures the generated
// composite Dockerfile mentions the wrapper builder stage so the
// final image's entrypoint exists.
func TestCompositeDockerfileIncludesWrapper(t *testing.T) {
	plan, err := Generate([]langdetect.Fact{{Language: langdetect.LangGo, Version: "1.22"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Composite.Dockerfile, "FROM golang:1.22-bookworm AS wrapper") {
		t.Errorf("Composite Dockerfile missing wrapper builder stage:\n%s", plan.Composite.Dockerfile)
	}
	if !strings.Contains(plan.Composite.Dockerfile, "/usr/local/bin/diffmind-index") {
		t.Errorf("Composite Dockerfile missing entrypoint COPY")
	}
	if _, ok := plan.Composite.ContextFiles["go.mod"]; !ok {
		t.Errorf("composite context missing synthesised go.mod")
	}
	// At least one wrapper Go source must be present.
	hasWrapper := false
	for p := range plan.Composite.ContextFiles {
		if strings.HasPrefix(p, "wrapper/") && strings.HasSuffix(p, ".go") {
			hasWrapper = true
			break
		}
	}
	if !hasWrapper {
		t.Errorf("composite context missing wrapper sources; got %v", keysOf(plan.Composite.ContextFiles))
	}
}

// TestCompositeDockerfileMatchesBaseLayouts is the Sprint 4
// follow-up regression: every COPY in the composite MUST reference
// a path the corresponding base image actually creates. Run
// 20260525T131803Z failed at "COPY /opt/scip-java" because the
// Java base no longer ships /opt/scip-java (scip-java is now a
// coursier-bootstrap script at /usr/local/bin/scip-java).
//
// This test guards against the same drift class. For each
// language we Generate a single-language plan and assert that the
// composite Dockerfile's COPY lines reference ONLY paths the base
// Dockerfile creates.
func TestCompositeDockerfileMatchesBaseLayouts(t *testing.T) {
	cases := []struct {
		fact          langdetect.Fact
		mustNotCopy   []string // paths the composite MUST NOT reference
		mustCopy      []string // paths the composite MUST reference
		baseMustHave  []string // markers that must appear in the base Dockerfile
	}{
		{
			fact:         langdetect.Fact{Language: langdetect.LangJava, Version: "21"},
			mustNotCopy:  []string{"/opt/scip-java"}, // dropped in Sprint 4
			mustCopy:     []string{"/usr/local/bin/scip-java", "/opt/java", "/opt/maven", "/opt/gradle"},
			baseMustHave: []string{"/usr/local/bin/scip-java"},
		},
		{
			fact:         langdetect.Fact{Language: langdetect.LangTypeScript, Version: "20"},
			mustNotCopy:  []string{"/opt/node"}, // replaced by /opt/node-tools
			mustCopy:     []string{"/opt/node-tools"},
			baseMustHave: []string{"NPM_CONFIG_PREFIX=/opt/node-tools"},
		},
		{
			fact:         langdetect.Fact{Language: langdetect.LangPython, Version: "3.12"},
			mustNotCopy:  []string{},
			mustCopy:     []string{"/opt/python-tools", "/opt/python"},
			baseMustHave: []string{"NPM_CONFIG_PREFIX=/opt/python-tools"},
		},
		{
			fact:         langdetect.Fact{Language: langdetect.LangGo, Version: "1.22"},
			mustNotCopy:  []string{},
			mustCopy:     []string{"/usr/local/go", "/usr/local/bin/scip-go"},
			baseMustHave: []string{"scip-go-linux-"},
		},
		{
			fact:         langdetect.Fact{Language: langdetect.LangRuby, Version: "3.3"},
			mustNotCopy:  []string{},
			mustCopy:     []string{"/usr/local/bin/scip-ruby"},
			baseMustHave: []string{"scip-ruby"},
		},
	}
	for _, c := range cases {
		t.Run(string(c.fact.Language), func(t *testing.T) {
			plan, err := Generate([]langdetect.Fact{c.fact})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			compDF := plan.Composite.Dockerfile
			for _, bad := range c.mustNotCopy {
				if strings.Contains(compDF, "COPY --from=lang_"+string(c.fact.Language)+" "+bad+" ") {
					t.Errorf("composite still COPYs %q from %s base (dropped in Sprint 4)\n%s",
						bad, c.fact.Language, compDF)
				}
			}
			for _, good := range c.mustCopy {
				if !strings.Contains(compDF, good) {
					t.Errorf("composite does NOT reference %q (expected for %s)\n%s",
						good, c.fact.Language, compDF)
				}
			}
			if len(plan.Base) > 0 {
				baseDF := plan.Base[0].Dockerfile
				for _, marker := range c.baseMustHave {
					if !strings.Contains(baseDF, marker) {
						t.Errorf("%s base Dockerfile missing expected marker %q\n%s",
							c.fact.Language, marker, baseDF)
					}
				}
			}
		})
	}
}

// TestPlanDigestIsStable proves the digest is deterministic given
// equal inputs (the per-machine cache directory key).
func TestPlanDigestIsStable(t *testing.T) {
	a, _ := Generate([]langdetect.Fact{
		{Language: langdetect.LangGo, Version: "1.22"},
		{Language: langdetect.LangPython, Version: "3.12"},
	})
	b, _ := Generate([]langdetect.Fact{
		// Same set, reverse order.
		{Language: langdetect.LangPython, Version: "3.12"},
		{Language: langdetect.LangGo, Version: "1.22"},
	})
	if a.Digest() != b.Digest() {
		t.Errorf("Digest unstable across input order: %q vs %q", a.Digest(), b.Digest())
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
