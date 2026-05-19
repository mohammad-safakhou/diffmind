// Package snapshot creates an isolated working copy of a repository for the
// extraction pipeline. OpenCode sessions can edit, create, and delete files
// inside the directory they are bound to. To keep the user's repository safe
// and avoid races between concurrent agents, the orchestrator never points
// OpenCode at the user's repo directly; it points at a snapshot produced by
// this package.
//
// Strategy:
//   - Copy the source tree into a fresh temp directory. The snapshot is a
//     real, independent copy: any edits, deletes, creates, or chmods that
//     OpenCode performs inside the snapshot are confined to the temp dir.
//     The user's repository is never touched.
//   - Skip noisy or large directories that are never useful for extraction
//     (.git, node_modules, build artifacts, IDE folders, .diffmind runs).
//   - Symlinks are recreated as symlinks. Special files (sockets, devices)
//     are skipped.
//   - Removal is best-effort but verified: Close() walks the snapshot and
//     deletes it even if individual files were chmod'd to read-only by an
//     agent.
//
// We deliberately do NOT do per-worker snapshots. The contract is: the
// snapshot is per-run, disposable, and may end up with concurrent edits
// inside it that we don't care about. We only consume the structured JSON
// responses from OpenCode; the snapshot's final state is irrelevant.
package snapshot

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// DefaultParent returns the recommended parent directory for snapshots:
// `~/.diffmind/snapshots`. Using a stable, user-owned location instead
// of `os.TempDir()` produces shorter and more predictable absolute
// paths, which materially reduces the rate of "wrong path"
// hallucinations from LLM agents when they re-enter their own tool
// arguments. Falls back to `os.TempDir()` when the user's home
// directory cannot be resolved (very rare; mostly relevant in
// containerised CI).
func DefaultParent() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return os.TempDir()
	}
	return filepath.Join(home, ".diffmind", "snapshots")
}

// Snapshot represents a hardlinked mirror of a source repository under a
// random temp directory. The Path field is the absolute path to the snapshot
// root and is what callers should pass to OpenCode as the session directory.
type Snapshot struct {
	// SourcePath is the original (user-supplied) repository path.
	SourcePath string
	// Path is the absolute path to the snapshot root inside the temp dir.
	Path string

	// retained, when true, makes Close() a no-op so the directory stays
	// on disk after the run finishes. Used by failure-report flows so a
	// later `diffmind retry` can re-attach to the exact same working
	// tree the original run saw.
	retained bool
}

// Retain marks the snapshot to be kept on disk past Close(). The
// caller is then responsible for removing it later (typically via
// the retry command after the run is fixed and re-run).
func (s *Snapshot) Retain() {
	if s == nil {
		return
	}
	s.retained = true
}

// defaultSkipDirs lists directory names that we never mirror. They are
// either huge, never useful for source-level extraction, or known to be
// modified by tooling. Keep this list aggressive: a smaller snapshot
// means a faster run and less load on whatever process reads it
// afterwards (OpenCode, ripgrep, LSPs spawned by tools, etc.).
var defaultSkipDirs = map[string]struct{}{
	// VCS / IDE
	".git":     {},
	".hg":      {},
	".svn":     {},
	".idea":    {},
	".vscode":  {},
	".vs":      {},
	".fleet":   {},
	".history": {},

	// Java / JVM
	".gradle":    {},
	".mvn":       {},
	"target":     {},
	"build":      {},
	".classpath": {},
	".settings":  {},

	// Node.js / Web
	"node_modules":     {},
	"bower_components": {},
	".pnpm-store":      {},
	".yarn":            {},
	"dist":             {},
	"out":              {},
	"public/build":     {},
	".next":            {},
	".nuxt":            {},
	".svelte-kit":      {},
	".astro":           {},
	".turbo":           {},
	".vercel":          {},
	".netlify":         {},
	"coverage":         {},

	// Python
	".pytest_cache": {},
	"__pycache__":   {},
	".venv":         {},
	"venv":          {},
	"env":           {},
	".tox":          {},
	".mypy_cache":   {},
	".ruff_cache":   {},
	".pytype":       {},
	"htmlcov":       {},

	// Go
	".gocache": {},

	// Rust
	"target.rust": {}, // unusual but harmless

	// Infra / cache
	".terraform":    {},
	".serverless":   {},
	".aws-sam":      {},
	".cache":        {},
	".diffmind":     {},
	".example-cache": {},

	// macOS / misc
	".DS_Store": {},
}

// defaultMaxFileBytes caps how large a file we will copy into the snapshot.
// Files above this size are skipped to keep the snapshot small. 4 MiB is
// generous for source files; binary / artifact files larger than this are
// almost never relevant to extraction.
const defaultMaxFileBytes int64 = 4 << 20

// Create materializes an independent copy of source under
// <parent>/<name>. Returns a Snapshot whose Path can be handed to
// OpenCode.
//
// Path stability: when name is empty we fall back to a random suffix
// (legacy behaviour, used by some tests). Production callers should
// always pass a stable, human-readable name — the run ID — so the
// resulting path does not contain random tokens. LLM agents are
// noticeably more reliable when copying short, predictable paths in
// tool calls; we have observed wrong-character hallucinations
// (e.g. "603b6b6" instead of "603t") on the older random-hex form.
//
// Failure modes (all fatal — the orchestrator must fail fast if it
// cannot guarantee isolation):
//   - source does not exist or is not a directory
//   - cannot create the parent dir or destination
//   - destination already exists with a non-empty content (we refuse
//     to write over a previous snapshot; the caller must delete or
//     reattach instead)
//   - copying a regular file fails
func Create(source, parent, name string) (*Snapshot, error) {
	abs, err := filepath.Abs(source)
	if err != nil {
		return nil, fmt.Errorf("snapshot: resolve source: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("snapshot: stat source: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("snapshot: source %q is not a directory", abs)
	}

	if parent == "" {
		parent = os.TempDir()
	}
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return nil, fmt.Errorf("snapshot: resolve parent: %w", err)
	}
	if err := os.MkdirAll(parentAbs, 0o755); err != nil {
		return nil, fmt.Errorf("snapshot: create parent dir: %w", err)
	}

	// Choose the leaf name. Stable when provided (preferred); random
	// otherwise (legacy/tests).
	leaf := strings.TrimSpace(name)
	if leaf == "" {
		suffix, err := randomSuffix()
		if err != nil {
			return nil, fmt.Errorf("snapshot: random suffix: %w", err)
		}
		leaf = "diffmind-snap-" + suffix
	}
	// dest must be absolute: the OpenCode server resolves the `directory`
	// query parameter relative to its own CWD, which we do not control. An
	// absolute path makes the destination unambiguous regardless of where
	// the server was started.
	dest := filepath.Join(parentAbs, leaf)
	if info, err := os.Stat(dest); err == nil {
		// Refuse to silently overwrite a previous snapshot at the same
		// path. A leftover snapshot is almost always evidence of a
		// crashed run; the caller should explicitly Close() or
		// Reattach() rather than risk a write race.
		if info.IsDir() {
			// Empty leftover dir? remove it. Anything non-empty: bail.
			entries, _ := os.ReadDir(dest)
			if len(entries) > 0 {
				return nil, fmt.Errorf("snapshot: dest %q already exists and is not empty; reattach() it or remove it manually", dest)
			}
			if err := os.Remove(dest); err != nil {
				return nil, fmt.Errorf("snapshot: clear stale dest: %w", err)
			}
		} else {
			return nil, fmt.Errorf("snapshot: dest %q exists and is not a directory", dest)
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("snapshot: create dest: %w", err)
	}

	util.Info("snapshot", "creating repo snapshot", map[string]any{
		"source": abs, "dest": dest,
	})

	if err := mirrorTree(abs, dest); err != nil {
		// Best-effort cleanup so we never leak a partial snapshot.
		_ = forceRemove(dest)
		return nil, fmt.Errorf("snapshot: mirror tree: %w", err)
	}

	util.Info("snapshot", "snapshot ready", map[string]any{
		"source": abs, "dest": dest,
	})
	return &Snapshot{SourcePath: abs, Path: dest}, nil
}

// Reattach binds an existing snapshot directory back to a Snapshot
// value. It validates that the directory exists and is a directory
// (anything else is treated as user error). Reattach does NOT touch
// the contents of the directory; it simply lets a retry consumer
// re-use a snapshot that was retained by a previous failed run.
func Reattach(source, snapshotPath string) (*Snapshot, error) {
	abs, err := filepath.Abs(source)
	if err != nil {
		return nil, fmt.Errorf("resolve source: %w", err)
	}
	info, err := os.Stat(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("stat snapshot: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("snapshot path %q is not a directory", snapshotPath)
	}
	util.Info("snapshot", "reattached existing snapshot", map[string]any{
		"path": snapshotPath, "source": abs,
	})
	return &Snapshot{SourcePath: abs, Path: snapshotPath}, nil
}

// Close removes the snapshot from disk. It is safe to call multiple times
// and safe to call on a nil receiver. When the snapshot has been marked
// for retention via Retain() the call is a no-op.
func (s *Snapshot) Close() error {
	if s == nil || s.Path == "" {
		return nil
	}
	if s.retained {
		util.Info("snapshot", "snapshot retained for retry", map[string]any{"path": s.Path})
		return nil
	}
	if err := forceRemove(s.Path); err != nil {
		util.Warn("snapshot", "snapshot cleanup failed", map[string]any{
			"path": s.Path, "error": err,
		})
		return err
	}
	util.Info("snapshot", "snapshot removed", map[string]any{"path": s.Path})
	return nil
}

// MapToSource translates a path that lives inside the snapshot back to the
// equivalent path inside the original source tree. It returns the input
// unchanged when the path is not under the snapshot root, when the receiver
// is nil, or when the input is empty. This lets agents return paths in their
// own world (the snapshot) while artifacts persist user-facing paths.
func (s *Snapshot) MapToSource(p string) string {
	if s == nil || strings.TrimSpace(p) == "" {
		return p
	}
	clean := filepath.Clean(p)
	abs := clean
	if !filepath.IsAbs(abs) {
		// Treat relative paths as already-source-relative; nothing to do.
		return p
	}
	rel, err := filepath.Rel(s.Path, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return filepath.Join(s.SourcePath, rel)
}

// MapRelativeToSource is like MapToSource but works on already-relative paths
// the LLM may emit. If the path begins with the snapshot's basename (which
// happens when an agent dumps a path relative to its CWD's parent), strip it.
func (s *Snapshot) MapRelativeToSource(p string) string {
	if s == nil {
		return p
	}
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return p
	}
	// Absolute path under the snapshot root?
	if filepath.IsAbs(trimmed) {
		return s.MapToSource(trimmed)
	}
	// Sometimes models echo the full path; normalize to forward slashes for
	// the comparison and strip a leading snapshot-name prefix.
	clean := filepath.ToSlash(filepath.Clean(trimmed))
	snapBase := filepath.Base(s.Path)
	if strings.HasPrefix(clean, snapBase+"/") {
		clean = strings.TrimPrefix(clean, snapBase+"/")
	}
	return clean
}

// ----------------------------------------------------------------------------
// Internal helpers
// ----------------------------------------------------------------------------

func randomSuffix() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// skippedExtensions are file extensions that are almost never useful for
// extraction and tend to be large. Keep them out of the snapshot to make
// runs lighter on every downstream tool.
var skippedExtensions = map[string]struct{}{
	// Compiled / packaged
	".class": {}, ".jar": {}, ".war": {}, ".ear": {},
	".pyc": {}, ".pyo": {}, ".whl": {},
	".o": {}, ".a": {}, ".so": {}, ".dylib": {}, ".dll": {}, ".exe": {},
	".wasm": {},
	// Lockfiles / bundles (huge, no info we don't already get from manifests)
	".lock": {}, ".tsbuildinfo": {},
	".map": {}, // sourcemaps
	// Media
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".bmp": {},
	".ico": {}, ".svg": {}, ".tiff": {}, ".heic": {},
	".mp3": {}, ".wav": {}, ".ogg": {}, ".flac": {}, ".m4a": {},
	".mp4": {}, ".mov": {}, ".avi": {}, ".webm": {}, ".mkv": {},
	".pdf": {}, ".doc": {}, ".docx": {}, ".xls": {}, ".xlsx": {}, ".ppt": {}, ".pptx": {},
	// Archives
	".zip": {}, ".tar": {}, ".tgz": {}, ".gz": {}, ".bz2": {}, ".xz": {},
	".rar": {}, ".7z": {},
	// Fonts
	".ttf": {}, ".otf": {}, ".woff": {}, ".woff2": {}, ".eot": {},
}

// stats tracks how the snapshot was constructed so we can log it once at
// the end and the user can see what got skipped.
type mirrorStats struct {
	dirsCreated       int
	filesCopied       int
	bytesCopied       int64
	skippedExt        int
	skippedSize       int
	skippedDir        int
	skippedNonRegular int
}

// mirrorTree walks src and recreates its structure at dst. Regular files are
// copied byte-for-byte, directories are mkdir'd with their original perms
// (capped to 0700+ user-read), symlinks are recreated, anything else is
// skipped. We also drop files whose extension is in skippedExtensions and
// files larger than defaultMaxFileBytes — these are noise for source-level
// extraction and just bloat the snapshot.
func mirrorTree(src, dst string) error {
	stats := &mirrorStats{}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		// Directory skip rules: applied on directory names anywhere in the tree.
		if d.IsDir() {
			if _, skip := defaultSkipDirs[d.Name()]; skip {
				stats.skippedDir++
				util.Trace("snapshot", "skipping dir", map[string]any{"path": rel})
				return fs.SkipDir
			}
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			perm := info.Mode().Perm() | 0o700 // ensure we can traverse it
			if err := os.MkdirAll(target, perm); err != nil {
				return err
			}
			stats.dirsCreated++
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		mode := info.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			linkTarget, readErr := os.Readlink(path)
			if readErr != nil {
				return readErr
			}
			// Replace any existing entry; ignore if target already absent.
			_ = os.Remove(target)
			return os.Symlink(linkTarget, target)
		case mode.IsRegular():
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if _, skip := skippedExtensions[ext]; skip {
				stats.skippedExt++
				return nil
			}
			if info.Size() > defaultMaxFileBytes {
				stats.skippedSize++
				util.Trace("snapshot", "skipping large file", map[string]any{"path": rel, "bytes": info.Size()})
				return nil
			}
			if err := copyRegular(path, target, mode.Perm()); err != nil {
				return err
			}
			stats.filesCopied++
			stats.bytesCopied += info.Size()
			return nil
		default:
			stats.skippedNonRegular++
			util.Trace("snapshot", "skipping non-regular file", map[string]any{"path": rel, "mode": mode.String()})
			return nil
		}
	})
	if err == nil {
		util.Info("snapshot", "snapshot stats", map[string]any{
			"src":                src,
			"dst":                dst,
			"dirs":               stats.dirsCreated,
			"files":              stats.filesCopied,
			"bytes":              stats.bytesCopied,
			"skipped_dirs":       stats.skippedDir,
			"skipped_extensions": stats.skippedExt,
			"skipped_oversize":   stats.skippedSize,
			"skipped_nonregular": stats.skippedNonRegular,
			"max_file_bytes":     defaultMaxFileBytes,
		})
	}
	return err
}

// copyRegular performs an independent byte-for-byte copy of src to dst,
// preserving the source's permission bits but always allowing the user write
// access so subsequent agent operations and Close() cleanup can succeed.
// Independent copies are required: hardlinks would let an agent's O_TRUNC
// write through to the user's repo via the shared inode.
func copyRegular(src, dst string, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	// Always include user-write so agents can edit inside the snapshot and
	// so Close() can remove the tree without a chmod recovery pass.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm|0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	return nil
}

// forceRemove tears down a directory tree, retrying with chmod when an entry
// has been made read-only by an agent inside the snapshot. We do not want
// the user's run to leak a stale snapshot just because an LLM tried to be
// clever.
func forceRemove(path string) error {
	if path == "" {
		return nil
	}
	if err := os.RemoveAll(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrPermission) {
		// Try chmod-then-remove as a recovery.
		if chErr := chmodWalk(path); chErr != nil {
			return fmt.Errorf("snapshot: chmod walk failed: %w (original: %v)", chErr, err)
		}
		if err2 := os.RemoveAll(path); err2 != nil {
			return err2
		}
		return nil
	} else {
		if chErr := chmodWalk(path); chErr != nil {
			return fmt.Errorf("snapshot: chmod walk failed: %w (original: %v)", chErr, err)
		}
		return os.RemoveAll(path)
	}
}

func chmodWalk(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort
		}
		_ = os.Chmod(p, 0o700)
		return nil
	})
}
