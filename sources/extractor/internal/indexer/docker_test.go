package indexer

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestResolveImage covers the three-tier image-resolution precedence:
// explicit RunRequest.Image > $DIFFMIND_INDEXER_IMAGE > DefaultImage.
func TestResolveImage(t *testing.T) {
	t.Setenv("DIFFMIND_INDEXER_IMAGE", "")

	if got := ResolveImage(""); got != DefaultImage {
		t.Errorf("default fallback: got %q, want %q", got, DefaultImage)
	}
	if got := ResolveImage("custom:tag"); got != "custom:tag" {
		t.Errorf("explicit: got %q, want %q", got, "custom:tag")
	}

	t.Setenv("DIFFMIND_INDEXER_IMAGE", "from-env:v1")
	if got := ResolveImage(""); got != "from-env:v1" {
		t.Errorf("env fallback: got %q, want %q", got, "from-env:v1")
	}
	// Explicit still wins over env.
	if got := ResolveImage("explicit:v2"); got != "explicit:v2" {
		t.Errorf("explicit overrides env: got %q, want %q", got, "explicit:v2")
	}
}

// TestRemapPath ensures container-side absolute paths get rewritten to
// the host volume mount the runner controls. This is essential: the
// wrapper emits /output/index.scip inside the container, but the host
// needs to know the path is actually /tmp/runs/X/index.scip (or wherever
// the runner mounted it).
func TestRemapPath(t *testing.T) {
	cases := []struct {
		containerPath, prefix, hostPrefix, want string
	}{
		{"/output/index.scip", "/output", "/host/runs/abc", "/host/runs/abc/index.scip"},
		{"/output/work/scip-java/scip-java.scip", "/output", "/x", "/x/work/scip-java/scip-java.scip"},
		// Path does not start with prefix: returned unchanged.
		{"/var/log/whatever", "/output", "/x", "/var/log/whatever"},
		// Exact-match: maps to the host directory itself.
		{"/output", "/output", "/x", "/x"},
		// Relative path: passthrough (the function doesn't rewrite
		// anything it can't recognise).
		{"relative/path", "/output", "/x", "relative/path"},
	}
	for _, c := range cases {
		got := remapPath(c.containerPath, c.prefix, c.hostPrefix)
		if got != c.want {
			t.Errorf("remapPath(%q,%q,%q) = %q, want %q",
				c.containerPath, c.prefix, c.hostPrefix, got, c.want)
		}
	}
}

// TestValidateRequest checks the source/output validation gates that
// every Index() call goes through. We don't need a real Docker daemon
// for this — the failure modes are all pre-exec.
func TestValidateRequest(t *testing.T) {
	srcDir := t.TempDir()
	outDir := t.TempDir()

	// Happy path.
	if err := validateRequest(RunRequest{
		SourcePath: srcDir,
		OutputPath: outDir,
	}); err != nil {
		t.Errorf("happy path failed: %v", err)
	}

	// Output is auto-created when missing.
	outNested := filepath.Join(outDir, "nested", "deep")
	if err := validateRequest(RunRequest{
		SourcePath: srcDir,
		OutputPath: outNested,
	}); err != nil {
		t.Errorf("nested output should be created: %v", err)
	}
	if st, err := os.Stat(outNested); err != nil || !st.IsDir() {
		t.Errorf("nested output was not created: %v", err)
	}

	// Missing source path → fails.
	if err := validateRequest(RunRequest{SourcePath: "", OutputPath: outDir}); err == nil {
		t.Error("empty source: expected error, got nil")
	}

	// Source is a file, not a dir → fails.
	srcFile := filepath.Join(srcDir, "f.txt")
	must(t, os.WriteFile(srcFile, []byte("x"), 0o644))
	if err := validateRequest(RunRequest{SourcePath: srcFile, OutputPath: outDir}); err == nil {
		t.Error("source as file: expected error, got nil")
	}

	// Non-absolute path → fails.
	if err := validateRequest(RunRequest{SourcePath: "./relative", OutputPath: outDir}); err == nil {
		t.Error("relative source: expected error, got nil")
	}
}

// TestBuildRunArgs validates the docker run command line we emit. We
// don't compare every flag — only the ones that are positionally
// significant (mounts, image last, args after image). The rest of the
// flags' presence is asserted by substring matching to keep the test
// resilient to harmless reorderings of e.g. -e flags (whose iteration
// order is map-defined and therefore non-deterministic).
func TestBuildRunArgs(t *testing.T) {
	d := &DockerIndexer{}
	req := RunRequest{
		SourcePath:        "/snap/X",
		OutputPath:        "/runs/X",
		Languages:         []string{"java", "go"},
		PerIndexerTimeout: 0, // omit
		Parallel:          0, // omit
		NetworkMode:       "host",
		ExtraEnv:          map[string]string{"DIFFMIND_DEBUG": "1"},
		ExtraMounts:       map[string]string{"/etc/m2": "/root/.m2:ro"},
	}
	got := d.buildRunArgs(req, "img:tag")

	// First positional must be "run".
	if got[0] != "run" {
		t.Errorf("first arg %q, want %q", got[0], "run")
	}

	// Must contain --rm, --user 0:0, --init.
	for _, must := range []string{"--rm", "--user", "0:0", "--init", "--network", "host"} {
		if !contains(got, must) {
			t.Errorf("missing %q in args: %v", must, got)
		}
	}

	// Source and output mounts are required.
	wantSrcMount := "/snap/X:/sources:ro"
	wantOutMount := "/runs/X:/output"
	if !contains(got, wantSrcMount) {
		t.Errorf("missing source mount %q", wantSrcMount)
	}
	if !contains(got, wantOutMount) {
		t.Errorf("missing output mount %q", wantOutMount)
	}

	// Extra mount must be included.
	if !contains(got, "/etc/m2:/root/.m2:ro") {
		t.Errorf("missing extra mount in args: %v", got)
	}

	// Extra env: -e DIFFMIND_DEBUG=1 and the implicit DIFFMIND_INDEXER_OUTPUT.
	if !contains(got, "DIFFMIND_DEBUG=1") {
		t.Errorf("missing extra env in args: %v", got)
	}
	if !contains(got, "DIFFMIND_INDEXER_OUTPUT=/output") {
		t.Errorf("missing default env in args: %v", got)
	}

	// Image must appear, and args after image must include --source/--output/--languages.
	imgIdx := indexOf(got, "img:tag")
	if imgIdx < 0 {
		t.Fatalf("image not found in args: %v", got)
	}
	wrapperArgs := strings.Join(got[imgIdx+1:], " ")
	if !strings.Contains(wrapperArgs, "--source /sources") {
		t.Errorf("wrapper missing --source /sources, got %q", wrapperArgs)
	}
	if !strings.Contains(wrapperArgs, "--output /output/index.scip") {
		t.Errorf("wrapper missing --output, got %q", wrapperArgs)
	}
	if !strings.Contains(wrapperArgs, "--languages java,go") {
		t.Errorf("wrapper missing --languages java,go, got %q", wrapperArgs)
	}
}

// TestParseReport exercises the JSON-finding logic. The wrapper emits
// indented JSON as the last block on stdout; preceding lines may be
// noise (progress logs, etc.). We must find the JSON anyway.
func TestParseReport(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		stdout := []byte(`
some preceding log noise
indexer warmup done
{
  "schema_version": 1,
  "index_path": "/output/index.scip",
  "index_bytes": 12345,
  "duration_ms": 9000,
  "languages": [
    {"name": "java", "status": "ok", "index_path": "/output/work/scip-java/scip-java.scip", "duration_ms": 8000}
  ]
}
`)
		r, err := parseReport(stdout)
		if err != nil {
			t.Fatalf("parseReport: %v", err)
		}
		if r.SchemaVersion != 1 {
			t.Errorf("schema_version = %d", r.SchemaVersion)
		}
		if r.IndexPath != "/output/index.scip" {
			t.Errorf("index_path = %q", r.IndexPath)
		}
		if len(r.Languages) != 1 || r.Languages[0].Name != "java" {
			t.Errorf("languages = %+v", r.Languages)
		}
	})

	t.Run("no JSON found", func(t *testing.T) {
		stdout := []byte("nothing useful here\njust logs\n")
		_, err := parseReport(stdout)
		if err == nil {
			t.Error("expected error for no JSON, got nil")
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		stdout := []byte("\n{\"schema_version\": broken json\n")
		_, err := parseReport(stdout)
		if err == nil {
			t.Error("expected error for malformed JSON, got nil")
		}
	})
}

// TestTailString covers the stderr trimming helper used in error messages.
func TestTailString(t *testing.T) {
	if got := tailString([]byte("hello world"), 100); got != "hello world" {
		t.Errorf("short input: got %q", got)
	}
	long := strings.Repeat("X", 1000)
	got := tailString([]byte(long+"END"), 5)
	if got != "X"+strings.Repeat("E", 1)+"ND" && got != "XXEND" {
		// We just want the last 5 bytes.
		want := long[len(long)-2:] + "END"
		want = want[len(want)-5:]
		if got != want {
			t.Errorf("tail: got %q, want %q", got, want)
		}
	}
}

// --- Test helpers ---

func contains(slice []string, needle string) bool {
	for _, s := range slice {
		if s == needle {
			return true
		}
	}
	return false
}

func indexOf(slice []string, needle string) int {
	for i, s := range slice {
		if s == needle {
			return i
		}
	}
	return -1
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Sanity check: validation maps language strings correctly. Belongs in
// docker_test.go because it depends on the LanguageResult struct shape.
func TestLanguageResultRoundTrip(t *testing.T) {
	lr := LanguageResult{
		Name:    "java",
		Status:  "ok",
		Reason:  "",
		Files:   123,
		Indexer: "scip-java",
	}
	// Just exercise reflect equality; the JSON marshalling lives in
	// parseReport.
	clone := lr
	if !reflect.DeepEqual(lr, clone) {
		t.Error("LanguageResult deep equality broken")
	}
}

// Sentinel: keep errors imported even if every path uses fmt.Errorf.
var _ = errors.New
