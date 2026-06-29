// Package langdetect inspects a source tree and identifies the
// languages + versions present, by reading well-known marker files
// (pom.xml, package.json, go.mod, etc.). It feeds deterministic
// indexing and base-image selection.
//
// Marker files matter for three reasons:
//
//  1. "Java" without a version is useless for picking the right base image.
//  2. Indexing needs language facts before framework detectors run.
//  3. If `go.mod` says `go 1.22`, that is the version that compiles.
//
// The package is intentionally small: one Fact type, one Inspect
// entrypoint, and one parser per supported language.
package langdetect

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Language is the canonical language identifier. The set MUST stay
// in sync with internal/indexerbuild/recipe (base-image templates)
// and indexerbuild/wrapper (per-indexer dispatch).
type Language string

const (
	LangJava       Language = "java"
	LangKotlin     Language = "kotlin"
	LangScala      Language = "scala"
	LangTypeScript Language = "typescript"
	LangJavaScript Language = "javascript"
	LangPython     Language = "python"
	LangGo         Language = "go"
	LangRuby       Language = "ruby"
	LangCSharp     Language = "csharp"
	LangFSharp     Language = "fsharp"
	LangCpp        Language = "cpp"
	LangC          Language = "c"
)

// Fact is the deterministic detection record for a single language
// in the source tree. Equivalent of "Java 21 with Maven 3.9.x".
//
// Version is what we MUST match in the base image. If the version
// can't be determined we leave it empty; the recipe falls back to
// our default (latest LTS) and emits a warning event.
//
// BuildTool ("maven", "gradle", "npm", "yarn", "pnpm", "pip",
// "poetry", "go", "bundler", "dotnet") drives which marker file
// the indexer wrapper reads inside the container.
type Fact struct {
	Language         Language `json:"language"`
	Version          string   `json:"version,omitempty"`
	BuildTool        string   `json:"build_tool,omitempty"`
	BuildToolVersion string   `json:"build_tool_version,omitempty"`
	// Sources is the list of marker files that contributed to
	// this fact. Surfaced in the indexer.build event so the user
	// can see why we picked Java 21 vs 17.
	Sources []string `json:"sources,omitempty"`
}

// Inspect walks `root` and returns one Fact per detected language.
// The walk is bounded: we descend up to MaxDepth directories and
// look only at filenames matching our marker set. We deliberately
// do NOT open large files; pom.xml etc. are bounded to MaxFileSize.
//
// Errors during individual file reads are not fatal — we still
// return whatever Facts we could derive. A nil error means the
// walk completed without an unrecoverable filesystem problem
// (typically just bad permissions on root itself).
func Inspect(ctx context.Context, root string) ([]Fact, error) {
	if root == "" {
		return nil, fmt.Errorf("langdetect: empty root")
	}
	markers, err := collectMarkers(ctx, root)
	if err != nil {
		return nil, err
	}
	facts := map[Language]*Fact{}
	for _, m := range markers {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		f := detectFromMarker(m)
		if f == nil {
			continue
		}
		// Merge: keep the most specific version seen and union
		// the Sources list. The order of marker files within a
		// language is not stable across operating systems, so
		// "most specific" means "non-empty wins".
		if existing, ok := facts[f.Language]; ok {
			if existing.Version == "" && f.Version != "" {
				existing.Version = f.Version
			}
			if existing.BuildTool == "" && f.BuildTool != "" {
				existing.BuildTool = f.BuildTool
				existing.BuildToolVersion = f.BuildToolVersion
			}
			existing.Sources = append(existing.Sources, f.Sources...)
		} else {
			facts[f.Language] = f
		}
	}
	out := make([]Fact, 0, len(facts))
	for _, v := range facts {
		// Dedupe sources for clean output.
		v.Sources = uniq(v.Sources)
		sort.Strings(v.Sources)
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Language < out[j].Language })
	return out, nil
}

// MaxDepth is the maximum directory depth we descend below root.
// Most marker files live within 4 levels (e.g. monorepo/subdir/
// service/pom.xml).
const MaxDepth = 6

// MaxFileSize bounds the bytes we read from any single marker file.
// Build files are tiny (<100 KB in 99.9% of cases); we'd rather
// truncate than parse a megabyte of garbage.
const MaxFileSize = 256 * 1024

// markerFile is one (path, basename) pair the walk produced.
type markerFile struct {
	Path string
	Base string
}

// collectMarkers walks `root` and returns every marker file we
// know how to parse, bounded by MaxDepth.
func collectMarkers(ctx context.Context, root string) ([]markerFile, error) {
	var out []markerFile
	rootClean := filepath.Clean(root)
	err := filepath.WalkDir(rootClean, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// Permission-denied on a directory is common in test
			// fixtures; skip the subtree rather than aborting.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// Skip well-known noise directories. node_modules / .git
		// / vendor each contain THOUSANDS of files that can match
		// our marker set but represent third-party code, not the
		// repo's own language.
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == "vendor" ||
				name == "dist" || name == "build" || name == "target" ||
				name == ".gradle" || name == ".mvn" || name == "out" ||
				name == "__pycache__" || name == ".venv" || name == "venv" {
				return fs.SkipDir
			}
			// Depth check.
			rel, _ := filepath.Rel(rootClean, path)
			if rel != "." && strings.Count(rel, string(filepath.Separator)) >= MaxDepth {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if isMarkerName(name) {
			out = append(out, markerFile{Path: path, Base: name})
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

// isMarkerName returns true when filename is one of the build /
// runtime marker files we know how to parse.
func isMarkerName(name string) bool {
	switch name {
	case "pom.xml",
		"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts",
		"package.json", ".nvmrc", ".node-version",
		"go.mod",
		"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile", ".python-version",
		"Gemfile", ".ruby-version",
		"global.json",
		".tool-versions",
		"CMakeLists.txt":
		return true
	}
	if strings.HasSuffix(name, ".csproj") ||
		strings.HasSuffix(name, ".fsproj") ||
		strings.HasSuffix(name, ".vbproj") ||
		strings.HasSuffix(name, ".sln") {
		return true
	}
	return false
}

// detectFromMarker reads one marker file and returns a Fact, or
// nil if it couldn't extract anything useful.
func detectFromMarker(m markerFile) *Fact {
	content, err := readBounded(m.Path)
	if err != nil {
		return nil
	}
	switch m.Base {
	case "pom.xml":
		return detectMaven(m.Path, content)
	case "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts":
		return detectGradle(m.Path, m.Base, content)
	case "package.json":
		return detectPackageJSON(m.Path, content)
	case ".nvmrc", ".node-version":
		return detectNvmrc(m.Path, content)
	case "go.mod":
		return detectGoMod(m.Path, content)
	case "pyproject.toml":
		return detectPyProject(m.Path, content)
	case "setup.py", "setup.cfg":
		return detectSetupPy(m.Path, content)
	case "requirements.txt", "Pipfile":
		return &Fact{Language: LangPython, BuildTool: pickPythonTool(m.Base), Sources: []string{m.Path}}
	case ".python-version":
		return detectPythonVersionFile(m.Path, content)
	case "Gemfile":
		return detectGemfile(m.Path, content)
	case ".ruby-version":
		return &Fact{Language: LangRuby, Version: strings.TrimSpace(string(content)), Sources: []string{m.Path}}
	case "global.json":
		return detectGlobalJSON(m.Path, content)
	case ".tool-versions":
		return detectToolVersions(m.Path, content)
	case "CMakeLists.txt":
		return &Fact{Language: LangCpp, BuildTool: "cmake", Sources: []string{m.Path}}
	}
	if strings.HasSuffix(m.Base, ".csproj") || strings.HasSuffix(m.Base, ".fsproj") || strings.HasSuffix(m.Base, ".vbproj") {
		return detectCsproj(m.Path, m.Base, content)
	}
	if strings.HasSuffix(m.Base, ".sln") {
		// .sln by itself doesn't carry a TFM. Mark .NET present;
		// version will be filled in by a sibling .csproj/.fsproj.
		return &Fact{Language: LangCSharp, BuildTool: "dotnet", Sources: []string{m.Path}}
	}
	return nil
}

// readBounded reads up to MaxFileSize bytes from path. Truncation
// is fine for our purposes; marker files are tiny and the version
// is always near the top.
func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, MaxFileSize)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}

// ---- per-language parsers ----

var (
	mvnSourceRe    = regexp.MustCompile(`(?i)<(?:maven\.compiler\.(?:source|release|target)|java\.version)>\s*([0-9.]+)\s*</`)
	gradleJavaRe   = regexp.MustCompile(`(?i)(?:sourceCompatibility|targetCompatibility|jvmTarget)\s*[=:]?\s*['"]?(JavaVersion\.VERSION_)?([0-9_.]+)['"]?`)
	gradleKotlinRe = regexp.MustCompile(`(?i)kotlin\s*\(\s*['"]jvm['"]\s*\)\s*version\s+['"]([0-9.]+)['"]`)
	gradleVerRe    = regexp.MustCompile(`(?i)distributionUrl=.*gradle-([0-9.]+)-`)
	goModRe        = regexp.MustCompile(`(?m)^go\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)
	pythonReq      = regexp.MustCompile(`(?i)requires-python\s*=\s*['"]([^'"]+)['"]`)
	pythonSetup    = regexp.MustCompile(`(?i)python_requires\s*=\s*['"]([^'"]+)['"]`)
	tfmRe          = regexp.MustCompile(`(?i)<TargetFramework[s]?>\s*([^<]+)\s*</TargetFramework`)
	gemRubyRe      = regexp.MustCompile(`(?m)^ruby\s+['"]([^'"]+)['"]`)
)

func detectMaven(path string, content []byte) *Fact {
	f := &Fact{Language: LangJava, BuildTool: "maven", Sources: []string{path}}
	if m := mvnSourceRe.FindSubmatch(content); m != nil {
		f.Version = normalizeJava(string(m[1]))
	}
	return f
}

func detectGradle(path, base string, content []byte) *Fact {
	// Could be Kotlin (.kts) or pure Java. We pick the language
	// based on file naming AND keyword presence.
	isKotlin := strings.HasSuffix(base, ".kts") || bytesContainsAny(content, []string{"kotlin(\"jvm\")", "kotlinOptions", "id(\"org.jetbrains.kotlin"})
	if isKotlin {
		f := &Fact{Language: LangKotlin, BuildTool: "gradle", Sources: []string{path}}
		if m := gradleKotlinRe.FindSubmatch(content); m != nil {
			f.Version = string(m[1])
		}
		return f
	}
	f := &Fact{Language: LangJava, BuildTool: "gradle", Sources: []string{path}}
	if m := gradleJavaRe.FindSubmatch(content); m != nil {
		f.Version = normalizeJava(strings.ReplaceAll(string(m[2]), "_", "."))
	}
	if base == "gradle-wrapper.properties" {
		if m := gradleVerRe.FindSubmatch(content); m != nil {
			f.BuildToolVersion = string(m[1])
		}
	}
	return f
}

func detectPackageJSON(path string, content []byte) *Fact {
	type pkg struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		TypeScript      bool              `json:"typescript,omitempty"`
		Type            string            `json:"type,omitempty"`
	}
	var p pkg
	if err := json.Unmarshal(content, &p); err != nil {
		return nil
	}
	lang := LangJavaScript
	if _, ok := p.DevDependencies["typescript"]; ok {
		lang = LangTypeScript
	} else if _, ok := p.Dependencies["typescript"]; ok {
		lang = LangTypeScript
	}
	f := &Fact{Language: lang, BuildTool: "npm", Sources: []string{path}}
	if p.Engines.Node != "" {
		f.Version = cleanSemver(p.Engines.Node)
	}
	return f
}

func detectNvmrc(path string, content []byte) *Fact {
	v := strings.TrimPrefix(strings.TrimSpace(string(content)), "v")
	return &Fact{Language: LangJavaScript, Version: v, Sources: []string{path}}
}

func detectGoMod(path string, content []byte) *Fact {
	f := &Fact{Language: LangGo, BuildTool: "go", Sources: []string{path}}
	if m := goModRe.FindSubmatch(content); m != nil {
		f.Version = string(m[1])
	}
	return f
}

func detectPyProject(path string, content []byte) *Fact {
	f := &Fact{Language: LangPython, BuildTool: "pip", Sources: []string{path}}
	if m := pythonReq.FindSubmatch(content); m != nil {
		f.Version = extractMinPythonVersion(string(m[1]))
	}
	// Detect poetry vs setuptools vs hatch by section presence.
	if bytesContainsAny(content, []string{"[tool.poetry]"}) {
		f.BuildTool = "poetry"
	} else if bytesContainsAny(content, []string{"[tool.hatch"}) {
		f.BuildTool = "hatch"
	}
	return f
}

func detectSetupPy(path string, content []byte) *Fact {
	f := &Fact{Language: LangPython, BuildTool: "pip", Sources: []string{path}}
	if m := pythonSetup.FindSubmatch(content); m != nil {
		f.Version = extractMinPythonVersion(string(m[1]))
	}
	return f
}

func detectPythonVersionFile(path string, content []byte) *Fact {
	return &Fact{Language: LangPython, Version: strings.TrimSpace(string(content)), Sources: []string{path}}
}

func detectGemfile(path string, content []byte) *Fact {
	f := &Fact{Language: LangRuby, BuildTool: "bundler", Sources: []string{path}}
	if m := gemRubyRe.FindSubmatch(content); m != nil {
		f.Version = string(m[1])
	}
	return f
}

func detectGlobalJSON(path string, content []byte) *Fact {
	type gj struct {
		SDK struct {
			Version string `json:"version"`
		} `json:"sdk"`
	}
	var g gj
	if err := json.Unmarshal(content, &g); err != nil {
		return nil
	}
	return &Fact{Language: LangCSharp, BuildTool: "dotnet", Version: majorMinor(g.SDK.Version), Sources: []string{path}}
}

func detectToolVersions(path string, content []byte) *Fact {
	// .tool-versions lines look like: "java 21.0.2" or "nodejs 20.10.0".
	// We extract every line and emit one Fact per recognised language.
	// To stay compatible with the per-marker API, we merge later in
	// Inspect via the Sources field; here we return only the first
	// recognised tool. A future iteration could split into multiple
	// markerFile entries; for now we pick whichever line appears first.
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "java":
			return &Fact{Language: LangJava, Version: normalizeJava(fields[1]), Sources: []string{path}}
		case "kotlin":
			return &Fact{Language: LangKotlin, Version: fields[1], Sources: []string{path}}
		case "nodejs", "node":
			return &Fact{Language: LangJavaScript, Version: fields[1], Sources: []string{path}}
		case "python":
			return &Fact{Language: LangPython, Version: fields[1], Sources: []string{path}}
		case "ruby":
			return &Fact{Language: LangRuby, Version: fields[1], Sources: []string{path}}
		case "golang", "go":
			return &Fact{Language: LangGo, Version: fields[1], Sources: []string{path}}
		case "dotnet":
			return &Fact{Language: LangCSharp, Version: majorMinor(fields[1]), Sources: []string{path}}
		}
	}
	return nil
}

func detectCsproj(path, base string, content []byte) *Fact {
	lang := LangCSharp
	if strings.HasSuffix(base, ".fsproj") {
		lang = LangFSharp
	}
	f := &Fact{Language: lang, BuildTool: "dotnet", Sources: []string{path}}
	if m := tfmRe.FindSubmatch(content); m != nil {
		f.Version = parseTFM(string(m[1]))
	}
	return f
}

// ---- helpers ----

// normalizeJava maps "1.8" -> "8", strips "JavaVersion.VERSION_"
// prefix, and otherwise keeps the version as-is. Maven historically
// uses "1.8" for Java 8 alongside the modern "11" / "17" / "21".
func normalizeJava(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "1.") {
		// "1.8" → "8"
		if rest := strings.TrimPrefix(v, "1."); rest != "" && rest != "." {
			return rest
		}
	}
	return v
}

// extractMinPythonVersion takes a constraint like ">=3.10,<3.13"
// and returns the lower bound ("3.10"). When no lower bound is
// present we leave the field empty so the recipe defaults to
// "latest supported".
func extractMinPythonVersion(constraint string) string {
	c := strings.TrimSpace(constraint)
	if c == "" {
		return ""
	}
	for _, part := range strings.Split(c, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, ">=") {
			return strings.TrimSpace(strings.TrimPrefix(part, ">="))
		}
	}
	// Sometimes pyproject just has "3.10" without an operator.
	if !strings.ContainsAny(c, "<>=") {
		return c
	}
	return ""
}

// majorMinor strips a trailing patch from a semver-ish string:
// "8.0.404" → "8.0".
func majorMinor(v string) string {
	v = strings.TrimSpace(v)
	parts := strings.Split(v, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

// parseTFM turns "net8.0" into "8.0"; passes through obvious
// versions; leaves anything else alone.
func parseTFM(tfm string) string {
	tfm = strings.TrimSpace(tfm)
	if strings.HasPrefix(strings.ToLower(tfm), "net") {
		return strings.TrimPrefix(strings.ToLower(tfm), "net")
	}
	return tfm
}

// cleanSemver strips leading ^ ~ >= = etc.
func cleanSemver(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{">=", "<=", "==", "^", "~", ">", "<", "="} {
		s = strings.TrimPrefix(s, p)
	}
	// "20.x" → "20"
	if i := strings.Index(s, ".x"); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// pickPythonTool returns the build tool corresponding to a Python
// marker file basename.
func pickPythonTool(base string) string {
	switch base {
	case "Pipfile":
		return "pipenv"
	default:
		return "pip"
	}
}

func bytesContainsAny(b []byte, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(string(b), n) {
			return true
		}
	}
	return false
}

func uniq(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
