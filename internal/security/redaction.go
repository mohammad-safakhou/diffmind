package security

import (
	"fmt"
	"strings"

	"diffmind/internal/graphschema"
)

const redactedValue = "[REDACTED]"

var sensitiveKeyPatterns = []string{
	"password",
	"secret",
	"token",
	"apikey",
	"api_key",
	"auth",
	"credential",
	"private_key",
}

func CanReadSensitive(ctx Context, includeSensitive bool) bool {
	if !includeSensitive {
		return false
	}
	if ctx.HasRole("platform_admin") || ctx.HasRole("compliance_auditor") {
		return true
	}
	return ctx.HasScope("sensitive:read")
}

func CanReadRawEvidence(ctx Context) bool {
	if ctx.HasRole("platform_admin") || ctx.HasRole("compliance_auditor") {
		return true
	}
	return ctx.HasScope("evidence:raw")
}

func RedactGraph(graph graphschema.Graph, ctx Context, includeSensitive bool) graphschema.Graph {
	sensitiveAllowed := CanReadSensitive(ctx, includeSensitive)
	evidenceAllowed := CanReadRawEvidence(ctx)

	for i := range graph.Nodes {
		graph.Nodes[i].Attributes = redactAttributes(graph.Nodes[i].Attributes, sensitiveAllowed)
	}
	for i := range graph.Edges {
		graph.Edges[i].Attributes = redactAttributes(graph.Edges[i].Attributes, sensitiveAllowed)
		if !evidenceAllowed {
			for j := range graph.Edges[i].EvidenceRefs {
				graph.Edges[i].EvidenceRefs[j].FilePath = redactedValue
				graph.Edges[i].EvidenceRefs[j].StartLine = 0
				graph.Edges[i].EvidenceRefs[j].StartCol = 0
				graph.Edges[i].EvidenceRefs[j].EndLine = 0
				graph.Edges[i].EvidenceRefs[j].EndCol = 0
			}
		}
	}

	if !sensitiveAllowed {
		for i := range graph.Meta.Services {
			graph.Meta.Services[i].Provenance.RunReportPath = redactedValue
			graph.Meta.Services[i].Provenance.SnapshotPath = redactedValue
			graph.Meta.Services[i].Provenance.BundleSHA256 = redactedValue
			graph.Meta.Services[i].Provenance.AnalyzerBundleSHA256 = redactedValue
		}
	}

	return graph
}

func RedactEvidenceAttributes(attrs map[string]any, ctx Context, includeSensitive bool) map[string]any {
	return redactAttributes(attrs, CanReadSensitive(ctx, includeSensitive))
}

func redactAttributes(attrs map[string]any, allowed bool) map[string]any {
	if attrs == nil {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if allowed {
			out[k] = v
			continue
		}
		if isSensitiveKey(k) || isSensitiveValue(v) {
			out[k] = redactedValue
			continue
		}
		switch vv := v.(type) {
		case map[string]any:
			out[k] = redactAttributes(vv, allowed)
		case []any:
			arr := make([]any, 0, len(vv))
			for _, item := range vv {
				if isSensitiveValue(item) {
					arr = append(arr, redactedValue)
				} else {
					arr = append(arr, item)
				}
			}
			out[k] = arr
		default:
			out[k] = v
		}
	}
	return out
}

func isSensitiveKey(k string) bool {
	lower := strings.ToLower(strings.TrimSpace(k))
	for _, p := range sensitiveKeyPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func isSensitiveValue(v any) bool {
	s := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "sk_") || strings.HasPrefix(s, "ghp_") {
		return true
	}
	if strings.Contains(s, "bearer ") {
		return true
	}
	return false
}
