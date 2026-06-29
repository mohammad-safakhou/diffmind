package discovery

import (
	"net/url"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// client_propagation.go — deterministic backbone-client → instance propagation.
//
// Instead of resolving an instance independently per operation, discovery
// surfaces the shared CLIENTS once (see clients.go / the
// connection_client objective) and this pass:
//  1. resolves each client to a concrete model.InstanceRef from its config
//     anchor (generalizing the repo-wide singleDatastoreRef to one-per-anchor —
//     so a repo with TWO datasources resolves each independently), and
//  2. fans that identity to every operation that uses the client.
//
// It then runs the existing single-resource StampInstanceRefs as a safety net,
// so behavior is a strict superset of today's: anything not matched to a client
// still resolves exactly as before. All stamping is additive (#4) and
// config-grounded (#6); operation stamping is currently scoped to db/cache —
// the datastore case StampInstanceRefs cannot disambiguate across multiple
// stores. queue/http multi-instance remains handled by StampInstanceRefs.

// PropagateClientInstances resolves client instance refs, stamps db/cache ops
// from their client, then applies StampInstanceRefs as the fallback. Returns the
// clients with InstanceRef filled (for output/state).
func PropagateClientInstances(idx *astpkg.ProjectIndex, clients []model.ConnectionClient, exposures []model.Exposure, deps []model.Dependency) []model.ConnectionClient {
	if idx == nil {
		return clients
	}
	for i := range clients {
		if clients[i].InstanceRef == nil {
			clients[i].InstanceRef = resolveClientRef(idx, clients[i])
		}
	}
	stampOpsFromClients(idx, clients, deps)
	// Safety net + queue/http/single-datastore handling, unchanged and additive.
	StampInstanceRefs(idx, exposures, deps)
	return clients
}

// resolveClientRef resolves a client's config anchor to a concrete InstanceRef.
// Anchor-first: it looks up the SPECIFIC property the client named rather than
// scanning for "the one" URL, which is what lets multiple datastores resolve
// independently. Returns nil when the anchor is absent or its value carries no
// resolvable instance (invariant #6: stamp nothing rather than guess).
func resolveClientRef(idx *astpkg.ProjectIndex, c model.ConnectionClient) *model.InstanceRef {
	if c.Kind == "http" {
		if configured := configuredHTTPTargetForClient(idx, c); configured.serviceRef != "" {
			fallback := model.InstanceRef{}
			if c.InstanceRef != nil {
				fallback = *c.InstanceRef
			}
			if ref := configuredHTTPTargetInstanceRef(configured, fallback); ref != nil {
				return ref
			}
		}
	}
	anchor := strings.TrimSpace(c.ConfigAnchor)
	if anchor == "" {
		return nil
	}
	value, ok := ConfigValue(idx, anchor)
	if !ok {
		if c.Kind == "db" {
			if ref := configuredDBRefFromProfiles(idx, anchor); ref != nil {
				return ref
			}
		}
		if c.Kind == "http" && configKeyExists(idx, anchor) {
			logical := KeySegmentName(anchor)
			if logical == "" {
				logical = c.LogicalName
			}
			if logical == "" {
				return nil
			}
			return &model.InstanceRef{
				Kind:         "http",
				LogicalName:  logical,
				URLTemplate:  "${" + anchor + "}",
				ConfigSource: anchor,
			}
		}
		return nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed := stripPlaceholderDefault(value)
	switch c.Kind {
	case "db":
		ref := &model.InstanceRef{Kind: dbPlatformFromConfigValue(value), URLTemplate: value, ConfigSource: anchor}
		ref.Database = databaseFromConnectionURL(idx, parsed)
		ref.LogicalName = FirstNonEmptyName(ref.Database, KeySegmentName(anchor), ref.Kind)
		if !containsPlaceholder(parsed) {
			ref.ResolvedURL = parsed
		}
		if host := hostFromConnectionURL(parsed); host != "" && !containsPlaceholder(host) {
			ref.Host = host
		}
		return ref
	case "cache":
		ref := &model.InstanceRef{Kind: cachePlatformFromConfigValue(value), URLTemplate: value, ConfigSource: anchor}
		ref.LogicalName = FirstNonEmptyName(KeySegmentName(anchor), ref.Kind)
		if !containsPlaceholder(parsed) {
			ref.ResolvedURL = parsed
		}
		if host := hostFromConnectionURL(parsed); host != "" && !containsPlaceholder(host) {
			ref.Host = host
		}
		return ref
	case "http":
		ref := &model.InstanceRef{Kind: "http", URLTemplate: value, ConfigSource: anchor}
		ref.LogicalName = FirstNonEmptyName(KeySegmentName(anchor), c.LogicalName)
		if !containsPlaceholder(parsed) {
			ref.ResolvedURL = parsed
			if u, err := url.Parse(parsed); err == nil && u.Host != "" {
				ref.Host = u.Host
			}
		}
		return ref
	case "queue", "stream":
		name := resolveConfigBackedQueueName(idx, anchor)
		if name == "" {
			name = TrailingResourceSegment(parsed)
		}
		if name == "" {
			name = KeySegmentName(anchor)
		}
		if name == "" {
			return nil
		}
		ref := &model.InstanceRef{Kind: queueKind(value), LogicalName: name, URLTemplate: value, ConfigSource: anchor}
		if !containsPlaceholder(value) {
			ref.ResolvedURL = value
		}
		return ref
	}
	return nil
}

func configuredDBRefFromProfiles(idx *astpkg.ProjectIndex, anchor string) *model.InstanceRef {
	var matches []dbConfigMatch
	for _, path := range sortedConfigPaths(idx) {
		cf := idx.Configs[path]
		if cf == nil {
			continue
		}
		for _, e := range cf.Entries {
			if !strings.EqualFold(strings.TrimSpace(e.Key), strings.TrimSpace(anchor)) {
				continue
			}
			profile := configProfile(path)
			if e.Profile != "" {
				profile = e.Profile
			}
			value := strings.TrimSpace(e.Value)
			platform := dbPlatformFromConfigValue(value)
			database := databaseFromConnectionURL(idx, stripPlaceholderDefault(value))
			if platform == "" || database == "" {
				continue
			}
			matches = append(matches, dbConfigMatch{profile: profile, value: value, platform: platform, database: database})
		}
	}
	for _, profiles := range [][]string{{"production", "prod"}, {"stage", "staging"}, {""}, {"dev", "local"}} {
		ref := dbRefForProfiles(anchor, matches, profiles)
		if ref != nil {
			return ref
		}
	}
	return dbRefForProfiles(anchor, matches, nil)
}

type dbConfigMatch struct {
	profile, value, platform, database string
}

func dbRefForProfiles(anchor string, matches []dbConfigMatch, profiles []string) *model.InstanceRef {
	profileAllowed := func(profile string) bool {
		if profiles == nil {
			return true
		}
		for _, p := range profiles {
			if strings.EqualFold(profile, p) {
				return true
			}
		}
		return false
	}
	var chosen []dbConfigMatch
	for _, m := range matches {
		if profileAllowed(m.profile) {
			chosen = append(chosen, m)
		}
	}
	if len(chosen) == 0 {
		return nil
	}
	platform, database := chosen[0].platform, chosen[0].database
	for _, m := range chosen[1:] {
		if !strings.EqualFold(m.platform, platform) || !strings.EqualFold(m.database, database) {
			return nil
		}
	}
	return &model.InstanceRef{
		Kind:         platform,
		LogicalName:  database,
		Database:     database,
		URLTemplate:  "${" + anchor + "}",
		ConfigSource: anchor,
	}
}

// stampOpsFromClients fans each resolved client's identity onto every operation
// that uses it — db/cache, outbound HTTP, and queue/stream. Matching order
// (most specific first): an op-side client symbol the op names (client /
// repository_class / source_call_symbol / ...) → the client whose call site the
// op is enclosed by (call-graph receiver type) → a unique client of the
// compatible kind disambiguated by platform → the single client of the kind.
// Stamping reuses the per-objective instance writers so convergence stays
// identical; it is the linking, not the writing, that this generalizes.
func stampOpsFromClients(idx *astpkg.ProjectIndex, clients []model.ConnectionClient, deps []model.Dependency) {
	byName := map[string]*model.ConnectionClient{}
	byKind := map[string][]*model.ConnectionClient{}
	for i := range clients {
		c := &clients[i]
		if c.InstanceRef == nil {
			continue
		}
		for _, n := range clientNameKeys(c) {
			byName[n] = c
		}
		byKind[c.Kind] = append(byKind[c.Kind], c)
	}
	for i := range deps {
		d := &deps[i].BaseEntity
		opKind := opClientKind(d.Type)
		if opKind == "" {
			continue
		}
		c := matchClient(idx, d, opKind, byName, byKind)
		if c == nil || c.InstanceRef == nil {
			continue
		}
		applyClientInstance(idx, c, d, opKind)
	}
}

// opClientKind maps a dependency type to the client kind that backs it.
func opClientKind(t string) string {
	switch t {
	case "db_operation":
		return "db"
	case "cache_operation":
		return "cache"
	case "outbound_http":
		return "http"
	case "queue_publish":
		return "queue"
	case "stream_consume":
		return "stream"
	}
	return ""
}

// clientNameKeys are the lowercased aliases a client can be matched on.
func clientNameKeys(c *model.ConnectionClient) []string {
	var out []string
	add := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	add(c.LogicalName)
	add(c.Symbol)
	add(lastTypeSegment(strings.ToLower(strings.TrimSpace(c.Symbol))))
	return out
}

// matchClient links an operation to the client backing it. Each step is additive
// and high-precision; when none fires the op is left for StampInstanceRefs.
func matchClient(idx *astpkg.ProjectIndex, d *model.BaseEntity, opKind string, byName map[string]*model.ConnectionClient, byKind map[string][]*model.ConnectionClient) *model.ConnectionClient {
	compatible := func(c *model.ConnectionClient) bool {
		return c != nil && kindCompatible(opKind, c.Kind)
	}
	// 1. The op names a client/repository/datasource symbol directly.
	for _, key := range []string{"client", "client_class", "repository_class", "repository", "datasource", "source_call_symbol", "client_type", "handler"} {
		raw := scalarDetail(d, key)
		if raw == "" {
			continue
		}
		for _, v := range symbolNameVariants(raw) {
			if c := byName[v]; compatible(c) {
				return c
			}
		}
	}
	// 2. Call-graph: the receiver type at the op's call site is the client bean.
	if typ := receiverTypeAtLocation(idx, d); typ != "" {
		for _, v := range symbolNameVariants(typ) {
			if c := byName[v]; compatible(c) {
				return c
			}
		}
	}
	// 3+4. Candidates of the compatible kind; queue/stream are interchangeable.
	cs := append([]*model.ConnectionClient{}, byKind[opKind]...)
	if opKind == "queue" {
		cs = append(cs, byKind["stream"]...)
	} else if opKind == "stream" {
		cs = append(cs, byKind["queue"]...)
	}
	if len(cs) == 1 {
		if opPlat := opPlatform(d); opPlat != "" {
			if clientPlat := clientPlatform(cs[0]); clientPlat != "" && clientPlat != opPlat {
				return nil
			}
		}
		return cs[0]
	}
	if len(cs) > 1 {
		// Disambiguate two same-kind clients (e.g. a Postgres DataSource and a
		// DynamoDB client, both kind "db") by the op's resolved platform.
		if c := uniqueClientByPlatform(cs, opPlatform(d)); c != nil {
			return c
		}
	}
	return nil
}

func kindCompatible(opKind, clientKind string) bool {
	if opKind == clientKind {
		return true
	}
	// A stream client backs a queue op and vice versa (same broker family).
	return (opKind == "queue" && clientKind == "stream") || (opKind == "stream" && clientKind == "queue")
}

// symbolNameVariants yields lowercased lookup keys for a raw symbol/repository
// reference: the whole value, its last type segment, and (for "Type.method")
// the receiver type.
func symbolNameVariants(raw string) []string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return nil
	}
	out := []string{raw, lastTypeSegment(raw)}
	if i := strings.LastIndex(raw, "."); i > 0 {
		recv := raw[:i] // "orderrepository.save" → "orderrepository"
		out = append(out, recv, lastTypeSegment(recv))
	}
	return out
}

// opPlatform is the operation's resolved platform class (postgres, dynamodb,
// redis, sqs, ...), best-effort from its own fields.
func opPlatform(d *model.BaseEntity) string {
	if p := platformClass(strings.ToLower(strings.TrimSpace(d.Platform))); p != "" {
		return p
	}
	return platformClass(scalarDetail(d, "platform", "database_type", "database"))
}

// clientPlatform is a client's platform class, from its resolved InstanceRef
// kind when available, else sniffed from its symbol/framework (an SDK type names
// the engine even when no connection URL is configured — e.g. DynamoDbClient).
func clientPlatform(c *model.ConnectionClient) string {
	if c.InstanceRef != nil {
		if p := platformClass(strings.ToLower(c.InstanceRef.Kind)); p != "" {
			return p
		}
	}
	return platformClass(strings.ToLower(c.Symbol + " " + c.Framework + " " + c.LogicalName))
}

// uniqueClientByPlatform returns the one candidate whose platform matches the
// op's, or nil when the platform is unknown or the match is not unique
// (invariant #6: never guess which datastore an op belongs to).
func uniqueClientByPlatform(cs []*model.ConnectionClient, plat string) *model.ConnectionClient {
	if plat == "" {
		return nil
	}
	var hit *model.ConnectionClient
	for _, c := range cs {
		if clientPlatform(c) == plat {
			if hit != nil {
				return nil
			}
			hit = c
		}
	}
	return hit
}

// platformClass folds platform spellings into a canonical engine name. Empty for
// generic placeholders (database/jdbc/...) so they never drive a match.
func platformClass(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "postgres"):
		return "postgres"
	case strings.Contains(s, "mysql"), strings.Contains(s, "mariadb"):
		return "mysql"
	case strings.Contains(s, "dynamo"):
		return "dynamodb"
	case strings.Contains(s, "mongo"):
		return "mongodb"
	case strings.Contains(s, "cassandra"):
		return "cassandra"
	case strings.Contains(s, "redis"):
		return "redis"
	case strings.Contains(s, "memcache"):
		return "memcached"
	case strings.Contains(s, "elastic"), strings.Contains(s, "opensearch"):
		return "elasticsearch"
	case strings.Contains(s, "sqs"):
		return "sqs"
	case strings.Contains(s, "sns"):
		return "sns"
	case strings.Contains(s, "kafka"):
		return "kafka"
	case strings.Contains(s, "kinesis"):
		return "kinesis"
	}
	return ""
}

// receiverTypeAtLocation maps an op's source location to its enclosing symbol's
// call site and returns the receiver's declared type (the client bean/repository
// it calls through), using the AST field/local type maps. Best-effort: empty
// when the location can't be tied to an indexed symbol.
func receiverTypeAtLocation(idx *astpkg.ProjectIndex, d *model.BaseEntity) string {
	if idx == nil || len(d.Locations) == 0 {
		return ""
	}
	for _, loc := range d.Locations {
		caller := astpkg.EnclosingSymbolAt(idx, loc.File, loc.StartLine)
		if caller == "" {
			continue
		}
		for _, call := range idx.CallGraph[caller] {
			if call.File != loc.File || call.ReceiverRaw == "" {
				continue
			}
			if !lineWithin(int(call.Range.StartLine), loc.StartLine, loc.EndLine) {
				continue
			}
			className := caller
			if dot := strings.LastIndex(className, "."); dot > 0 {
				className = className[:dot]
			}
			if typ := idx.FieldTypes[className+"."+call.ReceiverRaw]; typ != "" {
				return typ
			}
			if typ := idx.LocalTypes[caller+"."+call.ReceiverRaw]; typ != "" {
				return typ
			}
		}
	}
	return ""
}

func lineWithin(line, start, end int) bool {
	// Candidate locations are 1-based; AST ranges are 0-based. Compare with a
	// one-line tolerance so the off-by-one never drops a match.
	return line+1 >= start-1 && line-1 <= end+1
}

// applyClientInstance writes the matched client's instance identity onto the op,
// per objective, reusing the existing convergence writers.
func applyClientInstance(idx *astpkg.ProjectIndex, c *model.ConnectionClient, d *model.BaseEntity, opKind string) {
	ref := c.InstanceRef
	switch opKind {
	case "db", "cache":
		stampDatastoreInstance(idx, ref, d)
		if ref.Kind != "" && isGenericDBPlatform(d.Platform) {
			d.Platform = ref.Kind
			if d.Details == nil {
				d.Details = map[string]any{}
			}
			if _, ok := d.Details["database_type"]; !ok {
				d.Details["database_type"] = ref.Kind
			}
		}
	case "http":
		if d.InstanceRef == nil {
			cp := *ref
			d.InstanceRef = &cp
		}
		if d.Details == nil {
			d.Details = map[string]any{}
		}
		if ref.LogicalName != "" && genericInstance(idx, d.Instance, d.Platform) {
			d.Instance = ref.LogicalName
			d.Details["instance"] = ref.LogicalName
		}
		if ref.LogicalName != "" {
			d.Details["target_service"] = ref.LogicalName
		}
		if ref.URLTemplate != "" {
			d.Details["url_template"] = ref.URLTemplate
			d.Details["base_url"] = ref.URLTemplate
		}
		if ref.ResolvedURL != "" {
			d.Details["resolved_url"] = ref.ResolvedURL
		}
		if ref.Host != "" {
			d.Details["host"] = ref.Host
		}
		if ref.ConfigSource != "" {
			d.Details["config_source"] = ref.ConfigSource
		}
	case "queue", "stream":
		if d.InstanceRef == nil {
			cp := *ref
			d.InstanceRef = &cp
		}
		if ref.LogicalName != "" {
			convergeQueueIdentity(d, ref.LogicalName)
		}
	}
}

// scalarDetail returns the first non-empty scalar detail among keys.
func scalarDetail(d *model.BaseEntity, keys ...string) string {
	if d.Details == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := d.Details[k]; ok {
			if s := strings.ToLower(strings.TrimSpace(scalarOf(v))); s != "" {
				return s
			}
		}
	}
	return ""
}

var genericDBPlatforms = map[string]bool{"": true, "database": true, "jdbc": true, "sql": true, "rdbms": true, "unknown": true}

func isGenericDBPlatform(p string) bool {
	return genericDBPlatforms[strings.ToLower(strings.TrimSpace(p))]
}

func cachePlatformFromConfigValue(v string) string {
	l := strings.ToLower(v)
	switch {
	case strings.Contains(l, "redis"):
		return "redis"
	case strings.Contains(l, "memcache"):
		return "memcached"
	}
	return ""
}

func lastTypeSegment(s string) string {
	if i := strings.LastIndexAny(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

func scalarOf(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
