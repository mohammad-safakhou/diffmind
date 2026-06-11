package ast

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// ParseFile parses one source file and returns its FileAST.
// The language is determined from the file extension.
// Returns nil, nil when the file extension is not a recognised source language.
func ParseFile(ctx context.Context, repoRoot, relPath string) (*FileAST, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	lang := LanguageForExtension(ext)
	if lang == "" {
		return nil, nil
	}
	sitterLang := GetLanguage(lang)
	if sitterLang == nil {
		return nil, nil
	}
	abs := filepath.Join(repoRoot, relPath)
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relPath, err)
	}
	return parseSource(ctx, src, lang, sitterLang, relPath)
}

// ParseConfigFile parses a configuration file and returns a ConfigFile.
// Returns nil when the file extension is not a recognised config format.
func ParseConfigFile(repoRoot, relPath string) (*ConfigFile, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	format := ConfigFormatForExtension(ext)
	if format == "" {
		return nil, nil
	}
	abs := filepath.Join(repoRoot, relPath)
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", relPath, err)
	}
	entries := extractConfigEntries(src, format)
	return &ConfigFile{
		Path:    relPath,
		Format:  format,
		Entries: entries,
	}, nil
}

// parseSource runs tree-sitter on src and extracts the FileAST.
func parseSource(ctx context.Context, src []byte, lang string, sitterLang *sitter.Language, relPath string) (*FileAST, error) {
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(sitterLang)

	tree, err := p.ParseCtx(ctx, nil, src)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relPath, err)
	}
	defer tree.Close()

	root := tree.RootNode()
	fa := &FileAST{
		Path:     relPath,
		Language: lang,
	}

	// Load queries for this language.
	qs := queriesForLanguage(lang)
	if qs == nil {
		// No queries registered yet: return a skeleton FileAST without
		// symbols or calls. The walker will still handle it correctly
		// (an empty node has no outgoing edges).
		return fa, nil
	}

	// Extract imports.
	fa.Imports = extractImports(root, src, lang, sitterLang)

	// Extract symbol definitions.
	fa.Symbols = extractSymbols(root, src, lang, sitterLang, relPath)

	// Extract call sites.
	fa.Calls = extractCalls(root, src, lang, sitterLang, relPath, fa.Symbols)

	// Extract lightweight field declarations for receiver/type resolution.
	fa.FieldTypes = extractFieldTypes(src, fa.Symbols)
	fa.LocalTypes = extractLocalTypes(src, fa.Symbols)
	fa.Implements = extractImplements(src, fa.Symbols)

	return fa, nil
}
