package agents

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/ast/framework"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/scip"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Ensure astpkg is used even when the AST path is disabled at runtime.
var _ = (*astpkg.ProjectIndex)(nil)

// runConnectionsBatch is Stage 4. It maps each exposure to the
// dependencies it reaches by walking the SCIP call graph produced in
// the preceding index stage.
//
// COMPARED TO THE PREVIOUS LLM-BASED IMPLEMENTATION
//
//   - Zero LLM calls: deterministic, fast, free.
//   - Full transitive paths with file:line for every hop.
//   - Conditions extracted by lightweight source-text scanning around
//     each call site.
//   - Honest failure modes: when no SCIP index is available (Docker
//     missing, indexer crashed, etc.) we degrade to a name-based
//     fallback matcher that produces shallow Connection records (no
//     paths) so the run still completes with something useful.
//
// SIGNATURE PRESERVED for source compatibility with pipeline.go. The
// (error, string) return tuple is kept; we always return nil/"" since
// SCIP walking cannot "fail" in the LLM sense — it either produces
// connections or it doesn't.
func (o *orchestrator) runConnectionsBatch(
	ctx context.Context,
	exposures []model.Exposure,
	dependencies []model.Dependency,
	_ map[string]objectives.Objective, // no longer used by SCIP path; kept for API stability
	_ *repoFacts, //                      nor are repoFacts
	onResult func(),
) ([]model.Connection, []model.UnresolvedItem, error, string) {
	if len(exposures) == 0 || len(dependencies) == 0 {
		return nil, nil, nil, ""
	}

	expByID := make(map[string]model.Exposure, len(exposures))
	for _, e := range exposures {
		expByID[e.ID] = e
	}
	depByID := make(map[string]model.Dependency, len(dependencies))
	for _, d := range dependencies {
		depByID[d.ID] = d
	}

	// ── Path A: tree-sitter AST index (preferred) ────────────────────────
	// Only use the AST path when the index has actual content (at least
	// some symbols or call edges). An empty index (e.g. tests with empty
	// temp dirs, or a repo with no recognisable source files) should fall
	// through to the SCIP path or shallow matcher.
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
		// If the AST walk found zero connections, fall through to shallow
		// matcher so runs still produce something useful.
		if len(conns) > 0 || len(unresolved) > 0 {
			o.emitConnectionsAggregate(len(exposures), len(conns), exposuresWithoutPaths, "ast")
			sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })
			return conns, unresolved, nil, ""
		}
		util.Warn("agents.connections", "ast walk produced no connections; falling back to shallow matcher", nil)
	}

	// ── Path B: SCIP index (legacy fallback) ─────────────────────────────
	// If indexing is disabled or failed, fall back to the deterministic
	// name-based matcher. It produces shallow connections (no paths,
	// no conditions) but they are still valid model.Connection entries.
	if o.scipIndex == nil {
		util.Warn("agents.connections", "no index available; using shallow name matcher", nil)
		conns, unresolved := buildShallowConnections(exposures, dependencies, o.cfg.Quality.MinConfidence)
		o.emitConnectionsAggregate(len(exposures), len(conns), 0, "no_index")
		if onResult != nil {
			for range exposures {
				onResult()
			}
		}
		return conns, unresolved, nil, ""
	}

	resolver := scip.NewResolver(o.scipIndex)
	walker := scip.NewWalker(o.scipIndex)
	conditioner := scip.NewConditionExtractor(o.sessionDir)

	// 1. Resolve every dependency to one or more SCIP symbols. We do
	// this once up front so the per-exposure goroutines don't all
	// redo the same work.
	depSymbols, unresolvedDeps := resolveDependencySymbols(resolver, dependencies)

	// Compose the set of "target" symbols. A path is recorded when
	// its tail hits any of these.
	targetSet := make(map[string]string, len(depSymbols)) // symbol -> dependency_id
	for depID, syms := range depSymbols {
		for _, s := range syms {
			targetSet[s] = depID
		}
	}

	// 2. Resolve every exposure's entry symbol. We use the recorded
	// source_locations from detail to pinpoint the handler symbol.
	type expEntry struct {
		exp     model.Exposure
		symbols []string
	}
	expEntries := make([]expEntry, 0, len(exposures))
	for _, e := range exposures {
		syms := resolveExposureSymbols(resolver, e)
		expEntries = append(expEntries, expEntry{exp: e, symbols: syms})
	}

	// 3. Per-exposure walks, in parallel. Each exposure produces a
	// slice of Connection records; we merge them at the end.
	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 6
	}
	if workers > len(expEntries) {
		workers = len(expEntries)
	}

	jobCh := make(chan expEntry)
	type result struct {
		conns      []model.Connection
		unresolved []model.UnresolvedItem
	}
	resCh := make(chan result, len(expEntries))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range jobCh {
				if ctx.Err() != nil {
					resCh <- result{}
					continue
				}
				conns, unr := buildConnectionsForExposure(
					ctx, walker, conditioner,
					entry.exp, entry.symbols,
					targetSet, depByID,
					o.cfg.Quality.MinConfidence,
				)
				resCh <- result{conns: conns, unresolved: unr}
			}
		}()
	}
	go func() {
		for _, e := range expEntries {
			jobCh <- e
		}
		close(jobCh)
		wg.Wait()
		close(resCh)
	}()

	allConns := []model.Connection{}
	allUnresolved := []model.UnresolvedItem{}
	allUnresolved = append(allUnresolved, unresolvedDeps...)
	exposuresWithoutPaths := 0
	for r := range resCh {
		if onResult != nil {
			onResult()
		}
		if len(r.conns) == 0 {
			exposuresWithoutPaths++
		}
		allConns = append(allConns, r.conns...)
		allUnresolved = append(allUnresolved, r.unresolved...)
	}

	// Deterministic ordering for downstream artifact stability.
	sort.Slice(allConns, func(i, j int) bool { return allConns[i].ID < allConns[j].ID })

	o.emitConnectionsAggregate(len(exposures), len(allConns), exposuresWithoutPaths, "scip")
	return allConns, allUnresolved, nil, ""
}

// resolveDependencySymbols maps every dependency to its SCIP symbol
// set. We try positional resolution first (using source_locations
// recorded by the detail stage) and fall back to qualified-name
// resolution.
//
// Returns the resolution map plus a slice of unresolved-item records
// for dependencies that could not be located in the index.
func resolveDependencySymbols(
	r *scip.Resolver, deps []model.Dependency,
) (map[string][]string, []model.UnresolvedItem) {
	out := map[string][]string{}
	unresolved := []model.UnresolvedItem{}
	for _, d := range deps {
		syms := resolveOneDependency(r, d)
		if len(syms) == 0 {
			unresolved = append(unresolved, model.UnresolvedItem{
				Kind:       model.KindDependency,
				Type:       d.Type,
				Name:       d.Name,
				ReasonCode: "scip_unresolved",
				Reason:     fmt.Sprintf("dependency %q (%s) not found in SCIP index", d.Name, d.Type),
				Confidence: d.Confidence,
			})
			continue
		}
		out[d.ID] = syms
	}
	return out, unresolved
}

// resolveOneDependency tries the strategies in priority order:
//  1. positional (source_locations + line)
//  2. qualified name like "Class.method" or "Class#method"
//  3. plain display-name match
//
// Returns up to 8 symbols per dependency (overloads / multiple
// implementations). We bound the slice to keep walker work proportional.
func resolveOneDependency(r *scip.Resolver, d model.Dependency) []string {
	const maxSymbolsPerDep = 8

	// 1. Positional
	if len(d.Locations) > 0 {
		loc := d.Locations[0]
		res := r.Resolve(scip.EntityLocation{
			Name:      d.Name,
			File:      loc.File,
			StartLine: loc.StartLine,
			StartCol:  1,
			Hint:      d.Type,
		})
		if len(res.Symbols) > 0 {
			return truncateSymbols(res.Symbols, maxSymbolsPerDep)
		}
	}

	// 2. Qualified (Name often contains "Class.method" for repo / feign)
	if strings.ContainsAny(d.Name, ".#:") {
		res := r.ResolveByQualified(d.Name)
		if len(res.Symbols) > 0 {
			return truncateSymbols(res.Symbols, maxSymbolsPerDep)
		}
	}

	// 3. Plain display-name match
	res := r.Resolve(scip.EntityLocation{Name: d.Name})
	if len(res.Symbols) > 0 {
		return truncateSymbols(res.Symbols, maxSymbolsPerDep)
	}
	return nil
}

// resolveExposureSymbols maps an exposure to its entry symbol(s).
// Exposures almost always have an exact source_location (the handler
// method); positional resolution is highly reliable here.
func resolveExposureSymbols(r *scip.Resolver, e model.Exposure) []string {
	const maxSymbolsPerEntry = 4
	if len(e.Locations) > 0 {
		loc := e.Locations[0]
		res := r.Resolve(scip.EntityLocation{
			Name:      e.Name,
			File:      loc.File,
			StartLine: loc.StartLine,
			StartCol:  1,
			Hint:      e.Type,
		})
		if len(res.Symbols) > 0 {
			return truncateSymbols(res.Symbols, maxSymbolsPerEntry)
		}
	}
	res := r.Resolve(scip.EntityLocation{Name: e.Name})
	return truncateSymbols(res.Symbols, maxSymbolsPerEntry)
}

func truncateSymbols(in []string, max int) []string {
	if len(in) <= max {
		return in
	}
	return in[:max]
}

// buildConnectionsForExposure walks the graph from `entrySymbols`
// looking for any of `targetSet`'s symbols. Each hit becomes a
// model.Connection with full paths + conditions.
//
// An exposure with multiple entry symbols (rare: overloads) walks each
// in turn. Paths from all entries are merged into the result.
func buildConnectionsForExposure(
	ctx context.Context,
	walker *scip.Walker,
	conditioner *scip.ConditionExtractor,
	exposure model.Exposure,
	entrySymbols []string,
	targetSet map[string]string, // sym -> dep_id
	depByID map[string]model.Dependency,
	minConfidence float64,
) ([]model.Connection, []model.UnresolvedItem) {
	if len(entrySymbols) == 0 {
		// We did not find a SCIP symbol for this exposure. Surface
		// it so the run reports a partial coverage problem.
		return nil, []model.UnresolvedItem{{
			Kind:       model.KindExposure,
			Type:       exposure.Type,
			Name:       exposure.Name,
			ReasonCode: "scip_unresolved",
			Reason:     fmt.Sprintf("exposure %q not found in SCIP index", exposure.Name),
		}}
	}

	isTarget := func(sym string) bool {
		_, ok := targetSet[sym]
		return ok
	}

	type pathBucket struct {
		dep   model.Dependency
		paths []scip.Path
	}
	byDep := map[string]*pathBucket{}

	for _, entry := range entrySymbols {
		if ctx.Err() != nil {
			return nil, nil
		}
		paths := walker.Walk(entry, scip.WalkConfig{
			IsTarget:          isTarget,
			Context:           ctx,
			MaxDepth:          12,
			MaxPathsPerTarget: 8,
			MaxPathsPerSymbol: 4,
			MaxPathsTotal:     4000,
			MaxVisitedEdges:   250000,
		})
		scip.SortPaths(paths)
		for _, p := range paths {
			depID, ok := targetSet[p.TargetSymbol]
			if !ok {
				continue
			}
			dep, ok := depByID[depID]
			if !ok {
				continue
			}
			b, exists := byDep[depID]
			if !exists {
				b = &pathBucket{dep: dep}
				byDep[depID] = b
			}
			b.paths = append(b.paths, p)
		}
	}

	conns := make([]model.Connection, 0, len(byDep))
	for _, b := range byDep {
		c := buildConnection(exposure, b.dep, b.paths, conditioner, minConfidence)
		conns = append(conns, c)
	}
	return conns, nil
}

// buildConnection assembles a single model.Connection from one
// exposure → dependency walk. The Connection records:
//   - all distinct call paths (deduped by signature)
//   - all conditions extracted from the call sites along those paths
//   - a deterministic ID derived from the exposure / dependency / path
//     signatures so re-runs produce the same connection IDs
func buildConnection(
	exposure model.Exposure, dep model.Dependency,
	paths []scip.Path, conditioner *scip.ConditionExtractor,
	minConfidence float64,
) model.Connection {
	mPaths := make([]model.ConnectionPath, 0, len(paths))
	conditionsByExpr := map[string]model.Condition{}
	locations := []model.Location{}

	for i, p := range paths {
		mp := convertPath(p, conditioner, dep.Type)
		if mp.ID == "" {
			mp.ID = fmt.Sprintf("path-%d", i+1)
		}
		mPaths = append(mPaths, mp)
		for _, cond := range mp.Condition.Metadata { // placeholder no-op; conditions live per-step
			_ = cond
		}
		for _, step := range mp.Steps {
			if step.Location.File != "" {
				locations = append(locations, step.Location)
			}
		}
		// Surface step-level conditions as path-level conditions on the
		// connection too, so older consumers that only look at
		// Condition.* still see something useful.
		if mp.Condition.Kind != "" {
			conditionsByExpr[mp.Condition.Expression] = mp.Condition
		}
	}

	pathSig := buildPathSignature(paths)
	connID := util.StableID(exposure.ID, dep.ID, pathSig)

	primary := model.Condition{}
	for _, c := range conditionsByExpr {
		primary = c
		break // take any; UI shows full list via path-level conditions
	}

	if len(locations) == 0 {
		locations = append(locations, exposure.Locations...)
	}
	if len(locations) == 0 {
		locations = append(locations, dep.Locations...)
	}

	// Confidence: SCIP-derived paths are deterministic; we score by
	// path-length inverse (shorter chains are typically more direct
	// and less likely to be coincidental). 1-hop → 0.95, 2-hop → 0.9,
	// etc., floor at minConfidence so we never silently drop SCIP
	// connections.
	confidence := scoreConfidence(paths, minConfidence)

	return model.Connection{
		ID:             connID,
		FromExposureID: exposure.ID,
		ToDependencyID: dep.ID,
		Condition:      primary,
		PathSignature:  pathSig,
		Summary:        formatConnectionSummary(exposure, dep, len(paths)),
		Locations:      dedupeLocations(locations),
		Evidence:       buildEvidenceFromPaths(paths),
		Confidence:     confidence,
		FromType:       exposure.Type,
		ToType:         dep.Type,
		Paths:          mPaths,
	}
}

// convertPath translates an internal scip.Path to the public
// model.ConnectionPath. Steps are 1-indexed in the artifact (UI
// convention); locations are converted from SCIP zero-based to
// 1-based lines.
func convertPath(p scip.Path, conditioner *scip.ConditionExtractor, depType string) model.ConnectionPath {
	steps := make([]model.ConnectionPathStep, 0, len(p.Steps))
	pathConditions := []scip.Condition{}

	for i, cs := range p.Steps {
		loc := scipLocToModel(cs.At)
		conds := conditioner.Extract(cs, "") // language hint omitted; conditions auto-detect
		pathConditions = append(pathConditions, conds...)
		steps = append(steps, model.ConnectionPathStep{
			Order:     i + 1,
			Action:    "invoke",
			Operation: depType,
			From:      cs.CallerSymbol,
			To:        cs.CalleeSymbol,
			Location:  loc,
		})
	}

	// Roll up path-level conditions: take the most "specific" kind
	// available (auth > if_guard > optional > others). This keeps
	// the rolled-up Condition meaningful even when multiple guards
	// gate the call.
	rolled := rollupConditions(pathConditions)

	return model.ConnectionPath{
		ID:        pathSignatureFor(p),
		Summary:   formatPathSummary(p),
		Condition: rolled,
		Steps:     steps,
	}
}

// rollupConditions picks the highest-priority condition kind seen
// along the path and converts it to a model.Condition. Returns the
// zero-value when no conditions were found (the connection still
// renders fine — the UI just hides empty condition badges).
func rollupConditions(conds []scip.Condition) model.Condition {
	if len(conds) == 0 {
		return model.Condition{}
	}
	priority := map[scip.ConditionKind]int{
		scip.ConditionAuth:          100,
		scip.ConditionFeatureFlag:   90,
		scip.ConditionIfGuard:       80,
		scip.ConditionNullCheck:     75,
		scip.ConditionOptional:      70,
		scip.ConditionTernary:       65,
		scip.ConditionExceptionPath: 50,
		scip.ConditionLoop:          40,
	}
	best := conds[0]
	for _, c := range conds[1:] {
		if priority[c.Kind] > priority[best.Kind] {
			best = c
		}
	}
	return model.Condition{
		Kind:        string(best.Kind),
		Expression:  best.Expression,
		Explanation: best.Explanation,
	}
}

// buildPathSignature returns a stable string that identifies the
// SET of paths between an exposure and a dependency. We sort each
// path's symbol chain and join across paths so two equivalent
// connections from different runs produce identical IDs.
func buildPathSignature(paths []scip.Path) string {
	if len(paths) == 0 {
		return "no-paths"
	}
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		chain := []string{p.EntrySymbol}
		for _, s := range p.Steps {
			chain = append(chain, s.CalleeSymbol)
		}
		parts = append(parts, strings.Join(chain, "->"))
	}
	sort.Strings(parts)
	return strings.Join(parts, "||")
}

// pathSignatureFor returns a stable per-path ID.
func pathSignatureFor(p scip.Path) string {
	parts := []string{p.EntrySymbol}
	for _, s := range p.Steps {
		parts = append(parts, s.CalleeSymbol)
	}
	return util.StableID(parts...)
}

// formatPathSummary produces a short human-readable summary of a path.
// Used by the UI to label the path card.
func formatPathSummary(p scip.Path) string {
	if len(p.Steps) == 0 {
		return p.EntrySymbol
	}
	first := lastIdent(p.EntrySymbol)
	last := lastIdent(p.TargetSymbol)
	return fmt.Sprintf("%s → %s (%d hops)", first, last, len(p.Steps))
}

// formatConnectionSummary is the top-level summary line shown for the
// connection on the dashboard.
func formatConnectionSummary(exp model.Exposure, dep model.Dependency, pathCount int) string {
	if pathCount == 1 {
		return fmt.Sprintf("%s → %s (1 path)", exp.Name, dep.Name)
	}
	return fmt.Sprintf("%s → %s (%d paths)", exp.Name, dep.Name, pathCount)
}

// scoreConfidence applies a length-based heuristic. Single-hop paths
// score 0.95; each extra hop subtracts 0.05; floor at minConfidence
// to ensure we don't drop a SCIP-discovered connection because of
// path length alone.
func scoreConfidence(paths []scip.Path, minConfidence float64) float64 {
	best := 0.0
	for _, p := range paths {
		s := 0.95 - float64(len(p.Steps)-1)*0.05
		if s < 0.5 {
			s = 0.5
		}
		if s > best {
			best = s
		}
	}
	if best < minConfidence {
		best = minConfidence
	}
	return best
}

// buildEvidenceFromPaths emits one Evidence record per distinct call
// site appearing across all paths. We dedupe by (file, line) so
// repeated visits to the same call site (e.g. from different paths)
// don't bloat the artifact.
func buildEvidenceFromPaths(paths []scip.Path) []model.Evidence {
	seen := map[string]struct{}{}
	out := []model.Evidence{}
	for _, p := range paths {
		for _, s := range p.Steps {
			key := fmt.Sprintf("%s:%d:%d", s.At.File, s.At.StartLine, s.At.StartCol)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model.Evidence{
				Location: scipLocToModel(s.At),
				Snippet:  fmt.Sprintf("call to %s", lastIdent(s.CalleeSymbol)),
				Source:   "scip",
			})
		}
	}
	return out
}

// scipLocToModel converts SCIP's zero-based Location to diffmind's
// 1-based model.Location. We also normalise file separators to
// forward slashes (already the SCIP convention but defensive against
// future Windows indexers).
func scipLocToModel(loc scip.Location) model.Location {
	return model.Location{
		File:      filepath.ToSlash(loc.File),
		StartLine: int(loc.StartLine) + 1,
		EndLine:   int(loc.EndLine) + 1,
	}
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

// buildShallowConnections is the no-SCIP fallback. It matches each
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
			// If the resolved symbol is a class, also register all its methods as
			// targets. This way the walker can find paths to any method of the class.
			if defs, ok := idx.Symbols[sym]; ok {
				for _, def := range defs {
					if def.Kind == astpkg.SymbolKindClass || def.Kind == astpkg.SymbolKindInterface {
						// Find all methods in this class's file within the class range.
						if fa, ok := idx.Files[def.File]; ok {
							for _, msym := range fa.Symbols {
								if (msym.Kind == astpkg.SymbolKindMethod || msym.Kind == astpkg.SymbolKindFunction) &&
									msym.Range.StartLine >= def.Range.StartLine &&
									msym.Range.EndLine <= def.Range.EndLine {
									depBySymbol[msym.Qualified] = dep
								}
							}
						}
					}
				}
			}
		}
	}

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
		exp     model.Exposure
		entries []string
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
					ctx, walker, cfg, item.exp, item.entries, depBySymbol, minConfidence,
				)
				resCh <- result{conns: conns, unresolved: unr}
			}
		}()
	}

	go func() {
		for _, exp := range exposures {
			entries := resolveEntitySymbolsAST(idx, exp.Name, exp.Locations)
			if len(entries) == 0 {
				// No SCIP-equivalent entry point: try framework bindings.
				for _, fb := range idx.Frameworks {
					if fb.Symbol != "" && strings.Contains(fb.Trigger, exp.Name) {
						entries = append(entries, fb.Symbol)
					}
				}
			}
			jobCh <- workItem{exp: exp, entries: entries}
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
	var found []string

	// 1. Positional lookup.
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

		// Methods are better entry/target points than classes.
		if len(methods) > 0 {
			for _, m := range methods {
				found = append(found, m.Qualified)
			}
			return uniqueStrings(found)
		}

		// Only a class found: expand to all its methods within the file.
		// This handles the case where the LLM recorded the class start line.
		if len(classes) > 0 {
			for _, cls := range classes {
				// Add the class itself (walker can still use it to find field types)
				found = append(found, cls.Qualified)
				// Add all methods defined in this file that fall within the class range.
				for _, sym := range fa.Symbols {
					if sym.Kind == astpkg.SymbolKindMethod || sym.Kind == astpkg.SymbolKindFunction {
						if sym.Range.StartLine >= cls.Range.StartLine &&
							sym.Range.EndLine <= cls.Range.EndLine {
							found = append(found, sym.Qualified)
						}
					}
				}
			}
			return uniqueStrings(found)
		}

		// Nothing at exact line: widen ±30 lines, prefer methods.
		lo := line
		if line > 5 {
			lo = line - 5
		}
		for _, sym := range fa.Symbols {
			if sym.Range.StartLine >= lo && sym.Range.StartLine <= line+30 {
				found = append(found, sym.Qualified)
			}
		}
		if len(found) > 0 {
			return uniqueStrings(found)
		}
	}

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

// buildASTConnectionsForExposure walks from each entry symbol and builds
// Connection records for every dependency it reaches.
func buildASTConnectionsForExposure(
	ctx context.Context,
	walker *astpkg.Walker,
	cfg astpkg.WalkConfig,
	exposure model.Exposure,
	entrySymbols []string,
	depBySymbol map[string]model.Dependency,
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

	for _, entry := range entrySymbols {
		if ctx.Err() != nil {
			return nil, nil
		}
		paths := walker.Walk(entry, cfg)
		for _, p := range paths {
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
