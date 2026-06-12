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
	ref.Database = databaseFromConnectionURL(idx, h.value)
	ref.LogicalName = FirstNonEmptyName(ref.Database, KeySegmentName(h.key), ref.Kind)
	if !containsPlaceholder(h.value) {
		ref.ResolvedURL = h.value
	}
	if host := hostFromConnectionURL(h.value); host != "" && !containsPlaceholder(host) {
		ref.Host = host
	}
	return ref
}

// stampDatastoreInstance attaches the repo's single datastore identity to a
// db/cache dependency and converges generic Instance spellings (config keys,
// "unknown", rendered maps, bare platform words) onto the database name.
// Specific instances the LLM resolved itself are kept.
func stampDatastoreInstance(idx *astpkg.ProjectIndex, ref *model.InstanceRef, d *model.BaseEntity) {
	if ref == nil {
		return
	}
	// cache_operation on a non-cache engine (or vice versa) is a different
	// physical system than the relational datasource; never cross-stamp.
	if d.Type == "cache_operation" && ref.Kind != "redis" {
		return
	}
	if d.InstanceRef == nil {
		r := *ref
		d.InstanceRef = &r
	}
	if genericInstance(idx, d.Instance, d.Platform) && ref.LogicalName != "" {
		d.Instance = ref.LogicalName
		if d.Details == nil {
			d.Details = map[string]any{}
		}
		d.Details["instance"] = ref.LogicalName
	}
}

// stampQueueInstance attaches broker identity to a queue/stream entity. The
// Instance field already carries the resolved logical queue name (discovery
// resolves ${...} triggers); here we find the config entry that wires that
// queue to a concrete URL and preserve it verbatim.
func stampQueueInstance(idx *astpkg.ProjectIndex, e *model.BaseEntity) {
	name := strings.TrimSpace(e.Instance)
	if IsPlaceholder(name) {
		name = ResolveResourceName(idx, name)
		if name != "" && !IsPlaceholder(name) {
			e.Instance = name
		}
	}
	if name == "" || e.InstanceRef != nil {
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
	if _, isKey := ConfigValue(idx, s); isKey {
		return true
	}
	// Dotted, path-less, space-less strings are property keys, not instances
	// (covers keys from profiles the index didn't capture).
	return strings.Contains(s, ".") && !strings.ContainsAny(s, "/ :")
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
		return ""
	}
	return strings.TrimSpace(seg)
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
