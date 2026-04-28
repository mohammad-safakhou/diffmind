package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreateMirrorsRegularFiles(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.go"), "package a\n")
	writeFile(t, filepath.Join(src, "sub", "b.go"), "package sub\n")

	parent := t.TempDir()
	snap, err := Create(src, parent)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer snap.Close()

	if _, err := os.Stat(filepath.Join(snap.Path, "a.go")); err != nil {
		t.Fatalf("expected mirrored a.go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "sub", "b.go")); err != nil {
		t.Fatalf("expected mirrored sub/b.go: %v", err)
	}
}

func TestSnapshotEditsDoNotAffectSource(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.go"), "ORIGINAL\n")

	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer snap.Close()

	// Edit via the snapshot using O_TRUNC, the way an agent likely would.
	dst := filepath.Join(snap.Path, "a.go")
	if err := os.WriteFile(dst, []byte("MUTATED\n"), 0o644); err != nil {
		t.Fatalf("write through snapshot: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(src, "a.go"))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(out) != "ORIGINAL\n" {
		t.Fatalf("source was modified through snapshot edit; got %q", string(out))
	}
}

func TestSnapshotAppendsAlsoIsolatedFromSource(t *testing.T) {
	// Independent copies (not hardlinks) mean that even O_APPEND writes to
	// the snapshot must NOT show up in the source.
	src := t.TempDir()
	original := "ORIGINAL\n"
	writeFile(t, filepath.Join(src, "a.go"), original)
	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer snap.Close()

	f, err := os.OpenFile(filepath.Join(snap.Path, "a.go"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("APPEND\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = f.Close()

	srcContent, _ := os.ReadFile(filepath.Join(src, "a.go"))
	if string(srcContent) != original {
		t.Fatalf("source must remain pristine; got %q", string(srcContent))
	}
}

func TestSnapshotDeletesAlsoIsolatedFromSource(t *testing.T) {
	// An agent rm-ing a file inside the snapshot must not affect the source.
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.go"), "x\n")
	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer snap.Close()
	if err := os.Remove(filepath.Join(snap.Path, "a.go")); err != nil {
		t.Fatalf("rm in snap: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "a.go")); err != nil {
		t.Fatalf("source must still have a.go: %v", err)
	}
}

func TestCreateSkipsDefaultDirs(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, ".git", "HEAD"), "ref\n")
	writeFile(t, filepath.Join(src, "node_modules", "x", "package.json"), "{}\n")
	writeFile(t, filepath.Join(src, "src", "main.go"), "package main\n")

	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer snap.Close()

	if _, err := os.Stat(filepath.Join(snap.Path, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git should be skipped, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("node_modules should be skipped, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "src", "main.go")); err != nil {
		t.Fatalf("expected src/main.go to be present: %v", err)
	}
}

func TestCreateRecreatesSymlinks(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.go"), "x\n")
	if err := os.Symlink("a.go", filepath.Join(src, "alias.go")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer snap.Close()

	link, err := os.Readlink(filepath.Join(snap.Path, "alias.go"))
	if err != nil {
		t.Fatalf("expected mirrored symlink, got: %v", err)
	}
	if link != "a.go" {
		t.Fatalf("unexpected link target: %s", link)
	}
}

func TestCreateFailsOnNonexistentSource(t *testing.T) {
	if _, err := Create(filepath.Join(t.TempDir(), "missing"), t.TempDir()); err == nil {
		t.Fatal("expected error for non-existent source")
	}
}

func TestCreateFailsWhenSourceIsFile(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	writeFile(t, f, "hi\n")
	if _, err := Create(f, t.TempDir()); err == nil {
		t.Fatal("expected error when source is a file")
	}
}

func TestCloseRemovesSnapshot(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.go"), "x\n")
	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := snap.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(snap.Path); !os.IsNotExist(err) {
		t.Fatalf("expected snapshot to be removed, got err=%v", err)
	}
	// Idempotent.
	if err := snap.Close(); err != nil {
		t.Fatalf("second Close should be no-op: %v", err)
	}
}

func TestCloseRecoversFromReadOnlyFiles(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.go"), "x\n")
	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate an agent flipping a file to read-only.
	if err := os.Chmod(filepath.Join(snap.Path, "a.go"), 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := snap.Close(); err != nil {
		t.Fatalf("Close should recover from read-only files: %v", err)
	}
}

func TestMapToSourceHandlesAbsoluteAndRelative(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.go"), "x\n")
	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer snap.Close()

	abs := filepath.Join(snap.Path, "pkg", "a.go")
	mapped := snap.MapToSource(abs)
	if mapped != filepath.Join(src, "pkg", "a.go") {
		t.Fatalf("absolute mapping wrong: %s", mapped)
	}

	// Already-relative path is returned as-is by MapToSource.
	if got := snap.MapToSource("pkg/a.go"); got != "pkg/a.go" {
		t.Fatalf("relative path should not be rewritten by MapToSource, got %s", got)
	}

	// Path outside the snapshot is returned as-is.
	other := "/totally/elsewhere/file.go"
	if got := snap.MapToSource(other); got != other {
		t.Fatalf("outside path should be untouched, got %s", got)
	}
}

func TestMapRelativeToSourceStripsSnapshotBasename(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.go"), "x\n")
	snap, err := Create(src, t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer snap.Close()
	prefixed := filepath.Base(snap.Path) + "/pkg/a.go"
	got := snap.MapRelativeToSource(prefixed)
	if got != "pkg/a.go" {
		t.Fatalf("expected snapshot-name prefix stripped, got %q", got)
	}
}
