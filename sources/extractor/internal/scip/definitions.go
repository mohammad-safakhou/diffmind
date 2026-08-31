package scip

import (
	"sort"
	"strings"
)

// Location is diffmind's zero-based half-open source position. It is
// deliberately distinct from model.Location (which is 1-based, line-only
// for evidence purposes) so we don't accidentally leak SCIP's zero-based
// convention into the artifact JSON.
//
// Within this package, Location values are ALWAYS zero-based. Conversion
// happens at the API boundary (see resolver.go, walker.go).
type Location struct {
	// File is the SCIP relative_path (forward slashes, no leading "/").
	File string
	// StartLine, StartCol, EndLine, EndCol are zero-based, half-open.
	StartLine int32
	StartCol  int32
	EndLine   int32
	EndCol    int32
}

// CallSite is one resolved call (or read access) of a symbol inside
// the body of an enclosing symbol. The walker uses CallSites to chain
// hops together.
type CallSite struct {
	// CallerSymbol is the SCIP symbol of the method/function whose
	// body contains this call. May be "" for a top-level expression
	// (rare; we mostly walk method bodies).
	CallerSymbol string
	// CalleeSymbol is the SCIP symbol referenced at this location.
	CalleeSymbol string
	// At is the file:line:col range of the callee identifier.
	At Location
	// Enclosing is the file:line:col range of the *call expression*
	// (or the enclosing AST node, depending on what the indexer emits
	// for `enclosing_range`). When non-zero it includes argument
	// parentheses and arguments, which is more useful for snippet
	// extraction than just the bare identifier range in `At`.
	Enclosing Location
	// Roles is the raw SCIP symbol_roles bitset for the occurrence.
	// We expose it so the walker can prefer Definition over Read/Write
	// when picking the "primary" callee.
	Roles int32
}

// DefinitionAt returns the location(s) where the given symbol is
// defined in this index. Multiple definitions are possible for
// overloaded methods in some languages (Java erases generics; Scala
// has implicit conversion definitions); callers must handle the slice.
//
// Returns an empty slice if the symbol is not defined locally. This
// is normal for symbols that live in a Maven / npm / pip dependency:
// scip-java records them as "external symbols" but no Definition
// occurrence exists in our Document set.
func (i *Index) DefinitionAt(symbol string) []Location {
	if i == nil {
		return nil
	}
	locs := i.symbolDefinitions[symbol]
	if len(locs) == 0 {
		return nil
	}
	out := make([]Location, 0, len(locs))
	for _, l := range locs {
		doc := i.documentsByPath[l.DocumentPath]
		if doc == nil {
			continue
		}
		if l.OccurrenceIndex < 0 || l.OccurrenceIndex >= len(doc.GetOccurrences()) {
			continue
		}
		occ := doc.GetOccurrences()[l.OccurrenceIndex]
		out = append(out, occurrenceToLocation(l.DocumentPath, occ))
	}
	return out
}

// SymbolAt returns the SCIP symbol whose definition or reference range
// contains the given file:line:col position. The match is the smallest
// enclosing occurrence (so that, e.g., looking up `foo` inside
// `bar.foo.baz` returns the `foo` symbol, not the whole expression).
//
// Returns "" if no occurrence covers the position. This is the primary
// way diffmind maps "the controller handler at line N" to a SCIP
// symbol string.
//
// `line` and `col` are zero-based to match SCIP's convention. Callers
// receiving 1-based positions from a JSON artifact must subtract 1
// before calling. The wrapper in resolver.go does this for us.
func (i *Index) SymbolAt(file string, line, col int32) string {
	if i == nil {
		return ""
	}
	doc := i.documentsByPath[file]
	if doc == nil {
		return ""
	}
	best := ""
	bestArea := int64(-1) // smallest area wins
	for _, occ := range doc.GetOccurrences() {
		r := occurrenceRange(occ)
		if !rangeContains(r, line, col) {
			continue
		}
		area := rangeArea(r)
		if best == "" || area < bestArea {
			best = occ.GetSymbol()
			bestArea = area
		}
	}
	return best
}

// DocumentText returns the source text of the file at `relativePath`.
// If the indexer didn't embed Document.text, returns "" — callers that need
// source for snippet extraction should fall back to reading the file from the
// source root directly.
//
// We don't transparently read the file here because the index may
// have been merged from multiple sources whose project_root differs from the
// source root. Snippet/source-text resolution is a concern for the higher-level
// resolver (resolver.go), which knows the source root.
func (i *Index) DocumentText(relativePath string) string {
	if i == nil {
		return ""
	}
	doc := i.documentsByPath[relativePath]
	if doc == nil {
		return ""
	}
	return doc.GetText()
}

// PrefixMatchSymbols returns every locally-defined symbol whose
// SCIP string starts with `prefix`. Used by the resolver to find
// "all methods of class X" when we know the class symbol but not
// the exact erased signature.
//
// Linear scan over the symbol map; O(N) per call. For very large
// indexes we may add a prefix tree, but for Sprint 2 a sort suffices.
func (i *Index) PrefixMatchSymbols(prefix string) []string {
	if i == nil || prefix == "" {
		return nil
	}
	out := []string{}
	for sym := range i.symbolDefinitions {
		if strings.HasPrefix(sym, prefix) {
			out = append(out, sym)
		}
	}
	sort.Strings(out)
	return out
}

// FirstDefinitionInRange returns the closest locally-defined, callable symbol
// to `anchorLine` whose definition occurrence falls within [startLine, endLine)
// (0-based, half-open). It skips local variables, parameters, and class-level
// symbols (constructors, fields) and prefers method/function symbols.
//
// "Closest" means the candidate whose definition line is nearest to anchorLine.
// When two candidates are equidistant, method symbols ("().") beat type symbols.
//
// This is used by the resolver as a fallback when SymbolAt fails to hit the
// exact (line, col) recorded by detectors, which happens when scip-java places
// the identifier range at a different column than the method signature start
// line.
func (i *Index) FirstDefinitionInRange(file string, startLine, endLine int32) string {
	return i.closestDefinition(file, startLine, endLine, startLine)
}

func (i *Index) closestDefinition(file string, startLine, endLine, anchorLine int32) string {
	if i == nil || file == "" {
		return ""
	}
	doc := i.documentsByPath[file]
	if doc == nil {
		return ""
	}
	if startLine < 0 {
		startLine = 0
	}
	type candidate struct {
		sym      string
		dist     int32
		isMethod bool
	}
	var best *candidate
	for _, occ := range doc.GetOccurrences() {
		if !isDefinitionRole(occ.GetSymbolRoles()) {
			continue
		}
		sym := occ.GetSymbol()
		if sym == "" || strings.HasPrefix(sym, "local ") {
			continue
		}
		if !isCallableSymbol(sym) {
			continue
		}
		r := occurrenceRange(occ)
		if r == nil {
			continue
		}
		if r.startLine < startLine || r.startLine >= endLine {
			continue
		}
		dist := r.startLine - anchorLine
		if dist < 0 {
			dist = -dist
		}
		isMethod := strings.Contains(sym, "().")
		if best == nil ||
			dist < best.dist ||
			(dist == best.dist && isMethod && !best.isMethod) {
			best = &candidate{sym: sym, dist: dist, isMethod: isMethod}
		}
	}
	if best == nil {
		return ""
	}
	return best.sym
}
