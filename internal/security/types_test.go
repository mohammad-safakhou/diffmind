package security

import "testing"

func TestLoadAuthConfigFromEnv_ProfileDefaults(t *testing.T) {
	t.Setenv("DIFFMIND_AUTH_MODE", "jwt")
	t.Setenv("DIFFMIND_AUTH_PROFILE", "entra")
	t.Setenv("DIFFMIND_AUTH_TENANT_CLAIM", "")
	t.Setenv("DIFFMIND_AUTH_PRINCIPAL_CLAIM", "")
	t.Setenv("DIFFMIND_AUTH_ROLES_CLAIM", "")
	t.Setenv("DIFFMIND_AUTH_SCOPES_CLAIM", "")

	cfg := loadAuthConfigFromEnv()
	if cfg.Mode != AuthModeJWT {
		t.Fatalf("expected jwt mode, got %q", cfg.Mode)
	}
	if cfg.TenantClaim != "tid" || cfg.PrincipalClaim != "preferred_username" || cfg.RolesClaim != "roles" || cfg.ScopesClaim != "scp" {
		t.Fatalf("expected entra defaults, got %+v", cfg)
	}
}

func TestNormalizeAuthConfig_ExplicitClaimsOverrideProfile(t *testing.T) {
	cfg := normalizeAuthConfig(AuthConfig{
		Mode:           "jwt",
		Profile:        "keycloak",
		TenantClaim:    "tenant_custom",
		PrincipalClaim: "sub",
	})
	if cfg.TenantClaim != "tenant_custom" || cfg.PrincipalClaim != "sub" {
		t.Fatalf("expected explicit claims preserved, got %+v", cfg)
	}
	if cfg.RolesClaim != "realm_access.roles" || cfg.ScopesClaim != "scope" {
		t.Fatalf("expected unresolved claims to use profile defaults, got %+v", cfg)
	}
}
