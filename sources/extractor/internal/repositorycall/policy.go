// Package repositorycall owns the high-precision policy for recognizing and
// classifying repository operations in the AST call graph.
package repositorycall

import (
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

func IsOperationSymbol(symbol string) bool {
	owner, method, ok := SplitOwnerMethod(NormalizeOperationName(symbol))
	if !ok || owner == "" || method == "" || IsLowSignalOwner(owner) {
		return false
	}
	lowerOwner := strings.ToLower(owner)
	if !(strings.HasSuffix(lowerOwner, "repository") ||
		strings.HasSuffix(lowerOwner, "dao") ||
		strings.HasSuffix(lowerOwner, "entitymanager")) {
		return false
	}
	return !isLowSignalTarget(symbol)
}

func NormalizeOperationName(symbol string) string {
	owner, method, ok := SplitOwnerMethod(strings.TrimSpace(symbol))
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

func SplitOwnerMethod(name string) (string, string, bool) {
	name = strings.TrimSpace(name)
	if dot := strings.LastIndex(name, "."); dot > 0 && dot+1 < len(name) {
		return name[:dot], name[dot+1:], true
	}
	if hash := strings.LastIndex(name, "#"); hash > 0 && hash+1 < len(name) {
		return name[:hash], name[hash+1:], true
	}
	return "", "", false
}

func TableEntity(owner string) (string, string) {
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

func IsLowSignalOwner(owner string) bool {
	switch strings.ToLower(strings.TrimSpace(lastIdent(owner))) {
	case "entitymanager", "sessionfactory", "session", "transactionmanager",
		"datasource", "jdbctemplate", "namedparameterjdbctemplate", "querydsl":
		return true
	}
	return false
}

func IsJunkTable(table string) bool {
	table = strings.ToLower(strings.TrimSpace(table))
	if table == "" || table == "entity_manager" {
		return true
	}
	return strings.HasSuffix(table, "_seq") ||
		strings.HasSuffix(table, "_id_seq") ||
		strings.HasSuffix(table, "_sequence")
}

func InferOperationKind(index *astpkg.ProjectIndex, symbol string) string {
	for _, definition := range lookupMethodDefinitions(index, symbol) {
		for _, annotation := range definition.Annotations {
			name := strings.ToLower(annotation.Name)
			arguments := strings.ToLower(annotation.Arguments)
			if strings.Contains(name, "modifying") {
				return "write"
			}
			if strings.Contains(name, "query") && arguments != "" {
				if hasTokenPrefix(arguments, "insert", "update", "delete", "merge", "upsert") {
					return "write"
				}
				if hasTokenPrefix(arguments, "select", "with") {
					return "read"
				}
			}
		}
	}
	if kind := inferMethodKind(symbol); kind != "unknown" {
		return kind
	}
	method := symbol
	if _, parsed, ok := SplitOwnerMethod(symbol); ok {
		method = parsed
	}
	if looksLikeFinder(method) {
		return "read"
	}
	return "unknown"
}

func inferMethodKind(name string) string {
	_, method, ok := SplitOwnerMethod(name)
	if !ok {
		method = name
	}
	method = strings.ToLower(method)
	switch {
	case hasPrefix(method, "save", "insert", "update", "upsert", "delete", "remove"):
		return "write"
	case hasPrefix(method, "exists", "count", "find", "get", "list", "read", "query", "search"):
		return "read"
	default:
		return "unknown"
	}
}

func looksLikeFinder(method string) bool {
	return hasPrefix(strings.ToLower(strings.TrimSpace(method)),
		"select", "load", "fetch", "scan", "stream", "by", "all", "page", "retrieve")
}

func hasPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func hasTokenPrefix(value string, prefixes ...string) bool {
	value = strings.TrimLeft(strings.TrimSpace(value), "(\"'` \t")
	return hasPrefix(value, prefixes...)
}

func lookupMethodDefinitions(index *astpkg.ProjectIndex, symbol string) []astpkg.SymbolDef {
	if index == nil || strings.TrimSpace(symbol) == "" {
		return nil
	}
	if definitions, ok := index.Symbols[symbol]; ok {
		return definitions
	}
	var out []astpkg.SymbolDef
	for qualified, definitions := range index.Symbols {
		if qualified == symbol || strings.HasSuffix(qualified, "."+symbol) {
			out = append(out, definitions...)
		}
	}
	return out
}

func isLowSignalTarget(symbol string) bool {
	name := strings.ToLower(lastIdent(symbol))
	switch name {
	case "equals", "hashcode", "tostring", "build", "builder", "copy", "clone", "valueof", "fromstring":
		return true
	}
	return strings.Contains(strings.ToLower(symbol), "exception")
}

func lastIdent(value string) string {
	value = strings.TrimSpace(value)
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	if paren := strings.Index(value, "("); paren > 0 {
		value = value[:paren]
	}
	value = strings.TrimRight(value, ".#/")
	if separator := strings.LastIndexAny(value, "#./"); separator >= 0 {
		value = value[separator+1:]
	}
	return value
}

func camelToSnake(value string) string {
	var out strings.Builder
	for index, char := range value {
		if index > 0 && char >= 'A' && char <= 'Z' {
			previous := rune(value[index-1])
			if previous >= 'a' && previous <= 'z' || previous >= '0' && previous <= '9' {
				out.WriteByte('_')
			}
		}
		if char >= 'A' && char <= 'Z' {
			char += 'a' - 'A'
		}
		out.WriteRune(char)
	}
	return out.String()
}
