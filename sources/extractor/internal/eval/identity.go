package eval

import (
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/reconcile"
)

// identityKey is the architectural identity of an exposure/dependency for
// match purposes. It intentionally keys on the discovery-level identity (an
// http_route IS its method+path, a db op IS its resource+operation+datastore),
// which is what "the same exposure/dependency" means for accuracy. For db/cache
// and anything unrecognised it defers to reconcile.SemanticKeyLoose so the
// matcher's notion of "same" tracks the pipeline's dedup exactly. Loose
// (file-insensitive) throughout: golden labels never pin paths.
func identityKey(b model.BaseEntity) string {
	t := strings.ToLower(strings.TrimSpace(b.Type))
	switch t {
	case "http_route", "webhook", "outbound_http":
		method := lc(detail(b, "method"))
		path := normPath(detail(b, "path"))
		if method == "" && path == "" {
			method, path = methodPathFromName(b.Name)
		}
		return strings.Join([]string{t, method, path}, "|")
	case "queue_consumer", "queue_publish", "stream_consume":
		dest := firstNonEmpty(detail(b, "queue"), detail(b, "topic"), detail(b, "destination"), detail(b, "stream"))
		if dest == "" {
			dest = lc(b.Name)
		}
		// Suffix-normalize so "<queue>" and "<queue>-consumer" are one fact,
		// identical to reconcile dedup (Item 5).
		return strings.Join([]string{t, lc(b.Platform), reconcile.NormalizeQueueDest(dest)}, "|")
	case "rpc_endpoint", "outbound_rpc":
		return strings.Join([]string{t, lc(detail(b, "service")), lc(detail(b, "method"))}, "|")
	case "cli_command", "command_exec":
		cmd := firstNonEmpty(detail(b, "command"), detail(b, "invocation"))
		if cmd == "" {
			cmd = lc(b.Name)
		}
		return strings.Join([]string{t, cmd}, "|")
	default:
		// scheduled_job, db_operation, cache_operation, and anything else.
		return reconcile.SemanticKeyLoose(b)
	}
}

// connectionPairKey is the identity of a connection: the identity of its
// endpoints. Endpoints that don't resolve to a known entity produce an
// "<unresolved>" side, so such a connection lands as a false positive.
func connectionPairKey(fromKey, toKey string) string {
	if fromKey == "" {
		fromKey = "<unresolved>"
	}
	if toKey == "" {
		toKey = "<unresolved>"
	}
	return fromKey + " => " + toKey
}

func detail(b model.BaseEntity, key string) string {
	if b.Details == nil {
		return ""
	}
	v, ok := b.Details[key]
	if !ok || v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		return ""
	}
	return s
}

func lc(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := lc(v); s != "" {
			return s
		}
	}
	return ""
}

// normPath defers to reconcile.CanonicalizeRoutePath so the matcher canonicalizes
// paths (incl. parameter syntax {id}/:id/<int:id>/*) exactly as the pipeline's
// dedup does (C1).
func normPath(p string) string {
	return reconcile.CanonicalizeRoutePath(p)
}

// methodPathFromName splits a name like "GET /orders" into ("get","/orders").
// When the first token isn't an HTTP verb, the whole name is treated as a path.
func methodPathFromName(name string) (string, string) {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) == 0 {
		return "", ""
	}
	switch strings.ToUpper(fields[0]) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "ANY":
		return strings.ToLower(fields[0]), normPath(strings.Join(fields[1:], " "))
	}
	return "", normPath(name)
}
