package security

import (
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Context struct {
	TenantID  string
	Principal string
	Roles     []string
	Scopes    []string
	Attrs     map[string]string
}

type Action string

const (
	ActionQueryEntities Action = "query_entities"
	ActionQueryGraph    Action = "query_graph"
	ActionReadEvidence  Action = "read_evidence"
	ActionBuildGraph    Action = "build_graph"
	ActionCompareGraph  Action = "compare_graph"
	ActionDeleteCompare Action = "delete_compare"
	ActionAuditRead     Action = "audit_read"
	ActionAuditExport   Action = "audit_export"
	ActionAuditPrune    Action = "audit_prune"
	ActionOperateOps    Action = "operate_ops"
	ActionRuntimePlan   Action = "runtime_plan"
	ActionRuntimeRun    Action = "runtime_reconcile"
)

type Request struct {
	Action         Action
	ResourceTenant string
	Method         string
	Path           string
	Sensitive      bool
	Mutating       bool
}

type Decision struct {
	Allow  bool
	Reason string
}

func ContextFromHeaders(h http.Header) (Context, error) {
	cfg := loadAuthConfigFromEnv()
	return ContextFromHeadersWithConfig(h, cfg)
}

func ContextFromHeadersWithConfig(h http.Header, cfg AuthConfig) (Context, error) {
	cfg = normalizeAuthConfig(cfg)
	mode := cfg.Mode
	switch mode {
	case AuthModeHeader:
		return contextFromDiffMindHeaders(h)
	case AuthModeJWT:
		return contextFromJWTHeader(h, cfg)
	case AuthModeAuto:
		authz := strings.TrimSpace(h.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
			return contextFromJWTHeader(h, cfg)
		}
		return contextFromDiffMindHeaders(h)
	default:
		return Context{}, errors.New("unsupported auth mode")
	}
}

func (c Context) HasRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	for _, r := range c.Roles {
		if strings.ToLower(strings.TrimSpace(r)) == role {
			return true
		}
	}
	return false
}

func (c Context) HasScope(scope string) bool {
	scope = strings.ToLower(strings.TrimSpace(scope))
	for _, s := range c.Scopes {
		if strings.ToLower(strings.TrimSpace(s)) == scope {
			return true
		}
	}
	return false
}

func splitCSVHeader(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

const (
	AuthModeHeader = "header"
	AuthModeJWT    = "jwt"
	AuthModeAuto   = "auto"
)

type AuthConfig struct {
	Mode                  string
	Profile               string
	JWTHS256Secret        string
	JWTRS256PublicKeyPEM  string
	JWTRS256PublicKeyPath string
	JWTOIDCIssuer         string
	JWTJWKSURL            string
	JWTJWKSCacheSeconds   int
	TenantClaim           string
	PrincipalClaim        string
	RolesClaim            string
	ScopesClaim           string
	AttrsClaim            string
}

func loadAuthConfigFromEnv() AuthConfig {
	return normalizeAuthConfig(AuthConfig{
		Mode:                  strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_MODE")),
		Profile:               strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_PROFILE")),
		JWTHS256Secret:        strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_JWT_HS256_SECRET")),
		JWTRS256PublicKeyPEM:  strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_JWT_RS256_PUBLIC_KEY_PEM")),
		JWTRS256PublicKeyPath: strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_JWT_RS256_PUBLIC_KEY_PATH")),
		JWTOIDCIssuer:         strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_JWT_OIDC_ISSUER")),
		JWTJWKSURL:            strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_JWT_JWKS_URL")),
		JWTJWKSCacheSeconds:   envIntWithDefault("DIFFMIND_AUTH_JWT_JWKS_CACHE_SECONDS", 300),
		TenantClaim:           strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_TENANT_CLAIM")),
		PrincipalClaim:        strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_PRINCIPAL_CLAIM")),
		RolesClaim:            strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_ROLES_CLAIM")),
		ScopesClaim:           strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_SCOPES_CLAIM")),
		AttrsClaim:            strings.TrimSpace(os.Getenv("DIFFMIND_AUTH_ATTRS_CLAIM")),
	})
}

func envIntWithDefault(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func normalizeAuthConfig(cfg AuthConfig) AuthConfig {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	cfg.Profile = strings.ToLower(strings.TrimSpace(cfg.Profile))
	if cfg.Mode == "" {
		cfg.Mode = AuthModeHeader
	}
	return applyProfileDefaults(cfg)
}

func applyProfileDefaults(cfg AuthConfig) AuthConfig {
	preset := authProfileDefaults(cfg.Profile)
	if preset == nil {
		return cfg
	}
	if strings.TrimSpace(cfg.TenantClaim) == "" {
		cfg.TenantClaim = preset.TenantClaim
	}
	if strings.TrimSpace(cfg.PrincipalClaim) == "" {
		cfg.PrincipalClaim = preset.PrincipalClaim
	}
	if strings.TrimSpace(cfg.RolesClaim) == "" {
		cfg.RolesClaim = preset.RolesClaim
	}
	if strings.TrimSpace(cfg.ScopesClaim) == "" {
		cfg.ScopesClaim = preset.ScopesClaim
	}
	if strings.TrimSpace(cfg.AttrsClaim) == "" {
		cfg.AttrsClaim = preset.AttrsClaim
	}
	return cfg
}

func authProfileDefaults(profile string) *AuthConfig {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "custom":
		return nil
	case "keycloak":
		return &AuthConfig{
			TenantClaim:    "tenant",
			PrincipalClaim: "preferred_username",
			RolesClaim:     "realm_access.roles",
			ScopesClaim:    "scope",
			AttrsClaim:     "attrs",
		}
	case "entra", "azuread", "azure_ad":
		return &AuthConfig{
			TenantClaim:    "tid",
			PrincipalClaim: "preferred_username",
			RolesClaim:     "roles",
			ScopesClaim:    "scp",
			AttrsClaim:     "attrs",
		}
	case "cognito":
		return &AuthConfig{
			TenantClaim:    "tenant",
			PrincipalClaim: "username",
			RolesClaim:     "cognito:groups",
			ScopesClaim:    "scope",
			AttrsClaim:     "custom:attrs",
		}
	default:
		return nil
	}
}

func contextFromDiffMindHeaders(h http.Header) (Context, error) {
	ctx := Context{
		TenantID:  strings.TrimSpace(h.Get("X-DiffMind-Tenant")),
		Principal: strings.TrimSpace(h.Get("X-DiffMind-Principal")),
		Roles:     splitCSVHeader(h.Get("X-DiffMind-Roles")),
		Scopes:    splitCSVHeader(h.Get("X-DiffMind-Scopes")),
		Attrs:     map[string]string{},
	}
	if ctx.TenantID == "" {
		return Context{}, errors.New("missing X-DiffMind-Tenant header")
	}
	if ctx.Principal == "" {
		return Context{}, errors.New("missing X-DiffMind-Principal header")
	}
	for k, vals := range h {
		if len(vals) == 0 {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(k))
		if !strings.HasPrefix(lower, "x-diffmind-attr-") {
			continue
		}
		attrKey := strings.TrimPrefix(lower, "x-diffmind-attr-")
		ctx.Attrs[attrKey] = strings.TrimSpace(vals[0])
	}
	return ctx, nil
}
