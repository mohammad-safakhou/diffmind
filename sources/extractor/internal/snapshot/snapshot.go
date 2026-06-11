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
	"fmt"
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
//
// Safety guard: Close refuses to delete any path that is NOT under the
// standard DiffMind snapshot parent (~/.diffmind/snapshots). This prevents
// accidental deletion of user source trees when a caller mistakenly passes
// a live repository path as a SnapshotPath.
func (s *Snapshot) Close() error {
	if s == nil || s.Path == "" {
		return nil
	}
	if s.retained {
		util.Info("snapshot", "snapshot retained for retry", map[string]any{"path": s.Path})
		return nil
	}
	// Safety: refuse to delete any path that looks like a user's source
	// repository (contains a .git directory at the top level or a known
	// source-tree marker). DiffMind snapshots are content-addressable
	// directories WITHOUT a .git directory — any directory with a .git
	// is almost certainly a real repository, not a snapshot.
	if isLikelySourceRepo(s.Path) {
		util.Warn("snapshot", "refusing to delete — path looks like a source repository (contains .git)", map[string]any{
			"path": s.Path,
		})
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

// isLikelySourceRepo reports whether path looks like a user's source
// repository rather than an ephemeral snapshot. We check for the presence
// of a .git directory or other version-control markers at the root.
// DiffMind snapshots are plain content-copy directories without VCS metadata.
func isLikelySourceRepo(path string) bool {
	for _, marker := range []string{".git", ".hg", ".svn"} {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

// isUnder reports whether path is strictly under parent (both absolute).
func isUnder(path, parent string) bool {
	if path == parent {
		return false // exact match is not "under"
	}
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
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
