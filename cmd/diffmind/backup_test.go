package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/backup"
)

func TestBackupCLI(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("DIFFMIND_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tgz")
	var out bytes.Buffer
	for _, args := range [][]string{{"create", "--output", archive}, {"restore", "--archive", archive}, {"verify", "--archive", archive, "extra"}, {"create", "--offline", "--output", archive, "--max-bytes", "0"}} {
		if err := runBackup(args, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := runBackup([]string{"create", "--offline", "--output", archive, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var r backup.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.SHA256) != 64 {
		t.Fatal("missing archive digest")
	}
	out.Reset()
	if err := runBackup([]string{"verify", "--archive", archive, "--sha256", r.SHA256}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "verify complete") {
		t.Fatal(out.String())
	}
	if err := runBackup([]string{"restore", "--archive", archive, "--destination", home + "-restore", "--offline", "--allow-path-mismatch"}, &out); err != nil {
		t.Fatal(err)
	}
}

func TestManagedBackupCLI(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("DIFFMIND_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "catalog")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"rotate", "--directory", directory, "--keep-last", "1"}, {"rotate", "--directory", directory, "--offline"}, {"rotate", "--directory", directory, "--offline", "--keep-last", "0"}, {"list", "--directory", directory, "--keep-last", "1"}, {"list", "--directory", directory, "extra"}} {
		if err := runBackup(args, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	for range 2 {
		out.Reset()
		if err := runBackup([]string{"rotate", "--directory", directory, "--keep-last", "1", "--offline", "--json"}, &out); err != nil {
			t.Fatal(err)
		}
		var result backup.RotationReport
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Created.ID == "" || len(result.Retained) != 1 {
			t.Fatalf("report %+v %v", result, err)
		}
	}
	out.Reset()
	if err := runBackup([]string{"list", "--directory", directory, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var listed []backup.ManagedSnapshot
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("list %+v %v", listed, err)
	}
	for _, command := range []string{"rotate", "list"} {
		out.Reset()
		if err := runBackup([]string{command, "--help"}, &out); err != nil || !strings.Contains(out.String(), "permanently removing") {
			t.Fatalf("help %v %s", err, out.String())
		}
	}
}
