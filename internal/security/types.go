package security

import (
	"errors"
	"net/http"
	"sort"
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
