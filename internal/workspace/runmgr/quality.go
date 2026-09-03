package runmgr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
	"github.com/mohammad-safakhou/diffmind/protocol"
)

type architectureRunStats struct {
	serviceCount int
	edgeCount    int
	quality      store.GraphQuality
}

// ArchitectureStats returns the persisted-count equivalent for a graph run.
// UI read paths use it to enrich runs created before quality metadata existed.
func (m *Manager) ArchitectureStats(pid string, manifest store.RunManifest) (int, int, *store.GraphQuality) {
	stats := m.architectureStats(pid, manifest)
	if stats.serviceCount == 0 {
		return 0, 0, nil
	}
	quality := stats.quality
	return stats.serviceCount, stats.edgeCount, &quality
}

func (m *Manager) architectureStats(pid string, manifest store.RunManifest) architectureRunStats {
	stats := architectureRunStats{}
	serviceRepoDirs := m.architectureServiceRepoDirs(pid, manifest)
	if len(serviceRepoDirs) > 0 {
		var graph archgraph.ArchGraph
		data, err := os.ReadFile(filepath.Join(m.store.RunDir(pid, manifest.ID), "graph.json"))
		if err != nil || json.Unmarshal(data, &graph) != nil || graph.RunID != manifest.ID {
			graph = *archgraph.Build(manifest.ID, serviceRepoDirs)
		}
		stats.serviceCount = len(graph.Services)
		stats.edgeCount = len(graph.Edges)
	}
	pathShaped := map[string]bool{}
	unresolved := map[string]bool{}

	for _, ref := range manifest.Repos {
		repo, err := m.store.GetRepo(pid, ref.RepoID)
		if err != nil || repo.Kind == "infra_repo" || ref.DiffMindRunID == "" {
			continue
		}
		if info, ok := artifacts.DiffMindRunByID(m.diffmindRunsDir, ref.DiffMindRunID); !ok || !artifacts.RunMatchesRepo(info, repo.Name, repo.ID, repo.Path) {
			continue
		}
		runPath := filepath.Join(m.diffmindRunsDir, ref.DiffMindRunID)
		if strings.EqualFold(repo.DiffMindFreshness, "stale") {
			stats.quality.StaleRepos++
		}

		_, dependencies, _ := artifacts.ReadDiffMindFileMaps(runPath)

		for _, item := range dependencies["outbound_http"] {
			target, rawWasPath := statsHTTPServiceTarget(item)
			if rawWasPath {
				pathShaped[target] = true
			}
			if rawWasPath || target == "" {
				unresolved["unresolved-http-target"] = true
			}
		}

		if doc, err := artifacts.ReadProtocol(runPath); err == nil {
			if doc.Repository.Dirty {
				stats.quality.DirtyRepos++
			}
			stats.quality.MissingEvidenceObjects += countProtocolMissingEvidence(doc)
		}
	}

	stats.quality.PathShapedExternalNodes = len(pathShaped)
	stats.quality.UnresolvedExternalServices = len(unresolved)
	stats.quality.Warnings = graphQualityWarnings(stats.quality)
	return stats
}

func (m *Manager) persistArchitectureGraph(pid string, manifest store.RunManifest, supplements map[string]archgraph.Supplement) (*archgraph.ArchGraph, error) {
	serviceRepoDirs := m.architectureServiceRepoDirs(pid, manifest)
	if len(serviceRepoDirs) == 0 {
		return nil, nil
	}
	graph := archgraph.BuildWithSupplements(manifest.ID, serviceRepoDirs, supplements)
	data, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(m.store.RunDir(pid, manifest.ID), "graph.json"), append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	overviewData, err := json.MarshalIndent(archgraph.Overview(graph), "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(m.store.RunDir(pid, manifest.ID), "graph-overview.json"), append(overviewData, '\n'), 0o644); err != nil {
		return nil, err
	}
	return graph, nil
}

func (m *Manager) architectureServiceRepoDirs(pid string, manifest store.RunManifest) map[string]string {
	serviceRepoDirs := map[string]string{}
	for _, ref := range manifest.Repos {
		repo, err := m.store.GetRepo(pid, ref.RepoID)
		if err != nil || repo.Kind == "infra_repo" || ref.DiffMindRunID == "" {
			continue
		}
		if info, ok := artifacts.DiffMindRunByID(m.diffmindRunsDir, ref.DiffMindRunID); !ok || !artifacts.RunMatchesRepo(info, repo.Name, repo.ID, repo.Path) {
			continue
		}
		serviceRepoDirs[repo.Name] = filepath.Join(m.diffmindRunsDir, ref.DiffMindRunID)
	}
	return serviceRepoDirs
}

func statsHTTPServiceTarget(item map[string]any) (string, bool) {
	details := statsMap(item, "details")
	target := statsMap(details, "target")
	candidates := []string{
		statsString(details, "target_service"),
		statsString(details, "target_ref"),
		statsString(item, "instance"),
		statsString(target, "ref"),
		statsString(target, "service"),
		statsString(details, "target_url"),
		statsString(details, "url_template"),
		statsString(details, "url"),
		statsString(details, "base_url"),
		statsString(item, "name"),
	}
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if statsIsHTTPOperationLabel(raw) || strings.HasPrefix(raw, "/") || statsLooksLikeHTTPMethodSlug(raw) {
			return raw, true
		}
		raw = strings.TrimPrefix(raw, "service.")
		raw = strings.ReplaceAll(raw, "_", "-")
		return raw, false
	}
	return "", false
}

func statsMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key]; ok {
		if out, ok := v.(map[string]any); ok {
			return out
		}
	}
	return nil
}

func statsString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

func statsIsHTTPOperationLabel(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) < 2 {
		return false
	}
	switch strings.ToUpper(fields[0]) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func statsLooksLikeHTTPMethodSlug(raw string) bool {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "service.")
	raw = strings.ReplaceAll(strings.ToLower(raw), "_", "-")
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
		if raw == method || strings.HasPrefix(raw, method+"-") {
			return true
		}
	}
	return false
}

func countProtocolMissingEvidence(doc *protocol.Document) int {
	if doc == nil {
		return 0
	}
	missing := 0
	add := func(base protocol.ObjectiveBase) {
		if len(base.Observations) == 0 || len(base.EvidenceRefs) == 0 {
			missing++
		}
	}
	for _, v := range doc.Objects.HTTPEndpoints {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.HTTPCalls {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.DBResources {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.DBQueries {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.QueueConsumers {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.QueuePublishers {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.RPCEndpoints {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.RPCCalls {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.CLICommands {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.Activations {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.CacheOperations {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.ConfigReads {
		add(v.ObjectiveBase)
	}
	for _, v := range doc.Objects.FeatureFlags {
		add(v.ObjectiveBase)
	}
	return missing
}

func graphQualityWarnings(q store.GraphQuality) []string {
	var warnings []string
	if q.PathShapedExternalNodes > 0 {
		warnings = append(warnings, fmt.Sprintf("%d HTTP operation labels were suppressed from external service nodes; add diffmind-configuration.yaml http_targets or service aliases where needed", q.PathShapedExternalNodes))
	}
	if q.UnresolvedExternalServices > 0 {
		warnings = append(warnings, fmt.Sprintf("%d unresolved external HTTP service target remains; add deterministic aliases/config patterns when the target is company-specific", q.UnresolvedExternalServices))
	}
	if q.MissingEvidenceObjects > 0 {
		warnings = append(warnings, fmt.Sprintf("%d Protocol objectives are missing observation or evidence refs; improve detector evidence or mark imported/config-derived facts explicitly", q.MissingEvidenceObjects))
	}
	if q.StaleRepos > 0 {
		warnings = append(warnings, fmt.Sprintf("%d selected repos have stale DiffMind freshness metadata", q.StaleRepos))
	}
	if q.DirtyRepos > 0 {
		warnings = append(warnings, fmt.Sprintf("%d DiffMind runs were produced from dirty working trees", q.DirtyRepos))
	}
	return warnings
}
