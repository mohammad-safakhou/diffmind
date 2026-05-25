package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

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
