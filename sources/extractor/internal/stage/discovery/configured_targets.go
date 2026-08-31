package discovery

import (
	"net/url"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/serviceconfig"
)

type configuredHTTPTarget struct {
	serviceRef  string
	urlTemplate string
	baseURL     string
	host        string
	configKey   string
	external    bool
}

func configuredHTTPTargetForClient(idx *astpkg.ProjectIndex, c model.ConnectionClient) configuredHTTPTarget {
	cfg := loadServiceConfigForIndex(idx)
	if cfg == nil || len(cfg.HTTPTargets) == 0 {
		return configuredHTTPTarget{}
	}
	for _, target := range cfg.HTTPTargets {
		if !httpTargetMatchesClient(target, c) {
			continue
		}
		return configuredHTTPTargetFromConfig(idx, target)
	}
	return configuredHTTPTarget{}
}

func configuredHTTPTargetForOperation(idx *astpkg.ProjectIndex, handler, path string) configuredHTTPTarget {
	cfg := loadServiceConfigForIndex(idx)
	if cfg == nil || len(cfg.HTTPTargets) == 0 {
		return configuredHTTPTarget{}
	}
	for _, target := range cfg.HTTPTargets {
		if !httpTargetMatchesOperation(target, handler, path) {
			continue
		}
		return configuredHTTPTargetFromConfig(idx, target)
	}
	return configuredHTTPTarget{}
}

func loadServiceConfigForIndex(idx *astpkg.ProjectIndex) *serviceconfig.Config {
	if idx == nil || strings.TrimSpace(idx.RepoRoot) == "" {
		return nil
	}
	cfg, err := serviceconfig.Load(idx.RepoRoot)
	if err != nil {
		return nil
	}
	return cfg
}

func httpTargetMatchesClient(target serviceconfig.HTTPTargetConfig, c model.ConnectionClient) bool {
	if strings.TrimSpace(target.ServiceRef) == "" {
		return false
	}
	if sameConfigKey(target.ConfigKey, c.ConfigAnchor) {
		return true
	}
	if target.ClientClass != "" {
		for _, raw := range []string{c.LogicalName, c.Symbol, lastTypeSegment(c.Symbol)} {
			if sameSymbolName(raw, target.ClientClass) {
				return true
			}
		}
	}
	for _, alias := range target.Aliases {
		for _, raw := range []string{c.LogicalName, c.Symbol, lastTypeSegment(c.Symbol), c.ConfigAnchor} {
			if sameSymbolName(raw, alias) || sameConfigKey(raw, alias) {
				return true
			}
		}
	}
	if target.URLHost != "" && c.InstanceRef != nil {
		for _, raw := range []string{c.InstanceRef.Host, c.InstanceRef.URLTemplate, c.InstanceRef.ResolvedURL} {
			if sameURLHost(raw, target.URLHost) {
				return true
			}
		}
	}
	return false
}

func httpTargetMatchesOperation(target serviceconfig.HTTPTargetConfig, handler, path string) bool {
	if strings.TrimSpace(target.ServiceRef) == "" {
		return false
	}
	handlerClass := handlerReceiver(handler)
	matched := false
	if target.ClientClass != "" {
		matched = sameSymbolName(handlerClass, target.ClientClass) || sameSymbolName(handler, target.ClientClass)
	}
	for _, alias := range target.Aliases {
		if sameSymbolName(handlerClass, alias) || sameSymbolName(handler, alias) {
			matched = true
			break
		}
	}
	if target.PathPrefix != "" {
		p := strings.TrimSpace(path)
		prefix := strings.TrimSpace(target.PathPrefix)
		if p != "" && prefix != "" && strings.HasPrefix(p, prefix) {
			matched = true
		}
	}
	return matched
}

func configuredHTTPTargetFromConfig(idx *astpkg.ProjectIndex, target serviceconfig.HTTPTargetConfig) configuredHTTPTarget {
	out := configuredHTTPTarget{
		serviceRef: strings.TrimSpace(target.ServiceRef),
		configKey:  strings.TrimSpace(target.ConfigKey),
		external:   target.External,
	}
	if out.serviceRef == "" {
		out.serviceRef = strings.TrimSpace(target.ID)
	}
	if out.configKey != "" {
		if v, ok := ConfigValue(idx, out.configKey); ok {
			out.urlTemplate = strings.TrimSpace(v)
			out.baseURL = out.urlTemplate
		}
	}
	if out.host == "" {
		out.host = strings.TrimSpace(target.URLHost)
	}
	if out.host == "" {
		out.host = hostFromHTTPValue(out.urlTemplate)
	}
	return out
}

func configuredHTTPTargetInstanceRef(t configuredHTTPTarget, fallback model.InstanceRef) *model.InstanceRef {
	if t.serviceRef == "" {
		return nil
	}
	ref := fallback
	ref.Kind = "http"
	ref.LogicalName = normalizeConfiguredServiceRef(t.serviceRef)
	if t.urlTemplate != "" {
		ref.URLTemplate = t.urlTemplate
	}
	if t.baseURL != "" && ref.URLTemplate == "" {
		ref.URLTemplate = t.baseURL
	}
	if t.host != "" {
		ref.Host = t.host
	}
	if t.configKey != "" {
		ref.ConfigSource = t.configKey
	}
	if ref.ResolvedURL == "" && ref.URLTemplate != "" && !containsPlaceholder(ref.URLTemplate) {
		ref.ResolvedURL = ref.URLTemplate
	}
	return &ref
}

func normalizeConfiguredServiceRef(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "service.")
	raw = strings.TrimPrefix(raw, "external.")
	raw = strings.ReplaceAll(raw, "_", "-")
	return strings.Trim(raw, "-")
}

func handlerReceiver(handler string) string {
	handler = strings.TrimSpace(handler)
	if i := strings.LastIndex(handler, "."); i > 0 {
		return handler[:i]
	}
	return handler
}

func sameSymbolName(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	aLast := strings.ToLower(lastTypeSegment(a))
	bLast := strings.ToLower(lastTypeSegment(b))
	return a == b || aLast == b || a == bLast || aLast == bLast
}

func sameConfigKey(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	return a != "" && b != "" && a == b
}

func sameURLHost(raw, want string) bool {
	raw = strings.TrimSpace(raw)
	want = strings.TrimSpace(want)
	if raw == "" || want == "" {
		return false
	}
	if h := hostFromHTTPValue(raw); h != "" {
		raw = h
	}
	if h := hostFromHTTPValue(want); h != "" {
		want = h
	}
	return strings.EqualFold(raw, want)
}

func hostFromHTTPValue(raw string) string {
	raw = strings.TrimSpace(stripPlaceholderDefault(raw))
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}
