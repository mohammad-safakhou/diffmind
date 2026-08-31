package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// language represents one of the languages we support indexing.
// Each language maps to exactly one SCIP indexer binary, but a single
// indexer can serve multiple languages (scip-java handles Java, Scala,
// and Kotlin; scip-typescript handles TypeScript and JavaScript).
type language string

const (
	langJava       language = "java"
	langScala      language = "scala"
	langKotlin     language = "kotlin"
	langTypeScript language = "typescript"
	langJavaScript language = "javascript"
	langPython     language = "python"
	langGo         language = "go"
	langRuby       language = "ruby"
	langCPP        language = "cpp"
	langC          language = "c"
	langCSharp     language = "csharp"
)

// allLanguages returns every supported language identifier.
// Order is stable so the --languages=all flag produces deterministic
// runs (useful for tests + golden file diffs).
func allLanguages() []string {
	return []string{
		string(langJava), string(langScala), string(langKotlin),
		string(langTypeScript), string(langJavaScript),
		string(langPython),
		string(langGo),
		string(langRuby),
		string(langCPP), string(langC),
		string(langCSharp),
	}
}

// validateLanguage normalises a CLI token and returns its canonical
// language code, or "" if unknown. Accepts a few common synonyms
// (e.g. "js" → "javascript", "ts" → "typescript") so users don't have
// to remember the exact spelling.
func validateLanguage(token string) language {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "java":
		return langJava
	case "scala":
		return langScala
	case "kotlin", "kt":
		return langKotlin
	case "typescript", "ts":
		return langTypeScript
	case "javascript", "js":
		return langJavaScript
	case "python", "py":
		return langPython
	case "go", "golang":
		return langGo
	case "ruby", "rb":
		return langRuby
	case "cpp", "c++", "cxx":
		return langCPP
	case "c":
		return langC
	case "csharp", "c#", "cs", "dotnet":
		return langCSharp
	}
	return ""
}

// detectLanguages walks the source tree and returns the set of
// languages present. The walker is deliberately shallow on directories
// that conventionally contain dependency code (node_modules, vendor,
// .venv, target, build, dist) — indexing those would balloon both
// time and output size without adding insight.
//
// Detection is by file extension only. A few configuration files
// strongly imply a language (pom.xml → Java, go.mod → Go) and bump
// the score so we don't miss a project whose source files live in
// non-obvious places.
func detectLanguages(root string) []language {
	// Use a map for O(1) presence checks; the final slice is stable-ordered
	// by allLanguages() to keep outputs deterministic.
	found := map[language]bool{}

	markersByLang := map[string]language{
		"pom.xml":               langJava,
		"build.gradle":          langJava,
		"build.gradle.kts":      langJava, // Kotlin DSL — also marks Kotlin source via .kt detection
		"settings.gradle":       langJava,
		"settings.gradle.kts":   langKotlin,
		"build.sbt":             langScala,
		"tsconfig.json":         langTypeScript,
		"package.json":          langJavaScript, // refined later if .ts files exist
		"pyproject.toml":        langPython,
		"requirements.txt":      langPython,
		"setup.py":              langPython,
		"go.mod":                langGo,
		"Gemfile":               langRuby,
		"CMakeLists.txt":        langCPP,
		"compile_commands.json": langCPP,
	}

	// C#/F# projects are detected via solution/project file extensions
	// rather than marker file NAMES, since project files are
	// user-named. The walker below picks up *.csproj / *.sln / *.fsproj.

	extsByLang := map[string]language{
		".java":  langJava,
		".scala": langScala,
		".sc":    langScala,
		".kt":    langKotlin,
		".kts":   langKotlin,
		".ts":    langTypeScript,
		".tsx":   langTypeScript,
		".js":    langJavaScript,
		".jsx":   langJavaScript,
		".mjs":   langJavaScript,
		".cjs":   langJavaScript,
		".py":    langPython,
		".pyi":   langPython,
		".go":    langGo,
		".rb":    langRuby,
		".cpp":   langCPP,
		".cc":    langCPP,
		".cxx":   langCPP,
		".hpp":   langCPP,
		".hh":    langCPP,
		".c":   langC,
		".h":   langC, // ambiguous (could be C++); we'll fall through to C unless .cpp is also present
		".cs":  langCSharp,
		".csx": langCSharp,
	}

	// projectExtsByLang fires on file extensions that strongly imply
	// a project root even when no individual source file has been
	// seen yet (e.g. an empty new repo with only a .csproj). Separate
	// from the file-extension map above so we can detect the
	// project shape from a single marker file.
	projectExtsByLang := map[string]language{
		".csproj": langCSharp,
		".sln":    langCSharp,
		".fsproj": langCSharp,
	}

	// Hard cap on walk to avoid pathological repos (millions of files).
	// 200k file checks is plenty for any reasonable codebase. We bail
	// once every language is detected anyway, so on a small mono-lang
	// repo this exits in microseconds.
	const maxEntries = 200_000
	count := 0

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permission or transient FS errors: skip the entry, keep walking.
			return nil
		}
		count++
		if count > maxEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if isSkippableDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()

		if lang, ok := markersByLang[name]; ok {
			found[lang] = true
		}

		ext := strings.ToLower(filepath.Ext(name))
		if lang, ok := extsByLang[ext]; ok {
			found[lang] = true
		}
		if lang, ok := projectExtsByLang[ext]; ok {
			found[lang] = true
		}
		return nil
	})

	// Stable result ordering for deterministic outputs.
	out := make([]language, 0, len(found))
	for _, name := range allLanguages() {
		if found[language(name)] {
			out = append(out, language(name))
		}
	}
	return out
}

// isSkippableDir returns true for directory names that conventionally
// contain dependency code or build artefacts. Skipping them speeds up
// language detection by orders of magnitude on large monorepos and
// prevents false positives (e.g. a Java project that vendors a Python
// build tool inside node_modules should not be reported as Python).
func isSkippableDir(name string) bool {
	switch name {
	case
		// VCS / IDE
		".git", ".hg", ".svn", ".idea", ".vscode", ".gradle", ".mvn",
		// JS
		"node_modules", "bower_components",
		// Python
		"__pycache__", ".venv", "venv", ".tox", ".pytest_cache", ".mypy_cache",
		// Java
		"target", "build", "out", "bin",
		// Go + Ruby (both use "vendor")
		"vendor", ".bundle",
		// C/C++
		"cmake-build-debug", "cmake-build-release",
		// Misc
		"dist", "tmp", ".cache":
		return true
	}
	return false
}
