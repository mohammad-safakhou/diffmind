package graphschema

import (
	"fmt"
	"strings"
)

const (
	OntologyVersionV1 = "v1"
	OntologyVersionV2 = "v2"
)

const (
	SectionExposure     = "exposure"
	SectionLogic        = "logic"
	SectionDependencies = "dependencies"
)

const (
	VerificationVerified    = "verified"
	VerificationNeedsReview = "needs_review"
	VerificationDisputed    = "disputed"
	VerificationInferred    = "inferred"
)

var supportedOntologyVersions = map[string]struct{}{
	OntologyVersionV1: {},
	OntologyVersionV2: {},
}

var validSections = map[string]struct{}{
	SectionExposure:     {},
	SectionLogic:        {},
	SectionDependencies: {},
}

var validVerificationStates = map[string]struct{}{
	VerificationVerified:    {},
	VerificationNeedsReview: {},
	VerificationDisputed:    {},
	VerificationInferred:    {},
}

// NormalizeGraphSemantics applies ontology-v2 defaults while preserving explicit values.
func NormalizeGraphSemantics(graph *Graph) {
	if graph == nil {
		return
	}
	if strings.TrimSpace(graph.Meta.OntologyVersion) == "" {
		graph.Meta.OntologyVersion = OntologyVersionV2
	}
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if strings.TrimSpace(n.Section) == "" {
			n.Section = nodeSectionForType(n.Type)
		}
		if strings.TrimSpace(n.Class) == "" {
			n.Class = nodeClassForType(n.Type)
		}
		if strings.TrimSpace(n.VerificationState) == "" {
			n.VerificationState = deriveVerificationState(n.Attributes, n.Inferred, n.Type)
		}
	}
	for i := range graph.Edges {
		e := &graph.Edges[i]
		if strings.TrimSpace(e.Section) == "" {
			e.Section = edgeSectionForType(e.Type)
		}
		if strings.TrimSpace(e.Class) == "" {
			e.Class = edgeClassForType(e.Type)
		}
		if strings.TrimSpace(e.VerificationState) == "" {
			e.VerificationState = deriveVerificationState(e.Attributes, e.Inferred, e.Type)
		}
	}
}

func IsSupportedOntologyVersion(v string) bool {
	_, ok := supportedOntologyVersions[strings.ToLower(strings.TrimSpace(v))]
	return ok
}

func IsValidSection(v string) bool {
	_, ok := validSections[strings.ToLower(strings.TrimSpace(v))]
	return ok
}

func IsValidVerificationState(v string) bool {
	_, ok := validVerificationStates[strings.ToLower(strings.TrimSpace(v))]
	return ok
}

func nodeSectionForType(typ string) string {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "endpoint", "sensitive_surface":
		return SectionExposure
	case "queue", "topic", "database", "table", "dependency", "build_artifact", "canonical_api_host":
		return SectionDependencies
	default:
		return SectionLogic
	}
}

func nodeClassForType(typ string) string {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "service":
		return "service"
	case "endpoint":
		return "api_endpoint"
	case "queue":
		return "queue"
	case "topic":
		return "topic"
	case "database":
		return "database"
	case "table":
		return "table"
	case "config_key":
		return "config_key"
	case "runtime_unit":
		return "runtime_unit"
	case "pipeline_step":
		return "pipeline_step"
	case "deployment":
		return "deployment"
	case "dependency":
		return "external_dependency"
	case "owner":
		return "ownership"
	case "dependency_risk":
		return "dependency_risk"
	case "conflict":
		return "conflict"
	case "verification_decision":
		return "verification_decision"
	case "unresolved_api_call":
		return "verification_decision"
	case "sensitive_surface":
		return "sensitive_input"
	case "environment":
		return "environment"
	case "build_artifact":
		return "build_artifact"
	case "canonical_api_host":
		return "api_host"
	default:
		return "domain_entity"
	}
}

func edgeSectionForType(typ string) string {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "service_calls_endpoint", "service_exposes_endpoint", "queue_delivers_to_service", "service_exposes_sensitive_surface":
		return SectionExposure
	case "service_calls_service", "service_depends_on_dependency", "service_reads_db", "service_writes_db", "service_publishes_queue", "dependency_owned_by", "dependency_has_risk", "service_has_dependency_risk", "service_alias_of_canonical_api_host":
		return SectionDependencies
	case "service_has_unresolved_api_call":
		return SectionLogic
	default:
		return SectionLogic
	}
}

func edgeClassForType(typ string) string {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "service_calls_endpoint":
		return "api_call"
	case "service_exposes_endpoint":
		return "service_exposure"
	case "service_calls_service":
		return "service_call"
	case "service_publishes_queue":
		return "queue_publish"
	case "queue_delivers_to_service":
		return "queue_consume"
	case "service_reads_db":
		return "db_read"
	case "service_writes_db":
		return "db_write"
	case "service_depends_on_dependency":
		return "dependency_link"
	case "dependency_owned_by":
		return "ownership_link"
	case "dependency_has_risk", "service_has_dependency_risk":
		return "risk_link"
	case "service_uses_config":
		return "config_usage"
	case "config_scoped_to_environment":
		return "config_scope"
	case "service_built_by_pipeline_step":
		return "build_lineage"
	case "pipeline_step_produces_artifact":
		return "artifact_lineage"
	case "artifact_deployed_to_runtime", "service_deployed_to_runtime":
		return "deployment_link"
	case "service_has_runtime_unit":
		return "runtime_link"
	case "service_has_conflict", "service_has_verification_decision", "verification_decision_targets_entity":
		return "verification_link"
	case "service_has_unresolved_api_call":
		return "verification_link"
	case "service_alias_of_canonical_api_host":
		return "service_call"
	case "service_exposes_sensitive_surface", "config_has_sensitive_surface":
		return "sensitive_surface_link"
	default:
		return "domain_relation"
	}
}

func deriveVerificationState(attrs map[string]any, inferred bool, typ string) string {
	if inferred {
		return VerificationInferred
	}
	if attrs != nil {
		if raw, ok := attrs["verification_status"]; ok {
			if normalized := normalizeVerificationState(raw); normalized != "" {
				return normalized
			}
		}
		if raw, ok := attrs["status"]; ok {
			if normalized := normalizeVerificationState(raw); normalized != "" {
				return normalized
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(typ), "conflict") {
		return VerificationDisputed
	}
	return VerificationVerified
}

func normalizeVerificationState(v any) string {
	raw := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
	switch raw {
	case "verified", "pass", "passed", "ok":
		return VerificationVerified
	case "needs_review", "needs-review", "pending", "unreviewed":
		return VerificationNeedsReview
	case "disputed", "conflict", "rejected", "failed", "fail", "unresolved":
		return VerificationDisputed
	case "inferred":
		return VerificationInferred
	default:
		return ""
	}
}
