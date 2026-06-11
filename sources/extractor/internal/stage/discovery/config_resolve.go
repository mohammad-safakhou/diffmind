package discovery

import (
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// config_resolve.go turns Spring-style ${a.b.c} property placeholders — which
// frameworks like @SqsListener("${services.aws.sqs.foo.url}") leave as the raw
// resource name — into the real queue/topic name. The tree-sitter pass already
// flattened every config file into dotted (key, value) entries (idx.Configs),
// so the placeholder body IS a config key. Resolution is deterministic and
// best-effort: it never loses data (falls back to the raw placeholder).

// resolveResourceName returns a clean human resource name for a (possibly
// placeholder) framework trigger value. Order:
//  1. not a ${...} placeholder → return trimmed as-is.
//  2. resolve against idx.Configs (following a few indirections); if the value
//     is a URL/ARN, take its trailing segment, else use the value.
//  3. fall back to the last meaningful segment of the placeholder KEY itself
//     (…sqs.foo.url → "foo"), so an unset/external value still yields a name.
//  4. last resort → the raw placeholder unchanged.
func ResolveResourceName(idx *astpkg.ProjectIndex, trigger string) string {
	t := strings.TrimSpace(trigger)
	body, _, ok := SplitPlaceholder(t)
	if !ok {
		return t
	}
	if val, found := ResolvePlaceholder(idx, t, 0); found && val != "" && !IsPlaceholder(val) {
		if seg := TrailingResourceSegment(val); seg != "" {
			return seg
		}
		return val
	}
	if seg := KeySegmentName(body); seg != "" {
		return seg
	}
	return t
}

func IsPlaceholder(s string) bool {
	_, _, ok := SplitPlaceholder(strings.TrimSpace(s))
	return ok
}

// splitPlaceholder parses "${a.b.c}" or "${a.b.c:default}".
func SplitPlaceholder(s string) (body, def string, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") || len(s) < 4 {
		return "", "", false
	}
	inner := s[2 : len(s)-1]
	if i := strings.Index(inner, ":"); i >= 0 {
		return strings.TrimSpace(inner[:i]), strings.TrimSpace(inner[i+1:]), true
	}
	return strings.TrimSpace(inner), "", true
}

// resolvePlaceholder resolves ${key} (or ${key:default}) against idx.Configs,
// following up to a few indirections when the value is itself a placeholder.
func ResolvePlaceholder(idx *astpkg.ProjectIndex, raw string, depth int) (string, bool) {
	if depth > 5 {
		return "", false
	}
	body, def, ok := SplitPlaceholder(raw)
	if !ok {
		return strings.TrimSpace(raw), true
	}
	if val, found := ConfigValue(idx, body); found {
		if IsPlaceholder(val) {
			return ResolvePlaceholder(idx, val, depth+1)
		}
		return val, true
	}
	if def != "" {
		if IsPlaceholder(def) {
			return ResolvePlaceholder(idx, def, depth+1)
		}
		return def, true
	}
	return "", false
}

func ConfigValue(idx *astpkg.ProjectIndex, key string) (string, bool) {
	key = strings.TrimSpace(key)
	if idx == nil || key == "" {
		return "", false
	}
	for _, cf := range idx.Configs {
		for _, e := range cf.Entries {
			if strings.EqualFold(strings.TrimSpace(e.Key), key) {
				return strings.TrimSpace(e.Value), true
			}
		}
	}
	return "", false
}

// trailingResourceSegment extracts the queue/topic name from a URL or ARN:
// "https://sqs.../123/my-queue" → "my-queue"; "arn:aws:sqs:…:my-queue.fifo" →
// "my-queue.fifo". Returns "" for non-URL/ARN values.
func TrailingResourceSegment(val string) string {
	val = strings.TrimSpace(val)
	if val == "" || IsPlaceholder(val) {
		return ""
	}
	if !strings.Contains(val, "://") && !strings.HasPrefix(val, "arn:") {
		return ""
	}
	seg := val
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	if i := strings.LastIndex(seg, ":"); i >= 0 {
		seg = seg[i+1:]
	}
	return strings.TrimSpace(seg)
}

// keySegmentName turns a dotted property key into a resource name by taking the
// last meaningful segment, stripping trailing descriptor suffixes:
// "services.aws.sqs.catalogue-target-response-sqs.url" →
// "catalogue-target-response-sqs".
func KeySegmentName(key string) string {
	parts := strings.Split(key, ".")
	for len(parts) > 0 {
		switch strings.ToLower(parts[len(parts)-1]) {
		case "url", "uri", "name", "arn", "queue", "topic", "endpoint", "destination", "stream", "value":
			parts = parts[:len(parts)-1]
		default:
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return ""
}
