package connections

import (
	"fmt"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

type databaseContext struct {
	Platform     string
	Instance     string
	DatabaseName string
}

func buildASTDerivedDBDependency(idx *astpkg.ProjectIndex, name string, path astpkg.CallPath, minConfidence float64, dbCtx databaseContext) model.Dependency {
	last := path.Steps[len(path.Steps)-1]
	locs := []model.Location{{File: last.File, StartLine: int(last.Range.StartLine) + 1, EndLine: int(last.Range.EndLine) + 1}}
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
	platform := extraction.FirstNonEmpty(dbCtx.Platform, "database")
	instance := extraction.FirstNonEmpty(dbCtx.Instance, dbCtx.DatabaseName, platform)
	base := model.BaseEntity{
		ID:         util.StableID("dependency", "db_operation", name, locs[0].File, fmt.Sprintf("%d:%d", locs[0].StartLine, locs[0].EndLine)),
		Type:       "db_operation",
		Name:       name,
		Summary:    fmt.Sprintf("AST-derived database operation %s on %s", name, extraction.FirstNonEmpty(table, entity, owner)),
		Locations:  locs,
		Confidence: conf,
		Evidence:   []model.Evidence{{Location: locs[0], Snippet: fmt.Sprintf("repository call %s", name), Source: "ast"}},
		Details: map[string]any{
			"platform": platform, "database_type": platform, "instance": instance,
			"database_name":      extraction.FirstNonEmpty(dbCtx.DatabaseName, instance),
			"operation_kind":     operationKind,
			"operation_type":     operationKind,
			"operation":          method,
			"repository_class":   owner,
			"repository_method":  method,
			"entity":             entity,
			"table":              table,
			"table_or_entity":    extraction.FirstNonEmpty(table, entity),
			"discovered_by":      "ast_repository_call",
			"source_call_symbol": path.TargetSymbol,
		},
		PluginSource: "ast",
	}
	extraction.EnrichEntityGrouping(&base)
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
		platform := extraction.FirstNonEmpty(dep.Platform, detailString(dep.Details, "platform"), detailString(dep.Details, "database_type"), detailString(dep.Details, "database"))
		instance := extraction.FirstNonEmpty(dep.Instance, detailString(dep.Details, "instance"), detailString(dep.Details, "database_name"), detailString(dep.Details, "database"))
		dbName := extraction.FirstNonEmpty(detailString(dep.Details, "database_name"), databaseNameFromDetails(dep.Details))
		if platform == "unknown" || platform == "" {
			platform = extraction.DBPlatform(dep.Name+" "+dep.Summary, fmt.Sprint(dep.Details))
		}
		if instance == "unknown" || instance == "" || instance == platform {
			instance = extraction.FirstNonEmpty(dbName, instance, platform)
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
		if end := strings.IndexAny(rest, "}/?& )`\n\t"); end >= 0 {
			rest = rest[:end]
		}
		return strings.Trim(rest, "${}:,.;'")
	}
	if idx := strings.Index(text, "jdbc:postgresql://"); idx >= 0 {
		rest := text[idx+len("jdbc:postgresql://"):]
		if slash := strings.Index(rest, "/"); slash >= 0 && slash+1 < len(rest) {
			rest = rest[slash+1:]
			if end := strings.IndexAny(rest, "?& )`\n\t"); end >= 0 {
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
	entity := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(owner, "Repository"), "Dao"), "DAO")
	if entity == "" {
		entity = owner
	}
	return entity, camelToSnake(entity)
}

func camelToSnake(s string) string {
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
	if !ok || owner == "" || method == "" || isLowSignalRepositoryOwner(owner) {
		return false
	}
	lowerOwner := strings.ToLower(owner)
	if !(strings.HasSuffix(lowerOwner, "repository") || strings.HasSuffix(lowerOwner, "dao") || strings.HasSuffix(lowerOwner, "entitymanager")) {
		return false
	}
	return !isLowSignalTargetSymbol(sym)
}

func normalizeRepositoryOperationName(sym string) string {
	owner, method, ok := splitOwnerMethod(strings.TrimSpace(sym))
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
