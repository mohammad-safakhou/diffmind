package indexerbuild

import (
	"io/fs"
	"testing"
)

// TestEmbedContainsDockerfile is a smoke test: the embedded FS must
// expose Dockerfile.indexer at its root. Without this file the
// auto-build path fails before invoking docker.
func TestEmbedContainsDockerfile(t *testing.T) {
	data, err := Context.ReadFile(DockerfileName)
	if err != nil {
		t.Fatalf("read %s: %v", DockerfileName, err)
	}
	if len(data) < 1024 {
		// The real Dockerfile is several KB. A near-empty payload
		// usually means the file was renamed without updating the
		// go:embed pattern.
		t.Errorf("Dockerfile.indexer is suspiciously small (%d bytes); embed pattern broken?", len(data))
	}
}

// TestEmbedContainsWrapperMain ensures the entrypoint Go source is
// reachable via the embedded FS at the expected path. The Dockerfile
// references `./wrapper`, so a missing wrapper/main.go would only
// surface as a docker build failure deep inside a multi-stage cold
// build — slow to debug. This test fails fast.
func TestEmbedContainsWrapperMain(t *testing.T) {
	if _, err := Context.ReadFile("wrapper/main.go"); err != nil {
		t.Fatalf("wrapper/main.go missing from embed: %v", err)
	}
}

// TestEmbedHasWrapperGoSources verifies the embed FS actually carries
// at least one non-test wrapper source. This is the file the Docker
// builder will compile; without it, the cold build fails at the
// wrapper-builder stage with a confusing "no Go files in /src/wrapper"
// error. Catching that here is much faster than catching it 5 min
// into a docker build.
//
// We DELIBERATELY do not assert "no _test.go files" — go:embed of a
// whole directory DOES include them. They're stripped at extraction
// time by internal/indexer/build.go::extractEmbed. The test for that
// behaviour lives next to the extractor in TestExtractEmbedSkipsTestFiles.
func TestEmbedHasWrapperGoSources(t *testing.T) {
	prodCount := 0
	err := fs.WalkDir(Context, "wrapper", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if hasSuffix(path, ".go") && !hasSuffix(path, "_test.go") {
			prodCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if prodCount == 0 {
		t.Error("wrapper directory contains no non-test .go files; embed pattern broken?")
	}
}

// hasSuffix is a tiny stdlib-free helper (the embed package imports
// no third-party deps; we keep tests stdlib-light too).
func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
