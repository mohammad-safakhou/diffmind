package sourcefilter

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestSkipDirNamePreservesSnapshotAndASTRules(t *testing.T) {
	for _, name := range []string{".git", "node_modules", ".venv", ".diffmind", ".diffmind", "vendor", "target", "fixtures"} {
		if !SkipDirName(name) {
			t.Fatalf("expected %s to be skipped", name)
		}
	}
	if SkipDirName("internal") {
		t.Fatalf("internal must remain indexable")
	}
}

func TestSkipFileInfoSkipsGeneratedAndLargeInputs(t *testing.T) {
	files := fstest.MapFS{
		"ok.go":      {Data: []byte("package ok\n")},
		"bundle.jar": {Data: []byte("jar")},
		"large.py":   {Data: make([]byte, maxFileBytes+1)},
	}
	for name, wantSkip := range map[string]bool{
		"ok.go":      false,
		"bundle.jar": true,
		"large.py":   true,
	} {
		info, err := fs.Stat(files, name)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := SkipFileInfo(info); got != wantSkip {
			t.Fatalf("SkipFileInfo(%s)=%v want %v", name, got, wantSkip)
		}
	}
}
