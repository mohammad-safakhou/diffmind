package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mohammad-safakhou/diffmind/indexerbuild"
)

// TestExtractEmbedSkipsTestFiles confirms that extraction filters out
// *_test.go files. Production wrapper sources go to the extracted
// build context; their test files stay behind.
func TestExtractEmbedSkipsTestFiles(t *testing.T) {
	// Build a synthetic FS so the assertion is independent of what
	// indexerbuild/wrapper currently contains.
	src := fstest.MapFS{
		"wrapper/main.go":           {Data: []byte("package main\nfunc main(){}\n")},
		"wrapper/main_test.go":      {Data: []byte("package main\n")},
		"wrapper/helper.go":         {Data: []byte("package main\n")},
		"wrapper/helper_test.go":    {Data: []byte("package main\n")},
		"Dockerfile.indexer":        {Data: []byte("FROM scratch\n")},
	}
	dst := t.TempDir()
	if err := extractEmbed(src, dst); err != nil {
		t.Fatalf("extractEmbed: %v", err)
	}

	mustExist := []string{"wrapper/main.go", "wrapper/helper.go", "Dockerfile.indexer"}
	mustNotExist := []string{"wrapper/main_test.go", "wrapper/helper_test.go"}

	for _, p := range mustExist {
		if _, err := os.Stat(filepath.Join(dst, p)); err != nil {
			t.Errorf("expected %s to be extracted: %v", p, err)
		}
	}
	for _, p := range mustNotExist {
		if _, err := os.Stat(filepath.Join(dst, p)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("test file %s should NOT have been extracted (err=%v)", p, err)
		}
	}
}

// TestComputeEmbedDigestStable: the same input produces the same
// digest across runs. Crucial because the cache directory name is
// derived from this digest — instability would invalidate the cache
// on every run and force re-extraction.
func TestComputeEmbedDigestStable(t *testing.T) {
	src := fstest.MapFS{
		"a.txt":     {Data: []byte("hello")},
		"sub/b.txt": {Data: []byte("world")},
	}
	a, err := computeEmbedDigest(src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := computeEmbedDigest(src)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("digest unstable: %s vs %s", a, b)
	}
	if len(a) != 16 {
		t.Errorf("digest length = %d, want 16", len(a))
	}
}

// TestComputeEmbedDigestDifferentiatesContent: a single-byte content
// change must produce a different digest. Otherwise a Dockerfile edit
// could be hidden by a warm cache.
func TestComputeEmbedDigestDifferentiatesContent(t *testing.T) {
	base := fstest.MapFS{"a.txt": {Data: []byte("hello")}}
	tweaked := fstest.MapFS{"a.txt": {Data: []byte("helloo")}}
	a, _ := computeEmbedDigest(base)
	b, _ := computeEmbedDigest(tweaked)
	if a == b {
		t.Errorf("digest collided across different content: %s", a)
	}
}

// TestComputeEmbedDigestDifferentiatesPaths: renaming a file must
// change the digest, even when content stays identical.
func TestComputeEmbedDigestDifferentiatesPaths(t *testing.T) {
	a, _ := computeEmbedDigest(fstest.MapFS{"a.txt": {Data: []byte("x")}})
	b, _ := computeEmbedDigest(fstest.MapFS{"b.txt": {Data: []byte("x")}})
	if a == b {
		t.Errorf("digest collided across different paths: %s", a)
	}
}

// TestEnsureImageNeverPolicyReturnsSentinel verifies the
// AutoBuildNever path when the image is absent. We can't easily fake
// "docker image inspect" returning fail without subprocess plumbing,
// so we use a builder that points DockerPath at a non-existent
// binary — the inspect call will fail (= image absent in our model)
// and the policy should bail out with ErrImageMissing.
func TestEnsureImageNeverPolicyReturnsSentinel(t *testing.T) {
	b := &Builder{DockerPath: "/definitely/not/a/binary/here"}
	_, err := b.EnsureImage(t.Context(), "diffmind-indexer:test-only", AutoBuildNever)
	if !errors.Is(err, ErrImageMissing) {
		t.Errorf("expected ErrImageMissing, got %v", err)
	}
}

// TestPrepareContextDirIdempotent: running the extraction twice
// should reuse the cache directory and NOT re-write its contents.
// We detect re-writes by capturing the mtime of one extracted file
// before and after the second call.
func TestPrepareContextDirIdempotent(t *testing.T) {
	b := &Builder{BuildContextRoot: t.TempDir()}
	digest, err := computeEmbedDigest(indexerbuild.Context)
	if err != nil {
		t.Fatal(err)
	}
	dir1, err := b.prepareContextDir(digest)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	// Confirm the stamp file exists.
	stamp := filepath.Join(dir1, ".extracted")
	st1, err := os.Stat(stamp)
	if err != nil {
		t.Fatalf("stamp not written: %v", err)
	}

	dir2, err := b.prepareContextDir(digest)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if dir1 != dir2 {
		t.Errorf("second prepare returned different dir: %s vs %s", dir1, dir2)
	}
	st2, err := os.Stat(stamp)
	if err != nil {
		t.Fatalf("stamp lost on second prepare: %v", err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("stamp was rewritten on warm cache: %v -> %v", st1.ModTime(), st2.ModTime())
	}
}

// TestPrepareContextDirRecoversFromPartialExtraction: simulate a
// previous crashed extraction (dir present, stamp missing) and
// confirm the next call wipes-and-redoes the extraction.
func TestPrepareContextDirRecoversFromPartialExtraction(t *testing.T) {
	b := &Builder{BuildContextRoot: t.TempDir()}
	digest, err := computeEmbedDigest(indexerbuild.Context)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(b.BuildContextRoot, digest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Drop a stale partial file that's NOT in the embed.
	stalePath := filepath.Join(dir, "stale.tmp")
	if err := os.WriteFile(stalePath, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := b.prepareContextDir(digest); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := os.Stat(stalePath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale partial file should have been wiped (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".extracted")); err != nil {
		t.Errorf("stamp file should be present after recovery: %v", err)
	}
}

// TestNeedsLegacyFallbackRecognisesBuildxMissing covers the matcher
// used to flip from BuildKit to the legacy docker builder. These are
// the exact error strings we've seen on macOS hosts with colima +
// Homebrew's docker CLI; getting them wrong means the auto-build
// path fails on a class of dev machines we want to support.
func TestNeedsLegacyFallbackRecognisesBuildxMissing(t *testing.T) {
	cases := []struct {
		name string
		log  string
		want bool
	}{
		{
			name: "buildkit_buildx_missing",
			log:  "ERROR: BuildKit is enabled but the buildx component is missing or broken.\n",
			want: true,
		},
		{
			name: "unknown_command_buildx",
			log:  "docker: unknown command: docker buildx\n",
			want: true,
		},
		{
			name: "case_insensitive",
			log:  "BUILDX COMPONENT IS BROKEN\n",
			want: true,
		},
		{
			name: "real_build_failure_unrelated",
			log:  "Step 5/76 : COPY foo .\nERROR: file not found: foo\n",
			want: false,
		},
		{
			name: "empty_log",
			log:  "",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsLegacyFallback(tc.log); got != tc.want {
				t.Errorf("needsLegacyFallback(%q) = %v, want %v", tc.log, got, tc.want)
			}
		})
	}
}

// TestTailBufResetClearsContents guards the helper used between
// BuildKit and legacy-fallback build attempts. Without Reset the
// second attempt's error tail would carry over the first attempt's
// stderr.
func TestTailBufResetClearsContents(t *testing.T) {
	tb := &tailBuf{cap: 100}
	_, _ = tb.Write([]byte("hello world"))
	if tb.String() != "hello world" {
		t.Fatalf("pre-reset = %q", tb.String())
	}
	tb.Reset()
	if tb.String() != "" {
		t.Errorf("post-reset = %q, want empty", tb.String())
	}
	// Reuse after reset must work.
	_, _ = tb.Write([]byte("fresh"))
	if tb.String() != "fresh" {
		t.Errorf("post-reuse = %q", tb.String())
	}
}

// TestSyntheticDigestFormat sanity-checks the hex format independently
// of the internal SHA encoding. The cache directory name shows up in
// user-facing logs; we want a stable lowercase-hex shape.
func TestSyntheticDigestFormat(t *testing.T) {
	digest, err := computeEmbedDigest(fstest.MapFS{"x": {Data: []byte("y")}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(digest) != digest {
		t.Errorf("digest not lowercase: %s", digest)
	}
	if len(digest) != 16 {
		t.Errorf("digest length = %d", len(digest))
	}
	if _, err := hex.DecodeString(digest); err != nil {
		t.Errorf("digest not valid hex: %v", err)
	}
	// And just confirm sha256 alphabet/length is what we expect upstream.
	h := sha256.New()
	h.Write([]byte("anything"))
	if len(hex.EncodeToString(h.Sum(nil))) != 64 {
		t.Errorf("sha256 hex changed shape unexpectedly")
	}
}
