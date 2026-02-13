package parser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"diffmind/internal/snapshot"
)

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	root, err := filepath.Abs(opts.Source)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}

	inventory, err := snapshot.BuildInventory(root, snapshot.InventoryOptions{ExcludeDirs: map[string]struct{}{
		".git": {}, ".diffmind": {}, ".gocache": {}, "bin": {}, "node_modules": {},
	}})
	if err != nil {
		return fmt.Errorf("build inventory for parser: %w", err)
	}

	snapshotID := opts.SnapshotID
	if snapshotID == "" {
		snapshotID = deriveParserSnapshotID(root, inventory)
	}

	report := ParseReport{
		GeneratedAt:   time.Now().UTC(),
		SourceRoot:    root,
		SnapshotID:    snapshotID,
		ParserVersion: parserVersion,
		TotalFiles:    len(inventory),
	}

	for _, file := range inventory {
		abs := filepath.Join(root, filepath.FromSlash(file.Path))
		artifact, err := parseFile(ctx, snapshotID, abs, file.Path, file.SHA256)
		if err != nil {
			report.FailedCount++
			slog.Warn("parse failed", "file", file.Path, "error", err)
			continue
		}
		if err := writeArtifact(opts.OutDir, artifact); err != nil {
			report.FailedCount++
			slog.Warn("artifact write failed", "file", file.Path, "error", err)
			continue
		}
		report.ArtifactsCreated++
		switch artifact.ArtifactType {
		case "config":
			report.StructuredCount++
		case "tree_sitter":
			report.TreeSitterCount++
		default:
			report.FallbackCount++
		}
	}

	reportPath, err := writeReport(opts.OutDir, report)
	if err != nil {
		return fmt.Errorf("write parser report: %w", err)
	}

	slog.Info("parser completed",
		"snapshot_id", report.SnapshotID,
		"files", report.TotalFiles,
		"artifacts", report.ArtifactsCreated,
		"structured", report.StructuredCount,
		"tree_sitter", report.TreeSitterCount,
		"fallback", report.FallbackCount,
		"failed", report.FailedCount,
		"report_path", reportPath,
	)
	fmt.Println(reportPath)
	return nil
}

func parseOptions(args []string) (Options, error) {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	source := fs.String("source", ".", "Repository source path")
	outDir := fs.String("out", ".diffmind", "Output root for parser artifacts")
	snapshotID := fs.String("snapshot-id", "", "Optional snapshot id; if empty a deterministic parser snapshot id is derived")

	if err := fs.Parse(filterParserArgs(args)); err != nil {
		return Options{}, fmt.Errorf("parse parse flags: %w", err)
	}
	return Options{Source: *source, OutDir: *outDir, SnapshotID: *snapshotID}, nil
}

func filterParserArgs(args []string) []string {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--source" || arg == "--out" || arg == "--snapshot-id":
			filtered = append(filtered, arg)
			if i+1 < len(args) {
				i++
				filtered = append(filtered, args[i])
			}
		case strings.HasPrefix(arg, "--source=") || strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--snapshot-id="):
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

func parseFile(ctx context.Context, snapshotID string, absPath string, relPath string, fileHash string) (ParseArtifact, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return ParseArtifact{}, fmt.Errorf("read file: %w", err)
	}

	lineCount := 1
	if len(content) > 0 {
		lineCount = strings.Count(string(content), "\n") + 1
	}

	artifact := ParseArtifact{
		SnapshotID:    snapshotID,
		FilePath:      relPath,
		FileHash:      fileHash,
		ParserVersion: parserVersion,
		LineCount:     lineCount,
		GeneratedAt:   time.Now().UTC(),
	}

	if isLikelyBinary(content) {
		artifact.ArtifactType = "text"
		artifact.ParserName = "binary-fallback"
		return artifact, nil
	}

	if structured, ok, err := parseStructured(relPath, content); ok {
		artifact.ArtifactType = "config"
		artifact.ParserName = "structured-config"
		artifact.Structured = structured
		if err != nil {
			artifact.Error = err.Error()
		}
		return artifact, nil
	}

	if ts, ok, err := parseSourceWithTreeSitter(ctx, strings.ToLower(filepath.Ext(relPath)), content); ok {
		if err != nil {
			artifact.ArtifactType = "text"
			artifact.ParserName = "tree-sitter-fallback"
			artifact.Error = err.Error()
			return artifact, nil
		}
		artifact.ArtifactType = ts.ArtifactType
		artifact.Language = ts.Language
		artifact.ParserName = ts.ParserName
		artifact.Tree = ts.Tree
		artifact.Symbols = ts.Symbols
		return artifact, nil
	}

	artifact.ArtifactType = "text"
	artifact.ParserName = "text-fallback"
	return artifact, nil
}

func deriveParserSnapshotID(root string, inventory []snapshot.FileEntry) string {
	h := sha256.New()
	_, _ = h.Write([]byte(root))
	for _, file := range inventory {
		_, _ = h.Write([]byte(file.Path))
		_, _ = h.Write([]byte(file.SHA256))
	}
	_, _ = h.Write([]byte(parserVersion))
	return hex.EncodeToString(h.Sum(nil))
}

func isLikelyBinary(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}
