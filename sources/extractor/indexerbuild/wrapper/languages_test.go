package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ensure unused imports are used by the new tests (context and strings
// are consumed by TestScipJavaUsesWritableCopyNotSource below)
var _ = context.Background
var _ = strings.TrimSpace

// TestValidateLanguage covers the synonym table that turns user-supplied
// strings into canonical language constants. The synonyms are the only
// thing tests need to verify; the validation logic itself is a switch.
func TestValidateLanguage(t *testing.T) {
	cases := map[string]language{
		"java":       langJava,
		"JAVA":       langJava, // case-insensitive
		"   kotlin ": langKotlin,
		"kt":         langKotlin,
		"ts":         langTypeScript,
		"typescript": langTypeScript,
		"js":         langJavaScript,
		"javascript": langJavaScript,
		"py":         langPython,
		"python":     langPython,
		"golang":     langGo,
		"go":         langGo,
		"c++":        langCPP,
		"cxx":        langCPP,
		"cpp":        langCPP,
		"c":          langC,
		"rb":         langRuby,
		"ruby":       langRuby,
		"csharp":     langCSharp,
		"c#":         langCSharp,
		"cs":         langCSharp,
		"dotnet":     langCSharp,
		"":           "", // empty rejects
		"haskell":    "", // unknown rejects
	}
	for in, want := range cases {
		if got := validateLanguage(in); got != want {
			t.Errorf("validateLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDetectLanguagesByMarkerFiles ensures that the presence of a
// recognised manifest file (pom.xml, package.json, ...) bumps the
// corresponding language even if no source files are present yet.
// This is the path taken for freshly-cloned repos whose source layout
// is non-obvious.
func TestDetectLanguagesByMarkerFiles(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644))

	got := detectLanguages(dir)
	want := []language{langJava, langJavaScript, langGo}
	sortLangs(got)
	sortLangs(want)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("detectLanguages markers: got %v, want %v", got, want)
	}
}

// TestDetectLanguagesByExtension verifies file-extension-based detection
// works without any marker file. The walker is also expected to skip
// node_modules/.venv/etc. so a misleading file inside those dirs does
// not pollute the result.
func TestDetectLanguagesByExtension(t *testing.T) {
	dir := t.TempDir()

	// Real source files: should be detected.
	must(t, os.MkdirAll(filepath.Join(dir, "src", "main", "java", "com", "ex"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "src", "main", "java", "com", "ex", "App.java"),
		[]byte("class App {}"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('x')"), 0o644))

	// Decoy file inside a skipped directory: must NOT be detected.
	// scip-typescript would refuse to index node_modules; we should
	// not advertise TypeScript here just because of vendored .ts files.
	must(t, os.MkdirAll(filepath.Join(dir, "node_modules", "react"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "node_modules", "react", "index.ts"),
		[]byte("export {}"), 0o644))

	got := detectLanguages(dir)
	want := []language{langJava, langPython}
	sortLangs(got)
	sortLangs(want)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("detectLanguages ext: got %v, want %v", got, want)
	}
}

// TestDetectLanguagesKotlin covers detection of a Kotlin/Gradle
// project. The marker file `build.gradle.kts` currently maps to Java
// because it can host Java projects too; a Kotlin source file (.kt)
// elsewhere in the tree triggers the Kotlin entry as well.
func TestDetectLanguagesKotlin(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "settings.gradle.kts"), []byte("// kotlin"), 0o644))
	must(t, os.MkdirAll(filepath.Join(dir, "src", "main", "kotlin"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "src", "main", "kotlin", "App.kt"),
		[]byte("fun main(){}"), 0o644))

	got := detectLanguages(dir)
	if !containsLang(got, langKotlin) {
		t.Errorf("expected kotlin in %v", got)
	}
}

// TestDetectLanguagesCSharp checks both file-name (.sln) and
// extension-based (.csproj, .cs) detection paths.
func TestDetectLanguagesCSharp(t *testing.T) {
	t.Run("by_csproj", func(t *testing.T) {
		dir := t.TempDir()
		must(t, os.WriteFile(filepath.Join(dir, "MyService.csproj"),
			[]byte("<Project/>"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "Program.cs"),
			[]byte("class Program{}"), 0o644))
		if !containsLang(detectLanguages(dir), langCSharp) {
			t.Error("expected csharp by .csproj + .cs")
		}
	})
	t.Run("by_sln", func(t *testing.T) {
		dir := t.TempDir()
		must(t, os.WriteFile(filepath.Join(dir, "MySolution.sln"),
			[]byte("Microsoft Visual Studio Solution File"), 0o644))
		if !containsLang(detectLanguages(dir), langCSharp) {
			t.Error("expected csharp by .sln alone")
		}
	})
}

// containsLang reports whether the slice contains the given language.
// Test-only convenience used by the language-detection assertions.
func containsLang(haystack []language, needle language) bool {
	for _, l := range haystack {
		if l == needle {
			return true
		}
	}
	return false
}

// TestDetectLanguagesEmptyDir confirms a directory with no recognised
// content returns an empty list (not nil — callers may want to
// differentiate, but reflect.DeepEqual treats nil and []language{} as
// equal anyway so we don't enforce it here).
func TestDetectLanguagesEmptyDir(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "README"), []byte("hello"), 0o644))

	got := detectLanguages(dir)
	if len(got) != 0 {
		t.Errorf("expected no languages, got %v", got)
	}
}

// TestParseLanguages verifies the CLI-flag parser handles the "auto",
// "all", and explicit-comma-list forms.
func TestParseLanguages(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{"auto"}},
		{"auto", []string{"auto"}},
		{"java", []string{"java"}},
		{"java,python", []string{"java", "python"}},
		{"  java , Python ,, ", []string{"java", "python"}},
		{"all", allLanguages()},
	}
	for _, c := range cases {
		got := parseLanguages(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseLanguages(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// sortLangs sorts a slice of languages in place by string value.
// Detection results are sorted by allLanguages() ordering already,
// but a test that builds a `want` list by hand needs to match.
func sortLangs(ls []language) {
	sort.Slice(ls, func(i, j int) bool { return string(ls[i]) < string(ls[j]) })
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestCopyDirInto verifies that copyDirInto produces an identical tree
// in the destination directory, preserving file contents and nested
// structure. This guards the scip-java read-only snapshot fix.
func TestCopyDirInto(t *testing.T) {
	src := t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "pom.xml"), []byte("<project/>"), 0o644))
	must(t, os.MkdirAll(filepath.Join(src, "src", "main", "java"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "src", "main", "java", "Foo.java"), []byte("class Foo {}"), 0o644))

	dst := t.TempDir()
	if err := copyDirInto(src, dst); err != nil {
		t.Fatalf("copyDirInto: %v", err)
	}

	for _, rel := range []string{"pom.xml", filepath.Join("src", "main", "java", "Foo.java")} {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("missing %s after copy: %v", rel, err)
			continue
		}
		src, _ := os.ReadFile(filepath.Join(src, rel))
		if string(got) != string(src) {
			t.Errorf("%s content mismatch after copy", rel)
		}
	}
}

// TestScipJavaUsesWritableCopyNotSource verifies that when scip-java is
// invoked, it is pointed at a writable copy of the source tree and NOT
// at in.Source directly. This is the regression guard for the
// "Read-only file system: /sources/target" failure.
//
// We use a fake `scip-java` script (via PATH manipulation) that records
// its working directory to a file so we can assert it is NOT in.Source.
func TestScipJavaUsesWritableCopyNotSource(t *testing.T) {
	// Create a fake source with a pom.xml so hasJavaBuildTool passes.
	src := t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "pom.xml"), []byte("<project/>"), 0o644))
	workdir := t.TempDir()

	// Build a fake scip-java that writes its $PWD to a sentinel file and exits 0.
	sentinel := filepath.Join(t.TempDir(), "cwd.txt")
	fakeScipJava := filepath.Join(t.TempDir(), "scip-java")
	must(t, os.WriteFile(fakeScipJava, []byte("#!/bin/sh\npwd > "+sentinel+"\necho '{}' ; exit 0\n"), 0o755))

	origPATH := os.Getenv("PATH")
	t.Setenv("PATH", filepath.Dir(fakeScipJava)+":"+origPATH)

	idxr := &scipJava{}
	results := idxr.Run(context.Background(), indexerInput{
		Source:    src,
		Workdir:   workdir,
		Languages: []language{langJava},
	})
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	// The result may be failed because our fake doesn't write a real SCIP
	// file, but what matters is WHERE it was called from.
	cwdBytes, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel file not written — fake scip-java was not invoked: %v", err)
	}
	cwd := strings.TrimSpace(string(cwdBytes))
	if cwd == src {
		t.Fatalf("scip-java was run from in.Source (%s) — must use writable copy instead", src)
	}
	if strings.Contains(cwd, src) && !strings.Contains(cwd, workdir) {
		t.Fatalf("scip-java cwd %q looks like a subdirectory of in.Source, not the workdir copy", cwd)
	}
}
