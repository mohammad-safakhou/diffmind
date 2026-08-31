package ast

import (
	"path/filepath"
	"strings"
)

// EnclosingSymbolAt returns the qualified name of the innermost function/method/
// constructor symbol whose source range contains the given line in file, or ""
// when none matches. It tolerates the 0-based (tree-sitter ranges) vs 1-based
// (model.Location) line convention with a one-line slack, and matches file paths
// by slash-normalized equality or suffix so an absolute location and a
// repo-relative SymbolDef.File both resolve.
//
// It exists so deterministic passes can map a discovered operation's source
// location back to the code symbol that produced it — the entry point for
// linking an operation to the client/bean it calls through.
func EnclosingSymbolAt(idx *ProjectIndex, file string, line int) string {
	if idx == nil || strings.TrimSpace(file) == "" {
		return ""
	}
	want := filepath.ToSlash(strings.TrimSpace(file))
	best := ""
	var bestSpan uint32
	haveBest := false
	for _, defs := range idx.Symbols {
		for _, s := range defs {
			switch s.Kind {
			case SymbolKindFunction, SymbolKindMethod, SymbolKindConstructor:
			default:
				continue
			}
			if !fileMatches(filepath.ToSlash(s.File), want) {
				continue
			}
			lo, hi := int(s.Range.StartLine), int(s.Range.EndLine)
			if line < lo-1 || line > hi+1 {
				continue
			}
			span := s.Range.EndLine - s.Range.StartLine
			if !haveBest || span < bestSpan {
				best, bestSpan, haveBest = s.Qualified, span, true
			}
		}
	}
	return best
}

// fileMatches compares two slash-normalized paths by equality or path-suffix, so
// absolute vs repo-relative spellings of the same file match.
func fileMatches(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}
