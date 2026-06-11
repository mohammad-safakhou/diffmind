package extraction

// identity.go holds the shared identity / detail-derivation helpers used across
// pipeline stages (discovery, reexamination, sharding). They are PURE free
// functions — no orchestrator receiver, no orchestrator state — so they live in
// core and can be shared once the stages become separate packages.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/entitykey"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// ShouldReexamine decides whether a seed needs Stage 2 re-examination.
// Rules (all LLM-only, no filesystem checks):
//   - confidence below minConfidence
//   - missing name or type
//   - no source_locations entries
//   - type-specific required detail fields missing (e.g. http_route
//     without method/path) AND those fields cannot be derived from the
//     entity's name. We back-fill derived fields into the entity in
//     place so the rest of the pipeline sees structured details.
//
// IMPORTANT: this function MUTATES e.Details when it can derive missing
// fields from the name (e.g. parsing "GET /accounts/{id}" into
// {method, path}). The mutation is the cheap fix for the previous
// reexamination flood: most LLM responses already encode the required
// fields in the name, just not in the details object.
func ShouldReexamine(obj objectives.Objective, e *Candidate, minConfidence float64) (string, string, bool) {
	if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Type) == "" {
		return "missing_name_or_type", "Candidate is missing name or type fields.", true
	}
	if _, ok := CanonicalObjectiveType(obj, e.Type); !ok {
		return "wrong_objective_type", "Candidate type does not match the objective type.", true
	}
	if len(e.Locations) == 0 {
		return "no_source_location", "Candidate has no source_locations entry; confirm the file and line range.", true
	}
	if e.Confidence < minConfidence {
		return "low_confidence", "Candidate confidence is below the run threshold. Re-verify or reject.", true
	}
	DeriveDetailsFromName(obj.Type, e)
	if missing := MissingRequiredDetails(obj.Type, e.Details); missing != "" {
		return "missing_required_details", "Candidate is missing required detail fields: " + missing, true
	}
	return "", "", false
}

// MissingRequiredDetails returns a comma separated list of missing field keys
// for the given objective type. Empty result means all required fields are
// present.
func MissingRequiredDetails(objType string, details map[string]any) string {
	required := map[string][]string{
		"http_route":      {"method", "path"},
		"webhook":         {"path"},
		"rpc_endpoint":    {"service", "method"},
		"queue_consumer":  {"queue"},
		"scheduled_job":   {"schedule"},
		"cli_command":     {"command"},
		"db_operation":    {"operation"},
		"outbound_http":   {"method"},
		"outbound_rpc":    {"method"},
		"queue_publish":   {"destination"},
		"cache_operation": {"operation"},
		"stream_consume":  {"stream"},
		"command_exec":    {"command"},
	}
	keys, ok := required[objType]
	if !ok {
		return ""
	}
	missing := []string{}
	for _, k := range keys {
		if !HasDetailKey(details, k) {
			missing = append(missing, k)
		}
	}
	return strings.Join(missing, ",")
}

// HTTPMethods enumerates the HTTP verbs we recognize when parsing
// route names like "GET /users/{id}".
var HTTPMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {},
	"HEAD": {}, "OPTIONS": {}, "TRACE": {}, "CONNECT": {},
}

// DeriveDetailsFromName fills in entity.Details from name/summary when
// the detail field is implicit. This is purely a pre-processing step:
// the LLM frequently emits "GET /users/{id}" as the name and leaves
// details empty, so without derivation we'd send 100% of items into
// reexamination unnecessarily.
//
// Mutations are conservative: we only fill fields that are clearly
// implied by the name's syntax for that objective type.
func DeriveDetailsFromName(objType string, e *Candidate) {
	if e == nil {
		return
	}
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	name := strings.TrimSpace(e.Name)
	switch objType {
	case "http_route", "webhook", "outbound_http":
		// Pattern: "<METHOD> <path>" or "<METHOD>:<path>"
		method, path, ok := SplitMethodPath(name)
		if ok {
			if !HasDetailKey(e.Details, "method") {
				e.Details["method"] = method
			}
			if !HasDetailKey(e.Details, "path") {
				e.Details["path"] = path
			}
		}
	case "rpc_endpoint", "outbound_rpc":
		// Pattern: "Service.Method" or "Service/Method" or "Service#Method"
		svc, meth, ok := SplitServiceMethod(name)
		if ok {
			if !HasDetailKey(e.Details, "service") {
				e.Details["service"] = svc
			}
			if !HasDetailKey(e.Details, "method") {
				e.Details["method"] = meth
			}
		}
	case "queue_consumer", "queue_publish", "stream_consume":
		// Pattern: name often IS the queue/topic/stream identifier
		// (e.g. "catalogue-target-request-sqs"). Only fall back to it
		// when the name looks like a queue identifier, not free prose.
		if LooksLikeIdentifier(name) {
			required := "queue"
			if objType == "queue_publish" {
				required = "destination"
			}
			if objType == "stream_consume" {
				required = "stream"
			}
			if !HasDetailKey(e.Details, required) {
				e.Details[required] = name
			}
		}
	case "db_operation", "cache_operation":
		// Many model responses encode the operation in the name
		// (e.g. "users_table_select", "orders.upsert", "redis SET key").
		op := GuessOperation(name, e.Summary)
		if op != "" && !HasDetailKey(e.Details, "operation") {
			e.Details["operation"] = op
		}
	case "scheduled_job":
		if !HasDetailKey(e.Details, "schedule") {
			// If the summary mentions a cron-ish token, use that;
			// otherwise leave it empty (requires the model).
			if cron := ExtractCronLike(e.Summary); cron != "" {
				e.Details["schedule"] = cron
			}
		}
	case "cli_command", "command_exec":
		if !HasDetailKey(e.Details, "command") {
			if LooksLikeCommand(name) {
				e.Details["command"] = name
			}
		}
	}
}

// SplitMethodPath parses strings like "GET /v1/users" or "POST: /charge"
// into (method, path). Returns ok=false when the prefix isn't a
// recognised HTTP verb.
func SplitMethodPath(name string) (method, path string, ok bool) {
	s := strings.TrimSpace(name)
	if s == "" {
		return "", "", false
	}
	// Allow "GET /x", "GET:/x", "GET /x  ", "GET /x (handler)", etc.
	for i, r := range s {
		if r == ' ' || r == ':' || r == '\t' {
			head := strings.ToUpper(s[:i])
			if _, isVerb := HTTPMethods[head]; isVerb {
				rest := strings.TrimLeft(s[i:], " :\t")
				// If the path itself has trailing prose ("/x (handler)"),
				// keep just the leading slash-path token.
				p := rest
				if idx := strings.IndexAny(rest, " \t("); idx >= 0 {
					p = rest[:idx]
				}
				if strings.HasPrefix(p, "/") {
					return head, p, true
				}
			}
			return "", "", false
		}
	}
	return "", "", false
}

// SplitServiceMethod parses strings like "Foo.bar", "foo/bar",
// "FooService#bar", or "fooBar.baz" into (service, method).
func SplitServiceMethod(name string) (svc, meth string, ok bool) {
	for _, sep := range []string{"#", ".", "/"} {
		if i := strings.LastIndex(name, sep); i > 0 && i < len(name)-1 {
			s := strings.TrimSpace(name[:i])
			m := strings.TrimSpace(name[i+1:])
			if s != "" && m != "" {
				return s, m, true
			}
		}
	}
	return "", "", false
}

// LooksLikeIdentifier returns true for short names without spaces — the
// kind of thing that IS a queue/topic/stream id rather than free prose.
func LooksLikeIdentifier(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 96 {
		return false
	}
	if strings.ContainsAny(s, " \t\n,;()") {
		return false
	}
	return true
}

// LooksLikeCommand returns true for tokens that look like CLI commands
// or shell pipelines.
func LooksLikeCommand(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	first := strings.IndexAny(s, " ")
	head := s
	if first > 0 {
		head = s[:first]
	}
	// Must look like an executable name: alnum, dashes, underscores,
	// dots, slashes — no parentheses or punctuation.
	for _, r := range head {
		if !(r == '-' || r == '_' || r == '.' || r == '/' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// GuessOperation extracts a CRUD-like operation keyword from name/summary.
// Returns "" when nothing obvious is present.
func GuessOperation(name, summary string) string {
	candidates := strings.ToLower(name + " " + summary)
	for _, op := range []string{
		"select", "insert", "update", "delete", "upsert",
		"read", "write", "scan", "query", "fetch", "get", "put",
		"connect", "create", "find",
	} {
		if strings.Contains(candidates, op) {
			return op
		}
	}
	return ""
}

// ExtractCronLike picks out a cron-style schedule expression from text
// (very loose match: 5–7 whitespace-separated tokens of the form
// digits/asterisks/commas/slashes/dashes).
func ExtractCronLike(s string) string {
	if s == "" {
		return ""
	}
	fields := strings.Fields(s)
	for i := 0; i+5 <= len(fields); i++ {
		end := i + 5
		if end < len(fields) && cronTokenLike(fields[end]) {
			end++
		}
		if end < len(fields) && cronTokenLike(fields[end]) {
			end++
		}
		ok := true
		for j := i; j < end; j++ {
			if !cronTokenLike(fields[j]) {
				ok = false
				break
			}
		}
		if ok && end-i >= 5 {
			return strings.Join(fields[i:end], " ")
		}
	}
	return ""
}

func cronTokenLike(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		switch {
		case r == '*' || r == ',' || r == '/' || r == '-' || r == '?':
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// HasDetailKey performs a tolerant existence check. We accept several common
// spellings so prompts that return e.g. "queue_name" instead of "queue" are
// not punished.
func HasDetailKey(details map[string]any, key string) bool {
	if details == nil {
		return false
	}
	aliases := map[string][]string{
		"method":      {"method", "http_method", "verb"},
		"path":        {"path", "route", "url_path", "endpoint_path"},
		"service":     {"service", "service_name", "rpc_service"},
		"queue":       {"queue", "queue_name", "topic", "stream"},
		"schedule":    {"schedule", "cron", "interval", "fixed_rate", "fixed_delay"},
		"command":     {"command", "command_name", "bin", "handler"},
		"operation":   {"operation", "op", "action", "verb"},
		"destination": {"destination", "queue", "topic", "queue_name", "topic_name", "arn"},
		"stream":      {"stream", "stream_name", "arn"},
	}
	candidates := aliases[key]
	if len(candidates) == 0 {
		candidates = []string{key}
	}
	for _, c := range candidates {
		if v, ok := details[c]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
				continue
			}
			return true
		}
	}
	return false
}

// DiscoverySemanticKey is the discovery-merge identity for an entity: the
// objective's semantic key (method+path for routes, resource+operation for db
// ops, ...), falling back to ShardEntityKey for objectives without a semantic
// identity.
func DiscoverySemanticKey(obj objectives.Objective, e Candidate) string {
	DeriveDetailsFromName(obj.Type, &e)
	get := func(key string) string {
		if e.Details == nil {
			return ""
		}
		if v, ok := e.Details[key]; ok {
			return strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
		}
		return ""
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := get(k); v != "" {
				return v
			}
		}
		return ""
	}
	switch obj.Type {
	case "http_route", "webhook", "outbound_http":
		method, path := get("method"), NormalizePathForKey(get("path"))
		if method != "" || path != "" {
			return strings.Join([]string{obj.ID, method, path}, "|")
		}
	case "queue_consumer", "queue_publish", "stream_consume":
		platform := get("platform")
		dest := first("queue", "topic", "destination", "stream")
		if platform != "" || dest != "" {
			return strings.Join([]string{obj.ID, platform, dest}, "|")
		}
	case "scheduled_job":
		schedule, handler := get("schedule"), get("handler")
		if schedule != "" || handler != "" {
			return strings.Join([]string{obj.ID, schedule, handler}, "|")
		}
	case "db_operation", "cache_operation":
		// High-level identity: a dependency is "<operation> on <resource>"
		// (e.g. read from orders), NOT one row per repository method. Keying
		// on (resource, operation) collapses the LLM's per-method jitter into
		// the architectural fact the extractor is after, which is also what
		// stabilises the run-to-run count.
		resource := first("table", "entity", "cache", "key", "collection", "index")
		op := get("operation")
		if resource != "" || op != "" {
			return strings.Join([]string{obj.ID, resource, op}, "|")
		}
	case "rpc_endpoint", "outbound_rpc":
		svc, meth := get("service"), get("method")
		if svc != "" || meth != "" {
			return strings.Join([]string{obj.ID, svc, meth}, "|")
		}
	case "cli_command", "command_exec":
		cmd := first("command", "invocation")
		handler := get("handler")
		if cmd != "" || handler != "" {
			return strings.Join([]string{obj.ID, cmd, handler}, "|")
		}
	}
	return ShardEntityKey(e)
}

// NormalizePathForKey defers to reconcile.CanonicalizeRoutePath so the
// discovery-merge key canonicalizes paths (incl. parameter syntax
// {id}/:id/<int:id>/*) identically to reconcile dedup and the eval matcher (C1).
func NormalizePathForKey(path string) string {
	return entitykey.CanonicalRoutePath(path)
}

// IsCompleteDeterministicSeed reports whether a deterministic exposure seed is
// complete enough to bypass re-examination (high-confidence, located, with all
// required detail fields present or derivable from its name).
func IsCompleteDeterministicSeed(obj objectives.Objective, e *Candidate) bool {
	if e == nil {
		return false
	}
	if obj.Kind != model.KindExposure {
		return false
	}
	switch obj.Type {
	case "http_route", "queue_consumer", "scheduled_job":
	default:
		return false
	}
	if !HasDeterministicEvidence(*e) {
		return false
	}
	if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Type) == "" {
		return false
	}
	if _, ok := CanonicalObjectiveType(obj, e.Type); !ok {
		return false
	}
	if len(e.Locations) == 0 || strings.TrimSpace(e.Locations[0].File) == "" || e.Locations[0].StartLine <= 0 {
		return false
	}
	if e.Confidence < 1.0 {
		return false
	}
	DeriveDetailsFromName(obj.Type, e)
	return MissingRequiredDetails(obj.Type, e.Details) == ""
}

// HasDeterministicEvidence reports whether an entity carries a deterministic
// evidence source or the "deterministic" tag.
func HasDeterministicEvidence(e Candidate) bool {
	for _, ev := range e.Evidence {
		if strings.HasPrefix(strings.TrimSpace(ev.Source), "deterministic_") {
			return true
		}
	}
	for _, tag := range e.Tags {
		if strings.TrimSpace(tag) == "deterministic" {
			return true
		}
	}
	return false
}

// ShardEntityKey is the fallback identity for an entity without an
// objective-specific semantic key: type|name|firstLocation.
func ShardEntityKey(e Candidate) string {
	file, line := "", 0
	if len(e.Locations) > 0 {
		file = e.Locations[0].File
		line = e.Locations[0].StartLine
	}
	return strings.ToLower(e.Type) + "|" + strings.ToLower(e.Name) + "|" + file + ":" + strconv.Itoa(line)
}
