package discovery

import (
	"net/url"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// client_propagation.go — deterministic backbone-client → instance propagation.
//
// Instead of resolving an instance per operation (the old per-op LLM guessing),
// discovery surfaces the shared CLIENTS once (see clients.go / the
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
	stampDataOpsFromClients(idx, clients, deps)
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
	anchor := strings.TrimSpace(c.ConfigAnchor)
	if anchor == "" {
		return nil
	}
	value, ok := ConfigValue(idx, anchor)
	if !ok {
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

// stampDataOpsFromClients fans each resolved db/cache client's identity onto the
// operations that use it. Matching order: explicit details.client → single
// client of the compatible kind. Stamping reuses stampDatastoreInstance (so the
// generic-instance convergence stays identical) and additionally sets a
// per-client Platform when the op's is generic — the bit that keeps two
// same-named tables on different engines distinct downstream.
func stampDataOpsFromClients(idx *astpkg.ProjectIndex, clients []model.ConnectionClient, deps []model.Dependency) {
	byName := map[string]*model.ConnectionClient{}
	byKind := map[string][]*model.ConnectionClient{}
	for i := range clients {
		c := &clients[i]
		if c.InstanceRef == nil {
			continue
		}
		if n := strings.ToLower(strings.TrimSpace(c.LogicalName)); n != "" {
			byName[n] = c
		}
		if s := strings.ToLower(strings.TrimSpace(c.Symbol)); s != "" {
			byName[s] = c
			byName[lastTypeSegment(s)] = c
		}
		byKind[c.Kind] = append(byKind[c.Kind], c)
	}
	for i := range deps {
		d := &deps[i].BaseEntity
		opKind := ""
		switch d.Type {
		case "db_operation":
			opKind = "db"
		case "cache_operation":
			opKind = "cache"
		default:
			continue
		}
		c := matchDataClient(d, opKind, byName, byKind)
		if c == nil || c.InstanceRef == nil {
			continue
		}
		stampDatastoreInstance(idx, c.InstanceRef, d)
		if c.InstanceRef.Kind != "" && isGenericDBPlatform(d.Platform) {
			d.Platform = c.InstanceRef.Kind
			if d.Details == nil {
				d.Details = map[string]any{}
			}
			if _, ok := d.Details["database_type"]; !ok {
				d.Details["database_type"] = c.InstanceRef.Kind
			}
		}
	}
}

// matchDataClient resolves a db/cache op to its client by the LLM-provided
// details.client first, then by a unique client of the compatible kind.
func matchDataClient(d *model.BaseEntity, opKind string, byName map[string]*model.ConnectionClient, byKind map[string][]*model.ConnectionClient) *model.ConnectionClient {
	if d.Details != nil {
		if v, ok := d.Details["client"]; ok {
			if name := strings.ToLower(strings.TrimSpace(scalarOf(v))); name != "" {
				if c := byName[name]; c != nil && dataKindCompatible(opKind, c.Kind) {
					return c
				}
			}
		}
	}
	if cs := byKind[opKind]; len(cs) == 1 {
		return cs[0]
	}
	return nil
}

func dataKindCompatible(opKind, clientKind string) bool {
	return opKind == clientKind
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
