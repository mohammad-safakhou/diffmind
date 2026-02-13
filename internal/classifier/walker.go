package classifier

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ScanTree(root string) ([]ScannedFile, RepoStats, error) {
	files := make([]ScannedFile, 0, 1024)
	stats := RepoStats{ExtensionCount: make(map[string]int)}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolve rel path: %w", err)
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if shouldSkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(rel))
		files = append(files, ScannedFile{Path: rel, Ext: ext})
		stats.TotalFiles++
		stats.ExtensionCount[ext]++
		if isSourceExt(ext) {
			stats.SourceFiles++
		}
		if isConfigPath(rel) {
			stats.ConfigFiles++
		}
		return nil
	})
	if err != nil {
		return nil, RepoStats{}, fmt.Errorf("walk repo: %w", err)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, stats, nil
}

func shouldSkipDir(rel string) bool {
	base := filepath.Base(rel)
	skip := map[string]struct{}{
		".git": {}, ".diffmind": {}, ".gocache": {}, "bin": {}, "node_modules": {},
	}
	_, ok := skip[base]
	return ok
}

func isSourceExt(ext string) bool {
	source := map[string]struct{}{
		".go": {}, ".js": {}, ".ts": {}, ".tsx": {}, ".jsx": {},
		".py": {}, ".java": {}, ".rb": {}, ".rs": {}, ".php": {},
		".cs": {}, ".c": {}, ".h": {}, ".cpp": {}, ".hpp": {},
		".kt": {}, ".swift": {}, ".scala": {}, ".sh": {},
	}
	_, ok := source[ext]
	return ok
}

func isConfigPath(path string) bool {
	lower := strings.ToLower(path)
	ext := strings.ToLower(filepath.Ext(lower))
	switch ext {
	case ".yaml", ".yml", ".json", ".toml", ".ini", ".xml", ".env", ".properties", ".conf":
		return true
	}
	base := filepath.Base(lower)
	configNames := map[string]struct{}{
		"dockerfile": {}, "docker-compose.yml": {}, "makefile": {}, "go.mod": {}, "go.sum": {},
		"package.json": {}, "pnpm-lock.yaml": {}, "package-lock.json": {}, "cargo.toml": {},
		"pom.xml": {}, "build.gradle": {}, "build.gradle.kts": {}, "pyproject.toml": {},
	}
	_, ok := configNames[base]
	return ok
}
