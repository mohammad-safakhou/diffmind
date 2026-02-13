package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type InventoryOptions struct {
	ExcludeDirs map[string]struct{}
}

func BuildInventory(root string, opts InventoryOptions) ([]FileEntry, error) {
	entries := make([]FileEntry, 0, 1024)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if path == root {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolve relative path for %q: %w", path, err)
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if shouldExcludeDir(rel, opts.ExcludeDirs) {
				return filepath.SkipDir
			}
			return nil
		}

		if !d.Type().IsRegular() {
			return nil
		}

		entry, err := buildFileEntry(path, rel)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk repository tree: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	return entries, nil
}

func BuildSnapshot(prepared PreparedSource, inventory []FileEntry) Snapshot {
	return Snapshot{
		SnapshotID:  buildSnapshotID(prepared, inventory),
		RepoLocator: prepared.RepoLocator,
		Ref:         prepared.Ref,
		CommitSHA:   prepared.CommitSHA,
		SourceType:  prepared.SourceType,
		FileCount:   len(inventory),
		GeneratedAt: time.Now().UTC(),
		ToolVersion: ToolVersion,
	}
}

func buildSnapshotID(prepared PreparedSource, inventory []FileEntry) string {
	h := sha256.New()
	_, _ = io.WriteString(h, prepared.RepoLocator)
	_, _ = io.WriteString(h, "\n")
	_, _ = io.WriteString(h, prepared.Ref)
	_, _ = io.WriteString(h, "\n")
	_, _ = io.WriteString(h, prepared.CommitSHA)
	_, _ = io.WriteString(h, "\n")

	for _, entry := range inventory {
		_, _ = io.WriteString(h, entry.Path)
		_, _ = io.WriteString(h, "|")
		_, _ = io.WriteString(h, fmt.Sprintf("%d", entry.SizeBytes))
		_, _ = io.WriteString(h, "|")
		_, _ = io.WriteString(h, entry.SHA256)
		_, _ = io.WriteString(h, "|")
		_, _ = io.WriteString(h, entry.FileType)
		_, _ = io.WriteString(h, "|")
		_, _ = io.WriteString(h, entry.Classification)
		_, _ = io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func buildFileEntry(absPath string, relativePath string) (FileEntry, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return FileEntry{}, fmt.Errorf("open file %q: %w", absPath, err)
	}
	defer f.Close()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return FileEntry{}, fmt.Errorf("hash file %q: %w", absPath, err)
	}

	head := make([]byte, 8192)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return FileEntry{}, fmt.Errorf("rewind file %q: %w", absPath, err)
	}
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return FileEntry{}, fmt.Errorf("sample file %q: %w", absPath, err)
	}
	head = head[:n]

	fileType := detectFileType(relativePath, head)
	classification := classifyFile(relativePath, fileType)

	return FileEntry{
		Path:           relativePath,
		SizeBytes:      size,
		SHA256:         hex.EncodeToString(h.Sum(nil)),
		FileType:       fileType,
		Classification: classification,
	}, nil
}

func shouldExcludeDir(relativePath string, exclude map[string]struct{}) bool {
	base := filepath.Base(relativePath)
	if _, ok := exclude[base]; ok {
		return true
	}
	if strings.Contains(relativePath, "/.git/") {
		return true
	}
	return false
}

func detectFileType(path string, sample []byte) string {
	if isBinary(sample) {
		return "binary"
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "text"
	}
	return strings.TrimPrefix(ext, ".")
}

func classifyFile(path string, fileType string) string {
	lowerPath := strings.ToLower(path)

	if fileType == "binary" {
		return "binary"
	}
	if strings.Contains(lowerPath, "/vendor/") || strings.HasPrefix(lowerPath, "vendor/") || strings.Contains(lowerPath, "node_modules/") {
		return "vendor"
	}
	if strings.HasSuffix(lowerPath, ".pb.go") || strings.HasSuffix(lowerPath, ".gen.go") || strings.Contains(lowerPath, "/generated/") || strings.Contains(lowerPath, "/gen/") {
		return "generated"
	}
	if isConfigFile(lowerPath) {
		return "config"
	}
	if isDocFile(lowerPath) {
		return "doc"
	}
	if isSourceFile(lowerPath) {
		return "source"
	}
	return "other"
}

func isBinary(sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	for _, b := range sample {
		if b == 0 {
			return true
		}
	}
	return false
}

func isConfigFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml", ".json", ".toml", ".ini", ".xml", ".env", ".properties", ".conf":
		return true
	default:
		base := filepath.Base(path)
		configNames := map[string]struct{}{
			"dockerfile":        {},
			"makefile":          {},
			"go.mod":            {},
			"go.sum":            {},
			"package.json":      {},
			"package-lock.json": {},
			"pnpm-lock.yaml":    {},
			"cargo.toml":        {},
			"pom.xml":           {},
		}
		_, ok := configNames[base]
		return ok
	}
}

func isDocFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".rst", ".txt", ".adoc":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(filepath.Base(path)), "readme")
	}
}

func isSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	sourceExt := map[string]struct{}{
		".go": {}, ".js": {}, ".ts": {}, ".tsx": {}, ".jsx": {},
		".py": {}, ".java": {}, ".rb": {}, ".rs": {}, ".php": {},
		".cs": {}, ".c": {}, ".h": {}, ".cpp": {}, ".hpp": {},
		".kt": {}, ".swift": {}, ".scala": {}, ".sh": {},
	}
	_, ok := sourceExt[ext]
	return ok
}
