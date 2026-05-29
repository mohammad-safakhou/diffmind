package agents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/ast/framework"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// runConnectionsBatch is Stage 4. It maps each exposure to the
// dependencies it reaches via the tree-sitter AST call graph.
//
//   - Zero LLM calls: deterministic, fast, free.
//   - Full transitive paths with file:line for every hop.
//   - Per-hop conditions derived from tree-sitter enclosing context.
//   - Graceful fallback: when the AST index is empty (e.g. unsupported
//     language, empty snapshot), a name-based shallow matcher produces
//     Connection records without paths so the run still completes.
func (o *orchestrator) runConnectionsBatch(
	ctx context.Context,
	exposures []model.Exposure,
	dependencies []model.Dependency,
	_ map[string]objectives.Objective, // reserved for future use
	_ *repoFacts,
	onResult func(),
) ([]model.Connection, []model.UnresolvedItem, error, string) {
	if len(exposures) == 0 || len(dependencies) == 0 {
		return nil, nil, nil, ""
	}

	// ── AST path (preferred) ─────────────────────────────────────────────
	if o.astIndex != nil && (len(o.astIndex.Symbols) > 0 || len(o.astIndex.Frameworks) > 0) {
		conns, unresolved := runASTConnections(ctx, o.astIndex, exposures, dependencies,
			o.cfg.Quality.MinConfidence, o.cfg.Runtime.Workers, onResult)
		exposuresWithoutPaths := 0
		for _, exp := range exposures {
			found := false
			for _, c := range conns {
				if c.FromExposureID == exp.ID {
					found = true
					break
				}
			}
			if !found {
				exposuresWithoutPaths++
			}
		}
		// Only fall through to shallow matcher when the AST walk produced
		// absolutely nothing — empty index or fully unsupported language.
		if len(conns) > 0 || len(unresolved) > 0 {
			o.emitConnectionsAggregate(len(exposures), len(conns), exposuresWithoutPaths, "ast")
			sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })
			return conns, unresolved, nil, ""
		}
		util.Warn("agents.connections", "ast walk produced no connections; falling back to shallow matcher", nil)
	}

	// ── Shallow name-based fallback ──────────────────────────────────────
	util.Warn("agents.connections", "no ast index available; using shallow name matcher", nil)
	conns, unresolved := buildShallowConnections(exposures, dependencies, o.cfg.Quality.MinConfidence)
	o.emitConnectionsAggregate(len(exposures), len(conns), 0, "no_index")
	if onResult != nil {
		for range exposures {
			onResult()
		}
	}
	return conns, unresolved, nil, ""
}

// dedupeLocations removes duplicate file:line entries from the slice
// while preserving the first occurrence's order.
func dedupeLocations(in []model.Location) []model.Location {
	seen := map[string]struct{}{}
	out := []model.Location{}
	for _, l := range in {
		key := fmt.Sprintf("%s:%d-%d", l.File, l.StartLine, l.EndLine)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, l)
	}
	return out
}

// lastIdent returns the last "name" component of a SCIP symbol string.
// SCIP symbols have shape "scheme pkg pkg-name version desc desc ..."
// where each desc ends in a suffix character (#, ., /, etc.). We walk
// backwards to the last such suffix and return whatever follows.
func lastIdent(sym string) string {
	if sym == "" {
		return ""
	}
	// Drop trailing dot (method suffix).
	end := len(sym)
	if sym[end-1] == '.' || sym[end-1] == ')' {
		// "Foo#m(+1)." → trim leading scheme/pkg and disambiguator.
	}
	// Cheap approach: split by "/" and take the last segment, then
	// strip suffix chars and parens.
	last := sym
	if i := strings.LastIndex(sym, "/"); i >= 0 {
		last = sym[i+1:]
	}
	// strip trailing "(...)." or "#" or "."
	if i := strings.Index(last, "("); i > 0 {
		last = last[:i]
	}
	last = strings.TrimRight(last, ".#/")
	// strip leading "Foo#" path: keep just the final identifier.
	if i := strings.LastIndexAny(last, "#./"); i >= 0 {
		last = last[i+1:]
	}
	if last == "" {
		return sym
	}
	return last
}

// formatConnectionSummary is the top-level summary line shown for the
// connection on the dashboard.
func formatConnectionSummary(exp model.Exposure, dep model.Dependency, pathCount int) string {
	if pathCount == 1 {
		return fmt.Sprintf("%s → %s (1 path)", exp.Name, dep.Name)
	}
	return fmt.Sprintf("%s → %s (%d paths)", exp.Name, dep.Name, pathCount)
}

// buildShallowConnections is the name-based fallback. It matches each
// exposure to dependencies by string overlap of details + name. The
// produced connections have empty Paths and zero confidence above the
// floor — they're better than nothing but should be clearly marked
// "low fidelity" in any UI.
//
// Strategy:
//   - exposure.details may include `repository`, `service_method`,
//     `dependencies[].name` strings. We extract them and match against
//     each dependency's Name / class / repository fields.
//   - exposure.details.db_operations[].{repository,method} → match
//     against repo-method dependencies of the same name.
func buildShallowConnections(
	exposures []model.Exposure, deps []model.Dependency, minConfidence float64,
) ([]model.Connection, []model.UnresolvedItem) {
	depIndex := indexDependenciesByKey(deps)
	conns := []model.Connection{}
	unresolved := []model.UnresolvedItem{}

	for _, exp := range exposures {
		matched := matchShallow(exp, depIndex)
		if len(matched) == 0 {
			continue
		}
		for _, dep := range matched {
			pathSig := "shallow:" + exp.ID + "->" + dep.ID
			connID := util.StableID(exp.ID, dep.ID, pathSig)
			conns = append(conns, model.Connection{
				ID:             connID,
				FromExposureID: exp.ID,
				ToDependencyID: dep.ID,
				Summary:        formatConnectionSummary(exp, dep, 0),
				Locations:      exp.Locations,
				Evidence: []model.Evidence{
					{
						Location: firstLocation(exp.Locations),
						Snippet:  fmt.Sprintf("name-based match (no SCIP paths) to %s", dep.Name),
						Source:   "shallow",
					},
				},
				Confidence:    minConfidence,
				FromType:      exp.Type,
				ToType:        dep.Type,
				PathSignature: pathSig,
			})
		}
	}
	return conns, unresolved
}

// indexDependenciesByKey builds lookup tables for the shallow matcher.
// Keys: dep name (case-insensitive), repository, table.
type depIndexT struct {
	byName       map[string][]model.Dependency
	byRepository map[string][]model.Dependency
}

func indexDependenciesByKey(deps []model.Dependency) *depIndexT {
	idx := &depIndexT{
		byName:       map[string][]model.Dependency{},
		byRepository: map[string][]model.Dependency{},
	}
	for _, d := range deps {
		nameKey := strings.ToLower(d.Name)
		idx.byName[nameKey] = append(idx.byName[nameKey], d)
		if repo, ok := d.Details["repository"].(string); ok && repo != "" {
			idx.byRepository[strings.ToLower(repo)] = append(idx.byRepository[strings.ToLower(repo)], d)
		}
		if cls, ok := d.Details["class"].(string); ok && cls != "" {
			idx.byRepository[strings.ToLower(cls)] = append(idx.byRepository[strings.ToLower(cls)], d)
		}
	}
	return idx
}

func matchShallow(exp model.Exposure, idx *depIndexT) []model.Dependency {
	matched := map[string]model.Dependency{}
	add := func(deps []model.Dependency) {
		for _, d := range deps {
			matched[d.ID] = d
		}
	}

	// 1. db_operations: each entry has a repository + method. Look up
	// dependencies whose repository / class matches.
	if ops, ok := exp.Details["db_operations"].([]any); ok {
		for _, raw := range ops {
			op, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if repo, ok := op["repository"].(string); ok && repo != "" {
				add(idx.byRepository[strings.ToLower(repo)])
			}
			if method, ok := op["method"].(string); ok && method != "" {
				// Strip "(...)" tail.
				if p := strings.Index(method, "("); p > 0 {
					method = method[:p]
				}
				add(idx.byName[strings.ToLower(method)])
			}
		}
	}
	// 2. dependencies[]: named services / repos the exposure already
	// admitted to using (some detail prompts list these explicitly).
	if depList, ok := exp.Details["dependencies"].([]any); ok {
		for _, raw := range depList {
			d, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := d["name"].(string); ok && name != "" {
				add(idx.byName[strings.ToLower(name)])
				add(idx.byRepository[strings.ToLower(name)])
			}
		}
	}
	if len(matched) == 0 {
		return nil
	}
	out := make([]model.Dependency, 0, len(matched))
	for _, d := range matched {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// firstLocation returns the first Location from a slice or the zero
// value when empty. Pulled out for readability at the call site.
func firstLocation(in []model.Location) model.Location {
	if len(in) == 0 {
		return model.Location{}
	}
	return in[0]
}

// emitConnectionsAggregate publishes a single summary event so the
// dashboard can show "X connections across Y exposures (Z without
// paths)" without subscribing to every per-exposure event. We emit
// this at the END of the stage; per-exposure events are kept off the
// bus because the stage now completes in milliseconds.
func (o *orchestrator) emitConnectionsAggregate(
	exposures, connections, exposuresWithoutPaths int, source string,
) {
	o.emit(events.Event{
		Kind: events.KindLog, Stage: "connections", JobID: "connections.summary",
		Message: fmt.Sprintf("%d connections across %d exposures (%d with no paths)",
			connections, exposures, exposuresWithoutPaths),
		Payload: map[string]any{
			"connections":             connections,
			"exposures":               exposures,
			"exposures_without_paths": exposuresWithoutPaths,
			"source":                  source,
		},
	})
}

// ─── Tree-sitter connection engine ────────────────────────────────────────────

// runASTConnections finds all connections between exposures and dependencies
// using the tree-sitter project index. It replaces the SCIP-based connection
// mapping entirely.
//
// Algorithm:
//  1. Resolve every dependency to a set of qualified symbol names using
//     the AST index (positional lookup from source_locations, then name lookup).
//  2. Resolve every exposure's entry symbol the same way.
//  3. BFS from each entry symbol to any dependency symbol, collecting paths.
//  4. Each path's hops carry per-step condition + repetition derived from the
//     tree-sitter enclosing context (populated at parse time, no LLM).
func runASTConnections(
	ctx context.Context,
	idx *astpkg.ProjectIndex,
	exposures []model.Exposure,
	dependencies []model.Dependency,
	minConfidence float64,
	workerCount int,
	onResult func(),
) ([]model.Connection, []model.UnresolvedItem) {
	if workerCount <= 0 {
		workerCount = 6
	}

	// Build the dependency symbol set.
	depBySymbol := map[string]model.Dependency{} // resolved symbol → dep
	var unresolvedDeps []model.UnresolvedItem

	for _, dep := range dependencies {
		syms := resolveEntitySymbolsAST(idx, dep.Name, dep.Locations)
		if len(syms) == 0 {
			unresolvedDeps = append(unresolvedDeps, model.UnresolvedItem{
				Kind:       model.KindDependency,
				Type:       dep.Type,
				Name:       dep.Name,
				ReasonCode: "ast_unresolved",
				Reason:     fmt.Sprintf("dependency %q not found in AST index", dep.Name),
				Confidence: dep.Confidence,
			})
			continue
		}
		for _, sym := range syms {
			depBySymbol[sym] = dep
			// If the resolved symbol is a class or interface, register its methods
			// as targets too. Constrain the expansion to the dep's source_location
			// range when available — this prevents overly broad matches when the
			// dep identifies a specific range within a large class (e.g.
			// campaign.save/update at lines 11-19 should not expand to ALL methods
			// of CampaignRepository).
			if defs, ok := idx.Symbols[sym]; ok {
				for _, def := range defs {
					if def.Kind == astpkg.SymbolKindClass || def.Kind == astpkg.SymbolKindInterface {
						// Determine the effective method-search range.
						// If the dep has a location, constrain to that range.
						// Otherwise fall back to the full class body.
						rangeStart := def.Range.StartLine
						rangeEnd := def.Range.EndLine
						if len(dep.Locations) > 0 {
							loc := dep.Locations[0]
							if loc.StartLine > 0 {
								// Use the dep location range, converted to 0-based.
								ls := uint32(loc.StartLine - 1)
								le := uint32(loc.EndLine - 1)
								// Only constrain when the range is meaningful (more than a single line).
								if le > ls+2 {
									rangeStart = ls
									rangeEnd = le
								}
							}
						}
						if fa, ok := idx.Files[def.File]; ok {
							for _, msym := range fa.Symbols {
								if (msym.Kind == astpkg.SymbolKindMethod || msym.Kind == astpkg.SymbolKindFunction) &&
									msym.Range.StartLine >= rangeStart &&
									msym.Range.EndLine <= rangeEnd {
									depBySymbol[msym.Qualified] = dep
								}
							}
						}
					}
				}
			}
		}
	}

	// (Per-exposure class sets are computed lazily in the producer goroutine.)

	// Build a single target predicate.
	isTarget := func(sym string) bool {
		_, ok := depBySymbol[sym]
		return ok
	}

	walker := astpkg.NewWalker(idx)
	cfg := astpkg.WalkConfig{
		IsTarget:          isTarget,
		Context:           ctx,
		MaxDepth:          12,
		MaxPathsPerTarget: 8,
		MaxPathsTotal:     4000,
		MaxVisitedEdges:   250000,
	}

	// Per-exposure parallel walk.
	type workItem struct {
		exp        model.Exposure
		entries    []string
		expClasses map[string]struct{} // exposure entry classes for circular-ref filtering
	}
	type result struct {
		conns      []model.Connection
		unresolved []model.UnresolvedItem
	}

	jobCh := make(chan workItem, len(exposures))
	resCh := make(chan result, len(exposures))

	var wg sync.WaitGroup
	if workerCount > len(exposures) {
		workerCount = len(exposures)
	}
	if workerCount < 1 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobCh {
				if ctx.Err() != nil {
					resCh <- result{}
					continue
				}
				conns, unr := buildASTConnectionsForExposure(
					ctx, walker, cfg, item.exp, item.entries, depBySymbol, item.expClasses, minConfidence,
				)
				resCh <- result{conns: conns, unresolved: unr}
			}
		}()
	}

	go func() {
		for _, exp := range exposures {
			entries := resolveEntitySymbolsAST(idx, exp.Name, exp.Locations)
			if len(entries) == 0 {
				// No entry point found by position/name: try framework bindings.
				// Match by name fragment or trigger annotation.
				baseName := exp.Name
				if idx2 := strings.LastIndex(baseName, " - "); idx2 >= 0 {
					baseName = baseName[idx2+3:] // strip suffix like "- SQS Message Handler"
				}
				for _, fb := range idx.Frameworks {
					if fb.Symbol != "" && (strings.Contains(fb.Trigger, exp.Name) ||
						strings.Contains(fb.Trigger, baseName) ||
						strings.Contains(fb.Symbol, baseName)) {
						entries = append(entries, fb.Symbol)
					}
				}
			}
			// Build a per-exposure self-reference filter: only classes that are
			// the entry point of THIS specific exposure. Do not use the global
			// exposureClasses set (which would incorrectly block targets whose
			// class is also an entry point of a DIFFERENT exposure).
			thisExpClasses := perExposureClasses(idx, exp)
			jobCh <- workItem{exp: exp, entries: entries, expClasses: thisExpClasses}
		}
		close(jobCh)
		wg.Wait()
		close(resCh)
	}()

	var allConns []model.Connection
	allUnresolved := append([]model.UnresolvedItem{}, unresolvedDeps...)

	for r := range resCh {
		if onResult != nil {
			onResult()
		}
		allConns = append(allConns, r.conns...)
		allUnresolved = append(allUnresolved, r.unresolved...)
	}

	return allConns, allUnresolved
}

// resolveEntitySymbolsAST looks up callable symbols for an entity using the
// AST index. For exposures it returns the method entry points to walk FROM.
// For dependencies it returns the methods to use as walk TARGETS.
//
// Strategy (in priority order):
//  1. Positional: symbols at the entity's source_locations line/file.
//     Prefers method/function symbols over class symbols.
//     When a class is found, expands to all its methods (the actual callables).
//  2. Name-based: search for symbols whose unqualified name matches.
func resolveEntitySymbolsAST(idx *astpkg.ProjectIndex, name string, locs []model.Location) []string {
	// Positional lookup strategy:
	//   - For each location, collect method and class symbols at that line.
	//   - Methods are preferred over classes.
	//   - Class symbols are expanded to all their methods.
	//   - ALL locations are processed, collecting the union of results.
	//   - When any location yields method symbols, those are included together
	//     with any class expansion from earlier locations.
	//
	// This ensures that:
	//   - An exposure whose first location is the class declaration (line 1)
	//     gets all the class's methods as entry points.
	//   - An exposure whose second location points to an inner method also
	//     includes that method — even when the first location yielded a class.

	var directMethods []string // methods found at exact lines
	var classExpansion []string // class-to-method expansions

	for _, loc := range locs {
		if loc.File == "" || loc.StartLine <= 0 {
			continue
		}
		fa, ok := idx.Files[loc.File]
		if !ok {
			continue
		}
		line := uint32(loc.StartLine - 1) // 1-based → 0-based

		// Collect all symbols that contain this line.
		var methods, classes []astpkg.SymbolDef
		for _, sym := range fa.Symbols {
			if sym.Range.StartLine <= line && sym.Range.EndLine >= line {
				switch sym.Kind {
				case astpkg.SymbolKindMethod, astpkg.SymbolKindFunction, astpkg.SymbolKindConstructor:
					methods = append(methods, sym)
				case astpkg.SymbolKindClass, astpkg.SymbolKindInterface:
					classes = append(classes, sym)
				}
			}
		}

		// Collect direct method hits.
		for _, m := range methods {
			directMethods = append(directMethods, m.Qualified)
		}

		// Expand classes to their methods (only accumulate once per class).
		if len(methods) == 0 && len(classes) > 0 {
			for _, cls := range classes {
				classExpansion = append(classExpansion, cls.Qualified)
				for _, sym := range fa.Symbols {
					if sym.Kind == astpkg.SymbolKindMethod || sym.Kind == astpkg.SymbolKindFunction {
						if sym.Range.StartLine >= cls.Range.StartLine &&
							sym.Range.EndLine <= cls.Range.EndLine {
							classExpansion = append(classExpansion, sym.Qualified)
						}
					}
				}
			}
			continue // don't do widening if class was found
		}

		// Nothing at exact line: widen ±5 leading / +30 trailing lines.
		if len(methods) == 0 && len(classes) == 0 {
			lo := line
			if line > 5 {
				lo = line - 5
			}
			for _, sym := range fa.Symbols {
				if sym.Range.StartLine >= lo && sym.Range.StartLine <= line+30 {
					directMethods = append(directMethods, sym.Qualified)
				}
			}
		}
	}

	// Prefer direct method hits; augment with class expansion.
	// When we have both, union them: the class expansion covers the entry class
	// and the direct methods cover specific handler methods.
	var combined []string
	combined = append(combined, directMethods...)
	// Only include class expansion if we didn't find direct methods in that same
	// file/class — class expansion without any direct methods gives ALL methods
	// of the class as entry points, which is the correct behaviour.
	if len(directMethods) == 0 {
		combined = append(combined, classExpansion...)
	} else {
		// We have direct methods: still include the class expansion because the
		// second location may be in a different service class.
		combined = append(combined, classExpansion...)
	}
	if len(combined) > 0 {
		return uniqueStrings(combined)
	}

	var found []string

	// 2. Name-based lookup.
	// Extract the method/function name from "ClassName.methodName" or bare "methodName".
	searchName := name
	if dot := strings.LastIndexAny(name, ".#/"); dot >= 0 {
		searchName = name[dot+1:]
	}
	if paren := strings.Index(searchName, "("); paren > 0 {
		searchName = searchName[:paren]
	}
	searchName = strings.TrimSpace(searchName)

	for qualified, defs := range idx.Symbols {
		for _, def := range defs {
			if def.Name == searchName || def.Qualified == name {
				found = append(found, qualified)
				break
			}
		}
	}
	return uniqueStrings(found)
}

// perExposureClasses returns the set of class/interface names that serve as the
// primary entry point for exp. Only the FIRST source_location is used to
// identify the exposure's class — additional locations may point to service
// classes that contain dependency methods and must not be blocked.
func perExposureClasses(idx *astpkg.ProjectIndex, exp model.Exposure) map[string]struct{} {
	classes := make(map[string]struct{})
	// Only look at the first location to avoid blocking dependency targets
	// that happen to appear in service classes listed as secondary locations.
	for _, loc := range exp.Locations {
		if loc.File == "" || loc.StartLine <= 0 {
			continue
		}
		fa, ok := idx.Files[loc.File]
		if !ok {
			continue
		}
		line := uint32(loc.StartLine - 1)
		for _, sym := range fa.Symbols {
			if (sym.Kind == astpkg.SymbolKindClass || sym.Kind == astpkg.SymbolKindInterface) &&
				sym.Range.StartLine <= line && sym.Range.EndLine >= line {
				classes[sym.Qualified] = struct{}{}
			}
		}
		// Stop after the first location that maps to a class — only the
		// primary class is excluded from targets.
		if len(classes) > 0 {
			break
		}
	}
	return classes
}

// buildASTConnectionsForExposure walks from each entry symbol and builds
// Connection records for every dependency it reaches.
//
// exposureClasses is the set of class names that are the entry points for this
// specific exposure. Any connection whose target symbol belongs to a class in
// exposureClasses is filtered out as a self-referential (circular) connection.
func buildASTConnectionsForExposure(
	ctx context.Context,
	walker *astpkg.Walker,
	cfg astpkg.WalkConfig,
	exposure model.Exposure,
	entrySymbols []string,
	depBySymbol map[string]model.Dependency,
	exposureClasses map[string]struct{},
	minConfidence float64,
) ([]model.Connection, []model.UnresolvedItem) {
	if len(entrySymbols) == 0 {
		return nil, []model.UnresolvedItem{{
			Kind:       model.KindExposure,
			Type:       exposure.Type,
			Name:       exposure.Name,
			ReasonCode: "ast_unresolved",
			Reason:     fmt.Sprintf("exposure %q not found in AST index", exposure.Name),
		}}
	}

	// Bucket paths by dependency.
	type bucket struct {
		dep   model.Dependency
		paths []astpkg.CallPath
	}
	byDep := map[string]*bucket{}

	// Build a set of "entry class prefixes" from this specific exposure's
	// entry symbols. Any target that starts with one of these prefixes is
	// a method on the same class as the exposure itself — skip those to
	// avoid circular intra-class connections.
	entryPrefixes := make(map[string]struct{}, len(entrySymbols))
	for _, e := range entrySymbols {
		if dot := strings.LastIndex(e, "."); dot > 0 {
			entryPrefixes[e[:dot]] = struct{}{}
		}
	}
	// Also include the broader exposure-class set passed from the caller.
	for cls := range exposureClasses {
		entryPrefixes[cls] = struct{}{}
	}

	for _, entry := range entrySymbols {
		if ctx.Err() != nil {
			return nil, nil
		}
		paths := walker.Walk(entry, cfg)
		for _, p := range paths {
			// Skip self-referential targets: a method on the same class as
			// the exposure is not an external dependency.
			targetClass := p.TargetSymbol
			if dot := strings.LastIndex(targetClass, "."); dot > 0 {
				targetClass = targetClass[:dot]
			}
			if _, selfRef := entryPrefixes[targetClass]; selfRef {
				continue
			}

			dep, ok := depBySymbol[p.TargetSymbol]
			if !ok {
				continue
			}
			b, exists := byDep[dep.ID]
			if !exists {
				b = &bucket{dep: dep}
				byDep[dep.ID] = b
			}
			b.paths = append(b.paths, p)
		}
	}

	conns := make([]model.Connection, 0, len(byDep))
	for _, b := range byDep {
		c := buildASTConnection(exposure, b.dep, b.paths, minConfidence)
		conns = append(conns, c)
	}
	return conns, nil
}

// buildASTConnection assembles a model.Connection from tree-sitter paths.
func buildASTConnection(
	exposure model.Exposure,
	dep model.Dependency,
	paths []astpkg.CallPath,
	minConfidence float64,
) model.Connection {
	mPaths := make([]model.ConnectionPath, 0, len(paths))
	var primaryCond model.Condition
	var locs []model.Location

	for i, p := range paths {
		mp := convertASTPath(p, dep.Type)
		if mp.ID == "" {
			mp.ID = fmt.Sprintf("path-%d", i+1)
		}
		mPaths = append(mPaths, mp)
		if mp.Condition.Kind != "" && primaryCond.Kind == "" {
			primaryCond = mp.Condition
		}
		for _, step := range mp.Steps {
			if step.Location.File != "" {
				locs = append(locs, step.Location)
			}
		}
	}

	if len(locs) == 0 {
		locs = append(locs, exposure.Locations...)
	}
	if len(locs) == 0 {
		locs = append(locs, dep.Locations...)
	}

	pathSig := buildASTPathSignature(paths)
	connID := util.StableID(exposure.ID, dep.ID, pathSig)
	confidence := scoreASTConfidence(paths, minConfidence)

	return model.Connection{
		ID:             connID,
		FromExposureID: exposure.ID,
		ToDependencyID: dep.ID,
		Condition:      primaryCond,
		PathSignature:  pathSig,
		Summary:        fmt.Sprintf("%s → %s", exposure.Name, dep.Name),
		Locations:      dedupeLocations(locs),
		Evidence:       buildASTEvidence(paths),
		Confidence:     confidence,
		FromType:       exposure.Type,
		ToType:         dep.Type,
		Paths:          mPaths,
	}
}

// convertASTPath converts a tree-sitter CallPath to a model.ConnectionPath.
func convertASTPath(p astpkg.CallPath, depType string) model.ConnectionPath {
	steps := make([]model.ConnectionPathStep, 0, len(p.Steps))
	var pathCond model.Condition

	for _, s := range p.Steps {
		loc := model.Location{
			File:      s.File,
			StartLine: int(s.Range.StartLine) + 1, // convert 0-based to 1-based
			EndLine:   int(s.Range.EndLine) + 1,
		}

		// Convert per-step condition from tree-sitter.
		stepCond := model.Condition{
			Kind:        s.Condition.Kind,
			Expression:  s.Condition.Expression,
			Explanation: s.Condition.Explanation,
		}
		// If this step has a meaningful condition and the path has none, promote it.
		if stepCond.Kind != "" && stepCond.Kind != "unconditional" && pathCond.Kind == "" {
			pathCond = stepCond
		}

		// Convert per-step repetition — encode in condition when loop.
		if s.Repetition.Kind == "loop" {
			if stepCond.Kind == "" || stepCond.Kind == "unconditional" {
				stepCond = model.Condition{
					Kind:        "loop",
					Expression:  s.Repetition.IteratesOver,
					Explanation: "Call is inside a loop; executed once per element",
				}
				if pathCond.Kind == "" {
					pathCond = stepCond
				}
			}
		}

		// Build args summary.
		argsSummary := ""
		if len(s.Arguments) > 0 {
			parts := make([]string, 0, len(s.Arguments))
			for _, a := range s.Arguments {
				parts = append(parts, a.Source)
			}
			argsSummary = strings.Join(parts, ", ")
		}

		steps = append(steps, model.ConnectionPathStep{
			Order:     s.Order,
			Action:    "invoke",
			Operation: depType,
			From:      s.Caller,
			To:        s.Callee,
			Condition: stepCond,
			Location:  loc,
		})
		_ = argsSummary // will be exposed in graph export endpoint
	}

	// Roll up path condition.
	if pathCond.Kind == "" {
		pathCond = model.Condition{Kind: "unconditional"}
	}

	return model.ConnectionPath{
		ID:        buildASTPathID(p),
		Summary:   buildASTPathSummary(p),
		Condition: pathCond,
		Steps:     steps,
	}
}

func buildASTPathID(p astpkg.CallPath) string {
	parts := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		parts = append(parts, s.Callee)
	}
	return util.StableID(strings.Join(parts, "->"))
}

func buildASTPathSummary(p astpkg.CallPath) string {
	if len(p.Steps) == 0 {
		return ""
	}
	first := lastIdent(p.Steps[0].Caller)
	last := lastIdent(p.Steps[len(p.Steps)-1].Callee)
	return fmt.Sprintf("%s → %s (%d hops)", first, last, len(p.Steps))
}

func buildASTPathSignature(paths []astpkg.CallPath) string {
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		parts = append(parts, buildASTPathID(p))
	}
	sort.Strings(parts)
	return strings.Join(parts, "||")
}

func buildASTEvidence(paths []astpkg.CallPath) []model.Evidence {
	seen := map[string]struct{}{}
	var out []model.Evidence
	for _, p := range paths {
		for _, s := range p.Steps {
			key := s.File + ":" + fmt.Sprint(s.Range.StartLine)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model.Evidence{
				Location: model.Location{
					File:      s.File,
					StartLine: int(s.Range.StartLine) + 1,
					EndLine:   int(s.Range.EndLine) + 1,
				},
				Source: "ast",
			})
			if len(out) >= 5 {
				return out
			}
		}
	}
	return out
}

func scoreASTConfidence(paths []astpkg.CallPath, minConfidence float64) float64 {
	if len(paths) == 0 {
		return minConfidence
	}
	// Shorter paths = higher confidence.
	minHops := len(paths[0].Steps)
	for _, p := range paths {
		if len(p.Steps) < minHops {
			minHops = len(p.Steps)
		}
	}
	score := 0.98 - float64(minHops-1)*0.04
	if score < 0.5 {
		score = 0.5
	}
	if score < minConfidence {
		score = minConfidence
	}
	return score
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// _ keeps imports referenced even when not all helpers are wired into
// the public surface yet.
var _ = time.Second
