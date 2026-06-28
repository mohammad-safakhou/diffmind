package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiffMindCommandUsesLocalModuleDirectory(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if err := os.Chdir(filepath.Join(wd, "..", "..")); err != nil {
		t.Fatal(err)
	}

	name, args, dir := diffmindCommand("diffmind", []string{"run", "--repo", "/tmp/repo"})
	if name != "go" {
		t.Fatalf("name = %q, want go", name)
	}
	if len(args) < 2 || args[0] != "run" || args[1] != "./cmd/diffmind" {
		t.Fatalf("args = %#v, want go run ./cmd/diffmind ...", args)
	}
	if filepath.Base(dir) != "diffmind" {
		t.Fatalf("dir = %q, want diffmind module dir", dir)
	}
}
