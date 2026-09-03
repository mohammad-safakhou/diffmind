package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseArchiveValidation(t *testing.T) {
	for _, kind := range []string{"valid", "missing", "extra", "duplicate", "traversal", "symlink", "nonexecutable", "empty", "oversize", "owner", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "archive.tar.gz")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			headers := []tar.Header{{Name: "diffmind", Mode: 0755, Size: 1, Typeflag: tar.TypeReg}, {Name: "LICENSE", Mode: 0644, Size: 1, Typeflag: tar.TypeReg}, {Name: "README.md", Mode: 0644, Size: 1, Typeflag: tar.TypeReg}}
			switch kind {
			case "owner":
				headers[0].Uname = "local-developer"
			case "missing":
				headers = headers[:2]
			case "extra":
				headers = append(headers, tar.Header{Name: "other", Size: 1, Typeflag: tar.TypeReg})
			case "duplicate":
				headers = append(headers, headers[0])
			case "traversal":
				headers[0].Name = "../diffmind"
			case "symlink":
				headers[0].Typeflag = tar.TypeSymlink
				headers[0].Linkname = "/other"
				headers[0].Size = 0
			case "nonexecutable":
				headers[0].Mode = 0644
			case "empty":
				headers[0].Size = 0
			case "oversize":
				headers[0].Size = 513 << 20
				headers = headers[:1]
			}
			for _, h := range headers {
				if err := tw.WriteHeader(&h); err != nil {
					t.Fatal(err)
				}
				if h.Typeflag == tar.TypeReg && h.Size == 1 {
					if _, err := tw.Write([]byte("x")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := tw.Close(); err != nil && kind != "oversize" {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if kind == "trailing" {
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.Write([]byte("not an archive"))
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			err = checkArchive(path)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("archive %s: %v", kind, err)
			}
		})
	}
}

func TestReleasePackageIsNeutralReproducibleAndNonOverwriting(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"diffmind", "LICENSE", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	one, two := filepath.Join(root, "one.tar.gz"), filepath.Join(root, "two.tar.gz")
	for _, output := range []string{one, two} {
		if err := packageArchive(output, filepath.Join(root, "diffmind"), root); err != nil {
			t.Fatal(err)
		}
		if err := checkArchive(output); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := os.ReadFile(one)
	b, _ := os.ReadFile(two)
	if !bytes.Equal(a, b) {
		t.Fatal("package not reproducible")
	}
	if err := packageArchive(one, filepath.Join(root, "diffmind"), root); err == nil {
		t.Fatal("package overwrote existing artifact")
	}
	gz, err := gzip.NewReader(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || len(h.PAXRecords) != 0 || len(h.Xattrs) != 0 || h.ModTime.Unix() != 0 {
			t.Fatalf("host metadata leaked: %+v", h)
		}
	}
}

func TestReleaseEnvironmentDoesNotInheritWorkspaceOrCredentials(t *testing.T) {
	t.Setenv("DIFFMIND_HOME", "/must-not-touch")
	t.Setenv("DIFFMIND_AUTH_TOKEN", "do-not-inherit")
	t.Setenv("GITHUB_TOKEN", "do-not-inherit")
	t.Setenv("GH_TOKEN", "do-not-inherit")
	for _, value := range isolatedEnv("DIFFMIND_HOME=/isolated") {
		if strings.Contains(value, "do-not-inherit") || strings.Contains(value, "must-not-touch") {
			t.Fatal("environment isolation failed")
		}
	}
}
