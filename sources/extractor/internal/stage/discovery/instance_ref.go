package discovery

import (
	"net/url"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// instance_ref.go — concrete instance identity stamping (the downstream contract).
//
// DiffMind's output feeds a cross-service graph that joins "service A talks to
// service B" on the CONCRETE instance both sides reference: the same queue, the
// same database. This pass derives a structured model.InstanceRef from the
// repo's parsed config and converges the free-text Instance field onto the
// resolved logical name, so one physical thing never appears under several
// spellings — real runs emitted the config property name ("spring.datasource.url"),
// "unknown", and a rendered Go map for the SAME database.
//
// Precision rule (invariant #6): stamp only what the config index supports
// unambiguously — a single configured datastore, a single matching queue URL.
// When in doubt, leave the entity untouched.

// StampInstanceRefs attaches InstanceRef identity to queue, datastore, and
// outbound-HTTP entities. Runs after detail (entities final, config index
// available) and is idempotent; it never removes or re-identifies an entity
// (invariant #4 — enrichment is additive).
func StampInstanceRefs(idx *astpkg.ProjectIndex, exposures []model.Exposure, deps []model.Dependency) {
	if idx == nil {
		return
	}
	datastore := singleDatastoreRef(idx)
	for i := range exposures {
		e := &exposures[i].BaseEntity
		if e.Type == "queue_consumer" {
			stampQueueInstance(idx, e)
		}
	}
	for i := range deps {
		d := &deps[i].BaseEntity
		switch d.Type {
		case "db_operation", "cache_operation":
			stampDatastoreInstance(idx, datastore, d)
		case "queue_publish", "stream_consume":
			stampQueueInstance(idx, d)
		case "outbound_http":
			stampOutboundHTTPInstance(idx, d)
		}
	}
}

// singleDatastoreRef finds the one concrete datastore the repo's config wires
// (a jdbc/mongodb/redis connection URL). Several distinct URLs → nil: an
// ambiguous stamp would silently merge real databases (invariant #5/#6).
func singleDatastoreRef(idx *astpkg.ProjectIndex) *model.InstanceRef {
	type hit struct{ path, key, value string }
	var hits []hit
	distinct := map[string]struct{}{}
	for _, path := range sortedConfigPaths(idx) {
		for _, e := range idx.Configs[path].Entries {
			v := strings.TrimSpace(e.Value)
			if dbPlatformFromConfigValue(v) == "" || !strings.Contains(v, "://") {
				continue
			}
			if _, seen := distinct[v]; !seen {
				distinct[v] = struct{}{}
				hits = append(hits, hit{path: path, key: e.Key, value: v})
			}
		}
	}
	if len(hits) != 1 {
		return nil
	}
	h := hits[0]
	ref := &model.InstanceRef{
		Kind:         dbPlatformFromConfigValue(h.value),
		URLTemplate:  h.value,
		ConfigSource: h.path + ": " + h.key,
	}
	// A value wrapped whole in "${VAR:jdbc:...}" parses via its inline default;
	// the template field still carries the wrapper verbatim.
	parsed := stripPlaceholderDefault(h.value)
	ref.Database = databaseFromConnectionURL(idx, parsed)
	ref.LogicalName = FirstNonEmptyName(ref.Database, KeySegmentName(h.key), ref.Kind)
	if !containsPlaceholder(parsed) {
		ref.ResolvedURL = parsed
	}
	if host := hostFromConnectionURL(parsed); host != "" && !containsPlaceholder(host) {
		ref.Host = host
	}
	return ref
}

// stampDatastoreInstance attaches the repo's single datastore identity to a
// db/cache dependency and converges generic Instance spellings (config keys,
// "unknown", rendered maps, bare platform words) onto the database name.
// Specific instances that were already resolved are kept.
func stampDatastoreInstance(idx *astpkg.ProjectIndex, ref *model.InstanceRef, d *model.BaseEntity) {
	if ref == nil {
		return
	}
	// cache_operation on a non-cache engine (or vice versa) is a different
	// physical system than the relational datasource; never cross-stamp.
	if d.Type == "cache_operation" && ref.Kind != "redis" {
		return
	}
	if opPlat := opPlatform(d); opPlat != "" {
		if refPlat := platformClass(ref.Kind); refPlat != "" && refPlat != opPlat {
			return
		}
	}
	if d.InstanceRef == nil {
		r := *ref
		d.InstanceRef = &r
	}
	generic := genericInstance(idx, d.Instance, d.Platform) || instanceIsResourceFallback(d)
	if generic && ref.LogicalName != "" {
		d.Instance = ref.LogicalName
		if d.Details == nil {
			d.Details = map[string]any{}
		}
		d.Details["instance"] = ref.LogicalName
	}
}

// instanceIsResourceFallback reports whether Instance merely repeats the
// table/entity/cache resource (classify's last-resort fallback when no real
// instance detail exists). The resource lives in the identity key already; as
// an instance it would split services that share a database but were detected
// via different tables.
func instanceIsResourceFallback(d *model.BaseEntity) bool {
	s := strings.ToLower(strings.TrimSpace(d.Instance))
	if s == "" || d.Details == nil {
		return false
	}
	for _, key := range []string{"table", "entity", "cache", "collection", "index"} {
		if v, ok := d.Details[key].(string); ok && strings.ToLower(strings.TrimSpace(v)) == s {
			return true
		}
	}
	return false
}

// stampQueueInstance attaches broker identity to a queue/stream entity and
// converges Instance and details.queue onto the config-backed logical queue
// name, so spelling variants of one physical queue share identity and the
// reconcile variant collapse can merge them. Prose never enters LogicalName;
// when nothing resolves to a clean token the entity keeps its fields and gets
// no ref (invariant #6).
func stampQueueInstance(idx *astpkg.ProjectIndex, e *model.BaseEntity) {
	if e.InstanceRef != nil {
		return
	}
	name := resolveQueueLogicalName(idx, e)
	if name == "" {
		return
	}
	ref := &model.InstanceRef{Kind: queueKind(e.Platform), LogicalName: name}
	if path, key, value, ok := singleQueueURL(idx, name); ok {
		ref.URLTemplate = value
		ref.ConfigSource = path + ": " + key
		if !containsPlaceholder(value) {
			ref.ResolvedURL = value
		}
		if ref.Kind == "" {
			ref.Kind = queueKind(value)
		}
	}
	e.InstanceRef = ref
	convergeQueueIdentity(e, name)
}

// queueDestinationDetailKeys are the detail fields that may carry the queue
// destination, in trust order (most identity-like first).
var queueDestinationDetailKeys = []string{
	"queue", "destination", "topic", "stream", "queue_name", "queue_url_property", "queue_url",
}

// resolveQueueLogicalName finds a clean queue name. Config-grounded candidates
// win over free-text Instance so descriptive labels such as
// "target-calculation-events-sqs-consumer" converge onto the physical queue
// named by its exact config property. A clean Instance remains the fallback
// when config carries no stronger evidence.
func resolveQueueLogicalName(idx *astpkg.ProjectIndex, e *model.BaseEntity) string {
	candidates := []string{e.Instance}
	for _, k := range queueDestinationDetailKeys {
		if v, ok := e.Details[k].(string); ok {
			candidates = append(candidates, v)
		}
	}
	for _, c := range candidates {
		if n := resolveConfigBackedQueueName(idx, c); n != "" {
			return n
		}
	}
	if cleanResourceToken(e.Instance) {
		return strings.TrimSpace(e.Instance)
	}
	for _, c := range candidates[1:] {
		if cleanResourceToken(c) {
			return strings.TrimSpace(c)
		}
	}
	return ""
}

func resolveConfigBackedQueueName(idx *astpkg.ProjectIndex, raw string) string {
	candidate := strings.TrimSpace(raw)
	if candidate == "" || noneSentinel(candidate) {
		return ""
	}
	if !cleanResourceToken(candidate) {
		candidate = firstProseToken(candidate)
	}
	switch {
	case IsPlaceholder(candidate):
		key, _, _ := SplitPlaceholder(candidate)
		if v, ok := ResolvePlaceholder(idx, candidate, 0); ok {
			if seg := TrailingResourceSegment(v); cleanResourceToken(seg) {
				return seg
			}
			if cleanResourceToken(v) {
				return v
			}
		}
		if configKeyExists(idx, key) {
			return KeySegmentName(key)
		}
	case looksLikePropertyKey(candidate):
		if !configKeyExists(idx, candidate) {
			return ""
		}
		if v, ok := ConfigValue(idx, candidate); ok {
			if seg := TrailingResourceSegment(stripPlaceholderDefault(v)); cleanResourceToken(seg) {
				return seg
			}
			if cleanResourceToken(v) {
				return v
			}
		}
		return KeySegmentName(candidate)
	case strings.Contains(candidate, "://") || strings.HasPrefix(candidate, "arn:"):
		if seg := configuredQueueURLSegment(idx, candidate); cleanResourceToken(seg) {
			return seg
		}
	}
	return ""
}

func configKeyExists(idx *astpkg.ProjectIndex, key string) bool {
	if idx == nil || strings.TrimSpace(key) == "" {
		return false
	}
	for _, path := range sortedConfigPaths(idx) {
		for _, e := range idx.Configs[path].Entries {
			if strings.EqualFold(strings.TrimSpace(e.Key), strings.TrimSpace(key)) {
				return true
			}
		}
	}
	return false
}

// configuredQueueURLSegment accepts a URL/ARN only when it occurs in config
// and every exact occurrence names the same trailing resource.
func configuredQueueURLSegment(idx *astpkg.ProjectIndex, raw string) string {
	raw = strings.TrimSpace(raw)
	var found string
	for _, path := range sortedConfigPaths(idx) {
		for _, e := range idx.Configs[path].Entries {
			v := strings.TrimSpace(stripPlaceholderDefault(e.Value))
			if !strings.EqualFold(v, raw) {
				continue
			}
			seg := TrailingResourceSegment(v)
			if seg == "" || found != "" && !strings.EqualFold(found, seg) {
				return ""
			}
			found = seg
		}
	}
	return found
}

func cleanResourceToken(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" &&
		!noneSentinel(s) &&
		!strings.ContainsAny(s, " \t\r\n") &&
		!strings.Contains(s, "->") &&
		!strings.Contains(s, "://") &&
		!strings.HasPrefix(s, "arn:") &&
		!IsPlaceholder(s)
}

func noneSentinel(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	normalized := strings.Trim(s, "_.- ")
	switch normalized {
	case "none", "n/a", "na", "null", "":
		return true
	}
	normalized = strings.NewReplacer("_", "-", " ", "-").Replace(normalized)
	return normalized == "not-found" ||
		strings.HasSuffix(normalized, "-none") ||
		strings.HasPrefix(normalized, "no-") && strings.HasSuffix(normalized, "-found")
}

func firstProseToken(s string) string {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], ",;()")
}

func looksLikePropertyKey(s string) bool {
	s = strings.TrimSpace(s)
	return strings.Contains(s, ".") &&
		!strings.ContainsAny(s, " \t\r\n/:") &&
		!IsPlaceholder(s)
}

// convergeQueueIdentity rewrites the identity-bearing fields onto the resolved
// logical name so dedup sees one spelling per physical queue. Original values
// are preserved under *_raw / instance_note; the operation text is rebuilt
// when it embedded the old spelling.
func convergeQueueIdentity(e *model.BaseEntity, name string) {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	if old := strings.TrimSpace(e.Instance); old != name {
		if !cleanResourceToken(old) && old != "" {
			e.Details["instance_note"] = old
		}
		e.Instance = name
	}
	e.Details["instance"] = name
	if old, ok := e.Details["queue"].(string); ok && strings.TrimSpace(old) != name {
		e.Details["queue_raw"] = old
		e.Details["queue"] = name
	}
	for _, verb := range []string{"consume ", "publish "} {
		if strings.HasPrefix(e.Operation, verb) && e.Operation != verb+name {
			e.Operation = verb + name
			e.Details["operation_normalized"] = e.Operation
			break
		}
	}
}

// stampOutboundHTTPInstance attaches the configured base URL of the target
// service. Multiple distinct URLs (typically per-environment profiles) are
// left for the profile-aware config model (V3a); ambiguity → logical name only.
func stampOutboundHTTPInstance(idx *astpkg.ProjectIndex, d *model.BaseEntity) {
	name := strings.TrimSpace(d.Instance)
	if name == "" || name == "unknown" || d.InstanceRef != nil {
		return
	}
	ref := &model.InstanceRef{Kind: "http", LogicalName: name}
	if path, key, value, ok := singleServiceURL(idx, name); ok {
		ref.URLTemplate = value
		ref.ConfigSource = path + ": " + key
		if !containsPlaceholder(value) {
			ref.ResolvedURL = value
			if u, err := url.Parse(value); err == nil && u.Host != "" {
				ref.Host = u.Host
			}
		}
	}
	d.InstanceRef = ref
}

// singleQueueURL finds the one config entry whose URL/ARN value names this
// queue (trailing segment match) or whose property key names it. Several
// distinct URLs → not ok.
func singleQueueURL(idx *astpkg.ProjectIndex, queue string) (path, key, value string, ok bool) {
	var matches int
	for _, p := range sortedConfigPaths(idx) {
		for _, e := range idx.Configs[p].Entries {
			v := strings.TrimSpace(e.Value)
			if !strings.Contains(v, "://") && !strings.HasPrefix(v, "arn:") {
				continue
			}
			named := strings.EqualFold(TrailingResourceSegment(stripPlaceholderDefault(v)), queue) ||
				strings.EqualFold(KeySegmentName(e.Key), queue)
			if !named {
				continue
			}
			if value != "" && value != v {
				return "", "", "", false // two different URLs claim this queue
			}
			if value == "" {
				path, key, value = p, e.Key, v
			}
			matches++
		}
	}
	return path, key, value, matches > 0
}

// singleServiceURL finds the one http(s) base URL whose property key mentions
// the target service name.
func singleServiceURL(idx *astpkg.ProjectIndex, service string) (path, key, value string, ok bool) {
	needle := normalizeServiceName(service)
	if needle == "" {
		return "", "", "", false
	}
	for _, p := range sortedConfigPaths(idx) {
		for _, e := range idx.Configs[p].Entries {
			v := strings.TrimSpace(e.Value)
			if !strings.HasPrefix(strings.ToLower(stripPlaceholderDefault(v)), "http") {
				continue
			}
			if !strings.Contains(normalizeServiceName(e.Key), needle) {
				continue
			}
			if value != "" && value != v {
				return "", "", "", false // per-environment variants; defer to V3a profiles
			}
			if value == "" {
				path, key, value = p, e.Key, v
			}
		}
	}
	return path, key, value, value != ""
}

// genericInstance reports whether the current Instance carries no concrete
// identity: empty/unknown, the bare platform word (classify's fallback), a
// config property key, or a rendered Go map — all observed in real runs.
func genericInstance(idx *astpkg.ProjectIndex, instance, platform string) bool {
	s := strings.TrimSpace(instance)
	switch strings.ToLower(s) {
	case "", "unknown", "database", strings.ToLower(strings.TrimSpace(platform)):
		return true
	}
	if strings.HasPrefix(s, "map[") {
		return true
	}
	if looksLikeHTTPOperationLabel(s) || looksLikeHTTPMethodSlug(s) {
		return true
	}
	if _, isKey := ConfigValue(idx, s); isKey {
		return true
	}
	// Dotted, path-less, space-less strings are property keys, not instances
	// (covers keys from profiles the index didn't capture).
	return strings.Contains(s, ".") && !strings.ContainsAny(s, "/ :")
}

func looksLikeHTTPOperationLabel(raw string) bool {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) < 2 {
		return false
	}
	switch strings.ToUpper(fields[0]) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return strings.HasPrefix(fields[1], "/")
	default:
		return false
	}
}

func looksLikeHTTPMethodSlug(raw string) bool {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "service.")
	raw = strings.ReplaceAll(strings.ToLower(raw), "_", "-")
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
		if raw == method || strings.HasPrefix(raw, method+"-") {
			return true
		}
	}
	return false
}

// databaseFromConnectionURL extracts the database name (last path segment) from
// a connection URL template, resolving a ${KEY:default} segment via the config
// index or its inline default so "jdbc:postgresql://${HOST}:${PORT}/${NAME:app_db}"
// yields "app_db".
func databaseFromConnectionURL(idx *astpkg.ProjectIndex, raw string) string {
	rest := strings.TrimPrefix(strings.TrimSpace(raw), "jdbc:")
	i := strings.Index(rest, "://")
	if i < 0 {
		return ""
	}
	rest = rest[i+3:]
	if q := strings.IndexAny(rest, "?;"); q >= 0 {
		rest = rest[:q]
	}
	slash := strings.Index(rest, "/")
	if slash < 0 || slash == len(rest)-1 {
		return ""
	}
	seg := rest[strings.LastIndex(rest, "/")+1:]
	if IsPlaceholder(seg) {
		if v, ok := ResolvePlaceholder(idx, seg, 0); ok && !IsPlaceholder(v) {
			return strings.TrimSpace(v)
		}
		if name := databaseNameFromEnvPlaceholder(seg); name != "" {
			return name
		}
		return ""
	}
	return strings.TrimSpace(seg)
}

func databaseNameFromEnvPlaceholder(seg string) string {
	body, _, ok := SplitPlaceholder(seg)
	if !ok {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(body))
	name = strings.NewReplacer("-", "_", ".", "_").Replace(name)
	for _, suffix := range []string{"_database_name", "_database", "_db_name", "_dbname"} {
		if strings.HasSuffix(name, suffix) {
			name = strings.TrimSuffix(name, suffix)
			break
		}
	}
	name = strings.Trim(name, "_")
	switch name {
	case "", "db", "database", "postgres", "postgresql", "mysql":
		return ""
	default:
		return name
	}
}

// hostFromConnectionURL returns the host[:port] part of a connection URL.
func hostFromConnectionURL(raw string) string {
	rest := strings.TrimPrefix(strings.TrimSpace(raw), "jdbc:")
	i := strings.Index(rest, "://")
	if i < 0 {
		return ""
	}
	rest = rest[i+3:]
	if j := strings.IndexAny(rest, "/?;"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// stripPlaceholderDefault unwraps "${KEY:default}" to its inline default so URL
// shape checks and trailing-segment matching see the concrete fallback value.
func stripPlaceholderDefault(v string) string {
	if _, def, ok := SplitPlaceholder(v); ok && def != "" {
		return def
	}
	return v
}

func containsPlaceholder(s string) bool {
	return strings.Contains(s, "${")
}

func queueKind(hint string) string {
	t := strings.ToLower(hint)
	for _, k := range []string{"sqs", "sns", "kafka", "rabbitmq", "kinesis"} {
		if strings.Contains(t, k) {
			return k
		}
	}
	return ""
}

// normalizeServiceName lowercases and strips separators so "salesforce-account-api"
// matches the property key "services.salesforceAccountApi.url".
func normalizeServiceName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FirstNonEmptyName returns the first non-blank value ("" when all are blank —
// unlike extraction.FirstNonEmpty it never substitutes "unknown", because an
// InstanceRef field must hold a fact or stay empty).
func FirstNonEmptyName(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// sortedConfigPaths gives a deterministic iteration order over the config map —
// map-order iteration here is exactly the V3a variance bug class.
func sortedConfigPaths(idx *astpkg.ProjectIndex) []string {
	paths := make([]string, 0, len(idx.Configs))
	for p := range idx.Configs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
