package discovery

import (
	"path/filepath"
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

// ConfigValue resolves a property key across config files with profile-aware,
// DETERMINISTIC precedence (V3a — the old map-order walk made the winner a
// per-run coin flip when base and profile files disagreed, flipping resource
// names and identity keys between runs):
//
//  1. every file agrees on the value → that value;
//  2. the active profile is known (literal spring.profiles.active) and its
//     overlay defines the key consistently → the overlay's value;
//  3. the base (unprofiled) files define it and no profile file disagrees →
//     the base value;
//  4. otherwise (unknown active profile + a disagreeing override) → unresolved.
//     Spring profile properties override base ones, so the base value cannot be
//     trusted as the winner, and lexical precedence among profiles would surface
//     an inactive profile's value as truth. Callers fall back to the raw
//     placeholder / key segment, which is stable run-to-run.
func ConfigValue(idx *astpkg.ProjectIndex, key string) (string, bool) {
	key = strings.TrimSpace(key)
	if idx == nil || key == "" {
		return "", false
	}
	type match struct{ profile, value string }
	var matches []match
	distinct := map[string]struct{}{}
	for _, path := range sortedConfigPaths(idx) {
		fileProfile := configProfile(path)
		for _, e := range idx.Configs[path].Entries {
			if !strings.EqualFold(strings.TrimSpace(e.Key), key) {
				continue
			}
			// A profile-activated document inside a multi-doc file overrides
			// the file-level profile (it is more specific).
			profile := fileProfile
			if e.Profile != "" {
				profile = e.Profile
			}
			v := strings.TrimSpace(e.Value)
			matches = append(matches, match{profile: profile, value: v})
			distinct[v] = struct{}{}
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	if len(distinct) == 1 {
		return matches[0].value, true
	}
	agreedValue := func(profile string) (string, bool) {
		v, found := "", false
		for _, m := range matches {
			if m.profile != profile {
				continue
			}
			if found && m.value != v {
				return "", false // the profile disagrees with itself
			}
			v, found = m.value, true
		}
		return v, found
	}
	if active := activeConfigProfile(idx); active != "" {
		if v, ok := agreedValue(active); ok {
			return v, true
		}
	}
	if base, ok := agreedValue(""); ok {
		overridden := false
		for _, m := range matches {
			if m.profile != "" && m.value != base {
				overridden = true
				break
			}
		}
		if !overridden {
			return base, true
		}
	}
	return "", false
}

// configProfile extracts the Spring-style profile from a config filename:
// "application-prod.yml" → "prod", "application.yml" / unrelated files → ""
// (base). Helm values files follow the same convention ("values-stage.yaml").
func configProfile(path string) string {
	name := strings.ToLower(filepath.Base(filepath.ToSlash(path)))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	for _, stem := range []string{"application-", "bootstrap-", "values-"} {
		if strings.HasPrefix(name, stem) {
			return name[len(stem):]
		}
	}
	return ""
}

// activeConfigProfile returns the literal active profile when the base config
// pins exactly one ("spring.profiles.active: prod"). Placeholders, env-only
// values, and comma lists stay "" — guessing an overlay would be V3a again.
func activeConfigProfile(idx *astpkg.ProjectIndex) string {
	for _, path := range sortedConfigPaths(idx) {
		if configProfile(path) != "" {
			continue // a profile file activating itself proves nothing about runtime
		}
		for _, e := range idx.Configs[path].Entries {
			if e.Profile != "" || !strings.EqualFold(strings.TrimSpace(e.Key), "spring.profiles.active") {
				continue // an overlay document activating itself proves nothing
			}
			v := strings.TrimSpace(e.Value)
			if v == "" || IsPlaceholder(v) || strings.Contains(v, ",") {
				return ""
			}
			return v
		}
	}
	return ""
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
		case "url", "uri", "baseurl", "base-url", "name", "arn", "queue", "topic", "endpoint", "destination", "stream", "value":
			parts = parts[:len(parts)-1]
		default:
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return ""
}
