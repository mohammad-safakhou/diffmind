package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/mohammad-safakhou/diffmind/"

func TestStageImportBoundaries(t *testing.T) {
	stageRoot := filepath.Join("..", "stage")
	err := filepath.WalkDir(stageRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			return nil
		}
		ownStage := filepath.Base(filepath.Dir(path))
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Errorf("unquote import in %s: %v", path, err)
				continue
			}
			switch {
			case importPath == module+"internal/pipeline":
				t.Errorf("%s imports pipeline", path)
			case importPath == module+"internal/agents" || strings.HasPrefix(importPath, module+"internal/agents/"):
				t.Errorf("%s imports transitional agents package", path)
			case strings.HasPrefix(importPath, module+"internal/stage/"):
				importedStage := strings.TrimPrefix(importPath, module+"internal/stage/")
				if importedStage != ownStage {
					t.Errorf("%s imports stage %s", path, importedStage)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoGenericStagePackages(t *testing.T) {
	for _, name := range []string{"core", "common", "utils"} {
		path := filepath.Join("..", "stage", name)
		if _, err := filepath.Glob(filepath.Join(path, "*.go")); err != nil {
			t.Fatal(err)
		}
		files, _ := filepath.Glob(filepath.Join(path, "*.go"))
		if len(files) > 0 {
			t.Errorf("generic stage package %q is forbidden", name)
		}
	}
}
