package scip

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Integration tests in this file run the REAL per-language SCIP
// indexer against the matching fixture under testdata/fixtures/.
//
// Each test:
//   1. Skips when the indexer binary is not on PATH (CI without the
//      diffmind-indexer image, dev machines without local installs).
//   2. Runs the indexer to produce a real index.scip.
//   3. Loads the index with scip.Load.
//   4. Resolves the fixture's "handler" symbol via the public Resolver.
//   5. Walks the call graph and asserts the path reaches the expected
//      target symbol(s).
//
// The fixtures are tiny but use the SAME structural pattern diffmind
// targets in production code: HTTP handler → service method →
// repository / dependency. If the walker fails on a fixture this small,
// it will fail on a real codebase too. If it succeeds, we have proof
// the SCIP path produces correct connections end-to-end for that
// language.
//
// NOTE on running these on your machine
//   - Go:         `go install github.com/scip-code/scip-go/cmd/scip-go@latest`
//   - Java:       requires JDK + Maven + scip-java (see Dockerfile.indexer)
//   - TypeScript: `npm i -g @sourcegraph/scip-typescript`
//   - Python:     `npm i -g @sourcegraph/scip-python`
//
// Or just run the full `diffmind-indexer` image which bundles all of
// the above:
//   `docker run -v $(pwd):/sources ghcr.io/anomalyco/diffmind-indexer:latest`

// indexerCommand finds the binary on PATH and returns its absolute
// location, or "" if it's not installed. A bare name like "scip-go"
// is enough; PATH is searched.
func indexerCommand(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

// fixturePath returns the absolute path to a fixture directory.
// Relative to this test file: testdata/fixtures/<name>.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	p := filepath.Join(cwd, "testdata", "fixtures", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture %q not found at %s: %v", name, p, err)
	}
	return p
}

// runIndexerCmd executes the indexer in `dir` and returns the output
// SCIP file path. Fails the test on indexer exit code != 0.
//
// We point the indexer at a per-test temp file so concurrent runs do
// not race on a shared `index.scip`.
func runIndexerCmd(t *testing.T, dir string, name string, extraArgs ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "index.scip")
	args := append([]string{}, extraArgs...)
	args = append(args, "--output", out)

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr // bubble up to `go test -v`
	// Most indexers' first run is fast (<10s) but cold caches can
	// push that to a minute. 5 minutes is generous for fixtures this
	// small without being abusive in CI.
	timer := time.AfterFunc(5*time.Minute, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()

	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s in %s failed after %s: %v", name, dir, time.Since(start), err)
	}
	t.Logf("%s produced %s in %s", name, out, time.Since(start))
	return out
}

// containsHandlerToRepository asserts that the walker found at least
// one path from `entrySymbol` to a target symbol containing
// `targetSubstr`. Returns the matching path for further inspection.
//
// `targetSubstr` lets the assertion work regardless of indexer-specific
// symbol formatting differences (package prefix, scheme name, etc.).
func containsHandlerToRepository(t *testing.T, idx *Index, entrySymbol, targetSubstr string) (Path, bool) {
	t.Helper()
	w := NewWalker(idx)
	paths := w.Walk(entrySymbol, WalkConfig{
		IsTarget: func(sym string) bool {
			return strings.Contains(sym, targetSubstr)
		},
	})
	for _, p := range paths {
		return p, true
	}
	return Path{}, false
}

// ---------------------------------------------------------------------
// Go fixture
// ---------------------------------------------------------------------

func TestIntegrationGoGin(t *testing.T) {
	bin := indexerCommand("scip-go")
	if bin == "" {
		t.Skip("scip-go not installed; install with `go install github.com/scip-code/scip-go/cmd/scip-go@latest`")
	}
	fixture := fixturePath(t, "go-gin")
	indexPath := runIndexerCmd(t, fixture, bin)

	idx, err := Load(indexPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx.DocumentCount() == 0 {
		t.Fatalf("empty index")
	}

	// Resolve the GetCampaign handler. The handler is declared at
	// line 69 in main.go (1-based) — we use the resolver's positional
	// path, with the name as a fallback safety net.
	resolver := NewResolver(idx)
	res := resolver.Resolve(EntityLocation{
		Name:      "GetCampaign",
		File:      "main.go",
		StartLine: 69,
		StartCol:  1,
	})
	if len(res.Symbols) == 0 {
		t.Fatalf("could not resolve GetCampaign in %s", indexPath)
	}
	entry := res.Symbols[0]
	t.Logf("entry symbol: %s (source=%s confidence=%.2f)", entry, res.Source, res.Confidence)

	// The walker must reach CampaignRepository.FindByID through the
	// service layer. The chain in the fixture is:
	//   GetCampaign → CampaignService.GetByID → CampaignRepository.FindByID
	p, ok := containsHandlerToRepository(t, idx, entry, "CampaignRepository#FindByID")
	if !ok {
		t.Fatalf("walker did not find a path to CampaignRepository.FindByID from %s", entry)
	}
	if len(p.Steps) < 2 {
		t.Errorf("expected at least 2 hops (handler→service→repo), got %d", len(p.Steps))
	}
	t.Logf("path: %d steps (%s → %s)", len(p.Steps), entry, p.TargetSymbol)
}

// ---------------------------------------------------------------------
// Java fixture
// ---------------------------------------------------------------------

func TestIntegrationJavaSpring(t *testing.T) {
	bin := indexerCommand("scip-java")
	if bin == "" {
		t.Skip("scip-java not installed; use the diffmind-indexer image or install scip-java locally")
	}
	if indexerCommand("mvn") == "" {
		t.Skip("mvn not installed; required by scip-java")
	}
	fixture := fixturePath(t, "java-spring")
	indexPath := runIndexerCmd(t, fixture, bin, "index")

	idx, err := Load(indexPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx.DocumentCount() == 0 {
		t.Fatalf("empty index")
	}

	resolver := NewResolver(idx)
	res := resolver.Resolve(EntityLocation{
		Name:      "deleteCampaign",
		File:      "src/main/java/example/CampaignController.java",
		StartLine: 20,
	})
	if len(res.Symbols) == 0 {
		t.Fatalf("could not resolve deleteCampaign in %s", indexPath)
	}
	entry := res.Symbols[0]
	p, ok := containsHandlerToRepository(t, idx, entry, "CampaignRepository#deleteById")
	if !ok {
		t.Fatalf("walker did not find a path to CampaignRepository.deleteById from %s", entry)
	}
	t.Logf("java path: %d steps (%s → %s)", len(p.Steps), entry, p.TargetSymbol)
}

// ---------------------------------------------------------------------
// TypeScript fixture
// ---------------------------------------------------------------------

func TestIntegrationTypeScriptExpress(t *testing.T) {
	bin := indexerCommand("scip-typescript")
	if bin == "" {
		t.Skip("scip-typescript not installed; `npm i -g @sourcegraph/scip-typescript`")
	}
	fixture := fixturePath(t, "typescript-express")
	// scip-typescript wants `npm install` first.
	if _, err := os.Stat(filepath.Join(fixture, "node_modules")); err != nil {
		install := exec.Command("npm", "install", "--no-audit", "--no-fund")
		install.Dir = fixture
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			t.Fatalf("npm install: %v", err)
		}
	}
	indexPath := runIndexerCmd(t, fixture, bin, "index")

	idx, err := Load(indexPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	resolver := NewResolver(idx)
	res := resolver.Resolve(EntityLocation{
		Name:      "getCampaign",
		File:      "src/handlers.ts",
		StartLine: 10,
	})
	if len(res.Symbols) == 0 {
		t.Fatalf("could not resolve getCampaign in %s", indexPath)
	}
	entry := res.Symbols[0]
	p, ok := containsHandlerToRepository(t, idx, entry, "findById")
	if !ok {
		t.Fatalf("walker did not find a path to CampaignRepository.findById from %s", entry)
	}
	t.Logf("ts path: %d steps", len(p.Steps))
}

// ---------------------------------------------------------------------
// Python fixture
// ---------------------------------------------------------------------

func TestIntegrationPythonFastAPI(t *testing.T) {
	bin := indexerCommand("scip-python")
	if bin == "" {
		t.Skip("scip-python not installed; `npm i -g @sourcegraph/scip-python`")
	}
	fixture := fixturePath(t, "python-fastapi")
	indexPath := runIndexerCmd(t, fixture, bin, "index")

	idx, err := Load(indexPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	resolver := NewResolver(idx)
	res := resolver.Resolve(EntityLocation{
		Name:      "get_campaign",
		File:      "app/handlers.py",
		StartLine: 8,
	})
	if len(res.Symbols) == 0 {
		t.Fatalf("could not resolve get_campaign in %s", indexPath)
	}
	entry := res.Symbols[0]
	p, ok := containsHandlerToRepository(t, idx, entry, "find_by_id")
	if !ok {
		t.Fatalf("walker did not find a path to find_by_id from %s", entry)
	}
	t.Logf("py path: %d steps", len(p.Steps))
}

// ---------------------------------------------------------------------
// Kotlin fixture
//
// scip-java handles Kotlin via the semanticdb-kotlinc compiler plugin,
// which is wired up through Gradle. The test:
//   - requires scip-java + a working `gradle` on PATH
//   - is otherwise structurally identical to the Java test
// ---------------------------------------------------------------------

func TestIntegrationKotlinSpring(t *testing.T) {
	bin := indexerCommand("scip-java")
	if bin == "" {
		t.Skip("scip-java not installed; use the diffmind-indexer image or install scip-java locally")
	}
	if indexerCommand("gradle") == "" {
		t.Skip("gradle not installed; scip-java's Kotlin path needs Gradle")
	}
	fixture := fixturePath(t, "kotlin-spring")
	indexPath := runIndexerCmd(t, fixture, bin, "index")

	idx, err := Load(indexPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx.DocumentCount() == 0 {
		t.Fatalf("empty index")
	}

	resolver := NewResolver(idx)
	res := resolver.Resolve(EntityLocation{
		Name:      "deleteCampaign",
		File:      "src/main/kotlin/example/CampaignController.kt",
		StartLine: 14, // anchor line in the fixture
	})
	if len(res.Symbols) == 0 {
		t.Fatalf("could not resolve deleteCampaign in %s", indexPath)
	}
	entry := res.Symbols[0]
	p, ok := containsHandlerToRepository(t, idx, entry, "CampaignRepository#deleteById")
	if !ok {
		t.Fatalf("walker did not find a path to CampaignRepository.deleteById from %s", entry)
	}
	t.Logf("kotlin path: %d steps (%s → %s)", len(p.Steps), entry, p.TargetSymbol)
}

// ---------------------------------------------------------------------
// JavaScript fixture (plain JS, no TypeScript)
//
// scip-typescript supports JS via `--infer-tsconfig`. The fixture uses
// CommonJS modules to mimic a typical Node service. The walker assertion
// is structurally identical to the TypeScript test, but the indexer is
// invoked in a different mode.
// ---------------------------------------------------------------------

func TestIntegrationJavaScriptExpress(t *testing.T) {
	bin := indexerCommand("scip-typescript")
	if bin == "" {
		t.Skip("scip-typescript not installed; `npm i -g @sourcegraph/scip-typescript`")
	}
	fixture := fixturePath(t, "javascript-express")
	indexPath := runIndexerCmd(t, fixture, bin, "index", "--infer-tsconfig")

	idx, err := Load(indexPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx.DocumentCount() == 0 {
		t.Fatalf("empty index")
	}

	resolver := NewResolver(idx)
	res := resolver.Resolve(EntityLocation{
		Name:      "getCampaign",
		File:      "src/handlers.js",
		StartLine: 21,
	})
	if len(res.Symbols) == 0 {
		t.Fatalf("could not resolve getCampaign in %s", indexPath)
	}
	entry := res.Symbols[0]
	p, ok := containsHandlerToRepository(t, idx, entry, "findById")
	if !ok {
		t.Fatalf("walker did not find a path to findById from %s", entry)
	}
	t.Logf("js path: %d steps", len(p.Steps))
}

// ---------------------------------------------------------------------
// C# fixture
//
// scip-dotnet indexes C# / F# / VB.NET via `dotnet build`. We assume:
//   - `dotnet` is on PATH (otherwise we skip)
//   - `scip-dotnet` is on PATH (installed via `dotnet tool install -g`)
//
// The fixture is a minimal class library (no ASP.NET dependencies) so
// the test stays fast and avoids dragging in heavy NuGet downloads
// for the runtime stack.
// ---------------------------------------------------------------------

func TestIntegrationCSharpAspnet(t *testing.T) {
	bin := indexerCommand("scip-dotnet")
	if bin == "" {
		t.Skip("scip-dotnet not installed; `dotnet tool install -g Sourcegraph.Scip.Dotnet`")
	}
	if indexerCommand("dotnet") == "" {
		t.Skip("dotnet SDK not installed")
	}
	fixture := fixturePath(t, "csharp-aspnet")
	indexPath := runIndexerCmd(t, fixture, bin,
		"index", "--working-directory", fixture,
	)

	idx, err := Load(indexPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx.DocumentCount() == 0 {
		t.Fatalf("empty index")
	}

	resolver := NewResolver(idx)
	res := resolver.Resolve(EntityLocation{
		Name:      "DeleteCampaign",
		File:      "CampaignController.cs",
		StartLine: 20, // anchor line in the fixture
	})
	if len(res.Symbols) == 0 {
		t.Fatalf("could not resolve DeleteCampaign in %s", indexPath)
	}
	entry := res.Symbols[0]
	p, ok := containsHandlerToRepository(t, idx, entry, "CampaignRepository#DeleteById")
	if !ok {
		t.Fatalf("walker did not find a path to CampaignRepository.DeleteById from %s", entry)
	}
	t.Logf("csharp path: %d steps (%s → %s)", len(p.Steps), entry, p.TargetSymbol)
}
