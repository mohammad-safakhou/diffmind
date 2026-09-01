package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/indexerbuild"
)

// prepareContextDir ensures the embedded build context is extracted to
// <BuildContextRoot>/<digest>/. The Dockerfile creates the wrapper's
// minimal module file inside its builder stage, so every required host
// input is part of the embedded context. Idempotent.
func (b *Builder) prepareContextDir(digest string) (string, error) {
	root := b.contextRoot()
	dir := filepath.Join(root, digest)
	stampPath := filepath.Join(dir, ".extracted")

	if _, err := os.Stat(stampPath); err == nil {
		// Warm cache. We deliberately do NOT re-verify the contents
		// against the embedded FS on every call — the digest already
		// proves they match. If a user tampers with the cached dir
		// we'd rather rebuild on Docker's side than silently re-extract.
		return dir, nil
	}

	// Wipe stale partial extractions (interrupted runs etc.).
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	if err := extractEmbed(indexerbuild.Context, dir); err != nil {
		return "", err
	}

	// Stamp file marks the extraction as complete. Written last so an
	// interrupted run is detected on the next attempt.
	if err := os.WriteFile(stampPath, []byte(digest+"\n"), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// contextRoot is BuildContextRoot or the default
// ~/.diffmind/indexer-build-context/.
func (b *Builder) contextRoot() string {
	if b.BuildContextRoot != "" {
		return b.BuildContextRoot
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".diffmind", "indexer-build-context")
}

// extractEmbed copies every file in src to dst, preserving relative
// paths. We do NOT try to be clever about mode bits — embed.FS
// returns files with mode 0444 regardless, and the docker daemon
// only cares about content.
//
// # FILE EXCLUSIONS
//
// We strip *_test.go from the wrapper directory before writing it
// out. Those files live next to the production sources so we can
// `go test ./indexerbuild/wrapper/...`, but they are NOT needed by
// the Docker build (which compiles only the wrapper binary, not its
// test code) and dragging them in would force the in-container `go
// build` to pull testing-package transitive deps for no benefit.
func extractEmbed(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dst, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
}

// computeEmbedDigest hashes every embedded path + content into a
// stable SHA-256. We use this as the cache key so a code change inside
// the wrapper (or a Dockerfile bump) produces a fresh extraction
// directory; old extractions stick around but never collide.
func computeEmbedDigest(src fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Mix path + content into the hash. Use a length prefix on
		// the path so two files whose paths concatenate identically
		// (impossible in practice, but free to defend against) still
		// produce distinct digests.
		fmt.Fprintf(h, "%d:%s\n", len(path), path)
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%d:", len(data))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{'\n'})
		return nil
	})
	if err != nil {
		return "", err
	}
	// 16 hex chars (= 8 bytes) is plenty of entropy for a per-machine
	// cache directory name. Full 64 hex char names are ugly without
	// adding any practical safety.
	full := hex.EncodeToString(h.Sum(nil))
	return full[:16], nil
}
