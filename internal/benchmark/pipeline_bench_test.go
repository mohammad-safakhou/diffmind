package benchmark

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diffmind/internal/classifier"
	"diffmind/internal/parser"
	"diffmind/internal/snapshot"
)

type profile struct {
	name      string
	goFiles   int
	jsFiles   int
	yamlFiles int
}

func BenchmarkRepoProfiles(b *testing.B) {
	profiles := []profile{
		{name: "small", goFiles: 20, jsFiles: 10, yamlFiles: 8},
		{name: "medium", goFiles: 140, jsFiles: 90, yamlFiles: 40},
		{name: "large", goFiles: 400, jsFiles: 260, yamlFiles: 120},
	}

	for _, p := range profiles {
		b.Run(p.name, func(b *testing.B) {
			root := b.TempDir()
			if err := seedProfileRepo(root, p); err != nil {
				b.Fatalf("seed profile repo: %v", err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, err := classifier.ScanTree(root)
				if err != nil {
					b.Fatalf("scan tree: %v", err)
				}
				_, err = snapshot.BuildInventory(root, snapshot.InventoryOptions{
					ExcludeDirs: map[string]struct{}{".git": {}, ".diffmind": {}, ".gocache": {}, "bin": {}},
				})
				if err != nil {
					b.Fatalf("build inventory: %v", err)
				}

				outDir := filepath.Join(root, ".bench-out", fmt.Sprintf("iter-%d", i))
				err = parser.Run(context.Background(), []string{"--source", root, "--out", outDir})
				if err != nil {
					b.Fatalf("parser run: %v", err)
				}
			}
		})
	}
}

func seedProfileRepo(root string, p profile) error {
	if err := os.MkdirAll(filepath.Join(root, "service", "handlers"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "web", "src"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		return err
	}

	goTemplate := "package handlers\n\nfunc H%d() string { return \"ok\" }\n"
	jsTemplate := "export function f%d(){ return fetch('/api/%d') }\n"
	yamlTemplate := "kind: Deployment\nmetadata:\n  name: app-%d\nspec:\n  replicas: 2\n"

	for i := 0; i < p.goFiles; i++ {
		path := filepath.Join(root, "service", "handlers", fmt.Sprintf("h_%04d.go", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf(goTemplate, i)), 0o644); err != nil {
			return err
		}
	}
	for i := 0; i < p.jsFiles; i++ {
		path := filepath.Join(root, "web", "src", fmt.Sprintf("mod_%04d.js", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf(jsTemplate, i, i)), 0o644); err != nil {
			return err
		}
	}
	for i := 0; i < p.yamlFiles; i++ {
		path := filepath.Join(root, "deploy", fmt.Sprintf("deploy_%04d.yaml", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf(yamlTemplate, i)), 0o644); err != nil {
			return err
		}
	}

	readme := filepath.Join(root, "README.md")
	return os.WriteFile(readme, []byte(strings.Repeat("benchmark profile\n", 20)), 0o644)
}
