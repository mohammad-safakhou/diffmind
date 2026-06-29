package connections

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/languages/framework"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

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

// Tree-sitter connection engine

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
//     tree-sitter enclosing context (populated at parse time).
//
// buildDependencySymbolIndex resolves every dependency to its AST symbol(s) and
// returns the symbol→deps target map plus the deps that could not be resolved.
// When a resolved symbol is a class/interface, its methods are registered as
// targets too — constrained to the dep's source_location range when meaningful
// so a dep pinned to a few lines doesn't expand to the whole class.
func buildDependencySymbolIndex(idx *astpkg.ProjectIndex, dependencies []model.Dependency) (map[string][]model.Dependency, []model.UnresolvedItem) {
	depBySymbol := map[string][]model.Dependency{} // resolved symbol → candidate deps
	var unresolvedDeps []model.UnresolvedItem
	for _, dep := range dependencies {
		syms := resolveEntitySymbolsAST(idx, dep.Name, dependencyTargetLocations(dep))
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
			depBySymbol[sym] = appendDependencyTarget(depBySymbol[sym], dep)
			defs, ok := idx.Symbols[sym]
			if !ok {
				continue
			}
			for _, def := range defs {
				if def.Kind != astpkg.SymbolKindClass && def.Kind != astpkg.SymbolKindInterface {
					continue
				}
				// Effective method-search range: the dep's location range
				// when meaningful (>2 lines), else the full class body.
				rangeStart := def.Range.StartLine
				rangeEnd := def.Range.EndLine
				if len(dep.Locations) > 0 {
					loc := dep.Locations[0]
					if loc.StartLine > 0 {
						ls := uint32(loc.StartLine - 1)
						le := uint32(loc.EndLine - 1)
						if le > ls+2 {
							rangeStart = ls
							rangeEnd = le
						}
					}
				}
				fa, ok := idx.Files[def.File]
				if !ok {
					continue
				}
				for _, msym := range fa.Symbols {
					if (msym.Kind == astpkg.SymbolKindMethod || msym.Kind == astpkg.SymbolKindFunction) &&
						msym.Range.StartLine >= rangeStart &&
						msym.Range.EndLine <= rangeEnd {
						depBySymbol[msym.Qualified] = appendDependencyTarget(depBySymbol[msym.Qualified], dep)
					}
				}
			}
		}
	}
	return depBySymbol, unresolvedDeps
}

// resolveExposureEntries resolves an exposure's entry symbol(s) by position/
// name, falling back to framework bindings (matched by name fragment or trigger
// annotation) when the AST lookup finds nothing.
func resolveExposureEntries(idx *astpkg.ProjectIndex, exp model.Exposure) []string {
	entries := resolveExposureEntrySymbolsAST(idx, exp)
	if len(entries) > 0 {
		return entries
	}
	baseName := exp.Name
	if i := strings.LastIndex(baseName, " - "); i >= 0 {
		baseName = baseName[i+3:] // strip a suffix like "- SQS Message Handler"
	}
	for _, fb := range idx.Frameworks {
		if fb.Symbol != "" && (strings.Contains(fb.Trigger, exp.Name) ||
			strings.Contains(fb.Trigger, baseName) ||
			strings.Contains(fb.Symbol, baseName)) {
			entries = append(entries, fb.Symbol)
		}
	}
	return entries
}

func runASTConnections(
	ctx context.Context,
	idx *astpkg.ProjectIndex,
	exposures []model.Exposure,
	dependencies []model.Dependency,
	minConfidence float64,
	workerCount int,
) ([]model.Connection, []model.UnresolvedItem) {
	if workerCount <= 0 {
		workerCount = 6
	}

	depBySymbol, unresolvedDeps := buildDependencySymbolIndex(idx, dependencies)

	// (Per-exposure class sets are computed lazily in the producer goroutine.)

	// Build a single target predicate.
	isTarget := func(sym string) bool {
		return len(depBySymbol[sym]) > 0
	}

	walker := astpkg.NewWalker(idx)
	cfg := astpkg.WalkConfig{
		IsTarget:          isTarget,
		Context:           ctx,
		MaxDepth:          32,
		MaxPathsPerTarget: 24,
		MaxPathsTotal:     1000000,
		MaxVisitedEdges:   50000000,
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
			entries := resolveExposureEntries(idx, exp)
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
		allConns = append(allConns, r.conns...)
		allUnresolved = append(allUnresolved, r.unresolved...)
	}

	return allConns, allUnresolved
}

func AugmentDependencies(idx *astpkg.ProjectIndex, exposures []model.Exposure, dependencies []model.Dependency, minConfidence float64) []model.Dependency {
	if idx == nil || len(exposures) == 0 {
		return dependencies
	}
	known := map[string]struct{}{}
	for _, dep := range dependencies {
		if key := dependencyNameKey(dep.Name); key != "" {
			known[key] = struct{}{}
		}
	}
	dbCtx := databaseContextFromDependencies(dependencies)

	walker := astpkg.NewWalker(idx)
	cfg := astpkg.WalkConfig{
		IsTarget: func(sym string) bool {
			return isRepositoryOperationSymbol(sym) && !isLowSignalTargetSymbol(sym)
		},
		MaxDepth:          12,
		MaxPathsPerTarget: 1,
		MaxPathsTotal:     1000,
		MaxVisitedEdges:   100000,
	}
	out := append([]model.Dependency(nil), dependencies...)
	for _, exp := range exposures {
		for _, entry := range resolveExposureEntrySymbolsAST(idx, exp) {
			for _, p := range walker.Walk(entry, cfg) {
				if !isProductionCallPath(p) || len(p.Steps) == 0 {
					continue
				}
				name := normalizeRepositoryOperationName(p.TargetSymbol)
				if name == "" {
					continue
				}
				// Drop junk tables (entity_manager, *_id_seq) on the
				// deterministic path — a wrong table poisons the output.
				if owner, _, ok := splitOwnerMethod(name); ok {
					if _, table := tableEntityFromRepository(owner); isJunkTableName(table) {
						continue
					}
				}
				if _, exists := known[dependencyNameKey(name)]; exists {
					continue
				}
				dep := buildASTDerivedDBDependency(idx, name, p, minConfidence, dbCtx)
				known[dependencyNameKey(dep.Name)] = struct{}{}
				out = append(out, dep)
			}
		}
	}
	return out
}

func resolveExposureEntrySymbolsAST(idx *astpkg.ProjectIndex, exp model.Exposure) []string {
	if idx == nil {
		return nil
	}
	for _, key := range []string{"handler", "entry_method", "service_method"} {
		if raw, ok := exp.Details[key].(string); ok && raw != "" {
			if syms := resolveExactEntitySymbolAST(idx, raw); len(syms) > 0 {
				return syms
			}
		}
	}
	if entrypoint, ok := exp.Details["entrypoint"].(map[string]any); ok {
		if raw, ok := entrypoint["method"].(string); ok && raw != "" {
			if syms := resolveExactEntitySymbolAST(idx, raw); len(syms) > 0 {
				return syms
			}
		}
	}

	for _, loc := range exp.Locations {
		if loc.File == "" || isTestLikeArtifactPath(loc.File) || isConfigLikeArtifactPath(loc.File) || isLowSignalArtifactPath(loc.File) {
			continue
		}
		return resolveEntitySymbolsAST(idx, exp.Name, []model.Location{loc})
	}
	return resolveEntitySymbolsAST(idx, exp.Name, nil)
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
	if looksMethodLikeName(name) {
		if exact := resolveExactEntitySymbolAST(idx, name); len(exact) > 0 {
			return exact
		}
	}
	directMethods, classExpansion := positionalSymbolLookup(idx, locs)

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

	return nameBasedSymbolLookup(idx, name)
}

// positionalSymbolLookup collects callable symbols at an entity's
// source_locations. For each location it gathers method/class symbols spanning
// that line: methods are returned directly; a class with no method at the line
// is expanded to all its methods; a line with nothing is widened ±5 leading /
// +30 trailing lines. All locations are unioned, so a first location on a class
// declaration and a second on an inner method both contribute.
func positionalSymbolLookup(idx *astpkg.ProjectIndex, locs []model.Location) (directMethods, classExpansion []string) {
	for _, loc := range locs {
		if loc.File == "" || loc.StartLine <= 0 {
			continue
		}
		fa, ok := idx.Files[loc.File]
		if !ok {
			continue
		}
		line := uint32(loc.StartLine - 1) // 1-based → 0-based

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

		for _, m := range methods {
			directMethods = append(directMethods, m.Qualified)
		}

		// Expand classes to their methods (only when no method hit the line).
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
			continue // don't widen if a class was found
		}

		// Nothing at the exact line: widen ±5 leading / +30 trailing lines.
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
	return directMethods, classExpansion
}

// nameBasedSymbolLookup is the fallback when positional lookup finds nothing:
// it matches symbols whose unqualified name equals the entity's method name
// (extracted from "Class.method"/"Class#method") or whose qualified name equals
// the full entity name.
func nameBasedSymbolLookup(idx *astpkg.ProjectIndex, name string) []string {
	searchName := name
	if dot := strings.LastIndexAny(name, ".#/"); dot >= 0 {
		searchName = name[dot+1:]
	}
	if paren := strings.Index(searchName, "("); paren > 0 {
		searchName = searchName[:paren]
	}
	searchName = strings.TrimSpace(searchName)

	var found []string
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

func resolveExactEntitySymbolAST(idx *astpkg.ProjectIndex, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" || idx == nil {
		return nil
	}
	if _, ok := idx.Symbols[name]; ok {
		return []string{name}
	}
	needle := name
	if paren := strings.Index(needle, "("); paren > 0 {
		needle = needle[:paren]
	}
	if synthetic := syntheticTypedMethodSymbol(idx, needle); synthetic != "" {
		return []string{synthetic}
	}
	var out []string
	for qualified := range idx.Symbols {
		if qualified == needle || strings.HasSuffix(qualified, "."+needle) || strings.HasSuffix(needle, "."+lastIdent(qualified)) {
			if lastIdent(qualified) == lastIdent(needle) || strings.Contains(needle, ".") {
				out = append(out, qualified)
			}
		}
	}
	sort.Strings(out)
	return uniqueStrings(out)
}

func syntheticTypedMethodSymbol(idx *astpkg.ProjectIndex, name string) string {
	owner, method, ok := splitOwnerMethod(name)
	if !ok || owner == "" || method == "" {
		return ""
	}
	for qualified, defs := range idx.Symbols {
		for _, def := range defs {
			if def.Kind != astpkg.SymbolKindClass && def.Kind != astpkg.SymbolKindInterface {
				continue
			}
			if qualified == owner || strings.HasSuffix(qualified, "."+owner) || lastIdent(qualified) == owner {
				return def.Qualified + "." + method
			}
		}
	}
	return ""
}

func splitOwnerMethod(name string) (string, string, bool) {
	name = strings.TrimSpace(name)
	if dot := strings.LastIndex(name, "."); dot > 0 && dot+1 < len(name) {
		return name[:dot], name[dot+1:], true
	}
	if hash := strings.LastIndex(name, "#"); hash > 0 && hash+1 < len(name) {
		return name[:hash], name[hash+1:], true
	}
	return "", "", false
}

func looksMethodLikeName(name string) bool {
	return strings.ContainsAny(name, ".#/") || strings.Contains(name, "(")
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
	depBySymbol map[string][]model.Dependency,
	exposureClasses map[string]struct{},
	minConfidence float64,
) ([]model.Connection, []model.UnresolvedItem) {
	if len(entrySymbols) == 0 {
		if skipASTExposureResolution(exposure) {
			return nil, nil
		}
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

	entryPrefixes := buildEntryPrefixes(entrySymbols, exposureClasses)

	// Direct containment: the dependency's own call site sits INSIDE an entry
	// method — a listener/cron/route handler doing the work itself. The BFS
	// only reports targets reached via call edges, so a zero-hop dependency is
	// otherwise invisible (a major cause of dangling exposures, A1). Gated on
	// call-site-narrow dependency locations (directContainment), so an entity
	// labeled with a whole class — e.g. the exposure itself re-reported as a
	// dependency — never connects to itself.
	for _, entry := range entrySymbols {
		for _, dep := range chooseDepsForSymbol(entry, depBySymbol[entry]) {
			if !directContainment(walker.Index(), entry, dep) {
				continue
			}
			b, exists := byDep[dep.ID]
			if !exists {
				b = &bucket{dep: dep}
				byDep[dep.ID] = b
			}
			b.paths = append(b.paths, astpkg.CallPath{EntrySymbol: entry, TargetSymbol: entry})
		}
	}

	walkTruncated := false
	for _, entry := range entrySymbols {
		if ctx.Err() != nil {
			return nil, nil
		}
		paths, truncated := walker.WalkVerbose(entry, cfg)
		if truncated {
			walkTruncated = true
		}
		for _, p := range paths {
			if !isProductionCallPath(p) || isLowSignalTargetSymbol(p.TargetSymbol) {
				continue
			}
			// Skip self-referential targets: a method on the same class as
			// the exposure is not an external dependency.
			targetClass := p.TargetSymbol
			if dot := strings.LastIndex(targetClass, "."); dot > 0 {
				targetClass = targetClass[:dot]
			}
			if _, selfRef := entryPrefixes[targetClass]; selfRef {
				continue
			}

			deps := chooseDepsForSymbol(p.TargetSymbol, depBySymbol[p.TargetSymbol])
			if len(deps) == 0 {
				continue
			}
			for _, dep := range deps {
				b, exists := byDep[dep.ID]
				if !exists {
					b = &bucket{dep: dep}
					byDep[dep.ID] = b
				}
				b.paths = append(b.paths, p)
			}
		}
	}

	conns := make([]model.Connection, 0, len(byDep))
	for _, b := range byDep {
		c := buildASTConnection(exposure, b.dep, b.paths, minConfidence)
		conns = append(conns, c)
	}
	// A2: if the walk hit a cap, some exposure->dependency paths may be missing.
	// Surface it through the unresolved channel so "fewer connections" is not
	// silently indistinguishable from "no more connections exist".
	var unresolved []model.UnresolvedItem
	if walkTruncated {
		unresolved = append(unresolved, model.UnresolvedItem{
			Kind:       model.KindExposure,
			Type:       exposure.Type,
			Name:       exposure.Name,
			ReasonCode: "connection_walk_truncated",
			Reason:     fmt.Sprintf("connection walk for %q hit a depth/path cap; some connections may be missing", exposure.Name),
		})
	}
	return conns, unresolved
}

func skipASTExposureResolution(exposure model.Exposure) bool {
	if exposure.Type != "queue_consumer" && exposure.Type != "scheduled_job" {
		return false
	}
	lower := strings.ToLower(strings.Join(append([]string{
		exposure.Platform,
		detailString(exposure.Details, "platform"),
		detailString(exposure.Details, "discovered_by"),
		detailString(exposure.Details, "event_source_type"),
	}, exposure.Tags...), " "))
	switch {
	case strings.Contains(lower, "aws_sam_event_source"):
		return true
	case strings.Contains(lower, "dynamodb_stream"):
		return true
	case strings.Contains(lower, "aws-sam"):
		return true
	case strings.Contains(lower, "kubernetes") || strings.Contains(lower, "helm"):
		return true
	default:
		return false
	}
}

// directContainment reports whether one of the dependency's pinned locations is
// a call-site-narrow range (≤3 lines) inside the entry method's own body. Wide
// (class-level) locations and other files never qualify — only a located call
// site is proof the entry itself performs the dependency.
func directContainment(idx *astpkg.ProjectIndex, entry string, dep model.Dependency) bool {
	if idx == nil {
		return false
	}
	for _, def := range idx.Symbols[entry] {
		switch def.Kind {
		case astpkg.SymbolKindMethod, astpkg.SymbolKindFunction, astpkg.SymbolKindConstructor:
		default:
			continue
		}
		for _, loc := range dep.Locations {
			if loc.File != def.File || loc.StartLine <= 0 || loc.EndLine-loc.StartLine > 2 {
				continue
			}
			ls, le := uint32(loc.StartLine-1), uint32(loc.EndLine-1)
			if def.Range.StartLine <= ls && def.Range.EndLine >= le {
				return true
			}
		}
	}
	return false
}

// buildEntryPrefixes returns the set of "entry class prefixes" for an exposure:
// the class of each entry symbol plus the broader exposure-class set from the
// caller. A walk target on one of these classes is a method on the exposure's
// own class, so it is skipped to avoid circular intra-class connections.
func buildEntryPrefixes(entrySymbols []string, exposureClasses map[string]struct{}) map[string]struct{} {
	entryPrefixes := make(map[string]struct{}, len(entrySymbols))
	for _, e := range entrySymbols {
		if dot := strings.LastIndex(e, "."); dot > 0 {
			entryPrefixes[e[:dot]] = struct{}{}
		}
	}
	for cls := range exposureClasses {
		entryPrefixes[cls] = struct{}{}
	}
	return entryPrefixes
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
