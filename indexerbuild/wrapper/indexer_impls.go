package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------
// scip-java
//
// scip-java is a JVM tool. It compiles the project under analysis via
// the project's own build tool (Maven, Gradle, sbt, Mill), with the
// `semanticdb-javac` plugin attached, then converts the resulting
// SemanticDB files into a single SCIP index.
//
// SUPPORTED BUILD TOOLS (auto-detected by presence of marker files):
//   - Maven        (pom.xml)
//   - Gradle       (build.gradle, build.gradle.kts, gradlew)
//   - sbt          (build.sbt)
//
// For any build tool we don't recognise, we return a "skipped" result
// with reason="unsupported build tool". This is conservative — running
// `scip-java index` on an unrecognised layout typically returns a
// confusing error.
//
// NETWORK REQUIREMENT: yes. scip-java triggers `mvn verify` (or the
// equivalent) which pulls dependencies. The container must be run
// with network access for Java projects.
// ---------------------------------------------------------------------

type scipJava struct{}

func newScipJava() indexer { return &scipJava{} }

func (s *scipJava) Name() string { return "scip-java" }

func (s *scipJava) Run(ctx context.Context, in indexerInput) []LanguageResult {
	ensureDir(in.Workdir)
	out := outputPath(in.Workdir, s.Name())

	// Refuse if no Java build tool marker is present. scip-java will
	// error confusingly otherwise, and the report becomes harder to
	// read.
	if !hasJavaBuildTool(in.Source) {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status: "skipped",
			Reason: "no Maven/Gradle/sbt project root detected",
		})
	}

	// Kotlin requires Gradle (scip-java's Maven path is Java-only).
	// If the caller asked us to index Kotlin but the project is
	// Maven-based, refuse with a clear reason instead of letting the
	// indexer silently produce a Java-only index.
	wantsKotlin := containsLanguage(in.Languages, langKotlin)
	if wantsKotlin && !hasGradleBuild(in.Source) {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status: "skipped",
			Reason: "Kotlin indexing requires Gradle (scip-java's Maven build does not support Kotlin)",
		})
	}

	// scip-java drives Maven/Gradle which write build artifacts (target/,
	// build/) directly under the project root. The snapshot at in.Source
	// is mounted read-only (:ro) by the Docker host, so those writes fail
	// with "Read-only file system". We work around this by copying the
	// source tree into a writable subdirectory of in.Workdir before running
	// the indexer.
	buildDir := filepath.Join(in.Workdir, "src")
	if err := copyDirInto(in.Source, buildDir); err != nil {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "failed to copy source tree to writable workdir",
			Error:      err.Error(),
			DurationMs: 0,
		})
	}

	// scip-java cleans the build cache before indexing, by design.
	// We pass --output to redirect the index file into our workdir.
	//
	// NOTE: scip-java (and Maven underneath it) writes all diagnostic
	// output — including [ERROR] lines — to STDOUT, not stderr. We merge
	// both streams into the error field on failure so the run report shows
	// the actual Maven error instead of an empty string.
	stdout, stderr, dur, err := runProcess(ctx, buildDir,
		"scip-java", "index", "--output", out)

	if err != nil {
		// Prefer stdout for the error message (Maven's [ERROR] lines land
		// there). Fall back to stderr if stdout is empty.
		errOutput := trimError(stdout)
		if errOutput == "" {
			errOutput = trimError(stderr)
		}
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-java index failed",
			Error:      errOutput,
			DurationMs: dur.Milliseconds(),
		})
	}
	_ = stdout

	if statSize(out) == 0 {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-java produced an empty index (build may have failed silently)",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}

	return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
		Status:     "ok",
		IndexPath:  out,
		DurationMs: dur.Milliseconds(),
	})
}

// copyDirInto recursively copies all files from src into dst, creating
// dst and any necessary subdirectories. Symlinks are followed.
//
// This is used to give scip-java (and any other JVM build tool) a
// writable working copy of the read-only source snapshot.
func copyDirInto(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFileMode(path, target, info.Mode())
	})
}

// copyFileMode copies a single regular file from src to dst, preserving mode bits.
func copyFileMode(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// hasJavaBuildTool returns true if root contains a recognised Java
// build-tool marker file. We DO NOT walk recursively here — scip-java
// is designed to be invoked at the project root, not on monorepos.
// If a monorepo contains a Java module in a subdirectory, callers
// can run the indexer with a sub-snapshot for that module separately.
func hasJavaBuildTool(root string) bool {
	for _, marker := range []string{
		"pom.xml", "build.gradle", "build.gradle.kts",
		"settings.gradle", "settings.gradle.kts",
		"build.sbt", "gradlew",
	} {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			return true
		}
	}
	return false
}

// hasGradleBuild is a stricter check than hasJavaBuildTool: it returns
// true only when a Gradle build file is present. Used to gate Kotlin
// indexing, which scip-java only supports through Gradle.
func hasGradleBuild(root string) bool {
	for _, marker := range []string{
		"build.gradle", "build.gradle.kts",
		"settings.gradle", "settings.gradle.kts",
		"gradlew",
	} {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			return true
		}
	}
	return false
}

// containsLanguage reports whether the list contains the given
// language. Used by the per-indexer Run methods to branch on which
// language(s) the orchestrator asked them to handle.
func containsLanguage(ls []language, want language) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// scip-typescript
//
// scip-typescript indexes TypeScript and JavaScript projects. It
// requires the project to have its dependencies installed (it walks
// into node_modules to resolve types), which means we run `npm install`
// first if no node_modules directory exists. We are conservative about
// this: a project with package-lock.json gets npm ci (deterministic),
// otherwise plain npm install.
//
// Project-detection heuristics:
//   - tsconfig.json present → run with default args (--cwd .)
//   - package.json only     → run with --infer-tsconfig (JS-only mode)
//   - neither               → skipped
//
// Workspace support:
//   - yarn workspaces       → --yarn-workspaces
//   - pnpm workspaces       → --pnpm-workspaces
//   - npm workspaces        → run once per workspace package (TODO)
//
// For Sprint 1 we handle the single-package case. Workspace fan-out
// is a follow-up.
// ---------------------------------------------------------------------

type scipTypeScript struct{}

func newScipTypeScript() indexer { return &scipTypeScript{} }

func (s *scipTypeScript) Name() string { return "scip-typescript" }

func (s *scipTypeScript) Run(ctx context.Context, in indexerInput) []LanguageResult {
	ensureDir(in.Workdir)
	out := outputPath(in.Workdir, s.Name())

	hasTsconfig := fileExists(in.Source, "tsconfig.json")
	hasPkgJSON := fileExists(in.Source, "package.json")
	if !hasTsconfig && !hasPkgJSON {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status: "skipped",
			Reason: "no tsconfig.json or package.json found",
		})
	}

	// Install dependencies if node_modules is absent. We skip this if
	// the user pre-installed (they often do via the host filesystem).
	// Failures here are downgraded to warnings on the per-language
	// result — scip-typescript will still try to index but with degraded
	// type resolution.
	if !dirExists(in.Source, "node_modules") {
		installCmd, installArgs := pickJSInstaller(in.Source)
		if _, _, _, err := runProcess(ctx, in.Source, installCmd, installArgs...); err != nil {
			// Continue — degraded mode is OK.
			logf("npm install failed for %s: %v", in.Source, err)
		}
	}

	args := []string{"index", "--output", out}
	if !hasTsconfig {
		args = append(args, "--infer-tsconfig")
	}
	// Disable global cache to keep our parallel runs isolated. Each
	// indexer worker has its own per-language workdir already; we
	// don't want runs to share state through ~/.cache.
	args = append(args, "--no-global-caches")

	_, stderr, dur, err := runProcess(ctx, in.Source, "scip-typescript", args...)
	if err != nil {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-typescript index failed",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}

	if statSize(out) == 0 {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-typescript produced an empty index",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}

	return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
		Status:     "ok",
		IndexPath:  out,
		DurationMs: dur.Milliseconds(),
	})
}

// pickJSInstaller chooses npm / yarn / pnpm based on lockfile presence.
// We prefer the deterministic install command (ci) when available.
func pickJSInstaller(root string) (string, []string) {
	switch {
	case fileExists(root, "pnpm-lock.yaml"):
		return "pnpm", []string{"install", "--frozen-lockfile"}
	case fileExists(root, "yarn.lock"):
		return "yarn", []string{"install", "--frozen-lockfile"}
	case fileExists(root, "package-lock.json"):
		return "npm", []string{"ci"}
	default:
		return "npm", []string{"install", "--no-audit", "--no-fund"}
	}
}

// ---------------------------------------------------------------------
// scip-python
//
// scip-python is a Node-based wrapper around Pyright. It needs the
// Python project's dependencies installed so Pyright can resolve
// imports. We run `pip install -r requirements.txt` or `pip install .`
// when a recognisable manifest is present.
// ---------------------------------------------------------------------

type scipPython struct{}

func newScipPython() indexer { return &scipPython{} }

func (s *scipPython) Name() string { return "scip-python" }

func (s *scipPython) Run(ctx context.Context, in indexerInput) []LanguageResult {
	ensureDir(in.Workdir)
	out := outputPath(in.Workdir, s.Name())

	if !hasPythonProject(in.Source) {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status: "skipped",
			Reason: "no Python project markers found",
		})
	}

	// Best-effort dep install; scip-python tolerates missing deps but
	// produces a worse index.
	installPythonDeps(ctx, in.Source)

	args := []string{"index", "--cwd", in.Source, "--output", out}
	_, stderr, dur, err := runProcess(ctx, in.Source, "scip-python", args...)
	if err != nil {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-python index failed",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}

	if statSize(out) == 0 {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-python produced an empty index",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}

	return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
		Status:     "ok",
		IndexPath:  out,
		DurationMs: dur.Milliseconds(),
	})
}

// installPythonDeps tries the most common manifest formats. Failures
// are silent; we only need the deps for better type resolution.
func installPythonDeps(ctx context.Context, root string) {
	venvBin := filepath.Join(root, ".venv", "bin", "pip")
	pip := "pip3"
	if _, err := os.Stat(venvBin); err == nil {
		pip = venvBin
	}
	switch {
	case fileExists(root, "requirements.txt"):
		_, _, _, _ = runProcess(ctx, root, pip, "install", "-r", "requirements.txt")
	case fileExists(root, "pyproject.toml"):
		_, _, _, _ = runProcess(ctx, root, pip, "install", ".")
	case fileExists(root, "setup.py"):
		_, _, _, _ = runProcess(ctx, root, pip, "install", ".")
	}
}

func hasPythonProject(root string) bool {
	for _, m := range []string{"pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "Pipfile"} {
		if fileExists(root, m) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// scip-go
//
// scip-go is a pure-Go binary. It works on any standard module-layout
// repository (go.mod at root). Multi-module monorepos require running
// from each module root; for Sprint 1 we handle the single-module case.
// ---------------------------------------------------------------------

type scipGo struct{}

func newScipGo() indexer { return &scipGo{} }

func (s *scipGo) Name() string { return "scip-go" }

func (s *scipGo) Run(ctx context.Context, in indexerInput) []LanguageResult {
	ensureDir(in.Workdir)
	out := outputPath(in.Workdir, s.Name())

	if !fileExists(in.Source, "go.mod") {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status: "skipped",
			Reason: "no go.mod at source root",
		})
	}

	// `go mod download` populates the module cache. scip-go uses
	// go/packages internally which would do this on demand anyway,
	// but doing it upfront gives us clearer error messages.
	_, _, _, _ = runProcess(ctx, in.Source, "go", "mod", "download")

	_, stderr, dur, err := runProcess(ctx, in.Source, "scip-go", "--output", out)
	if err != nil {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-go failed",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}

	if statSize(out) == 0 {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-go produced an empty index",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}

	return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
		Status:     "ok",
		IndexPath:  out,
		DurationMs: dur.Milliseconds(),
	})
}

// ---------------------------------------------------------------------
// scip-ruby
//
// scip-ruby is built on Sorbet. It needs the project's gems installed
// to follow non-stdlib references. Implemented as best-effort: a
// Gemfile triggers `bundle install` first.
//
// scip-ruby is still relatively young; we mark its Status conservatively
// and surface stderr in the report so users can triage.
// ---------------------------------------------------------------------

type scipRuby struct{}

func newScipRuby() indexer { return &scipRuby{} }

func (s *scipRuby) Name() string { return "scip-ruby" }

func (s *scipRuby) Run(ctx context.Context, in indexerInput) []LanguageResult {
	ensureDir(in.Workdir)
	out := outputPath(in.Workdir, s.Name())

	// We don't pre-install scip-ruby in the Sprint 1 image because the
	// gem build is heavy. Return skipped so callers learn the indexer
	// is not available without a confusing exec error.
	//
	// Future implementation (kept here as a comment so the wiring is
	// obvious):
	//
	//   if fileExists(in.Source, "Gemfile") {
	//       _, _, _, _ = runProcess(ctx, in.Source, "bundle", "install")
	//   }
	//   _, stderr, dur, err := runProcess(ctx, in.Source, "scip-ruby", "--output", out)
	//   ...
	_ = out
	_ = ctx
	return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
		Status: "skipped",
		Reason: "scip-ruby not yet bundled in this image (planned)",
	})
}

// ---------------------------------------------------------------------
// scip-clang
//
// scip-clang requires a `compile_commands.json` produced by CMake or
// Bear. We DO NOT attempt to build the project — that varies wildly
// across C/C++ projects. Users with a CMake build:
//
//     cmake -B build -DCMAKE_EXPORT_COMPILE_COMMANDS=ON .
//
// before invoking DiffMind. We then point scip-clang at that file.
// ---------------------------------------------------------------------

type scipClang struct{}

func newScipClang() indexer { return &scipClang{} }

func (s *scipClang) Name() string { return "scip-clang" }

func (s *scipClang) Run(ctx context.Context, in indexerInput) []LanguageResult {
	ensureDir(in.Workdir)
	out := outputPath(in.Workdir, s.Name())

	// Sprint 1: not yet bundled in the image. Same rationale as scip-ruby.
	_ = out
	return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
		Status: "skipped",
		Reason: "scip-clang not yet bundled in this image (planned)",
	})
}

// ---------------------------------------------------------------------
// scip-dotnet
//
// scip-dotnet indexes C# / F# / VB.NET projects via `dotnet build`.
// The indexer ships as a .NET tool you invoke against a .csproj or
// .sln; the in-image wrapper runs `dotnet restore` first to populate
// the NuGet cache, then `scip-dotnet index`.
//
// PROJECT DETECTION
//
// We look for either a `.sln` solution file at the repo root or a
// single top-level `.csproj` / `.fsproj` / `.vbproj`. When several
// project files exist at the root, scip-dotnet itself handles the
// fan-out (it indexes the whole solution graph).
//
// LIMITATIONS
//
//   - Requires a working .NET SDK (>= 8) inside the container.
//     Mismatched SDK versions across the project's TargetFramework
//     produce confusing failures; we surface the .NET CLI's stderr
//     verbatim when that happens.
//   - F# support is implicit (scip-dotnet handles it the same way as C#);
//     the diffmind language token "fsharp" is currently mapped onto
//     csharp via the indexer registry. F# users who want a separate
//     status row should pass --languages csharp,fsharp explicitly.
// ---------------------------------------------------------------------

type scipDotnet struct{}

func newScipDotnet() indexer { return &scipDotnet{} }

func (s *scipDotnet) Name() string { return "scip-dotnet" }

func (s *scipDotnet) Run(ctx context.Context, in indexerInput) []LanguageResult {
	ensureDir(in.Workdir)
	out := outputPath(in.Workdir, s.Name())

	target := pickDotnetProjectTarget(in.Source)
	if target == "" {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status: "skipped",
			Reason: "no .csproj / .sln / .fsproj found at source root",
		})
	}

	// dotnet restore primes the NuGet cache so the subsequent index
	// doesn't have to do it inline. Failure here is downgraded to a
	// warning: scip-dotnet may still succeed if a cached restore is
	// already present.
	if _, _, _, err := runProcess(ctx, in.Source, "dotnet", "restore", target); err != nil {
		logf("dotnet restore failed (continuing): %v", err)
	}

	_, stderr, dur, err := runProcess(ctx, in.Source,
		"scip-dotnet", "index",
		"--working-directory", in.Source,
		"--output", out,
		target,
	)
	if err != nil {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-dotnet index failed",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}
	if statSize(out) == 0 {
		return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
			Status:     "failed",
			Reason:     "scip-dotnet produced an empty index",
			Error:      trimError(stderr),
			DurationMs: dur.Milliseconds(),
		})
	}
	return fanOutLanguages(in.Languages, s.Name(), LanguageResult{
		Status:     "ok",
		IndexPath:  out,
		DurationMs: dur.Milliseconds(),
	})
}

// pickDotnetProjectTarget picks the file scip-dotnet should be pointed
// at. Solution files (.sln) take precedence over individual project
// files because they describe the whole build graph; if multiple
// solutions exist we pick the alphabetically first one for deterministic
// behavior.
//
// Returns "" if nothing recognisable is at root.
func pickDotnetProjectTarget(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var sln, proj string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".sln":
			if sln == "" || name < sln {
				sln = name
			}
		case ".csproj", ".fsproj", ".vbproj":
			if proj == "" || name < proj {
				proj = name
			}
		}
	}
	if sln != "" {
		return sln
	}
	return proj
}

// ---------------------------------------------------------------------
// Small filesystem helpers used by the indexer implementations above.
// ---------------------------------------------------------------------

func fileExists(root, name string) bool {
	st, err := os.Stat(filepath.Join(root, name))
	return err == nil && !st.IsDir()
}

func dirExists(root, name string) bool {
	st, err := os.Stat(filepath.Join(root, name))
	return err == nil && st.IsDir()
}

// _ keeps fmt referenced even if every error path is %w-only.
var _ = fmt.Sprintf
