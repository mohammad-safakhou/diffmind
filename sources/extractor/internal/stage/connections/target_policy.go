package connections

import (
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

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
	if allCallSiteNarrow(deps) {
		return deps
	}
	sort.SliceStable(deps, func(i, j int) bool {
		if len(deps[i].Name) != len(deps[j].Name) {
			return len(deps[i].Name) > len(deps[j].Name)
		}
		return deps[i].ID < deps[j].ID
	})
	return []model.Dependency{deps[0]}
}

func allCallSiteNarrow(deps []model.Dependency) bool {
	if len(deps) == 0 {
		return false
	}
	for _, dep := range deps {
		if len(dep.Locations) == 0 {
			return false
		}
		narrow := false
		for _, loc := range dep.Locations {
			if loc.File != "" && loc.StartLine > 0 && loc.EndLine-loc.StartLine <= 2 {
				narrow = true
				break
			}
		}
		if !narrow {
			return false
		}
	}
	return true
}
