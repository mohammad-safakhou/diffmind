package discovery

import (
	"path/filepath"
	"regexp"
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
	return ResolveResourceNameDetailed(idx, trigger).Name
}

// ResourceResolution records how a resource name was obtained, so downstream
// consumers (DiffMind cross-service matching) can tell a real infrastructure
// name from a best-effort fallback and join unresolved objects on the env-var
// breadcrumb instead of a junk name.
type ResourceResolution struct {
	Name      string // final display name (may be a fallback)
	Source    string // literal | config_value | env_value | config_key | raw
	ConfigKey string // ${...} placeholder body when the trigger was a placeholder
	EnvVar    string // deployment env var the resolution chain passed through
}

// ResolveResourceNameDetailed is ResolveResourceName with provenance. Same
// resolution ladder, plus deployment env values (helm values / k8s manifests)
// for ${ENV_VAR} placeholders whose value is injected at deploy time.
func ResolveResourceNameDetailed(idx *astpkg.ProjectIndex, trigger string) ResourceResolution {
	t := strings.TrimSpace(trigger)
	body, _, ok := SplitPlaceholder(t)
	if !ok {
		return ResourceResolution{Name: t, Source: "literal"}
	}
	res := ResourceResolution{ConfigKey: body, EnvVar: envVarInChain(idx, t, 0)}
	if val, found := ResolvePlaceholder(idx, t, 0); found && val != "" && !IsPlaceholder(val) {
		res.Source = "config_value"
		if res.EnvVar != "" {
			res.Source = "env_value"
		}
		if seg := TrailingResourceSegment(val); seg != "" {
			res.Name = seg
			return res
		}
		res.Name = val
		return res
	}
	if seg := KeySegmentName(body); seg != "" {
		res.Name, res.Source = seg, "config_key"
		return res
	}
	res.Name, res.Source = t, "raw"
	return res
}

// addResolutionDetails records name provenance on a candidate's details so
// DiffMind can distinguish real infrastructure names (safe to join across
// services) from config-key fallbacks (safe to display, unsafe to join), and
// can join unresolved pairs on the shared env-var breadcrumb.
func addResolutionDetails(details map[string]any, res ResourceResolution) {
	if details == nil {
		return
	}
	if res.Source != "" {
		details["destination_source"] = res.Source
	}
	if res.ConfigKey != "" {
		details["config_key"] = res.ConfigKey
	}
	if res.EnvVar != "" {
		details["env_var"] = res.EnvVar
	}
}

// envVarInChain walks the same indirections as ResolvePlaceholder and reports
// the first deployment env-var name the chain passes through ("" when none).
// The env var is the identity two services share when the actual value is
// injected at deploy time and unresolvable from either repo.
func envVarInChain(idx *astpkg.ProjectIndex, raw string, depth int) string {
	if depth > 5 {
		return ""
	}
	body, def, ok := SplitPlaceholder(strings.TrimSpace(raw))
	if !ok {
		return ""
	}
	if looksLikeEnvVar(body) {
		return body
	}
	if val, found := ConfigValue(idx, body); found && IsPlaceholder(val) {
		return envVarInChain(idx, val, depth+1)
	}
	if def != "" && IsPlaceholder(def) {
		return envVarInChain(idx, def, depth+1)
	}
	return ""
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
	// ${ENV_VAR} values live in deployment config (helm values / manifests)
	// under nested maps a full-key ConfigValue lookup can never see. The env
	// value is more authoritative than a code-side :default.
	if looksLikeEnvVar(body) {
		if val, found := EnvConfigValue(idx, body); found {
			if IsPlaceholder(val) {
				return ResolvePlaceholder(idx, val, depth+1)
			}
			return val, true
		}
	}
	if def != "" {
		if IsPlaceholder(def) {
			return ResolvePlaceholder(idx, def, depth+1)
		}
		return def, true
	}
	return "", false
}

// looksLikeEnvVar reports whether a placeholder body names a deployment
// environment variable (UPPER_SNAKE, no dots) rather than a dotted property key.
func looksLikeEnvVar(body string) bool {
	if body == "" || strings.Contains(body, ".") {
		return false
	}
	hasAlpha := false
	for _, r := range body {
		switch {
		case r >= 'A' && r <= 'Z':
			hasAlpha = true
		case r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return hasAlpha
}

// EnvConfigValue resolves a deployment env-var name against deployment config,
// where env vars sit under nested maps (configMaps.<name>.env.VAR in helm
// values, container env lists in manifests) that full-key lookups miss: the
// entry's LAST key segment is matched instead. Helm-templated values are
// reduced to their static suffix (HelmStaticName). Precedence: the graph
// describes production, so when per-environment overlays disagree the
// production overlay wins; otherwise all sources must agree.
func EnvConfigValue(idx *astpkg.ProjectIndex, envName string) (string, bool) {
	envName = strings.TrimSpace(envName)
	if idx == nil || envName == "" {
		return "", false
	}
	type match struct{ profile, value string }
	var matches []match
	for _, path := range sortedConfigPaths(idx) {
		profile := configProfile(path)
		for _, e := range idx.Configs[path].Entries {
			key := strings.TrimSpace(e.Key)
			if i := strings.LastIndex(key, "."); i >= 0 {
				key = key[i+1:]
			}
			if !strings.EqualFold(key, envName) {
				continue
			}
			if e.Profile != "" {
				profile = e.Profile
			}
			v := HelmStaticName(strings.TrimSpace(e.Value))
			if v == "" {
				continue
			}
			matches = append(matches, match{profile: profile, value: v})
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	distinct := map[string]struct{}{}
	for _, m := range matches {
		distinct[m.value] = struct{}{}
	}
	if len(distinct) == 1 {
		return matches[0].value, true
	}
	// Environments disagree: production is the architecture of record.
	prodValue, prodFound := "", false
	for _, m := range matches {
		if m.profile != "production" && m.profile != "prod" {
			continue
		}
		if prodFound && m.value != prodValue {
			return "", false // production disagrees with itself
		}
		prodValue, prodFound = m.value, true
	}
	if prodFound {
		return prodValue, true
	}
	return "", false
}

var (
	helmTemplateRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)
	separatorRunRe = regexp.MustCompile(`[-_]{2,}`)
)

// HelmStaticName reduces a helm-templated value to its stable static suffix:
// "{{.Values.account}}-{{.Values.uuid}}-orders-events-sqs" → "orders-events-sqs".
// Per-environment template prefixes (account, uuid) are deploy-time noise; the
// static remainder is the identity infra names share across services. Values
// that are all template (no meaningful static part) resolve to "".
func HelmStaticName(val string) string {
	val = strings.TrimSpace(val)
	if !strings.Contains(val, "{{") {
		return val
	}
	stripped := helmTemplateRe.ReplaceAllString(val, "")
	if strings.Contains(stripped, "{{") || strings.Contains(stripped, "}}") {
		return "" // unbalanced/nested template: don't guess
	}
	// Mid-string templates leave doubled separators ("cdp-{{uuid}}-store" →
	// "cdp--store"); collapse them so the name matches its untemplated form.
	stripped = separatorRunRe.ReplaceAllString(stripped, "-")
	stripped = strings.Trim(stripped, "-_./ ")
	letters := 0
	for _, r := range stripped {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
		}
	}
	if letters < 4 {
		return ""
	}
	return stripped
}

// ConfigValue resolves a property key across config files with profile-aware,
// DETERMINISTIC precedence (V3a — the old map-order walk made the winner a
// per-run coin flip when base and profile files disagreed, flipping resource
// names and identity keys between runs):
//
//  1. every file agrees on the value → that value;
//  2. the active profile is known (literal spring.profiles.active) and its
//     overlay defines the key consistently → the overlay's value;
//  3. a production overlay (prod/production/live) defines the key
//     consistently → its value. The graph describes production, so the
//     production overlay is the architecture of record when environments
//     disagree;
//  4. the base (unprofiled) files define it and no profile file disagrees →
//     the base value;
//  5. otherwise (non-production overlays disagreeing) → unresolved.
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
	// The graph describes production: with no pinned active profile, a
	// self-consistent production overlay is the architecture of record even
	// when other environments disagree.
	for _, prodProfile := range []string{"prod", "production", "live"} {
		if v, ok := agreedValue(prodProfile); ok {
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
	// Helm overlay trees keep the filename fixed and vary the directory:
	// .example/config/production/values.yaml overlays .example/config/values.yaml.
	if name == "values" || name == "application" || name == "bootstrap" {
		if dir := environmentDirName(path); dir != "" {
			return dir
		}
	}
	return ""
}

// environmentDirName returns the parent directory name when it is a
// recognized deployment environment, "" otherwise. The allowlist keeps
// arbitrary directory layouts (helm/values.yaml) in the base profile.
func environmentDirName(path string) string {
	dir := strings.ToLower(filepath.Base(filepath.Dir(filepath.ToSlash(path))))
	switch dir {
	case "prod", "production", "live",
		"stage", "staging", "preprod", "pre-prod",
		"dev", "development", "sandbox",
		"test", "testing", "qa", "uat", "integration", "int":
		return dir
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
