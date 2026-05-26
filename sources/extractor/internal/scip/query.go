package scip

import (
	"sort"
	"strings"

	pb "github.com/scip-code/scip/bindings/go/scip"
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

// FindOccurrencesBySymbol returns every occurrence (definition AND
// reference) of `symbol` across all documents. Used by the walker
// when it needs to know "who calls X?".
//
// The result is sorted by (file, startLine, startCol) for determinism.
func (i *Index) FindOccurrencesBySymbol(symbol string) []CallSite {
	if i == nil || symbol == "" {
		return nil
	}
	out := []CallSite{}
	for path, doc := range i.documentsByPath {
		for _, occ := range doc.GetOccurrences() {
			if occ.GetSymbol() != symbol {
				continue
			}
			out = append(out, CallSite{
				CalleeSymbol: symbol,
				At:           occurrenceToLocation(path, occ),
				Enclosing:    enclosingLocation(path, occ),
				Roles:        occ.GetSymbolRoles(),
			})
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].At.File != out[b].At.File {
			return out[a].At.File < out[b].At.File
		}
		if out[a].At.StartLine != out[b].At.StartLine {
			return out[a].At.StartLine < out[b].At.StartLine
		}
		return out[a].At.StartCol < out[b].At.StartCol
	})
	return out
}

// CallsFrom returns the call sites inside the body of `callerSymbol`.
// It works by:
//  1. Looking up `callerSymbol`'s definition location.
//  2. Determining the caller's body range from `enclosing_range`
//     when the indexer populated it, otherwise inferring it from the
//     gap between this definition and the next definition (or end of
//     file) in the same document.
//  3. Walking the document's occurrences and selecting references
//     whose own range falls inside the body.
//
// # INDEXER QUIRKS
//
// `enclosing_range` is OPTIONAL in the SCIP schema. The Sourcegraph
// scip-java JVM path populates it; the Kotlin compiler plugin path
// inside scip-java does NOT — we ran a Kotlin fixture live and got
// empty enclosing ranges for every method occurrence. scip-go and
// scip-typescript populate it. scip-python populates it inconsistently.
// The fallback below is therefore required, not optional, for
// real-world cross-language correctness.
func (i *Index) CallsFrom(callerSymbol string) []CallSite {
	if i == nil || callerSymbol == "" {
		return nil
	}
	return i.callsBySymbol[callerSymbol]
}

func (i *Index) callsFromDefinition(defLoc symbolLocation) []CallSite {
	if i == nil {
		return nil
	}
	doc := i.documentsByPath[defLoc.DocumentPath]
	if doc == nil {
		return nil
	}
	occs := doc.GetOccurrences()
	if defLoc.OccurrenceIndex < 0 || defLoc.OccurrenceIndex >= len(occs) {
		return nil
	}
	defOcc := occs[defLoc.OccurrenceIndex]
	callerSymbol := defOcc.GetSymbol()
	body := occurrenceEnclosing(defOcc)
	if body == nil {
		// Fallback: infer the body as the lines from this definition through
		// the next sibling definition in the same document. Some indexers omit
		// enclosing_range, and without this we would lose valid app-code edges.
		body = inferBodyRange(occs, defLoc.OccurrenceIndex)
		if body == nil {
			return nil
		}
	}

	out := []CallSite{}
	for k, occ := range occs {
		if k == defLoc.OccurrenceIndex {
			continue
		}
		callee := occ.GetSymbol()
		if callee == "" || callee == callerSymbol {
			continue
		}
		// Skip references to parameters and local variables — those are reads of
		// values, not invocations. The walker would otherwise spew
		// "deleteCampaign -> (id)" path hops that aren't real calls.
		if !isCallableSymbol(callee) {
			continue
		}
		roles := occ.GetSymbolRoles()
		if isDefinitionRole(roles) {
			// A nested definition (lambda, anonymous class) — skip; otherwise the
			// enclosing method appears to call its own nested scope definition.
			continue
		}
		r := occurrenceRange(occ)
		if !rangeContainedIn(r, body) {
			continue
		}
		out = append(out, CallSite{
			CallerSymbol: callerSymbol,
			CalleeSymbol: callee,
			At:           occurrenceToLocation(defLoc.DocumentPath, occ),
			Enclosing:    enclosingLocation(defLoc.DocumentPath, occ),
			Roles:        roles,
		})
	}
	sortCallSites(out)
	return out
}

func (i *Index) callsFromSlow(callerSymbol string) []CallSite {
	if i == nil || callerSymbol == "" {
		return nil
	}
	defs := i.symbolDefinitions[callerSymbol]
	if len(defs) == 0 {
		return nil
	}
	out := []CallSite{}
	for _, defLoc := range defs {
		doc := i.documentsByPath[defLoc.DocumentPath]
		if doc == nil {
			continue
		}
		occs := doc.GetOccurrences()
		if defLoc.OccurrenceIndex >= len(occs) {
			continue
		}
		defOcc := occs[defLoc.OccurrenceIndex]
		body := occurrenceEnclosing(defOcc)
		if body == nil {
			// Fallback: infer the body as the lines from this
			// definition through (a) the next sibling definition in
			// the same document, or (b) the end of the document. This
			// is coarser than the indexer's own `enclosing_range`, but
			// it lets us produce useful call sites for indexers that
			// leave `enclosing_range` empty (notably the Kotlin path
			// of scip-java).
			body = inferBodyRange(occs, defLoc.OccurrenceIndex)
			if body == nil {
				continue
			}
		}

		for k, occ := range occs {
			if k == defLoc.OccurrenceIndex {
				continue
			}
			callee := occ.GetSymbol()
			if callee == "" || callee == callerSymbol {
				continue
			}
			// Skip references to parameters and local variables —
			// those are reads of values, not invocations. The walker
			// would otherwise spew "deleteCampaign → (id)" path hops
			// that aren't real calls (this fired on Kotlin live).
			if !isCallableSymbol(callee) {
				continue
			}
			// We only follow read-access references (method invocations
			// in Java/TS appear as ReadAccess; in Python and Go too).
			// Write accesses are field stores, not calls.
			roles := occ.GetSymbolRoles()
			if isDefinitionRole(roles) {
				// A nested definition (lambda, anonymous class) — skip;
				// we'd otherwise infinite-recurse into our own body.
				continue
			}
			r := occurrenceRange(occ)
			if !rangeContainedIn(r, body) {
				continue
			}
			out = append(out, CallSite{
				CallerSymbol: callerSymbol,
				CalleeSymbol: callee,
				At:           occurrenceToLocation(defLoc.DocumentPath, occ),
				Enclosing:    enclosingLocation(defLoc.DocumentPath, occ),
				Roles:        roles,
			})
		}
	}
	sortCallSites(out)
	return out
}

// Implementations returns the set of symbols that implement an
// interface / abstract / trait method `symbol`. The SCIP indexer
// records this via `Relationship.is_implementation` on each impl's
// SymbolInformation.
//
// In Spring DI this is critical: a controller calls
// `someService.foo()` where `someService` is typed as an interface
// (`SomeService`). The interface symbol's body is empty, but its
// implementation (`SomeServiceImpl#foo()`) IS the one we want to
// continue walking from.
//
// Returns nil when there are no recorded implementations (e.g. an
// already-concrete method).
func (i *Index) Implementations(symbol string) []string {
	if i == nil || symbol == "" {
		return nil
	}
	return i.implementationsBySymbol[symbol]
}

// SymbolInfo returns the SymbolInformation block for `symbol`, or nil
// if it isn't defined locally. The block carries the symbol's display
// name, kind (Method, Class, Function, etc.), documentation, and any
// relationships (is_implementation, is_reference) the indexer attached.
func (i *Index) SymbolInfo(symbol string) *pb.SymbolInformation {
	if i == nil {
		return nil
	}
	if sym := i.symbolInfoBySymbol[symbol]; sym != nil {
		return sym
	}
	// External symbols (Maven libs, npm packages, etc.) are stored
	// separately. They have no definitions in our Document set but the
	// indexer still annotates them with hover docs.
	return i.externalInfoBySymbol[symbol]
}

// DocumentText returns the source text of the file at `relativePath`.
// If the indexer didn't embed Document.text, returns "" — callers that
// need source for snippet extraction should fall back to reading the
// file from the snapshot directly.
//
// We don't transparently read the file here because the index may
// have been merged from multiple sources whose project_root differs
// from the running snapshot. Snippet/source-text resolution is a
// concern for the higher-level resolver (resolver.go), which knows
// the snapshot path.
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
// exact (line, col) the LLM recorded, which happens when scip-java places the
// identifier range at a different column than the method signature start line
// the LLM chose.
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

func sortCallSites(out []CallSite) {
	sort.Slice(out, func(a, b int) bool {
		if out[a].At.File != out[b].At.File {
			return out[a].At.File < out[b].At.File
		}
		if out[a].At.StartLine != out[b].At.StartLine {
			return out[a].At.StartLine < out[b].At.StartLine
		}
		if out[a].At.StartCol != out[b].At.StartCol {
			return out[a].At.StartCol < out[b].At.StartCol
		}
		return out[a].CalleeSymbol < out[b].CalleeSymbol
	})
}

// ---------------------------------------------------------------------
// Range / occurrence helpers.
//
// SCIP encodes ranges as []int32 with either 3 or 4 elements (3 when
// start_line == end_line). These helpers normalise to a 4-tuple so the
// rest of the package doesn't need to repeat the branching.
// ---------------------------------------------------------------------

type rng struct {
	startLine, startCol, endLine, endCol int32
}

func occurrenceRange(occ *pb.Occurrence) *rng {
	r := occ.GetRange()
	return parseRange(r)
}

func occurrenceEnclosing(occ *pb.Occurrence) *rng {
	r := occ.GetEnclosingRange()
	if len(r) == 0 {
		return nil
	}
	return parseRange(r)
}

// inferBodyRange returns a coarse "body range" for the definition at
// `defIdx` in `occs`, used as a fallback when the indexer didn't
// populate `enclosing_range`. The inferred range spans from the
// definition's own line through:
//
//   - the start line of the next SIBLING-level definition in the same
//     document (i.e. another method / function / top-level symbol), OR
//   - a generous open-ended end when this is the last definition we
//     know of in the document.
//
// "SIBLING-level" matters: scip-java's Kotlin path emits `local N`
// definitions inside method bodies for `val campaign = ...` and
// emits `(paramName)` definitions on signature lines. Treating either
// as the "next method" would chop the body off after the first
// `val` declaration. We filter both out with isSiblingDefinitionSymbol.
//
// We use line-only comparison because indexers disagree on exact
// column placement of definitions across language plugins.
func inferBodyRange(occs []*pb.Occurrence, defIdx int) *rng {
	defOcc := occs[defIdx]
	defR := occurrenceRange(defOcc)
	if defR == nil {
		return nil
	}
	// Find the next sibling definition occurrence STRICTLY AFTER defR.
	const sentinel = int32(1<<30 - 1)
	nextLine := sentinel
	for j, occ := range occs {
		if j == defIdx {
			continue
		}
		if !isDefinitionRole(occ.GetSymbolRoles()) {
			continue
		}
		if !isSiblingDefinitionSymbol(occ.GetSymbol()) {
			continue
		}
		r := occurrenceRange(occ)
		if r == nil {
			continue
		}
		if r.startLine <= defR.startLine {
			continue
		}
		if r.startLine < nextLine {
			nextLine = r.startLine
		}
	}
	end := nextLine - 1
	if end < defR.startLine {
		end = defR.startLine
	}
	return &rng{
		startLine: defR.startLine,
		startCol:  0,
		endLine:   end,
		endCol:    sentinel, // include everything on the final line
	}
}

// isCallableSymbol returns true when `symbol` represents something
// the walker should treat as a CALL target: a method, function, or
// type-with-a-constructor.
//
// Returns false for parameters ("...().(name)"), type parameters
// ("...().[T]"), local variables ("local N"), and field/property
// reads where the underlying definition isn't a method.
//
// SCIP's symbol grammar lets us classify by the descriptor suffix
// without ambiguity:
//
//	'(' ... ').'  → method (callable)        ✔
//	'#'           → type                     ✔ (callable for constructors)
//	'/'           → namespace                ✘
//	'.'           → term (field / property)  ✘ usually not a call
//	"(name)"      → parameter                ✘
//	"local N"     → local var                ✘
//
// We err toward INCLUSION (returning true) for ambiguous cases: a
// false positive lets the walker visit a non-call symbol that
// usually has no body of its own, so the walk terminates harmlessly.
// A false negative drops a real call and breaks transitive paths.
func isCallableSymbol(symbol string) bool {
	if symbol == "" || strings.HasPrefix(symbol, "local ") {
		return false
	}
	// Parameter / type-param descriptor: "...().(<name>)"
	if strings.HasSuffix(symbol, ")") {
		return false
	}
	// Field / property terms end in '.' but so do methods; methods
	// have "()." in them, fields don't.
	if strings.HasSuffix(symbol, ".") && !strings.Contains(symbol, "()") {
		// Heuristic: getter accessors emitted by scip-java for Kotlin
		// properties look like "...#getName()." (with parens) — those
		// ARE callable. We're filtering bare-term fields like
		// "...#service." (no parens).
		return false
	}
	return true
}

// isSiblingDefinitionSymbol reports whether `symbol` looks like a
// member-level definition (method, function, field, type) rather than
// a nested scope artifact. We use this to decide which definitions
// can legitimately end an enclosing method's body in inferBodyRange.
//
// We reject:
//   - "local N"            - SCIP-encoded local variables
//   - any symbol that ends in "(<paramName>)" - parameters, type params
//   - anything whose final descriptor begins with "`<" - synthetic
//     constructor / accessor symbols (e.g. "<init>", "<clinit>") which
//     usually sit on the class signature line and would falsely chop
//     a method body that starts immediately after.
//
// Everything else is accepted, which is the safe default: a false
// positive only narrows the body range by a few lines, while a false
// negative drops legitimate call edges (the Kotlin failure mode).
func isSiblingDefinitionSymbol(symbol string) bool {
	if symbol == "" {
		return false
	}
	if strings.HasPrefix(symbol, "local ") {
		return false
	}
	// SCIP descriptors: parameters end with ").", type params end
	// with "]." but the latter is rare; we filter both. We also drop
	// synthetic constructor / accessor symbols that look like methods
	// but appear at class-signature lines.
	if strings.HasSuffix(symbol, ")") || strings.Contains(symbol, "().(") {
		return false
	}
	if strings.Contains(symbol, "#`<") {
		return false
	}
	return true
}

func parseRange(r []int32) *rng {
	switch len(r) {
	case 3:
		return &rng{startLine: r[0], startCol: r[1], endLine: r[0], endCol: r[2]}
	case 4:
		return &rng{startLine: r[0], startCol: r[1], endLine: r[2], endCol: r[3]}
	default:
		return nil
	}
}

func rangeContains(r *rng, line, col int32) bool {
	if r == nil {
		return false
	}
	if line < r.startLine || line > r.endLine {
		return false
	}
	if line == r.startLine && col < r.startCol {
		return false
	}
	if line == r.endLine && col >= r.endCol {
		return false
	}
	return true
}

func rangeContainedIn(inner, outer *rng) bool {
	if inner == nil || outer == nil {
		return false
	}
	// inner.start >= outer.start
	if inner.startLine < outer.startLine {
		return false
	}
	if inner.startLine == outer.startLine && inner.startCol < outer.startCol {
		return false
	}
	// inner.end <= outer.end
	if inner.endLine > outer.endLine {
		return false
	}
	if inner.endLine == outer.endLine && inner.endCol > outer.endCol {
		return false
	}
	return true
}

// rangeArea returns a coarse "size" of the range for the smallest-
// enclosing tie-break in SymbolAt. Multi-line ranges get a constant
// per-line cost so a single-line 80-column range still beats a 10-line
// range.
func rangeArea(r *rng) int64 {
	if r == nil {
		return 1 << 62
	}
	lines := int64(r.endLine - r.startLine + 1)
	if lines <= 0 {
		lines = 1
	}
	cols := int64(r.endCol - r.startCol)
	if r.startLine != r.endLine {
		cols = 0 // multi-line: dominated by line count
	}
	if cols < 0 {
		cols = 0
	}
	return lines*4096 + cols
}

func occurrenceToLocation(file string, occ *pb.Occurrence) Location {
	r := occurrenceRange(occ)
	if r == nil {
		return Location{File: file}
	}
	return Location{File: file, StartLine: r.startLine, StartCol: r.startCol, EndLine: r.endLine, EndCol: r.endCol}
}

func enclosingLocation(file string, occ *pb.Occurrence) Location {
	r := occurrenceEnclosing(occ)
	if r == nil {
		return Location{}
	}
	return Location{File: file, StartLine: r.startLine, StartCol: r.startCol, EndLine: r.endLine, EndCol: r.endCol}
}
