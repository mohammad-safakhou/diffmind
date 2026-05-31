// Package reconcile contains local (non-LLM) post-processing for the
// multi-step extraction pipeline. Stage 5 of the pipeline runs the helpers in
// this package to dedupe entities and connections, sort them deterministically
// for stable artifact IDs, and drop connections whose endpoints didn't survive
// earlier stages.
package reconcile

import (
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// Dedupe collapses entities by ID, keeping the highest-confidence version
// and merging non-empty fields from duplicates.
func DedupeExposures(in []model.Exposure) []model.Exposure {
	in = dedupeExposuresSemantic(in)
	byID := map[string]model.Exposure{}
	for _, e := range in {
		if existing, ok := byID[e.ID]; ok {
			byID[e.ID] = mergeExposure(existing, e)
			continue
		}
		byID[e.ID] = e
	}
	out := make([]model.Exposure, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func DedupeDependencies(in []model.Dependency) []model.Dependency {
	in = dropTransportDuplicates(in)
	in = dedupeDependenciesSemantic(in)
	byID := map[string]model.Dependency{}
	for _, e := range in {
		if existing, ok := byID[e.ID]; ok {
			byID[e.ID] = mergeDependency(existing, e)
			continue
		}
		byID[e.ID] = e
	}
	out := make([]model.Dependency, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func dedupeExposuresSemantic(in []model.Exposure) []model.Exposure {
	byKey := map[string]model.Exposure{}
	for _, e := range in {
		key := semanticKey(e.BaseEntity)
		if existing, ok := byKey[key]; ok {
			byKey[key] = chooseExposure(existing, e)
			continue
		}
		byKey[key] = e
	}
	out := make([]model.Exposure, 0, len(byKey))
	for _, e := range byKey {
		out = append(out, e)
	}
	return out
}

func dedupeDependenciesSemantic(in []model.Dependency) []model.Dependency {
	byKey := map[string]model.Dependency{}
	for _, d := range in {
		key := semanticKey(d.BaseEntity)
		if existing, ok := byKey[key]; ok {
			byKey[key] = chooseDependency(existing, d)
			continue
		}
		byKey[key] = d
	}
	out := make([]model.Dependency, 0, len(byKey))
	for _, d := range byKey {
		out = append(out, d)
	}
	return out
}

func semanticKey(b model.BaseEntity) string {
	loc := ""
	if len(b.Locations) > 0 {
		loc = b.Locations[0].File
	}
	if b.Type == "scheduled_job" || (b.Type == "cli_command" && !strings.EqualFold(b.Platform, "sqs")) {
		name := strings.ToLower(strings.TrimSpace(b.Name))
		if name != "" {
			return "exposure-job|" + name + "|" + loc
		}
	}
	instance := strings.ToLower(strings.TrimSpace(b.Instance))
	operation := strings.ToLower(strings.TrimSpace(b.Operation))
	if operation == "" {
		operation = strings.ToLower(strings.TrimSpace(b.Name))
	}
	return strings.Join([]string{strings.ToLower(b.Platform), instance, operation, loc}, "|")
}

func dropTransportDuplicates(in []model.Dependency) []model.Dependency {
	hasQueuePublish := false
	hasAthenaDB := false
	for _, d := range in {
		if d.Type == "queue_publish" && strings.EqualFold(d.Platform, "sqs") {
			hasQueuePublish = true
		}
		if d.Type == "db_operation" && (strings.EqualFold(d.Platform, "athena") || containsFold(d.Name, "athena")) {
			hasAthenaDB = true
		}
	}
	if !hasQueuePublish && !hasAthenaDB {
		return in
	}
	out := make([]model.Dependency, 0, len(in))
	for _, d := range in {
		if d.Type == "outbound_http" && hasQueuePublish && (strings.EqualFold(d.Platform, "sqs") || containsFold(d.Name, "sqs") || containsFold(d.Operation, "sendmessage")) {
			continue
		}
		if d.Type == "outbound_http" && hasAthenaDB && (strings.EqualFold(d.Platform, "athena") || containsFold(d.Name, "athena") || containsFold(d.Operation, "athena")) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func chooseExposure(a, b model.Exposure) model.Exposure {
	pa, pb := exposurePriority(a.Type), exposurePriority(b.Type)
	if pb > pa || (pb == pa && b.Confidence > a.Confidence) {
		b.BaseEntity = mergeBase(b.BaseEntity, a.BaseEntity)
		return b
	}
	a.BaseEntity = mergeBase(a.BaseEntity, b.BaseEntity)
	return a
}

func chooseDependency(a, b model.Dependency) model.Dependency {
	pa, pb := dependencyPriority(a.Type), dependencyPriority(b.Type)
	if pb > pa || (pb == pa && b.Confidence > a.Confidence) {
		b.BaseEntity = mergeBase(b.BaseEntity, a.BaseEntity)
		return b
	}
	a.BaseEntity = mergeBase(a.BaseEntity, b.BaseEntity)
	return a
}

func exposurePriority(t string) int {
	switch t {
	case "queue_consumer", "scheduled_job", "webhook", "rpc_endpoint":
		return 30
	case "http_route":
		return 20
	case "cli_command":
		return 10
	default:
		return 1
	}
}

func dependencyPriority(t string) int {
	switch t {
	case "queue_publish":
		return 40
	case "outbound_http", "outbound_rpc", "db_operation", "cache_operation":
		return 30
	case "stream_consume":
		return 20
	case "command_exec":
		return 10
	default:
		return 1
	}
}

// FilterConnections drops connections whose from/to IDs do not resolve to a
// known exposure/dependency, sorts the rest deterministically, and returns
// (kept, dropped).
func FilterConnections(
	conns []model.Connection,
	exposures []model.Exposure,
	dependencies []model.Dependency,
) ([]model.Connection, []model.UnresolvedItem) {
	expIDs := map[string]struct{}{}
	for _, e := range exposures {
		expIDs[e.ID] = struct{}{}
	}
	depIDs := map[string]struct{}{}
	for _, d := range dependencies {
		depIDs[d.ID] = struct{}{}
	}
	kept := make([]model.Connection, 0, len(conns))
	dropped := make([]model.UnresolvedItem, 0)
	seen := map[string]struct{}{}
	for _, c := range conns {
		if _, ok := expIDs[c.FromExposureID]; !ok {
			dropped = append(dropped, model.UnresolvedItem{
				Kind: model.KindDependency, Type: "connection",
				Name:       c.FromExposureID + " -> " + c.ToDependencyID,
				ReasonCode: "orphan_connection",
				Reason:     "from_exposure_id did not resolve to a known exposure after reconcile",
				Confidence: c.Confidence,
			})
			continue
		}
		if _, ok := depIDs[c.ToDependencyID]; !ok {
			dropped = append(dropped, model.UnresolvedItem{
				Kind: model.KindDependency, Type: "connection",
				Name:       c.FromExposureID + " -> " + c.ToDependencyID,
				ReasonCode: "orphan_connection",
				Reason:     "to_dependency_id did not resolve to a known dependency after reconcile",
				Confidence: c.Confidence,
			})
			continue
		}
		if _, dup := seen[c.ID]; dup {
			continue
		}
		seen[c.ID] = struct{}{}
		kept = append(kept, c)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	return kept, dropped
}

func DedupeUnresolved(in []model.UnresolvedItem) []model.UnresolvedItem {
	seen := map[string]struct{}{}
	out := make([]model.UnresolvedItem, 0, len(in))
	for _, u := range in {
		key := string(u.Kind) + "|" + u.Type + "|" + u.Name + "|" + u.ReasonCode
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ReasonCode < out[j].ReasonCode
	})
	return out
}

func DedupeWarnings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// mergeExposure picks the higher-confidence exposure as the base and fills in
// missing slices from the other. It is tolerant to the same entity being
// reported twice by different stages.
func mergeExposure(a, b model.Exposure) model.Exposure {
	base, other := a, b
	if b.Confidence > a.Confidence {
		base, other = b, a
	}
	base.BaseEntity = mergeBase(base.BaseEntity, other.BaseEntity)
	return base
}

func mergeDependency(a, b model.Dependency) model.Dependency {
	base, other := a, b
	if b.Confidence > a.Confidence {
		base, other = b, a
	}
	base.BaseEntity = mergeBase(base.BaseEntity, other.BaseEntity)
	return base
}

func mergeBase(base, other model.BaseEntity) model.BaseEntity {
	if strings.TrimSpace(base.Summary) == "" {
		base.Summary = other.Summary
	}
	if strings.TrimSpace(base.Platform) == "" {
		base.Platform = other.Platform
	}
	if strings.TrimSpace(base.Instance) == "" {
		base.Instance = other.Instance
	}
	if strings.TrimSpace(base.Operation) == "" {
		base.Operation = other.Operation
	}
	if strings.TrimSpace(base.OperationKind) == "" {
		base.OperationKind = other.OperationKind
	}
	if len(base.KeyActions) == 0 {
		base.KeyActions = other.KeyActions
	}
	if len(base.Inputs) == 0 {
		base.Inputs = other.Inputs
	}
	if len(base.Tags) == 0 {
		base.Tags = other.Tags
	}
	if len(base.Locations) == 0 {
		base.Locations = other.Locations
	}
	if len(base.Evidence) == 0 {
		base.Evidence = other.Evidence
	}
	if len(base.Details) == 0 {
		base.Details = other.Details
	} else {
		for k, v := range other.Details {
			if _, ok := base.Details[k]; !ok {
				base.Details[k] = v
			}
		}
	}
	return base
}
