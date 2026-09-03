package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	home := filepath.Join(parent, "workspace")
	for _, d := range []string{"projects/example/runs/old", "jobs", "runs/analysis", "packs", "repos/service/.git"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string]string{
		"projects/example/project.json":        `{"id":"example","created_at":"2020-01-02T03:04:05Z"}`,
		"projects/example/runs/old/graph.json": `{"services":[{"id":"service"}]}`,
		"jobs/delivery-old.json":               `{"attempts":[{"status":"failed","started_at":"2021-01-01T00:00:00Z"}]}`,
		"runs/analysis/result.json":            "{}", "diffmind-packs.lock": "version: 1\npacks: []\n",
		"repos/service/.git/HEAD": "ref: refs/heads/master\n", "repos/service/start.sh": "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(home, p), []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(home, "repos/service/start.sh"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("start.sh", filepath.Join(home, "repos/service/start")); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(parent, "snapshot.tar.gz")
}

func TestRoundTripPreservesBytesModesTimesAndHistory(t *testing.T) {
	home, archive := fixture(t)
	p := filepath.Join(home, "jobs/delivery-old.json")
	stamp := time.Date(2021, 1, 2, 3, 4, 5, 123456789, time.UTC)
	if err := os.Chtimes(p, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	r, err := Create(home, archive, "1.2.3", DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(archive, r.SHA256, DefaultMaxBytes)
	if err != nil || verified != r {
		t.Fatalf("verify %+v %v", verified, err)
	}
	info, _ := os.Stat(archive)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("archive must be private")
	}
	if _, err := Create(home, archive, "dev", DefaultMaxBytes); err == nil {
		t.Fatal("overwrote archive")
	}
	moved := home + "-original"
	if err := os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(archive, home, r.SHA256, DefaultMaxBytes, false); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(moved, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(moved, p)
		if rel == "." || rel == homelock.FileName {
			return nil
		}
		a, err := os.Lstat(p)
		if err != nil {
			return err
		}
		b, err := os.Lstat(filepath.Join(home, rel))
		if err != nil {
			return err
		}
		if a.Mode() != b.Mode() {
			t.Errorf("mode %s: %v != %v", rel, a.Mode(), b.Mode())
		}
		if a.Mode()&os.ModeSymlink != 0 {
			x, _ := os.Readlink(p)
			y, _ := os.Readlink(filepath.Join(home, rel))
			if x != y {
				t.Errorf("link %s", rel)
			}
			return nil
		}
		if !a.ModTime().Equal(b.ModTime()) {
			t.Errorf("time %s: %v != %v", rel, a.ModTime(), b.ModTime())
		}
		if !d.IsDir() {
			x, _ := os.ReadFile(p)
			y, _ := os.ReadFile(filepath.Join(home, rel))
			if !bytes.Equal(x, y) {
				t.Errorf("bytes %s", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(archive, home, "", DefaultMaxBytes, true); err == nil {
		t.Fatal("overwrote home")
	}
}

func TestMaintenanceLeaseAndDestinationGuards(t *testing.T) {
	home, archive := fixture(t)
	release, err := homelock.Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(home, archive, "dev", DefaultMaxBytes); err == nil {
		t.Fatal("backed up active workspace")
	}
	release()
	if _, err := Create(home, filepath.Join(home, "snapshot.tgz"), "dev", DefaultMaxBytes); err == nil {
		t.Fatal("archive inside source")
	}
	if _, err := Create(home, archive, "dev", 1); err == nil {
		t.Fatal("byte limit ignored")
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("failed backup published")
	}
	r, err := Create(home, archive, "dev", DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	destination := home + "-drill"
	if _, err := Restore(archive, destination, "", DefaultMaxBytes, false); err == nil {
		t.Fatal("path mismatch allowed")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("failed restore published")
	}
	if _, err := Restore(archive, destination, r.SHA256, DefaultMaxBytes, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(archive, strings.Repeat("0", 64), DefaultMaxBytes); err == nil {
		t.Fatal("wrong trusted checksum allowed")
	}
	if _, err := Verify(archive, "not-a-digest", DefaultMaxBytes); err == nil {
		t.Fatal("invalid digest allowed")
	}
	if _, err := Verify(archive, "", 1); err == nil {
		t.Fatal("verify byte limit ignored")
	}
	stages, _ := filepath.Glob(filepath.Join(filepath.Dir(home), ".diffmind-restore-*"))
	if len(stages) != 0 {
		t.Fatalf("leaked stages %v", stages)
	}
}

func TestRejectUnsafeSourceLinks(t *testing.T) {
	for _, link := range []string{"/etc/passwd", "../../outside", "missing", "start"} {
		t.Run(link, func(t *testing.T) {
			home, archive := fixture(t)
			if err := os.Symlink(link, filepath.Join(home, "repos/service/unsafe")); err != nil {
				t.Fatal(err)
			}
			if _, err := Create(home, archive, "dev", DefaultMaxBytes); err == nil {
				t.Fatal("unsafe link accepted")
			}
		})
	}
}

func forgedArchive(t *testing.T, m Manifest, headers []*tar.Header, bodies [][]byte, suffix []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "forged.tgz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	b, _ := json.Marshal(m)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(b)), Mode: 0o600, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(b); err != nil {
		t.Fatal(err)
	}
	for i, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if i < len(bodies) {
			if _, err := tw.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(suffix); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRejectMaliciousCorruptAndFutureArchives(t *testing.T) {
	base := Manifest{Format: 1, Version: "dev", Created: time.Now().UTC(), Home: "/original", Entries: []Entry{{Path: "projects", Kind: "directory", Mode: 0o700}}}
	for _, tc := range []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"future", func(m *Manifest) { m.Format = 2 }},
		{"traversal", func(m *Manifest) { m.Entries[0].Path = "../escape" }},
		{"absolute", func(m *Manifest) { m.Entries[0].Path = "/escape" }},
		{"windows", func(m *Manifest) { m.Entries[0].Path = `C:\escape` }},
		{"duplicate", func(m *Manifest) { m.Entries = append(m.Entries, m.Entries[0]) }},
		{"setuid", func(m *Manifest) { m.Entries[0].Mode = 0o4777 }},
		{"missing-parent", func(m *Manifest) { m.Entries[0].Path = "missing/projects" }},
		{"overflow", func(m *Manifest) { m.Bytes = -1 }},
		{"link-parent", func(m *Manifest) {
			m.Entries = append(m.Entries, Entry{Path: "projects/link", Kind: "symlink", Link: "../projects"}, Entry{Path: "projects/link/child", Kind: "directory"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Entries = append([]Entry(nil), base.Entries...)
			tc.mutate(&m)
			archive := forgedArchive(t, m, nil, nil, nil)
			if _, err := Verify(archive, "", DefaultMaxBytes); err == nil {
				t.Fatal("accepted invalid archive")
			}
			destination := filepath.Join(t.TempDir(), "restore")
			if _, err := Restore(archive, destination, "", DefaultMaxBytes, true); err == nil {
				t.Fatal("restored invalid archive")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("published invalid restore")
			}
		})
	}
	t.Run("unlisted", func(t *testing.T) {
		archive := forgedArchive(t, base, []*tar.Header{entryHeader(base.Entries[0]), {Name: "data/evil", Typeflag: tar.TypeReg}}, nil, nil)
		if _, err := Verify(archive, "", DefaultMaxBytes); err == nil {
			t.Fatal("unlisted file allowed")
		}
	})
	t.Run("trailing", func(t *testing.T) {
		archive := forgedArchive(t, base, []*tar.Header{entryHeader(base.Entries[0])}, nil, []byte("garbage"))
		if _, err := Verify(archive, "", DefaultMaxBytes); err == nil {
			t.Fatal("trailing data allowed")
		}
	})
	t.Run("checksum", func(t *testing.T) {
		m := base
		m.Bytes = 1
		m.Entries = append(append([]Entry(nil), base.Entries...), Entry{Path: "projects/a", Kind: "file", Size: 1, SHA256: strings.Repeat("0", 64)})
		archive := forgedArchive(t, m, []*tar.Header{entryHeader(m.Entries[0]), entryHeader(m.Entries[1])}, [][]byte{nil, []byte("a")}, nil)
		if _, err := Verify(archive, "", DefaultMaxBytes); err == nil {
			t.Fatal("corrupt file allowed")
		}
	})
	t.Run("truncated", func(t *testing.T) {
		home, archive := fixture(t)
		if _, err := Create(home, archive, "dev", DefaultMaxBytes); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(archive, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := f.Seek(-5, io.SeekEnd)
		if err := f.Truncate(n); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if _, err := Verify(archive, "", DefaultMaxBytes); err == nil {
			t.Fatal("missing gzip footer allowed")
		}
	})
}

func TestRenameNoReplacePreservesRacingDestination(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "stage")
	dst := filepath.Join(parent, "existing")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := renameNoReplace(src, dst); err == nil {
		t.Fatal("replaced existing empty directory")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("source disappeared")
	}
}

func TestRollbackReadOnlyDirectories(t *testing.T) {
	home, archive := fixture(t)
	if err := os.Chmod(filepath.Join(home, "projects/example"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, "projects/example"), 0o700) })
	if _, err := Create(home, archive, "dev", DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(archive, home+"-different", "", DefaultMaxBytes, false); err == nil {
		t.Fatal("expected path mismatch")
	}
	stages, _ := filepath.Glob(filepath.Join(filepath.Dir(home), ".diffmind-restore-*"))
	if len(stages) != 0 {
		t.Fatalf("read-only stage leaked: %v", stages)
	}
}
