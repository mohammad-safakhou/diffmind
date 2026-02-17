package security

import "strings"

func Authorize(ctx Context, req Request) Decision {
	if strings.TrimSpace(ctx.TenantID) == "" || strings.TrimSpace(ctx.Principal) == "" {
		return Decision{Allow: false, Reason: "missing_auth_context"}
	}
	if ctx.HasRole("platform_admin") {
		return Decision{Allow: true, Reason: "platform_admin"}
	}

	resourceTenant := strings.TrimSpace(req.ResourceTenant)
	if resourceTenant == "" {
		resourceTenant = "default"
	}
	if ctx.TenantID != resourceTenant {
		return Decision{Allow: false, Reason: "tenant_mismatch"}
	}

	switch req.Action {
	case ActionBuildGraph, ActionDeleteCompare:
		if ctx.HasRole("tenant_admin") || ctx.HasScope("graph:write") {
			return Decision{Allow: true, Reason: "write_permitted"}
		}
		return Decision{Allow: false, Reason: "write_forbidden"}
	case ActionAuditRead:
		if ctx.HasRole("compliance_auditor") || ctx.HasScope("audit:read") {
			return Decision{Allow: true, Reason: "audit_read_permitted"}
		}
		return Decision{Allow: false, Reason: "audit_read_forbidden"}
	case ActionAuditExport, ActionAuditPrune:
		if ctx.HasRole("compliance_auditor") || ctx.HasScope("audit:export") {
			return Decision{Allow: true, Reason: "compliance_mutation_permitted"}
		}
		return Decision{Allow: false, Reason: "compliance_mutation_forbidden"}
	case ActionReadEvidence:
		if ctx.HasRole("analyst") || ctx.HasRole("tenant_admin") || ctx.HasScope("graph:read") || ctx.HasScope("evidence:read") {
			return Decision{Allow: true, Reason: "evidence_read_permitted"}
		}
		return Decision{Allow: false, Reason: "evidence_read_forbidden"}
	case ActionCompareGraph, ActionQueryGraph, ActionQueryEntities:
		if ctx.HasRole("analyst") || ctx.HasRole("tenant_admin") || ctx.HasScope("graph:read") {
			return Decision{Allow: true, Reason: "read_permitted"}
		}
		return Decision{Allow: false, Reason: "read_forbidden"}
	default:
		return Decision{Allow: false, Reason: "unknown_action"}
	}
}
