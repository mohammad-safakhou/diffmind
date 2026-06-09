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
									depBySymbol[msym.Qualified] = appendDependencyTarget(depBySymbol[msym.Qualified], dep)
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
		return len(depBySymbol[sym]) > 0
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
			entries := resolveExposureEntrySymbolsAST(idx, exp)
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

func augmentDependenciesFromAST(idx *astpkg.ProjectIndex, exposures []model.Exposure, dependencies []model.Dependency, minConfidence float64) []model.Dependency {
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

type databaseContext struct {
	Platform     string
	Instance     string
	DatabaseName string
}

func buildASTDerivedDBDependency(idx *astpkg.ProjectIndex, name string, path astpkg.CallPath, minConfidence float64, dbCtx databaseContext) model.Dependency {
	last := path.Steps[len(path.Steps)-1]
	locs := []model.Location{{
		File:      last.File,
		StartLine: int(last.Range.StartLine) + 1,
		EndLine:   int(last.Range.EndLine) + 1,
	}}
	if owner, _, ok := splitOwnerMethod(name); ok {
		if defLoc := typeDefinitionLocation(idx, owner); defLoc.File != "" {
			locs = append(locs, defLoc)
		}
	}
	operationKind := inferDBOperationKindAST(idx, name)
	conf := 0.97
	if conf < minConfidence {
		conf = minConfidence
	}
	owner, method, _ := splitOwnerMethod(name)
	entity, table := tableEntityFromRepository(owner)
	platform := firstNonEmpty(dbCtx.Platform, "database")
	instance := firstNonEmpty(dbCtx.Instance, dbCtx.DatabaseName, platform)
	base := model.BaseEntity{
		ID:         util.StableID("dependency", "db_operation", name, locs[0].File, fmt.Sprintf("%d:%d", locs[0].StartLine, locs[0].EndLine)),
		Type:       "db_operation",
		Name:       name,
		Summary:    fmt.Sprintf("AST-derived database operation %s on %s", name, firstNonEmpty(table, entity, owner)),
		Locations:  locs,
		Confidence: conf,
		Evidence: []model.Evidence{{
			Location: locs[0],
			Snippet:  fmt.Sprintf("repository call %s", name),
			Source:   "ast",
		}},
		Details: map[string]any{
			"platform":           platform,
			"database_type":      platform,
			"instance":           instance,
			"database_name":      firstNonEmpty(dbCtx.DatabaseName, instance),
			"operation_kind":     operationKind,
			"operation_type":     operationKind,
			"operation":          method,
			"repository_class":   owner,
			"repository_method":  method,
			"entity":             entity,
			"table":              table,
			"table_or_entity":    firstNonEmpty(table, entity),
			"discovered_by":      "ast_repository_call",
			"source_call_symbol": path.TargetSymbol,
		},
		PluginSource: "ast",
	}
	enrichEntityGrouping(&base)
	return model.Dependency{BaseEntity: base}
}

func databaseContextFromDependencies(dependencies []model.Dependency) databaseContext {
	type candidate struct {
		platform, instance, databaseName string
		count                            int
	}
	byKey := map[string]*candidate{}
	for _, dep := range dependencies {
		if dep.Type != "db_operation" && dep.Type != "cache_operation" {
			continue
		}
		platform := firstNonEmpty(dep.Platform, detailString(dep.Details, "platform"), detailString(dep.Details, "database_type"), detailString(dep.Details, "database"))
		instance := firstNonEmpty(dep.Instance, detailString(dep.Details, "instance"), detailString(dep.Details, "database_name"), detailString(dep.Details, "database"))
		dbName := firstNonEmpty(detailString(dep.Details, "database_name"), databaseNameFromDetails(dep.Details))
		if platform == "unknown" || platform == "" {
			platform = dbPlatform(dep.Name+" "+dep.Summary, fmt.Sprint(dep.Details))
		}
		if instance == "unknown" || instance == "" || instance == platform {
			instance = firstNonEmpty(dbName, instance, platform)
		}
		key := strings.ToLower(platform + "|" + instance + "|" + dbName)
		if byKey[key] == nil {
			byKey[key] = &candidate{platform: platform, instance: instance, databaseName: dbName}
		}
		byKey[key].count++
	}
	var best *candidate
	for _, c := range byKey {
		if best == nil || c.count > best.count || c.count == best.count && c.databaseName != "" && best.databaseName == "" {
			best = c
		}
	}
	if best == nil {
		return databaseContext{Platform: "database", Instance: "database"}
	}
	return databaseContext{Platform: best.platform, Instance: best.instance, DatabaseName: best.databaseName}
}

func detailString(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	v, ok := details[key]
	if !ok || v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		return ""
	}
	return s
}

func databaseNameFromDetails(details map[string]any) string {
	if details == nil {
		return ""
	}
	for _, key := range []string{"datasource_config", "connection_source", "connection_string", "datasource", "jdbc_url", "url"} {
		if name := extractDatabaseNameFromText(detailString(details, key)); name != "" {
			return name
		}
	}
	return ""
}

func extractDatabaseNameFromText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || text == "<nil>" {
		return ""
	}
	if idx := strings.Index(text, "DATABASE_NAME:"); idx >= 0 {
		rest := text[idx+len("DATABASE_NAME:"):]
		end := strings.IndexAny(rest, "}/?& )`\n\t")
		if end >= 0 {
			rest = rest[:end]
		}
		return strings.Trim(rest, "${}:,.;'")
	}
	if idx := strings.Index(text, "jdbc:postgresql://"); idx >= 0 {
		rest := text[idx+len("jdbc:postgresql://"):]
		if slash := strings.Index(rest, "/"); slash >= 0 && slash+1 < len(rest) {
			rest = rest[slash+1:]
			end := strings.IndexAny(rest, "?& )`\n\t")
			if end >= 0 {
				rest = rest[:end]
			}
			return strings.Trim(rest, "${}:,.;'")
		}
	}
	return ""
}

func tableEntityFromRepository(owner string) (string, string) {
	owner = strings.TrimSpace(lastIdent(owner))
	if owner == "" {
		return "", ""
	}
	entity := strings.TrimSuffix(owner, "Repository")
	entity = strings.TrimSuffix(entity, "Dao")
	entity = strings.TrimSuffix(entity, "DAO")
	if entity == owner {
		entity = owner
	}
	return entity, camelToSnake(entity)
}

func camelToSnake(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := rune(s[i-1])
			if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
				b.WriteByte('_')
			}
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func typeDefinitionLocation(idx *astpkg.ProjectIndex, owner string) model.Location {
	if idx == nil || owner == "" {
		return model.Location{}
	}
	for qualified, defs := range idx.Symbols {
		if qualified != owner && !strings.HasSuffix(qualified, "."+owner) && lastIdent(qualified) != owner {
			continue
		}
		for _, def := range defs {
			if def.Kind == astpkg.SymbolKindClass || def.Kind == astpkg.SymbolKindInterface {
				return model.Location{File: def.File, StartLine: int(def.Range.StartLine) + 1, EndLine: int(def.Range.EndLine) + 1}
			}
		}
	}
	return model.Location{}
}

func isRepositoryOperationSymbol(sym string) bool {
	owner, method, ok := splitOwnerMethod(normalizeRepositoryOperationName(sym))
	if !ok || owner == "" || method == "" {
		return false
	}
	if isLowSignalRepositoryOwner(owner) {
		return false
	}
	lowerOwner := strings.ToLower(owner)
	if !(strings.HasSuffix(lowerOwner, "repository") || strings.HasSuffix(lowerOwner, "dao") || strings.HasSuffix(lowerOwner, "entitymanager")) {
		return false
	}
	return !isLowSignalTargetSymbol(sym)
}

func normalizeRepositoryOperationName(sym string) string {
	sym = strings.TrimSpace(sym)
	if sym == "" {
		return ""
	}
	owner, method, ok := splitOwnerMethod(sym)
	if !ok {
		return ""
	}
	owner = lastIdent(owner)
	method = lastIdent(method)
	if owner == "" || method == "" {
		return ""
	}
	return owner + "." + method
}

func dependencyNameKey(name string) string {
	name = strings.TrimSpace(name)
	if paren := strings.Index(name, "("); paren > 0 {
		name = name[:paren]
	}
	return strings.ToLower(normalizeRepositoryOperationName(name))
}

func inferDBOperationKind(name string) string {
	_, method, ok := splitOwnerMethod(name)
	if !ok {
		method = name
	}
	lower := strings.ToLower(method)
	switch {
	case strings.HasPrefix(lower, "save"), strings.HasPrefix(lower, "insert"), strings.HasPrefix(lower, "update"), strings.HasPrefix(lower, "upsert"), strings.HasPrefix(lower, "delete"), strings.HasPrefix(lower, "remove"):
		return "write"
	case strings.HasPrefix(lower, "exists"), strings.HasPrefix(lower, "count"), strings.HasPrefix(lower, "find"), strings.HasPrefix(lower, "get"), strings.HasPrefix(lower, "list"), strings.HasPrefix(lower, "read"), strings.HasPrefix(lower, "query"), strings.HasPrefix(lower, "search"):
		return "read"
	default:
		return "unknown"
	}
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

	var directMethods []string  // methods found at exact lines
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

func dependencyTargetLocations(dep model.Dependency) []model.Location {
	if len(dep.Locations) == 0 {
		return nil
	}
	filtered := make([]model.Location, 0, len(dep.Locations))
	for _, loc := range dep.Locations {
		if loc.File == "" || isTestLikeArtifactPath(loc.File) || isConfigLikeArtifactPath(loc.File) || isLowSignalArtifactPath(loc.File) {
			continue
		}
		if dep.Type == "db_operation" && !looksLikeDatabaseTargetPath(loc.File, dep.Name) {
			continue
		}
		filtered = append(filtered, loc)
	}
	if len(filtered) > 0 {
		return filtered
	}
	// Fall back to original locations only when filtering would otherwise make a
	// dependency unreachable; exact name resolution still runs before locations.
	return dep.Locations
}

func looksLikeDatabaseTargetPath(path, depName string) bool {
	lowerPath := strings.ToLower(filepathSlash(path))
	lowerName := strings.ToLower(depName)
	if strings.Contains(lowerPath, "/repository/") || strings.Contains(lowerPath, "/repositories/") || strings.Contains(lowerPath, "/dao/") {
		return true
	}
	if strings.Contains(lowerName, "repository.") || strings.Contains(lowerName, "dao.") || strings.Contains(lowerName, "entitymanager.") {
		return true
	}
	return false
}

func isProductionCallPath(p astpkg.CallPath) bool {
	if isLowSignalTargetSymbol(p.TargetSymbol) {
		return false
	}
	for _, step := range p.Steps {
		if isTestLikeArtifactPath(step.File) || isLowSignalTargetSymbol(step.Callee) {
			return false
		}
	}
	return true
}

func isLowSignalTargetSymbol(sym string) bool {
	name := strings.ToLower(lastIdent(sym))
	switch name {
	case "equals", "hashcode", "tostring", "build", "builder", "copy", "clone", "valueof", "fromstring":
		return true
	}
	if strings.Contains(strings.ToLower(sym), "exception") {
		return true
	}
	return false
}

// isLowSignalRepositoryOwner reports whether a repository/DAO owner name is a
// generic persistence handle rather than a concrete, table-bearing repository.
// EntityManager.persist(order) IS a real write, but the owner ("EntityManager")
// carries no table information, so the deriver would mint the junk table
// "entity_manager". Per invariant #6 ("prefer emit nothing over a guess") we
// drop these on the deterministic path and let the LLM — which reads the
// argument types — recover the real table. WHY a denylist: we keep accepting
// arbitrary *Repository/*Dao names; only the handful of framework handles that
// masquerade as repositories are rejected.
func isLowSignalRepositoryOwner(owner string) bool {
	switch strings.ToLower(strings.TrimSpace(lastIdent(owner))) {
	case "entitymanager", "sessionfactory", "session", "transactionmanager",
		"datasource", "jdbctemplate", "namedparameterjdbctemplate", "querydsl":
		return true
	}
	return false
}

// isJunkTableName reports whether a derived table/resource name is a database
// artifact rather than a real table: the generic "entity_manager" handle, and
// sequences (*_seq, *_id_seq, *_sequence). These are low-signal precision nits
// (see docs/PLATFORM.md roadmap #3); emitting one as a db_operation poisons the
// output, so the deterministic deriver drops them. LLM-originated rows are NOT
// touched here — the LLM is the authority on what exists.
func isJunkTableName(table string) bool {
	t := strings.ToLower(strings.TrimSpace(table))
	if t == "" || t == "entity_manager" {
		return true
	}
	return strings.HasSuffix(t, "_seq") || strings.HasSuffix(t, "_id_seq") || strings.HasSuffix(t, "_sequence")
}

// inferDBOperationKindAST infers read/write for a repository method using, in
// priority order: (1) a @Modifying / write-shaped @Query annotation on the
// method symbol → write, a read-shaped @Query → read; (2) the method-name
// prefix (inferDBOperationKind); (3) the Spring-Data finder convention (a name
// that reads but lacks a known prefix, e.g. "loadByStatus", "selectAll") →
// read. It only returns "unknown" as a last resort. WHY default finders to
// read: derived queries are overwhelmingly reads, and the precision rule is
// "never guess WRITE when unsure" — READ is the safe high-precision default.
func inferDBOperationKindAST(idx *astpkg.ProjectIndex, symbol string) string {
	for _, def := range lookupMethodDefs(idx, symbol) {
		for _, ann := range def.Annotations {
			name := strings.ToLower(ann.Name)
			args := strings.ToLower(ann.Arguments)
			if strings.Contains(name, "modifying") {
				return "write"
			}
			if strings.Contains(name, "query") && args != "" {
				if hasAnyTokenPrefix(args, "insert", "update", "delete", "merge", "upsert") {
					return "write"
				}
				if hasAnyTokenPrefix(args, "select", "with") {
					return "read"
				}
			}
		}
	}
	if kind := inferDBOperationKind(symbol); kind != "unknown" {
		return kind
	}
	method := symbol
	if _, m, ok := splitOwnerMethod(symbol); ok {
		method = m
	}
	if looksLikeFinderMethod(method) {
		return "read"
	}
	return "unknown"
}

// looksLikeFinderMethod recognises read-shaped repository method names that
// inferDBOperationKind's prefix list misses (select/load/fetch/scan/stream, and
// the Spring-Data "by..." derived-query form).
func looksLikeFinderMethod(method string) bool {
	m := strings.ToLower(strings.TrimSpace(method))
	if m == "" {
		return false
	}
	for _, p := range []string{"select", "load", "fetch", "scan", "stream", "by", "all", "page", "retrieve"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

// hasAnyTokenPrefix reports whether the first whitespace-delimited token of s
// (after trimming a leading quote/paren) starts with any of the prefixes. Used
// to classify @Query SQL text without a full parser.
func hasAnyTokenPrefix(s string, prefixes ...string) bool {
	s = strings.TrimLeft(strings.TrimSpace(s), "(\"'` \t")
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// lookupMethodDefs returns the symbol definitions for a qualified name,
// matching exactly or by qualified suffix (".Owner.method"). Suffix matching is
// constrained to the full owner.method tail, so it cannot bind to an unrelated
// method that merely shares the leaf name.
func lookupMethodDefs(idx *astpkg.ProjectIndex, symbol string) []astpkg.SymbolDef {
	if idx == nil || strings.TrimSpace(symbol) == "" {
		return nil
	}
	if defs, ok := idx.Symbols[symbol]; ok {
		return defs
	}
	var out []astpkg.SymbolDef
	for q, defs := range idx.Symbols {
		if q == symbol || strings.HasSuffix(q, "."+symbol) {
			out = append(out, defs...)
		}
	}
	return out
}

func isTestLikeArtifactPath(path string) bool {
	path = strings.ToLower(filepathSlash(path))
	base := path
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	return strings.Contains(path, "/src/test/") || strings.Contains(path, "/test/") || strings.Contains(path, "/tests/") || strings.Contains(path, "/__tests__/") || strings.Contains(path, "/fixtures/") || strings.Contains(path, "/fixture/") || strings.Contains(base, "_test.") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasSuffix(base, "test.java") || strings.HasSuffix(base, "tests.java")
}

func isConfigLikeArtifactPath(path string) bool {
	path = strings.ToLower(filepathSlash(path))
	return strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".toml") || strings.HasSuffix(path, ".properties") || strings.Contains(path, "/.github/") || strings.Contains(path, "/.example/config/")
}

func isLowSignalArtifactPath(path string) bool {
	path = strings.ToLower(filepathSlash(path))
	return strings.Contains(path, "/entity/") || strings.Contains(path, "/entities/") || strings.Contains(path, "/dto/") || strings.Contains(path, "/model/") || strings.Contains(path, "/exception/") || strings.Contains(path, "/mapper/")
}

func filepathSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

func appendDependencyTarget(in []model.Dependency, dep model.Dependency) []model.Dependency {
	for _, existing := range in {
		if existing.ID == dep.ID {
			return in
		}
	}
	return append(in, dep)
}

func chooseDepsForSymbol(sym string, deps []model.Dependency) []model.Dependency {
	if len(deps) <= 1 {
		return deps
	}
	symLast := strings.ToLower(lastIdent(sym))
	var exact []model.Dependency
	for _, dep := range deps {
		depName := strings.TrimSpace(dep.Name)
		depBase := depName
		if paren := strings.Index(depBase, "("); paren > 0 {
			depBase = depBase[:paren]
		}
		if depBase == sym || strings.HasSuffix(sym, "."+depBase) || strings.EqualFold(lastIdent(depBase), symLast) && strings.Contains(depBase, ".") {
			exact = append(exact, dep)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	sort.SliceStable(deps, func(i, j int) bool {
		if len(deps[i].Name) != len(deps[j].Name) {
			return len(deps[i].Name) > len(deps[j].Name)
		}
		return deps[i].ID < deps[j].ID
	})
	return []model.Dependency{deps[0]}
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
