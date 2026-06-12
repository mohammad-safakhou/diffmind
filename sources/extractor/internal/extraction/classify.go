package extraction

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/entitykey"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

var typeAliases = map[model.EntityKind]map[string]string{
	model.KindExposure: {
		"http_endpoint": "http_route", "api_route": "http_route", "rest_endpoint": "http_route",
		"sqs_listener": "queue_consumer", "message_listener": "queue_consumer", "event_listener": "queue_consumer",
		"cron_job": "scheduled_job", "scheduler": "scheduled_job", "lambda_handler": "cli_command",
	},
	model.KindDependency: {
		"outbound_http_service": "outbound_http", "external_service": "outbound_http", "http_client": "outbound_http", "api_client": "outbound_http",
		"sqs_publish": "queue_publish", "queue_send": "queue_publish", "topic_publish": "queue_publish",
		"stream_consumer": "stream_consume", "sqs_consumer": "stream_consume",
		"database": "db_operation", "sql_query": "db_operation", "repository_operation": "db_operation",
		"shell_command": "command_exec", "process_exec": "command_exec",
	},
}

func CanonicalObjectiveType(obj objectives.Objective, typ string) (string, bool) {
	t := NormalizeType(typ)
	if t == "" || t == obj.Type {
		return obj.Type, true
	}
	if aliases := typeAliases[obj.Kind]; aliases != nil {
		if c := aliases[t]; c == obj.Type {
			return obj.Type, true
		}
	}
	return obj.Type, false
}

func NormalizeType(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}

func ForceObjectiveType(obj objectives.Objective, e *Candidate) bool {
	if e == nil {
		return true
	}
	canonical, ok := CanonicalObjectiveType(obj, e.Type)
	e.Type = canonical
	return ok
}

func EntitySchemaForObjective(obj objectives.Objective) map[string]any {
	s := EntitySchema()
	props, _ := s["properties"].(map[string]any)
	if props != nil {
		props["type"] = map[string]any{"type": "string", "enum": []string{obj.Type}}
	}
	return s
}

func EntityListSchemaForObjective(obj objectives.Objective) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{"type": "array", "items": EntitySchemaForObjective(obj)},
		},
		"required": []string{"items"},
	}
}

func EnrichEntityGrouping(b *model.BaseEntity) {
	if b == nil {
		return
	}
	d := b.Details
	if d == nil {
		d = map[string]any{}
		b.Details = d
	}
	platform, instance, operation, opKind := DeriveGrouping(*b)
	b.Platform, b.Instance, b.Operation, b.OperationKind = platform, instance, operation, opKind
	d["platform"] = platform
	d["instance"] = instance
	d["operation_normalized"] = operation
	d["operation_kind"] = opKind
}

func DeriveGrouping(b model.BaseEntity) (platform, instance, operation, opKind string) {
	d := b.Details
	nameLower := strings.ToLower(b.Name + " " + strings.Join(b.Tags, " ") + " " + b.Summary)
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := d[k]; ok {
				if s, ok := scalarDetail(v); ok && s != "" {
					return s
				}
			}
		}
		return ""
	}

	switch b.Type {
	case "http_route", "webhook":
		platform = "http"
		instance = "inbound-http"
		operation = strings.TrimSpace(get("method") + " " + get("path"))
		if operation == "" {
			operation = b.Name
		}
		opKind = strings.ToLower(strings.Fields(operation)[0])
	case "queue_consumer", "stream_consume":
		// details.platform leads: the framework binding knows the broker
		// ("sqs: my-queue"); sniffing only the queue NAME made the consumer
		// side key as generic "queue" while the publisher side said "sqs",
		// splitting one physical queue across services downstream.
		platform = QueuePlatform(nameLower, get("platform", "queue", "stream", "topic", "queue_url", "queue_url_property"))
		instance = FirstNonEmpty(get("queue", "stream", "topic", "queue_name", "queue_url", "destination"), b.Name)
		operation = "consume " + instance
		opKind = "consume"
	case "scheduled_job":
		platform = "scheduler"
		instance = FirstNonEmpty(get("schedule"), "scheduler")
		operation = b.Name
		opKind = "run"
	case "cli_command":
		platform = "process"
		instance = FirstNonEmpty(get("command"), b.Name)
		operation = b.Name
		opKind = "execute"
	case "db_operation", "cache_operation":
		platform = DBPlatform(nameLower, get("database", "database_type", "aws_service", "cache_type", "client_class"))
		instance = FirstNonEmpty(get("database", "database_name", "datasource", "connection_string", "table", "entity", "cache_name", "namespace"), platform)
		operation = FirstNonEmpty(get("operation", "sql_equivalent", "query", "method", "service_method"), b.Name)
		// Canonical kind via the SAME folder the identity/dedup uses, so the
		// emitted operation_kind is genuinely read/write (delete/insert/saveAll
		// -> write, findBy/select -> read) and not a raw verb. The raw verb is
		// preserved in details["operation"] (C5).
		opKind = entitykey.NormalizeDBOperation(operation)
	case "outbound_http", "outbound_rpc":
		platform = map[bool]string{true: "rpc", false: "http"}[b.Type == "outbound_rpc"]
		instance = OutboundInstance(get("target_service", "service", "host", "target_host", "base_url", "target_url", "default_url", "production_url", "base_url_property", "client_class"), b.Name)
		operation = strings.TrimSpace(get("http_method", "method") + " " + get("path", "endpoint"))
		if operation == "" || strings.TrimSpace(operation) == "" {
			operation = b.Name
		}
		opKind = NormalizeOperationKind(operation)
	case "queue_publish":
		platform = QueuePlatform(nameLower, get("platform", "destination", "queue", "topic", "queue_url"))
		instance = FirstNonEmpty(get("destination", "queue", "topic", "queue_name", "queue_url", "destination_queue"), b.Name)
		operation = "publish " + instance
		opKind = "publish"
	case "command_exec":
		platform = "process"
		instance = FirstNonEmpty(get("command"), b.Name)
		operation = b.Name
		opKind = "execute"
	default:
		platform = b.Type
		instance = b.Name
		operation = b.Name
		opKind = "use"
	}
	return SanitizeGroup(platform), SanitizeGroup(instance), strings.TrimSpace(operation), SanitizeGroup(opKind)
}

// scalarDetail renders a detail value only when it is a scalar. Structured
// values (maps, lists) carry no single identity — rendering them leaked Go
// syntax ("map[url_template:...]") into Instance in real runs, splitting one
// physical database into several downstream identities.
func scalarDetail(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t), true
	case bool, int, int32, int64, float32, float64, json.Number:
		return strings.TrimSpace(fmt.Sprint(t)), true
	}
	return "", false
}

func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return "unknown"
}

func DBPlatform(text, hint string) string {
	t := strings.ToLower(text + " " + hint)
	switch {
	case strings.Contains(t, "athena"):
		return "athena"
	case strings.Contains(t, "postgres") || strings.Contains(t, "jdbc:postgresql"):
		return "postgres"
	case strings.Contains(t, "mysql"):
		return "mysql"
	case strings.Contains(t, "redis"):
		return "redis"
	case strings.Contains(t, "dynamodb"):
		return "dynamodb"
	case strings.Contains(t, "mongo"):
		return "mongodb"
	case strings.Contains(t, "elastic"):
		return "elasticsearch"
	}
	return "database"
}

func QueuePlatform(text, hint string) string {
	t := strings.ToLower(text + " " + hint)
	switch {
	case strings.Contains(t, "sqs"):
		return "sqs"
	case strings.Contains(t, "sns"):
		return "sns"
	case strings.Contains(t, "kafka"):
		return "kafka"
	case strings.Contains(t, "rabbit"):
		return "rabbitmq"
	case strings.Contains(t, "kinesis"):
		return "kinesis"
	}
	return "queue"
}

func OutboundInstance(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	if strings.HasPrefix(raw, "${") && strings.Contains(raw, ":") {
		raw = strings.TrimSuffix(strings.SplitN(raw[2:], ":", 2)[1], "}")
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

func NormalizeOperationKind(op string) string {
	f := strings.Fields(strings.ToLower(op))
	if len(f) == 0 {
		return "use"
	}
	switch f[0] {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return f[0]
	case "select", "find", "read", "getqueryresults":
		return "read"
	case "insert", "update", "upsert", "save", "write", "putitem":
		return "write"
	case "publish", "send":
		return "publish"
	}
	return f[0]
}

var nonGroupChars = regexp.MustCompile(`\s+`)

func SanitizeGroup(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return nonGroupChars.ReplaceAllString(s, " ")
}

func SemanticEntityKey(b model.BaseEntity) string {
	loc := ""
	if len(b.Locations) > 0 {
		loc = filepath.ToSlash(b.Locations[0].File)
	}
	return strings.Join([]string{b.Type, strings.ToLower(b.Platform), strings.ToLower(b.Instance), strings.ToLower(b.Operation), loc, ContainingName(b.Name)}, "|")
}

func ContainingName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "")
	return name
}

func SortLLMEntities(in []Candidate) {
	sort.SliceStable(in, func(i, j int) bool { return in[i].Name < in[j].Name })
}
