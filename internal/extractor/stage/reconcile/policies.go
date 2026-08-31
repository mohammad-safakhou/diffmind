// Package reconcile contains deterministic post-processing for the
// extraction pipeline. The pipeline runs the helpers in
// this package to dedupe entities and connections, sort them deterministically
// for stable artifact IDs, and drop connections whose endpoints didn't survive
// earlier stages.
package reconcile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/entitykey"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

// Dedupe collapses entities by ID, keeping the highest-confidence version
// and merging non-empty fields from duplicates.
func DedupeExposures(in []model.Exposure) []model.Exposure {
	in = collapseJobEntrypoints(in)
	in = collapseQueueExposureVariants(in)
	in = dedupeExposuresSemantic(in)
	byID := map[string]model.Exposure{}
	for _, e := range in {
		if existing, ok := byID[e.ID]; ok {
			byID[e.ID] = mergeExposure(existing, e)
			continue
		}
		byID[e.ID] = e
	}
	out := make([]model.Exposure, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func DedupeDependencies(in []model.Dependency) []model.Dependency {
	in = dropTransportDuplicates(in)
	in = dropJunkDataDeps(in)
	in = canonicalizeDataNames(in)
	in = collapseQueueDependencyVariants(in)
	in = dedupeDependenciesSemantic(in)
	byID := map[string]model.Dependency{}
	for _, e := range in {
		if existing, ok := byID[e.ID]; ok {
			byID[e.ID] = mergeDependency(existing, e)
			continue
		}
		byID[e.ID] = e
	}
	out := make([]model.Dependency, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// dropJunkDataDeps removes db/cache dependencies whose resource is an obvious
// non-table artifact (sequences, the JPA entity_manager handle). The
// deterministic path already filters these, but applying the filter to all data
// deps keeps sequence artifacts from leaking into Protocol.
func dropJunkDataDeps(in []model.Dependency) []model.Dependency {
	out := make([]model.Dependency, 0, len(in))
	for _, d := range in {
		if (d.Type == "db_operation" || d.Type == "cache_operation") && isJunkDataResource(rawResourceDetailOrName(d)) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func isJunkDataResource(resource string) bool {
	t := strings.ToLower(strings.TrimSpace(resource))
	if t == "" {
		return false // empty handled elsewhere; don't drop unknown-resource deps
	}
	if t == "entity_manager" {
		return true
	}
	// Strip a schema qualifier before the suffix check (public.foo_id_seq).
	if _, base := splitSchemaQualified(t); base != "" {
		t = base
	}
	return strings.HasSuffix(t, "_seq") || strings.HasSuffix(t, "_id_seq") || strings.HasSuffix(t, "_sequence")
}

func rawResourceDetailOrName(d model.Dependency) string {
	if _, v := rawResourceDetail(d.BaseEntity); v != "" {
		return v
	}
	return d.Name
}

// canonicalizeDataNames rewrites a db/cache dependency name that is actually a
// caller symbol (Class.method, no spaces) into the data-fact form
// "<operation_kind> <resource>", so e.g.
// "AthenaDebugController.campaignDeliveredQuantityDebug" becomes
// "read agg_catalogue_campaign_stats" (Item 9). Names that already read like a
// data fact are left untouched.
func canonicalizeDataNames(in []model.Dependency) []model.Dependency {
	out := make([]model.Dependency, len(in))
	copy(out, in)
	for i := range out {
		d := &out[i]
		if d.Type != "db_operation" && d.Type != "cache_operation" {
			continue
		}
		name := strings.TrimSpace(d.Name)
		looksLikeSymbol := name != "" && !strings.Contains(name, " ") && strings.Contains(name, ".")
		if !looksLikeSymbol {
			continue
		}
		res := strings.ToLower(rawResourceDetailOrName(*d))
		op := dataOperation(d.BaseEntity)
		if res == "" || op == "" || res == strings.ToLower(name) {
			continue
		}
		d.Name = op + " " + res
	}
	return out
}

// collapseJobEntrypoints removes a cli_command exposure when a scheduled_job is
// declared in the SAME source file. A profile-gated CommandLineRunner batch job
// is otherwise discovered twice — once as scheduled_job (by handler name) and
// once as cli_command (by profile/command string) — and the two never dedup
// because their names differ. The scheduled_job is kept (higher priority); a
// genuine standalone CLI (e.g. the app's main launcher, in its own file) is
// unaffected (Item 4).
func collapseJobEntrypoints(in []model.Exposure) []model.Exposure {
	schedFiles := map[string]struct{}{}
	for _, e := range in {
		if e.Type == "scheduled_job" {
			for _, l := range e.Locations {
				if l.File != "" {
					schedFiles[l.File] = struct{}{}
				}
			}
		}
	}
	if len(schedFiles) == 0 {
		return in
	}
	out := make([]model.Exposure, 0, len(in))
	for _, e := range in {
		if e.Type == "cli_command" {
			drop := false
			for _, l := range e.Locations {
				if _, ok := schedFiles[l.File]; ok {
					drop = true
					break
				}
			}
			if drop {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// collapseQueueExposureVariants collapses queue_consumer exposures whose
// destination matches after suffix-normalization, so labels like "<queue>" and
// "<queue>-consumer" variants become one (Item 5). The higher-confidence entry
// is kept. Other exposure types are untouched.
func collapseQueueExposureVariants(in []model.Exposure) []model.Exposure {
	idxByKey := map[string]int{}
	out := make([]model.Exposure, 0, len(in))
	for _, e := range in {
		if e.Type != "queue_consumer" {
			out = append(out, e)
			continue
		}
		key := strings.ToLower(e.Platform) + "|" + NormalizeQueueDest(queueDestOf(e.BaseEntity))
		if i, ok := idxByKey[key]; ok {
			out[i] = chooseExposure(out[i], e)
			continue
		}
		idxByKey[key] = len(out)
		out = append(out, e)
	}
	return out
}

// collapseQueueDependencyVariants is the dependency-side counterpart for
// queue_publish / stream_consume (Item 5).
func collapseQueueDependencyVariants(in []model.Dependency) []model.Dependency {
	idxByKey := map[string]int{}
	out := make([]model.Dependency, 0, len(in))
	for _, d := range in {
		if d.Type != "queue_publish" && d.Type != "stream_consume" {
			out = append(out, d)
			continue
		}
		key := d.Type + "|" + strings.ToLower(d.Platform) + "|" + NormalizeQueueDest(queueDestOf(d.BaseEntity))
		if i, ok := idxByKey[key]; ok {
			out[i] = chooseDependency(out[i], d)
			continue
		}
		idxByKey[key] = len(out)
		out = append(out, d)
	}
	return out
}

func dedupeExposuresSemantic(in []model.Exposure) []model.Exposure {
	byKey := map[string]model.Exposure{}
	for _, e := range in {
		key := semanticKey(e.BaseEntity)
		if existing, ok := byKey[key]; ok {
			byKey[key] = chooseExposure(existing, e)
			continue
		}
		byKey[key] = e
	}
	out := make([]model.Exposure, 0, len(byKey))
	for _, e := range byKey {
		out = append(out, e)
	}
	return out
}

func dedupeDependenciesSemantic(in []model.Dependency) []model.Dependency {
	// db/cache deps with a resolvable resource go through datastore-aware
	// grouping (below); everything else uses the generic semantic key.
	var data []model.Dependency
	byKey := map[string]model.Dependency{}
	for _, d := range in {
		if (d.Type == "db_operation" || d.Type == "cache_operation") && dataResource(d.BaseEntity) != "" {
			data = append(data, d)
			continue
		}
		key := semanticKey(d.BaseEntity)
		if existing, ok := byKey[key]; ok {
			byKey[key] = chooseDependency(existing, d)
			continue
		}
		byKey[key] = d
	}
	out := make([]model.Dependency, 0, len(byKey)+len(data))
	for _, d := range byKey {
		out = append(out, d)
	}
	out = append(out, dedupeDataDependencies(data)...)
	return out
}

// dedupeDataDependencies collapses db/cache operations by (resource, operation)
// — the high-level data dependency — while PRESERVING genuinely distinct
// datastores. Within a (resource, operation) group it splits into one row per
// distinct *specific* platform (postgres vs mysql vs dynamodb), so a service
// that reads the same table name from two real databases keeps both. Rows whose
// platform is generic/unreliable ("database", "unknown", a free-text config
// dump → normalised to "") are NOT treated as a separate datastore: they merge
// into the single specific platform when there is exactly one, and only stand
// alone when no specific platform is known. This removes the earlier single-DB
// assumption (which silently merged across distinct databases) without letting
// noisy platform/instance text re-fragment one logical store.
// resolveSchemaQualifiedResources fixes the schema-qualified false-split (C4):
// a bare resource ("orders") and a schema-qualified one ("public.orders") for
// the same table key differently and would not dedup. Schema STAYS part of
// identity (so public.orders and audit.orders never merge). We only qualify a
// bare resource when its (type, operation, base-name) group contains EXACTLY
// ONE explicit schema — an unambiguous, datastore-aware merge. With two or more
// schemas the bare resource is left unresolved (no guessing, no over-merge).
//
// NOTE: condition is intentionally conservative (unique candidate). Corroborating
// against a configured default schema/datasource is a future refinement.
func resolveSchemaQualifiedResources(in []model.Dependency) []model.Dependency {
	type gkey struct{ typ, op, base string }
	schemasFor := map[gkey]map[string]struct{}{}
	for _, d := range in {
		if d.Type != "db_operation" && d.Type != "cache_operation" {
			continue
		}
		_, raw := rawResourceDetail(d.BaseEntity)
		schema, base := splitSchemaQualified(raw)
		if schema == "" {
			continue
		}
		k := gkey{d.Type, dataOperation(d.BaseEntity), singularResource(base)}
		if schemasFor[k] == nil {
			schemasFor[k] = map[string]struct{}{}
		}
		schemasFor[k][strings.ToLower(schema)] = struct{}{}
	}
	if len(schemasFor) == 0 {
		return in
	}
	out := make([]model.Dependency, len(in))
	copy(out, in)
	for i := range out {
		d := &out[i]
		if d.Type != "db_operation" && d.Type != "cache_operation" {
			continue
		}
		key, raw := rawResourceDetail(d.BaseEntity)
		if raw == "" {
			continue
		}
		if schema, _ := splitSchemaQualified(raw); schema != "" {
			continue // already qualified
		}
		k := gkey{d.Type, dataOperation(d.BaseEntity), singularResource(raw)}
		ss := schemasFor[k]
		if len(ss) != 1 {
			continue // zero or ambiguous → leave bare
		}
		var only string
		for s := range ss {
			only = s
		}
		// Clone details before mutating (the map is shared with the input).
		nd := make(map[string]any, len(d.Details)+1)
		for kk, vv := range d.Details {
			nd[kk] = vv
		}
		nd[key] = only + "." + raw
		d.Details = nd
	}
	return out
}

// rawResourceDetail returns the resource detail key in use and its raw
// (case-preserving) value, in the same priority order dataResource reads.
func rawResourceDetail(b model.BaseEntity) (key, value string) {
	if b.Details == nil {
		return "", ""
	}
	for _, k := range []string{"table", "table_or_entity", "entity", "cache", "collection", "index", "key"} {
		if v, ok := b.Details[k]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				return k, s
			}
		}
	}
	return "", ""
}

// splitSchemaQualified splits a "schema.table" name on its last dot. A bare
// name returns an empty schema.
func splitSchemaQualified(name string) (schema, base string) {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		return name[:i], name[i+1:]
	}
	return "", name
}

func dedupeDataDependencies(in []model.Dependency) []model.Dependency {
	in = resolveSchemaQualifiedResources(in)
	groups := map[string][]model.Dependency{}
	var order []string
	for _, d := range in {
		k := d.Type + "|" + dataResource(d.BaseEntity) + "|" + dataOperation(d.BaseEntity)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], d)
	}
	var out []model.Dependency
	for _, k := range order {
		rows := groups[k]
		bySpec := map[string][]model.Dependency{}
		var specs []string
		var generic []model.Dependency
		for _, d := range rows {
			p := depPlatformClass(d)
			if p == "" {
				generic = append(generic, d)
				continue
			}
			if _, ok := bySpec[p]; !ok {
				specs = append(specs, p)
			}
			bySpec[p] = append(bySpec[p], d)
		}
		switch len(specs) {
		case 0:
			out = append(out, reduceDeps(generic))
		case 1:
			out = append(out, reduceDeps(append(bySpec[specs[0]], generic...)))
		default:
			sort.Strings(specs)
			for i, p := range specs {
				grp := bySpec[p]
				if i == 0 {
					grp = append(grp, generic...)
				}
				out = append(out, reduceDeps(grp))
			}
		}
	}
	return out
}

func reduceDeps(rows []model.Dependency) model.Dependency {
	out := rows[0]
	for _, d := range rows[1:] {
		out = chooseDependency(out, d)
	}
	return out
}

func dataResource(b model.BaseEntity) string {
	return entitykey.DataResource(b)
}

func dataOperation(b model.BaseEntity) string {
	return entitykey.DataOperation(b)
}

// depPlatformClass normalises a dependency's datastore platform to a coarse,
// reliable class. Generic/placeholder values ("database", "jdbc", "unknown",
// "") collapse to "" so they never masquerade as a distinct datastore; a real
// engine name (postgres, mysql, dynamodb, ...) is preserved so multi-datastore
// services keep their distinctions.
func depPlatformClass(d model.Dependency) string {
	return entitykey.PlatformClass(d.BaseEntity)
}

func semanticKey(b model.BaseEntity) string {
	return entitykey.Semantic(b)
}

// genericSemanticKey is the resource-less identity key. withLoc toggles the
// trailing source-file component: the dedup pass keeps it (two same-named items
// in different files are distinct facts), but the exported loose variant drops
// it so a hand-authored golden label needn't pin paths.
func genericSemanticKey(b model.BaseEntity, withLoc bool) string {
	loc := ""
	if withLoc && len(b.Locations) > 0 {
		loc = b.Locations[0].File
	}
	if b.Type == "scheduled_job" || (b.Type == "cli_command" && !strings.EqualFold(b.Platform, "sqs")) {
		name := strings.ToLower(strings.TrimSpace(b.Name))
		if name != "" {
			return "exposure-job|" + name + "|" + loc
		}
	}
	// NOTE: db_operation/cache_operation with a resolvable resource are deduped
	// by dedupeDataDependencies (datastore-aware (resource, operation) grouping),
	// not here. This generic key only catches resource-less data deps.
	instance := strings.ToLower(strings.TrimSpace(b.Instance))
	operation := strings.ToLower(strings.TrimSpace(b.Operation))
	if operation == "" {
		operation = strings.ToLower(strings.TrimSpace(b.Name))
	}
	return strings.Join([]string{strings.ToLower(b.Platform), instance, operation, loc}, "|")
}

// SemanticKey returns the architectural-identity key the dedup pass uses to
// decide two entities are the same fact. It is exported so external tools — the
// eval harness in particular — can judge a "match" exactly the way the pipeline
// judges a "duplicate", instead of inventing a parallel notion of identity that
// could silently drift from the real dedup behaviour. For db/cache operations
// with a resolvable resource it mirrors the datastore-aware
// (type, resource, operation, platform-class) grouping of dedupeDataDependencies;
// everything else uses the generic semantic key (including the source file).
func SemanticKey(b model.BaseEntity) string { return entitykey.Semantic(b) }

// SemanticKeyLoose is SemanticKey without the source-file component. Golden
// labels should not have to pin file paths or line numbers to match an
// extracted item, so the eval matcher keys on the loose form. For db/cache the
// two are identical (the data key never includes a file).
func SemanticKeyLoose(b model.BaseEntity) string { return entitykey.SemanticLoose(b) }

func semanticIdentity(b model.BaseEntity, withLoc bool) string {
	if (b.Type == "db_operation" || b.Type == "cache_operation") && dataResource(b) != "" {
		cls := depPlatformClass(model.Dependency{BaseEntity: b})
		return strings.Join([]string{b.Type, dataResource(b), dataOperation(b), cls}, "|")
	}
	// Route-shaped entities key on method + canonical path so the same route in
	// different framework param syntaxes ({id} / :id / <int:id> / *) is one fact
	// (C1). Shared with the discovery-merge and eval matchers via
	// CanonicalizeRoutePath.
	switch b.Type {
	case "http_route", "webhook", "outbound_http":
		method, path := routeMethodPath(b)
		loc := ""
		if withLoc && len(b.Locations) > 0 {
			loc = b.Locations[0].File
		}
		return strings.Join([]string{b.Type, method, CanonicalizeRoutePath(path), loc}, "|")
	}
	return genericSemanticKey(b, withLoc)
}

// queueDestOf returns the queue/topic/stream destination of a messaging entity,
// from details first, then the classified instance, then the name.
func queueDestOf(b model.BaseEntity) string {
	dest := firstDetail(b, "queue", "topic", "destination", "stream")
	if dest == "" {
		dest = strings.ToLower(strings.TrimSpace(b.Instance))
	}
	if dest == "" {
		dest = strings.ToLower(strings.TrimSpace(b.Name))
	}
	return dest
}

// NormalizeQueueDest lower-cases a queue/topic/stream name and strips a trailing
// consumer/listener suffix, so a destination and its "<name>-consumer" variant
// collapse to one identity. Exported so the eval matcher keys identically.
func NormalizeQueueDest(s string) string {
	return entitykey.QueueDestination(s)
}

// routeMethodPath extracts the HTTP method and path from a route-shaped entity,
// preferring explicit details and falling back to parsing the name ("GET /x").
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
	}
	return "", b.Name
}

// CanonicalizeRoutePath normalizes an HTTP path for identity: lower-cases,
// single leading slash, collapses repeated slashes, trims a trailing slash, and
// replaces every path PARAMETER segment (:id, {id}, {id:.*}, <id>, <int:id>, *,
// **) with a uniform "{}" token. The same route written in different framework
// syntaxes therefore keys identically. Applied on all sides, so the rule only
// has to be internally consistent.
func CanonicalizeRoutePath(p string) string {
	return entitykey.CanonicalRoutePath(p)
}

func isRouteParamSegment(s string) bool {
	switch {
	case s == "*" || s == "**":
		return true
	case strings.HasPrefix(s, ":"): // Express / Rails :id
		return true
	case strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}"): // Spring / chi {id}, {id:.*}
		return true
	case strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">"): // Flask <id>, <int:id>
		return true
	}
	return false
}

// firstDetail returns the first non-empty Details value for the given keys,
// lower-cased and trimmed.
func firstDetail(b model.BaseEntity, keys ...string) string {
	if b.Details == nil {
		return ""
	}
	for _, k := range keys {
		v, ok := b.Details[k]
		if !ok || v == nil {
			continue
		}
		s := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
		if s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

// uncountableResources are words that end in "s" but are not plurals; stripping
// them would mangle the key (and could split, never merge). Best-effort English.
var uncountableResources = map[string]bool{
	"series": true, "species": true, "news": true, "data": true, "media": true,
	"metadata": true, "schema": true, "status": true, "alias": true,
	"analysis": true, "diagnosis": true, "basis": true, "index": true,
}

// singularResource normalises a table/entity name to a singular form for keying
// so plural/singular variants of the same table — "orders" vs the deterministic
// deriver's entity-derived "order" — collapse. Deliberately
// CONSERVATIVE: it is a best-effort English heuristic that errs toward leaving
// a word unchanged (failure direction = "don't merge", never a wrong merge).
// It skips short words, known uncountables, and Latin-ish endings (-ss, -us,
// -is, -ous) that merely look plural ("status", "address", "analysis").
func singularResource(s string) string {
	return entitykey.DataResource(model.BaseEntity{Details: map[string]any{"table": s}})
}

// normalizeDBOp folds operation verbs into read/write classes so equivalent
// operations from different sources ("SELECT", AST "read") collapse. Verbs
// it does not recognise (e.g. a cache "evict") pass through unchanged.
// NormalizeDBOp folds a raw data verb to the canonical operation_kind
// (read/write), passing through unrecognized verbs (e.g. cache evict/expire,
// custom finders). Exported so the classify stage emits the same canonical kind
// the identity/dedup uses — one definition of read vs write (C5).
func NormalizeDBOp(op string) string { return entitykey.NormalizeDBOperation(op) }

func normalizeDBOp(op string) string {
	return entitykey.NormalizeDBOperation(op)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func dropTransportDuplicates(in []model.Dependency) []model.Dependency {
	hasQueuePublish := false
	hasAthenaDB := false
	for _, d := range in {
		if d.Type == "queue_publish" && strings.EqualFold(d.Platform, "sqs") {
			hasQueuePublish = true
		}
		if d.Type == "db_operation" && (strings.EqualFold(d.Platform, "athena") || containsFold(d.Name, "athena")) {
			hasAthenaDB = true
		}
	}
	if !hasQueuePublish && !hasAthenaDB {
		return in
	}
	out := make([]model.Dependency, 0, len(in))
	for _, d := range in {
		if d.Type == "outbound_http" && hasQueuePublish && (strings.EqualFold(d.Platform, "sqs") || containsFold(d.Name, "sqs") || containsFold(d.Operation, "sendmessage")) {
			continue
		}
		if d.Type == "outbound_http" && hasAthenaDB && (strings.EqualFold(d.Platform, "athena") || containsFold(d.Name, "athena") || containsFold(d.Operation, "athena")) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func chooseExposure(a, b model.Exposure) model.Exposure {
	pa, pb := exposurePriority(a.Type), exposurePriority(b.Type)
	if pb > pa || (pb == pa && b.Confidence > a.Confidence) {
		b.BaseEntity = mergeBase(b.BaseEntity, a.BaseEntity)
		return b
	}
	a.BaseEntity = mergeBase(a.BaseEntity, b.BaseEntity)
	return a
}

func chooseDependency(a, b model.Dependency) model.Dependency {
	pa, pb := dependencyPriority(a.Type), dependencyPriority(b.Type)
	if pb > pa || (pb == pa && b.Confidence > a.Confidence) {
		b.BaseEntity = mergeBase(b.BaseEntity, a.BaseEntity)
		return b
	}
	a.BaseEntity = mergeBase(a.BaseEntity, b.BaseEntity)
	return a
}

func exposurePriority(t string) int {
	switch t {
	case "queue_consumer", "scheduled_job", "webhook", "rpc_endpoint":
		return 30
	case "http_route":
		return 20
	case "cli_command":
		return 10
	default:
		return 1
	}
}

func dependencyPriority(t string) int {
	switch t {
	case "queue_publish":
		return 40
	case "outbound_http", "outbound_rpc", "workflow_orchestration", "db_operation", "cache_operation":
		return 30
	case "stream_consume":
		return 20
	case "command_exec":
		return 10
	default:
		return 1
	}
}

// FilterConnections drops connections whose from/to IDs do not resolve to a
// known exposure/dependency, sorts the rest deterministically, and returns
// (kept, dropped).
func FilterConnections(
	conns []model.Connection,
	exposures []model.Exposure,
	dependencies []model.Dependency,
) ([]model.Connection, []model.UnresolvedItem) {
	expIDs := map[string]struct{}{}
	for _, e := range exposures {
		expIDs[e.ID] = struct{}{}
	}
	depIDs := map[string]struct{}{}
	for _, d := range dependencies {
		depIDs[d.ID] = struct{}{}
	}
	kept := make([]model.Connection, 0, len(conns))
	dropped := make([]model.UnresolvedItem, 0)
	seen := map[string]struct{}{}
	for _, c := range conns {
		if _, ok := expIDs[c.FromExposureID]; !ok {
			dropped = append(dropped, model.UnresolvedItem{
				Kind: model.KindDependency, Type: "connection",
				Name:       c.FromExposureID + " -> " + c.ToDependencyID,
				ReasonCode: "orphan_connection",
				Reason:     "from_exposure_id did not resolve to a known exposure after reconcile",
				Confidence: c.Confidence,
			})
			continue
		}
		if _, ok := depIDs[c.ToDependencyID]; !ok {
			dropped = append(dropped, model.UnresolvedItem{
				Kind: model.KindDependency, Type: "connection",
				Name:       c.FromExposureID + " -> " + c.ToDependencyID,
				ReasonCode: "orphan_connection",
				Reason:     "to_dependency_id did not resolve to a known dependency after reconcile",
				Confidence: c.Confidence,
			})
			continue
		}
		if _, dup := seen[c.ID]; dup {
			continue
		}
		seen[c.ID] = struct{}{}
		kept = append(kept, c)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	return kept, dropped
}

func DedupeUnresolved(in []model.UnresolvedItem) []model.UnresolvedItem {
	seen := map[string]struct{}{}
	out := make([]model.UnresolvedItem, 0, len(in))
	for _, u := range in {
		key := string(u.Kind) + "|" + u.Type + "|" + u.Name + "|" + u.ReasonCode
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ReasonCode < out[j].ReasonCode
	})
	return out
}

func DedupeWarnings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// mergeExposure picks the higher-confidence exposure as the base and fills in
// missing slices from the other. It is tolerant to the same entity being
// reported twice by different stages.
func mergeExposure(a, b model.Exposure) model.Exposure {
	base, other := a, b
	if b.Confidence > a.Confidence {
		base, other = b, a
	}
	base.BaseEntity = mergeBase(base.BaseEntity, other.BaseEntity)
	return base
}

func mergeDependency(a, b model.Dependency) model.Dependency {
	base, other := a, b
	if b.Confidence > a.Confidence {
		base, other = b, a
	}
	base.BaseEntity = mergeBase(base.BaseEntity, other.BaseEntity)
	return base
}

func mergeBase(base, other model.BaseEntity) model.BaseEntity {
	if strings.TrimSpace(base.Summary) == "" {
		base.Summary = other.Summary
	}
	if strings.TrimSpace(base.Platform) == "" {
		base.Platform = other.Platform
	}
	if strings.TrimSpace(base.Instance) == "" {
		base.Instance = other.Instance
	}
	if base.InstanceRef == nil {
		base.InstanceRef = other.InstanceRef
	}
	if strings.TrimSpace(base.Operation) == "" {
		base.Operation = other.Operation
	}
	if strings.TrimSpace(base.OperationKind) == "" {
		base.OperationKind = other.OperationKind
	}
	if len(base.KeyActions) == 0 {
		base.KeyActions = other.KeyActions
	}
	if len(base.Inputs) == 0 {
		base.Inputs = other.Inputs
	}
	if len(base.Tags) == 0 {
		base.Tags = other.Tags
	}
	if len(base.Locations) == 0 {
		base.Locations = other.Locations
	}
	if len(base.Evidence) == 0 {
		base.Evidence = other.Evidence
	}
	if len(base.Details) == 0 {
		base.Details = other.Details
	} else {
		for k, v := range other.Details {
			if _, ok := base.Details[k]; !ok {
				base.Details[k] = v
			}
		}
	}
	return base
}
