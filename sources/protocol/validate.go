package protocol

import (
	"fmt"
	"regexp"
	"strings"
)

var lineIDPattern = regexp.MustCompile(`(?i)(^|[_.-])(line|ln|l)[_.-]?[0-9]+($|[_.-])|:[0-9]+`)

func Validate(doc *Document) error {
	if doc == nil {
		return fmt.Errorf("document is nil")
	}
	if strings.TrimSpace(doc.Schema) != SchemaServiceV1 {
		return fmt.Errorf("unsupported schema %q", doc.Schema)
	}
	if strings.TrimSpace(doc.Service.ID) == "" || strings.TrimSpace(doc.Service.Name) == "" {
		return fmt.Errorf("service.id and service.name are required")
	}
	objectIDs := map[string]struct{}{}
	add := func(base ObjectiveBase) error {
		if strings.TrimSpace(base.ID) == "" {
			return fmt.Errorf("object id is required")
		}
		if lineIDPattern.MatchString(base.ID) {
			return fmt.Errorf("object id %q looks source-line based", base.ID)
		}
		if _, dup := objectIDs[base.ID]; dup {
			return fmt.Errorf("duplicate object id %q", base.ID)
		}
		objectIDs[base.ID] = struct{}{}
		if strings.TrimSpace(base.Kind) == "" || strings.TrimSpace(base.Name) == "" {
			return fmt.Errorf("object %q kind and name are required", base.ID)
		}
		if err := validateStatus(base.Status, "object "+base.ID); err != nil {
			return err
		}
		if err := validateConfidence(base.Confidence, "object "+base.ID); err != nil {
			return err
		}
		if err := validateOrigin(base.Origin, "object "+base.ID); err != nil {
			return err
		}
		return nil
	}
	for _, v := range doc.Objects.HTTPEndpoints {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.HTTPCalls {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.DBResources {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.DBQueries {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.QueueConsumers {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.QueuePublishers {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.RPCEndpoints {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.RPCCalls {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.CLICommands {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.Activations {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.CacheOperations {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.ConfigReads {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.FeatureFlags {
		if err := add(v.ObjectiveBase); err != nil {
			return err
		}
	}

	obsIDs := map[string]Observation{}
	for _, obs := range doc.Observations {
		if strings.TrimSpace(obs.ID) == "" {
			return fmt.Errorf("observation id is required")
		}
		if _, dup := obsIDs[obs.ID]; dup {
			return fmt.Errorf("duplicate observation id %q", obs.ID)
		}
		if obs.ObjectRef != "" {
			if _, ok := objectIDs[obs.ObjectRef]; !ok {
				return fmt.Errorf("observation %q references unknown object %q", obs.ID, obs.ObjectRef)
			}
		}
		if err := validateConfidence(obs.Confidence, "observation "+obs.ID); err != nil {
			return err
		}
		obsIDs[obs.ID] = obs
	}

	evIDs := map[string]Evidence{}
	for _, ev := range doc.Evidence {
		if strings.TrimSpace(ev.ID) == "" {
			return fmt.Errorf("evidence id is required")
		}
		if _, dup := evIDs[ev.ID]; dup {
			return fmt.Errorf("duplicate evidence id %q", ev.ID)
		}
		if err := validateConfidence(ev.Confidence, "evidence "+ev.ID); err != nil {
			return err
		}
		evIDs[ev.ID] = ev
	}

	checkRefs := func(base ObjectiveBase) error {
		for _, ref := range base.Observations {
			if _, ok := obsIDs[ref]; !ok {
				return fmt.Errorf("object %q references unknown observation %q", base.ID, ref)
			}
		}
		for _, ref := range base.EvidenceRefs {
			if _, ok := evIDs[ref]; !ok {
				return fmt.Errorf("object %q references unknown evidence %q", base.ID, ref)
			}
		}
		return nil
	}
	if err := forEachBase(doc, func(base ObjectiveBase) error { return checkRefs(base) }); err != nil {
		return err
	}

	for _, flow := range doc.Flows {
		if err := validateFlow(flow, objectIDs, evIDs); err != nil {
			return err
		}
	}
	return nil
}

// ValidateCanonical applies the stricter generated-artifact contract on top of
// the semantic DiffMind protocol validation. It is intentionally stricter than Validate so
// hand-written minimal YAML can still be parsed while DiffMind-generated JSON
// remains complete and predictable for DiffMind.
func ValidateCanonical(doc *Document) error {
	if err := Validate(doc); err != nil {
		return err
	}
	if doc.Flows == nil {
		return fmt.Errorf("flows must be present")
	}
	if doc.Observations == nil {
		return fmt.Errorf("observations must be present")
	}
	if doc.Evidence == nil {
		return fmt.Errorf("evidence must be present")
	}
	if err := validateCanonicalObjectArrays(doc); err != nil {
		return err
	}
	for _, ev := range doc.Evidence {
		if strings.EqualFold(strings.TrimSpace(ev.Source), string(OriginLLM)) {
			return fmt.Errorf("evidence %q source %q is not allowed in canonical deterministic output", ev.ID, ev.Source)
		}
	}
	for _, flow := range doc.Flows {
		if flow.Origin == OriginLLM {
			return fmt.Errorf("flow %q origin %q is not allowed in canonical deterministic output", flow.ID, flow.Origin)
		}
	}
	return forEachBase(doc, func(base ObjectiveBase) error {
		if base.Status == "" {
			return fmt.Errorf("object %q status is required", base.ID)
		}
		if base.Confidence == "" {
			return fmt.Errorf("object %q confidence is required", base.ID)
		}
		if base.Origin == "" {
			return fmt.Errorf("object %q origin is required", base.ID)
		}
		if base.Origin == OriginLLM {
			return fmt.Errorf("object %q origin %q is not allowed in canonical deterministic output", base.ID, base.Origin)
		}
		if base.Observations == nil {
			return fmt.Errorf("object %q observations must be present", base.ID)
		}
		if base.EvidenceRefs == nil {
			return fmt.Errorf("object %q evidence_refs must be present", base.ID)
		}
		if len(base.Observations) == 0 {
			return fmt.Errorf("object %q must reference at least one observation", base.ID)
		}
		if len(base.EvidenceRefs) == 0 {
			return fmt.Errorf("object %q must reference at least one evidence item", base.ID)
		}
		return nil
	})
}

func validateCanonicalObjectArrays(doc *Document) error {
	checks := []struct {
		name string
		ok   bool
	}{
		{"objects.http_endpoints", doc.Objects.HTTPEndpoints != nil},
		{"objects.http_calls", doc.Objects.HTTPCalls != nil},
		{"objects.db_resources", doc.Objects.DBResources != nil},
		{"objects.db_queries", doc.Objects.DBQueries != nil},
		{"objects.queue_consumers", doc.Objects.QueueConsumers != nil},
		{"objects.queue_publishers", doc.Objects.QueuePublishers != nil},
		{"objects.rpc_endpoints", doc.Objects.RPCEndpoints != nil},
		{"objects.rpc_calls", doc.Objects.RPCCalls != nil},
		{"objects.cli_commands", doc.Objects.CLICommands != nil},
		{"objects.activations", doc.Objects.Activations != nil},
		{"objects.cache_operations", doc.Objects.CacheOperations != nil},
		{"objects.config_reads", doc.Objects.ConfigReads != nil},
		{"objects.feature_flags", doc.Objects.FeatureFlags != nil},
	}
	for _, check := range checks {
		if !check.ok {
			return fmt.Errorf("%s must be present", check.name)
		}
	}
	return nil
}

func forEachBase(doc *Document, fn func(ObjectiveBase) error) error {
	for _, v := range doc.Objects.HTTPEndpoints {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.HTTPCalls {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.DBResources {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.DBQueries {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.QueueConsumers {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.QueuePublishers {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.RPCEndpoints {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.RPCCalls {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.CLICommands {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.Activations {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.CacheOperations {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.ConfigReads {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	for _, v := range doc.Objects.FeatureFlags {
		if err := fn(v.ObjectiveBase); err != nil {
			return err
		}
	}
	return nil
}

func validateFlow(flow Flow, objects map[string]struct{}, evidence map[string]Evidence) error {
	if strings.TrimSpace(flow.ID) == "" {
		return fmt.Errorf("flow id is required")
	}
	if lineIDPattern.MatchString(flow.ID) {
		return fmt.Errorf("flow id %q looks source-line based", flow.ID)
	}
	if flow.From != "" {
		if _, ok := objects[flow.From]; !ok {
			return fmt.Errorf("flow %q from references unknown object %q", flow.ID, flow.From)
		}
	}
	if flow.To != "" {
		if _, ok := objects[flow.To]; !ok {
			return fmt.Errorf("flow %q to references unknown object %q", flow.ID, flow.To)
		}
	}
	if flow.Entrypoint != "" {
		if _, ok := objects[flow.Entrypoint]; !ok {
			return fmt.Errorf("flow %q entrypoint references unknown object %q", flow.ID, flow.Entrypoint)
		}
	}
	if err := validateStatus(flow.Status, "flow "+flow.ID); err != nil {
		return err
	}
	if err := validateConfidence(flow.Confidence, "flow "+flow.ID); err != nil {
		return err
	}
	if err := validateOrigin(flow.Origin, "flow "+flow.ID); err != nil {
		return err
	}
	if err := validateReachability(flow.Reachability, "flow "+flow.ID); err != nil {
		return err
	}
	for _, ref := range flow.EvidenceRefs {
		if _, ok := evidence[ref]; !ok {
			return fmt.Errorf("flow %q references unknown evidence %q", flow.ID, ref)
		}
	}
	nodes := map[string]FlowNode{}
	for _, n := range flow.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return fmt.Errorf("flow %q has node without id", flow.ID)
		}
		if _, dup := nodes[n.ID]; dup {
			return fmt.Errorf("flow %q has duplicate node %q", flow.ID, n.ID)
		}
		if n.Ref != "" {
			if _, ok := objects[n.Ref]; !ok {
				return fmt.Errorf("flow %q node %q references unknown object %q", flow.ID, n.ID, n.Ref)
			}
		}
		nodes[n.ID] = n
	}
	for _, e := range flow.Edges {
		if _, ok := nodes[e.From]; !ok {
			return fmt.Errorf("flow %q edge from unknown node %q", flow.ID, e.From)
		}
		if _, ok := nodes[e.To]; !ok {
			return fmt.Errorf("flow %q edge to unknown node %q", flow.ID, e.To)
		}
		if err := validateReachability(e.Reachability, "flow "+flow.ID+" edge"); err != nil {
			return err
		}
	}
	return nil
}

func validateStatus(v Status, where string) error {
	if v == "" {
		return nil
	}
	switch v {
	case StatusConfirmed, StatusProposed, StatusRejected, StatusStale, StatusDeprecated, StatusUnresolved, StatusConflicting:
		return nil
	default:
		return fmt.Errorf("%s has invalid status %q", where, v)
	}
}

func validateConfidence(v Confidence, where string) error {
	if v == "" {
		return nil
	}
	switch v {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow, ConfidenceUnknown:
		return nil
	default:
		return fmt.Errorf("%s has invalid confidence %q", where, v)
	}
}

func validateOrigin(v Origin, where string) error {
	if v == "" {
		return nil
	}
	switch v {
	case OriginManual, OriginDeterministic, OriginLLM, OriginImported, OriginRuntime, OriginExternal:
		return nil
	default:
		return fmt.Errorf("%s has invalid origin %q", where, v)
	}
}

func validateReachability(v Reachability, where string) error {
	if v == "" {
		return nil
	}
	switch v {
	case ReachabilityMust, ReachabilityConditional, ReachabilityMay, ReachabilityUnknown:
		return nil
	default:
		return fmt.Errorf("%s has invalid reachability %q", where, v)
	}
}
