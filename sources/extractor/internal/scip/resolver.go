package scip

import (
	"sort"
	"strings"
)

// Resolver maps diffmind's entity references (file:line locations and
// names recorded in detail_exposures.json / detail_dependencies.json)
// to SCIP symbols inside an Index.
//
// Resolution proceeds in two tiers, from highest to lowest confidence:
//
//  1. POSITIONAL: look up SymbolAt(file, line, col). When an entity's
//     source_locations array points to a definition (controller
//     handler, repository method body, etc.), this returns the
//     correct symbol with near-perfect precision.
//
//  2. NAME-BASED: scan SymbolInformation.DisplayName across the
//     index for the entity's `Name` field, scoped by the
//     classification (handler / repository.method / feign client /
//     etc.). Lower precision but works when source_locations are
//     missing or pointed at a wrapper file.
//
// The resolver does NOT mutate the Index. It is safe for concurrent
// reads.
type Resolver struct {
	idx *Index
}

// NewResolver constructs a Resolver bound to `idx`.
func NewResolver(idx *Index) *Resolver {
	return &Resolver{idx: idx}
}

// EntityLocation is diffmind's input to the resolver: a name plus a
// best-known file:line position from the detail stage. Field numbers
// follow the model.Location convention (1-based lines; we convert to
// zero-based internally).
//
// Hint disambiguates between resolution strategies when the entity
// could match multiple symbols. Empty Hint falls back to the default
// strategy (positional first, then exact-name match).
type EntityLocation struct {
	Name      string
	File      string
	StartLine int    // 1-based; the value diffmind stores in artifacts
	StartCol  int    // 1-based; same convention
	Hint      string // "handler", "repository_method", "feign_client", "sns_topic", "scheduler", ""
}

// Resolution captures the result of one resolver call. The chosen
// symbol(s) are listed in priority order; callers typically take
// Symbols[0] but may iterate for ambiguity reporting.
type Resolution struct {
	// Symbols is the resolved SCIP symbol(s). Empty when nothing
	// matched. Length > 1 indicates ambiguity (overloaded methods,
	// duplicate class names across packages, etc.).
	Symbols []string
	// Source describes which resolution strategy produced this result:
	// "positional", "exact_name", "prefix", "external", or "" when
	// unresolved.
	Source string
	// Confidence is a coarse score in [0,1]. 1.0 == positional match;
	// 0.6 == exact name match; 0.4 == prefix match. Used by the
	// caller as the connection's confidence floor.
	Confidence float64
}

// Resolve looks up an entity. Order of attempts:
//
//  1. Positional: SymbolAt with the recorded file:line.
//  2. Exact display-name match: scan all SymbolInformation blocks for
//     a matching DisplayName.
//  3. Prefix match: in case the indexer used a slightly different
//     spelling (e.g. erased generics), try a prefix of the symbol path.
//
// Returns a Resolution with empty Symbols when no strategy matches.
func (r *Resolver) Resolve(loc EntityLocation) Resolution {
	if r == nil || r.idx == nil {
		return Resolution{}
	}
	// Step 1: positional.
	if loc.File != "" && loc.StartLine > 0 {
		// Convert 1-based to zero-based for SymbolAt. We use line-1
		// and col-1; col may legitimately be 0 in some diffmind
		// artifacts that don't record it, so clamp.
		line := int32(loc.StartLine - 1)
		col := int32(loc.StartCol - 1)
		if col < 0 {
			col = 0
		}
		if sym := r.idx.SymbolAt(loc.File, line, col); sym != "" {
			return Resolution{
				Symbols:    []string{sym},
				Source:     "positional",
				Confidence: 1.0,
			}
		}

		// The LLM-recorded line may be off by several lines: it often
		// records the annotation line (@GetMapping) rather than the
		// actual method identifier line. Scan a progressively wider
		// window of lines until we find a callable definition.
		for _, window := range []int32{3, 10, 30} {
			if sym := r.idx.closestDefinition(loc.File, line-1, line+window, line); sym != "" {
				confidence := 0.9
				if window > 3 {
					confidence = 0.75 // wider window = less certain
				}
				return Resolution{
					Symbols:    []string{sym},
					Source:     "positional",
					Confidence: confidence,
				}
			}
		}
	}

	// Step 2: exact display-name match (and a few normalisations).
	if loc.Name != "" {
		matches := r.byDisplayName(loc.Name)
		if len(matches) > 0 {
			return Resolution{
				Symbols:    matches,
				Source:     "exact_name",
				Confidence: 0.6,
			}
		}
		// Strip parenthesised signatures: "findById(Long id)" → "findById".
		if i := strings.IndexAny(loc.Name, "(:"); i > 0 {
			short := strings.TrimSpace(loc.Name[:i])
			matches := r.byDisplayName(short)
			if len(matches) > 0 {
				return Resolution{
					Symbols:    matches,
					Source:     "exact_name",
					Confidence: 0.55,
				}
			}
		}
	}

	return Resolution{}
}

// byDisplayName returns every locally-defined symbol whose
// SymbolInformation.DisplayName equals `name` exactly. We deliberately
// don't lowercase: SCIP display names preserve the original casing,
// which we want for class.method matching.
//
// IMPORTANT: not every SCIP indexer populates SymbolInformation.DisplayName.
// scip-typescript, in particular, leaves it empty in its current
// release. When the field is empty we fall back to parsing the
// terminal identifier out of the SCIP symbol STRING, which has a
// predictable suffix grammar (every descriptor terminates with a
// single-character suffix like '#', '.', '/', ')', ']'). This keeps
// the resolver working across indexers without forcing each upstream
// to populate DisplayName.
//
// Result is sorted for deterministic ambiguity reporting.
func (r *Resolver) byDisplayName(name string) []string {
	if r == nil || r.idx == nil || name == "" {
		return nil
	}
	out := []string{}
	for s, sym := range r.idx.symbolInfoBySymbol {
		displayName := sym.GetDisplayName()
		if displayName == "" {
			displayName = displayNameFromSymbol(s)
		}
		if displayName == name {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// displayNameFromSymbol parses the terminal identifier out of a SCIP
// symbol string. The SCIP grammar (see scip.proto) ends every
// descriptor with one of:
//
//	'/'   namespace
//	'#'   type
//	'.'   term / method
//	':'   meta
//	'!'   macro
//	')'   parameter / disambiguator
//	']'   type-parameter
//
// We walk the string from the end, skip any disambiguator tail
// (parenthesised content, type-param brackets, dot), and return the
// substring between the previous descriptor suffix and that point.
//
// EXAMPLES
//
//	"scip-ts npm pkg 1.0 src/`f.ts`/Foo#bar()."
//	   → "bar"
//
//	"scip-go gomod m hash `m`/Foo#FindByID()."
//	   → "FindByID"
//
//	"scip-java maven g a v com/ex/Foo#m(+1)."
//	   → "m"
//
// Edge cases (empty input, missing suffix) return "".
func displayNameFromSymbol(sym string) string {
	if sym == "" || strings.HasPrefix(sym, "local ") {
		return ""
	}
	// Trim trailing parenthesised disambiguator: "m(+1)." → "m()."
	// We also trim the final suffix character itself (., #, /).
	end := len(sym)
	if end > 0 {
		switch sym[end-1] {
		case '.', '#', '/', ')', ']':
			end--
		}
	}
	// Trim parameter list "(...)" or type params "[...]" if present.
	for end > 0 {
		if sym[end-1] != ')' && sym[end-1] != ']' {
			break
		}
		open := byte('(')
		if sym[end-1] == ']' {
			open = '['
		}
		depth := 1
		i := end - 2
		for i >= 0 && depth > 0 {
			switch sym[i] {
			case open:
				depth--
			case sym[end-1]:
				depth++
			}
			i--
		}
		end = i + 1
	}
	// Now scan back to the previous descriptor boundary.
	start := end
	for start > 0 {
		c := sym[start-1]
		if c == '/' || c == '#' || c == '.' || c == ':' || c == '!' || c == ' ' || c == ')' || c == ']' {
			break
		}
		start--
	}
	out := sym[start:end]
	// Strip any backtick-escaped identifier wrappers used by scip-go
	// and scip-typescript for non-ASCII identifiers.
	out = strings.TrimSuffix(strings.TrimPrefix(out, "`"), "`")
	return out
}

// ResolveByQualified resolves a "ClassName.methodName" or
// "ClassName#methodName" name to a symbol. This is the common form
// stored in detail_dependencies.json for repository methods:
// `CampaignItemRepository.findByIdOrThrow`. We split on the dot/#
// and require both parts to match.
//
// Returns a Resolution with Source="exact_name" on a hit, empty
// otherwise. Caller is expected to combine this with positional
// resolution for higher confidence.
func (r *Resolver) ResolveByQualified(qualified string) Resolution {
	if r == nil || r.idx == nil {
		return Resolution{}
	}
	// Tolerate ".", "#", "::", "/".
	sepIdx := -1
	for i := 0; i < len(qualified); i++ {
		switch qualified[i] {
		case '.', '#':
			sepIdx = i
		}
	}
	if sepIdx <= 0 || sepIdx >= len(qualified)-1 {
		return r.Resolve(EntityLocation{Name: qualified})
	}
	className := strings.TrimSpace(qualified[:sepIdx])
	methodName := strings.TrimSpace(qualified[sepIdx+1:])
	// strip "(args)" tail from methodName
	if p := strings.Index(methodName, "("); p > 0 {
		methodName = methodName[:p]
	}

	matches := []string{}
	for s, sym := range r.idx.symbolInfoBySymbol {
		if !strings.Contains(s, className) {
			continue
		}
		disp := sym.GetDisplayName()
		if disp == "" {
			disp = displayNameFromSymbol(s)
		}
		if disp == methodName {
			matches = append(matches, s)
		}
	}
	if len(matches) > 0 {
		sort.Strings(matches)
		return Resolution{
			Symbols:    matches,
			Source:     "exact_name",
			Confidence: 0.7, // higher than plain name: both class and method matched
		}
	}
	return Resolution{}
}
