package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/protocol"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

const DiffMind protocolServiceJSON = ".diffmind/context/service.json"
const DiffMind protocolServiceYAML = "diffmind.yaml"

func hasDiffMind protocol(runDir string) bool {
	if st, err := os.Stat(filepath.Join(runDir, DiffMind protocolServiceJSON)); err == nil && !st.IsDir() {
		return true
	}
	if st, err := os.Stat(filepath.Join(runDir, DiffMind protocolServiceYAML)); err == nil && !st.IsDir() {
		return true
	}
	return false
}

func ReadDiffMind protocol(runDir string) (*protocol.Document, error) {
	if f, err := os.Open(filepath.Join(runDir, DiffMind protocolServiceJSON)); err == nil {
		defer f.Close()
		return protocol.DecodeJSON(f)
	}
	if f, err := os.Open(filepath.Join(runDir, DiffMind protocolServiceYAML)); err == nil {
		defer f.Close()
		return protocol.DecodeYAML(f)
	}
	return nil, fmt.Errorf("no DiffMind protocol service context found in %s", runDir)
}

func readDiffMind protocolRunDir(repoPath, runDir string) (*model.ServiceArchitecture, error) {
	doc, err := ReadDiffMind protocol(runDir)
	if err != nil {
		return nil, err
	}
	arch := protocolToArchitecture(doc)
	arch.RepoPath = firstString(repoPath, doc.Repository.Path)
	arch.Manifest = readManifestForDiffMind protocol(runDir, arch.RepoPath)
	return arch, nil
}

func protocolToArchitecture(doc *protocol.Document) *model.ServiceArchitecture {
	arch := &model.ServiceArchitecture{DiffMind protocol: doc, ServiceName: doc.Service.Name, RepoPath: doc.Repository.Path}
	for _, r := range doc.Objects.DBResources {
		arch.Resources = append(arch.Resources, model.Resource{
			ID:       r.ID,
			Kind:     "database",
			Platform: r.Engine,
			Name:     firstString(r.Table, r.Database, r.Name),
			Instance: firstString(r.Database, r.Table),
			Summary:  stringMeta(r.Metadata, "summary"),
			Details:  mapFromAny(r),
			Status:   string(r.Status),
			Source:   string(r.Origin),
		})
	}
	for _, o := range doc.Objects.HTTPEndpoints {
		arch.Exposures = append(arch.Exposures, model.Exposure{BaseEntity: baseFromDiffMind protocol(o.ObjectiveBase, "http_route", o.Name, "http", mapFromAny(o))})
	}
	for _, o := range doc.Objects.QueueConsumers {
		platform := normalizeQueuePlatform(o.Platform, o.Topic, o.Queue, o.Name)
		instance := firstString(o.Topic, o.Queue, o.Name)
		details := mapFromAny(o)
		details["platform"] = platform
		details["destination"] = instance
		base := baseFromDiffMind protocol(o.ObjectiveBase, "queue_consumer", o.Name, platform, details)
		base.Instance = instance
		arch.Exposures = append(arch.Exposures, model.Exposure{BaseEntity: base})
	}
	for _, o := range doc.Objects.CLICommands {
		arch.Exposures = append(arch.Exposures, model.Exposure{BaseEntity: baseFromDiffMind protocol(o.ObjectiveBase, "cli_command", o.Name, "process", mapFromAny(o))})
	}
	for _, o := range doc.Objects.Activations {
		arch.Exposures = append(arch.Exposures, model.Exposure{BaseEntity: baseFromDiffMind protocol(o.ObjectiveBase, "scheduled_job", o.Name, "scheduler", mapFromAny(o))})
	}
	for _, o := range doc.Objects.RPCEndpoints {
		arch.Exposures = append(arch.Exposures, model.Exposure{BaseEntity: baseFromDiffMind protocol(o.ObjectiveBase, "rpc_endpoint", o.Name, o.Protocol, mapFromAny(o))})
	}
	for _, o := range doc.Objects.HTTPCalls {
		details := mapFromAny(o)
		target := targetName(o.Target, o.URLTemplate)
		if o.Target != nil {
			details["target_ref"] = o.Target.Ref
			details["target_type"] = o.Target.Type
			details["target_unresolved"] = o.Target.Unresolved
		}
		details["target_service"] = target
		details["url_template"] = o.URLTemplate
		details["method"] = o.Method
		base := baseFromDiffMind protocol(o.ObjectiveBase, "outbound_http", o.Name, "http", details)
		base.Instance = target
		arch.Dependencies = append(arch.Dependencies, model.Dependency{BaseEntity: base})
	}
	for _, o := range doc.Objects.DBQueries {
		base := baseFromDiffMind protocol(o.ObjectiveBase, "db_operation", o.Name, o.Engine, mapFromAny(o))
		if o.Target != nil {
			base.Instance = firstString(o.Target.Database, strings.Join(o.Target.Tables, ","))
		}
		base.OperationKind = o.Operation
		base.Operation = o.Access
		arch.Dependencies = append(arch.Dependencies, model.Dependency{BaseEntity: base})
	}
	for _, o := range doc.Objects.QueuePublishers {
		platform := normalizeQueuePlatform(o.Platform, o.Topic, o.Queue, o.Name)
		instance := firstString(o.Topic, o.Queue, o.Name)
		details := mapFromAny(o)
		details["platform"] = platform
		details["destination"] = instance
		base := baseFromDiffMind protocol(o.ObjectiveBase, "queue_publish", o.Name, platform, details)
		base.Instance = instance
		arch.Dependencies = append(arch.Dependencies, model.Dependency{BaseEntity: base})
	}
	for _, o := range doc.Objects.CacheOperations {
		details := mapFromAny(o)
		cacheName := cacheTargetName(o)
		platform := normalizeCachePlatform(o.Platform, cacheName, o.Name)
		details["cache"] = cacheName
		details["cache_name"] = cacheName
		details["cache_type"] = platform
		base := baseFromDiffMind protocol(o.ObjectiveBase, "cache_operation", o.Name, platform, details)
		base.Instance = cacheName
		base.Operation = o.Operation
		arch.Dependencies = append(arch.Dependencies, model.Dependency{BaseEntity: base})
	}
	for _, o := range doc.Objects.RPCCalls {
		base := baseFromDiffMind protocol(o.ObjectiveBase, "outbound_rpc", o.Name, o.Protocol, mapFromAny(o))
		base.Instance = targetName(o.Target, o.Service)
		arch.Dependencies = append(arch.Dependencies, model.Dependency{BaseEntity: base})
	}
	for _, o := range doc.Objects.ConfigReads {
		if o.Kind != "workflow_orchestration" && stringMeta(o.Metadata, "legacy_type") != "workflow_orchestration" {
			continue
		}
		details := mapFromAny(o)
		flattenMetadataDetails(details, o.Metadata)
		details["key"] = o.Key
		details["value"] = o.Value
		details["source"] = o.Source
		platform := firstString(stringFromAny(details["orchestrator"]), stringFromAny(details["platform"]), "workflow")
		base := baseFromDiffMind protocol(o.ObjectiveBase, "workflow_orchestration", o.Name, platform, details)
		base.Instance = firstString(stringFromAny(details["target_service"]), stringFromAny(details["url_template"]), o.Value, platform)
		arch.Dependencies = append(arch.Dependencies, model.Dependency{BaseEntity: base})
	}
	for _, flow := range doc.Flows {
		conn, ok := connectionFromDiffMind protocolFlow(flow, arch)
		if ok {
			arch.Connections = append(arch.Connections, conn)
		}
	}
	return arch
}

func readManifestForDiffMind protocol(runDir, repoPath string) *model.RunManifest {
	data, err := os.ReadFile(filepath.Join(runDir, "run_manifest.json"))
	if err != nil {
		return &model.RunManifest{RunID: filepath.Base(runDir), RepoPath: repoPath, SchemaVersion: protocol.SchemaServiceV1}
	}
	var m model.RunManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return &model.RunManifest{RunID: filepath.Base(runDir), RepoPath: repoPath, SchemaVersion: protocol.SchemaServiceV1}
	}
	return &m
}

func baseFromDiffMind protocol(base protocol.ObjectiveBase, typ, name, platform string, details map[string]any) model.BaseEntity {
	if name == "" {
		name = base.Name
	}
	return model.BaseEntity{
		ID:           base.ID,
		Type:         typ,
		Name:         name,
		Platform:     platform,
		Summary:      stringMeta(base.Metadata, "summary"),
		Locations:    nil,
		Evidence:     nil,
		Confidence:   confidenceFloat(base.Confidence),
		Tags:         []string{"protocol", string(base.Origin)},
		Details:      details,
		PluginSource: string(base.Origin),
	}
}

func connectionFromDiffMind protocolFlow(flow protocol.Flow, arch *model.ServiceArchitecture) (model.Connection, bool) {
	from, to := flow.From, flow.To
	if from == "" || to == "" {
		for _, n := range flow.Nodes {
			if from == "" && isExposureID(arch, n.Ref) {
				from = n.Ref
			}
			if isDependencyID(arch, n.Ref) {
				to = n.Ref
			}
		}
	}
	if from == "" || to == "" {
		return model.Connection{}, false
	}
	return model.Connection{
		ID:             flow.ID,
		FromExposureID: from,
		ToDependencyID: to,
		Summary:        firstString(flow.Kind, flow.ID),
		Confidence:     confidenceFloat(flow.Confidence),
		FromType:       typeForExposureID(arch, from),
		ToType:         typeForDependencyID(arch, to),
		Details: map[string]any{
			"kind":              flow.Kind,
			"entrypoint":        flow.Entrypoint,
			"reachability":      flow.Reachability,
			"condition":         flow.Condition,
			"nodes":             flow.Nodes,
			"edges":             flow.Edges,
			"data_dependencies": flow.DataDependencies,
			"side_effects":      flow.SideEffects,
			"evidence_refs":     flow.EvidenceRefs,
		},
	}, true
}

func isExposureID(arch *model.ServiceArchitecture, id string) bool {
	return typeForExposureID(arch, id) != ""
}

func isDependencyID(arch *model.ServiceArchitecture, id string) bool {
	return typeForDependencyID(arch, id) != ""
}

func typeForExposureID(arch *model.ServiceArchitecture, id string) string {
	for _, e := range arch.Exposures {
		if e.ID == id {
			return e.Type
		}
	}
	return ""
}

func typeForDependencyID(arch *model.ServiceArchitecture, id string) string {
	for _, d := range arch.Dependencies {
		if d.ID == id {
			return d.Type
		}
	}
	return ""
}

func targetName(target *protocol.TargetRef, fallback string) string {
	name := strings.TrimSpace(fallback)
	if target != nil {
		name = firstString(target.Ref, fallback)
	}
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "service.")
	name = strings.TrimPrefix(name, "external.")
	name = strings.ReplaceAll(name, "_", "-")
	return name
}

func cacheTargetName(o protocol.CacheOperation) string {
	if o.Target != nil {
		for _, key := range []string{"cache", "cache_name", "resource", "name"} {
			if v, ok := o.Target[key]; ok {
				if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
					return s
				}
			}
		}
	}
	return o.Name
}

func normalizeCachePlatform(platform, cacheName, name string) string {
	lower := strings.ToLower(platform + " " + cacheName + " " + name)
	switch {
	case strings.Contains(lower, "redis"):
		return "redis"
	case strings.TrimSpace(platform) == "" || strings.EqualFold(platform, "database"):
		return "cache"
	default:
		return strings.ToLower(strings.TrimSpace(platform))
	}
}

func normalizeQueuePlatform(platform, topic, queue, name string) string {
	lower := strings.ToLower(platform + " " + topic + " " + queue + " " + name)
	switch {
	case strings.Contains(lower, "dynamodb_stream") || (strings.Contains(lower, "dynamodb") && strings.Contains(lower, "stream")):
		return "dynamodb_stream"
	case strings.TrimSpace(platform) == "":
		return "queue"
	default:
		return strings.ToLower(strings.TrimSpace(platform))
	}
}

func confidenceFloat(c protocol.Confidence) float64 {
	switch c {
	case protocol.ConfidenceHigh:
		return 1
	case protocol.ConfidenceMedium:
		return 0.75
	case protocol.ConfidenceLow:
		return 0.4
	default:
		return 0
	}
}

func mapFromAny(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

func flattenMetadataDetails(out map[string]any, metadata map[string]any) {
	if out == nil || metadata == nil {
		return
	}
	if details, ok := metadata["details"].(map[string]any); ok {
		for k, v := range details {
			out[k] = v
		}
		return
	}
	if details, ok := metadata["details"].(map[any]any); ok {
		for k, v := range details {
			out[fmt.Sprint(k)] = v
		}
	}
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func stringMeta(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func protocolFileMaps(path string) (map[string][]map[string]any, map[string][]map[string]any, map[string][]map[string]any, bool) {
	if !hasDiffMind protocol(path) {
		return nil, nil, nil, false
	}
	doc, err := ReadDiffMind protocol(path)
	if err != nil {
		return nil, nil, nil, false
	}
	arch := protocolToArchitecture(doc)
	exposures := map[string][]map[string]any{}
	dependencies := map[string][]map[string]any{}
	connections := map[string][]map[string]any{}
	for _, e := range arch.Exposures {
		exposures[e.Type] = append(exposures[e.Type], mapFromAny(e.BaseEntity))
	}
	for _, d := range arch.Dependencies {
		dependencies[d.Type] = append(dependencies[d.Type], mapFromAny(d.BaseEntity))
	}
	for _, c := range arch.Connections {
		connections["flow"] = append(connections["flow"], mapFromAny(c))
	}
	return exposures, dependencies, connections, true
}
