// Package entitykey defines the architectural identity shared by extraction,
// reconciliation, and evaluation.
package entitykey

import (
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

// Semantic returns the identity used when reconciling extracted entities.
func Semantic(b model.BaseEntity) string {
	return semantic(b, true)
}

// SemanticLoose omits source location so hand-authored labels do not need to
// pin an entity to a particular file.
func SemanticLoose(b model.BaseEntity) string {
	return semantic(b, false)
}

func semantic(b model.BaseEntity, withLocation bool) string {
	if (b.Type == "db_operation" || b.Type == "cache_operation") && DataResource(b) != "" {
		return strings.Join([]string{b.Type, DataResource(b), DataOperation(b), PlatformClass(b)}, "|")
	}
	switch b.Type {
	case "http_route", "webhook", "outbound_http":
		method, path := routeMethodPath(b)
		location := ""
		if withLocation && len(b.Locations) > 0 {
			location = b.Locations[0].File
		}
		return strings.Join([]string{b.Type, method, CanonicalRoutePath(path), location}, "|")
	}
	return generic(b, withLocation)
}

func generic(b model.BaseEntity, withLocation bool) string {
	location := ""
	if withLocation && len(b.Locations) > 0 {
		location = b.Locations[0].File
	}
	if b.Type == "scheduled_job" || (b.Type == "cli_command" && !strings.EqualFold(b.Platform, "sqs")) {
		name := strings.ToLower(strings.TrimSpace(b.Name))
		if name != "" {
			return "exposure-job|" + name + "|" + location
		}
	}
	instance := strings.ToLower(strings.TrimSpace(b.Instance))
	operation := strings.ToLower(strings.TrimSpace(b.Operation))
	if operation == "" {
		operation = strings.ToLower(strings.TrimSpace(b.Name))
	}
	return strings.Join([]string{strings.ToLower(b.Platform), instance, operation, location}, "|")
}

// DataResource returns the conservative singular resource identity.
func DataResource(b model.BaseEntity) string {
	return singularResource(firstDetail(b, "table", "table_or_entity", "entity", "cache", "collection", "index", "key"))
}

// DataOperation returns the canonical operation identity.
func DataOperation(b model.BaseEntity) string {
	if op := NormalizeDBOperation(firstDetail(b, "operation", "operation_kind", "operation_type")); op != "" {
		return op
	}
	return NormalizeDBOperation(b.Operation)
}

// PlatformClass preserves real datastore distinctions while collapsing generic
// placeholders such as "database", "jdbc", and "unknown".
func PlatformClass(b model.BaseEntity) string {
	p := strings.ToLower(strings.TrimSpace(b.Platform))
	if p == "" {
		p = firstDetail(b, "platform", "database_type", "database")
	}
	switch {
	case p == "":
		return ""
	case strings.Contains(p, "postgres"):
		return "postgres"
	case strings.Contains(p, "mysql"), strings.Contains(p, "mariadb"):
		return "mysql"
	case strings.Contains(p, "dynamo"):
		return "dynamodb"
	case strings.Contains(p, "mongo"):
		return "mongodb"
	case strings.Contains(p, "elastic"), strings.Contains(p, "opensearch"):
		return "elasticsearch"
	case strings.Contains(p, "redis"):
		return "redis"
	case strings.Contains(p, "memcache"):
		return "memcached"
	case strings.Contains(p, "cassandra"):
		return "cassandra"
	case strings.Contains(p, "athena"):
		return "athena"
	case strings.Contains(p, "snowflake"):
		return "snowflake"
	case strings.Contains(p, "bigquery"):
		return "bigquery"
	case p == "database" || p == "db" || p == "sql" || p == "rdbms" || p == "jdbc" || p == "relational" || p == "unknown":
		return ""
	default:
		return p
	}
}

// QueueDestination normalizes consumer/listener variants to one destination.
func QueueDestination(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, suffix := range []string{"-consumer", "_consumer", ".consumer", "-listener", "_listener", ".listener"} {
		s = strings.TrimSuffix(s, suffix)
	}
	return strings.TrimRight(s, "-_.")
}

// CanonicalRoutePath normalizes framework-specific route parameter syntax.
func CanonicalRoutePath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if routeParameter(segment) {
			segments[i] = "{}"
		}
	}
	path = strings.Join(segments, "/")
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func routeParameter(segment string) bool {
	return segment == "*" ||
		segment == "**" ||
		strings.HasPrefix(segment, ":") ||
		(strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")) ||
		(strings.HasPrefix(segment, "<") && strings.HasSuffix(segment, ">"))
}

// NormalizeDBOperation folds common datastore verbs into stable operation
// classes while preserving destructive deletes as their own semantic class.
func NormalizeDBOperation(operation string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	switch {
	case operation == "":
		return ""
	case hasAnyPrefix(operation, "read", "select", "find", "get", "list", "query", "search", "exists", "count", "scan", "load", "fetch"):
		return "read"
	case hasAnyPrefix(operation, "delete", "remove"):
		return "delete"
	case hasAnyPrefix(operation, "write", "insert", "update", "save", "upsert", "put", "merge", "persist", "store"):
		return "write"
	case containsAny(operation, "delete", "remove", "truncate"):
		return "delete"
	case containsAny(operation, "insert", "update", "upsert", "persist", "merge"):
		return "write"
	case containsAny(operation, "select", "findby", "fetch", "search", "exists", "query"):
		return "read"
	default:
		return operation
	}
}

func routeMethodPath(b model.BaseEntity) (method, path string) {
	method = firstDetail(b, "method")
	path = firstDetail(b, "path")
	if method != "" || path != "" {
		return method, path
	}
	fields := strings.Fields(strings.TrimSpace(b.Name))
	if len(fields) == 0 {
		return "", ""
	}
	switch strings.ToUpper(fields[0]) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT", "ANY", "ALL":
		return strings.ToLower(fields[0]), strings.Join(fields[1:], " ")
	default:
		return "", b.Name
	}
}

func firstDetail(b model.BaseEntity, keys ...string) string {
	for _, key := range keys {
		value, ok := b.Details[key]
		if !ok || value == nil {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

var uncountableResources = map[string]bool{
	"series": true, "species": true, "news": true, "data": true, "media": true,
	"metadata": true, "schema": true, "status": true, "alias": true,
	"analysis": true, "diagnosis": true, "basis": true, "index": true,
}

func singularResource(resource string) string {
	resource = strings.ToLower(strings.TrimSpace(resource))
	if len(resource) < 4 || !strings.HasSuffix(resource, "s") || uncountableResources[resource] {
		return resource
	}
	for _, suffix := range []string{"ss", "us", "is", "ous"} {
		if strings.HasSuffix(resource, suffix) {
			return resource
		}
	}
	switch {
	case strings.HasSuffix(resource, "ies") && len(resource) > 4:
		return resource[:len(resource)-3] + "y"
	case strings.HasSuffix(resource, "ses") && len(resource) > 4:
		return resource[:len(resource)-2]
	default:
		return resource[:len(resource)-1]
	}
}

func containsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
