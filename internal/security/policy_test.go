package security

import "testing"

func TestAuthorizeTenantIsolationAndRoles(t *testing.T) {
	ctx := Context{TenantID: "t1", Principal: "u1", Roles: []string{"analyst"}, Scopes: []string{"graph:read"}}
	if d := Authorize(ctx, Request{Action: ActionQueryGraph, ResourceTenant: "t1"}); !d.Allow {
		t.Fatalf("expected allow for same tenant analyst, got %+v", d)
	}
	if d := Authorize(ctx, Request{Action: ActionQueryGraph, ResourceTenant: "t2"}); d.Allow {
		t.Fatalf("expected deny on tenant mismatch")
	}
	if d := Authorize(ctx, Request{Action: ActionBuildGraph, ResourceTenant: "t1"}); d.Allow {
		t.Fatalf("expected deny without write role/scope")
	}
	admin := Context{TenantID: "t1", Principal: "u2", Roles: []string{"tenant_admin"}}
	if d := Authorize(admin, Request{Action: ActionBuildGraph, ResourceTenant: "t1"}); !d.Allow {
		t.Fatalf("expected tenant admin write allow")
	}
	platform := Context{TenantID: "t1", Principal: "u3", Roles: []string{"platform_admin"}}
	if d := Authorize(platform, Request{Action: ActionBuildGraph, ResourceTenant: "other"}); !d.Allow {
		t.Fatalf("expected platform admin cross-tenant allow")
	}
}

func TestRedactionRules(t *testing.T) {
	ctx := Context{TenantID: "t1", Principal: "u1", Roles: []string{"analyst"}, Scopes: []string{"graph:read"}}
	attrs := map[string]any{"api_token": "sk_live_abc", "safe": "ok"}
	red := RedactEvidenceAttributes(attrs, ctx, false)
	if red["api_token"] != "[REDACTED]" {
		t.Fatalf("expected redacted token, got %v", red["api_token"])
	}
	if red["safe"] != "ok" {
		t.Fatalf("expected non-sensitive value preserved")
	}
}
