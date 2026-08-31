package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
)

type ArchGraph = archgraph.ArchGraph
type SchedulerNode = archgraph.SchedulerNode
type ServiceNode = archgraph.ServiceNode
type ConnectionSummary = archgraph.ConnectionSummary
type ExternalNode = archgraph.ExternalNode
type ResourceNode = archgraph.ResourceNode
type QueueNode = archgraph.QueueNode
type DatabaseNode = archgraph.DatabaseNode
type GraphEdge = archgraph.GraphEdge
type EntitySummary = archgraph.EntitySummary

// handleRunArchGraph serves the architecture graph for a run, derived from the
// DiffMind artifacts bound to each service repo in the run manifest.
func (s *Server) handleRunArchGraph(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	mft, err := s.store.GetRun(pid, rid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if graph, err := s.persistedArchGraphForRunFast(pid, rid, r); err == nil {
		if archGraphNeedsResourceIndex(graph) {
			if rebuilt, rebuildErr := s.archGraphForRun(pid, mft); rebuildErr == nil {
				graph = rebuilt
				_ = s.persistArchGraphFiles(pid, rid, rebuilt)
			}
		}
		graph = archGraphView(graph, r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(graph)
		return
	}

	serviceRepoDirs := map[string]string{}
	for _, ref := range mft.Repos {
		repo, err := s.store.GetRepo(pid, ref.RepoID)
		if err != nil {
			continue
		}
		if repo.Kind == "infra_repo" || ref.DiffMindRunID == "" {
			continue
		}
		if info, ok := artifacts.DiffMindRunByID(s.diffmindRunsDir, ref.DiffMindRunID); !ok || !artifacts.RunMatchesRepo(info, repo.Name, repo.ID, repo.Path) {
			continue
		}
		serviceRepoDirs[repo.Name] = filepath.Join(s.diffmindRunsDir, ref.DiffMindRunID)
	}
	if len(serviceRepoDirs) == 0 {
		writeErr(w, http.StatusNotFound, errors.New("no service repos with diffmind artifacts in this run"))
		return
	}

	g := archGraphView(buildArchitectureGraph(rid, serviceRepoDirs), r)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(g)
}

func archGraphNeedsResourceIndex(graph *ArchGraph) bool {
	if graph == nil || len(graph.ResourceNodes) > 0 {
		return false
	}
	return len(graph.DatabaseNodes) > 0 || len(graph.QueueNodes) > 0 || len(graph.SchedulerNodes) > 0
}

func archGraphView(graph *ArchGraph, r *http.Request) *ArchGraph {
	if archGraphRequestOverview(r) {
		return archgraph.Overview(graph)
	}
	return graph
}

func archGraphRequestOverview(r *http.Request) bool {
	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	return view == "" || view == "overview" || view == "metadata"
}

func buildArchitectureGraph(runID string, serviceRepoDirs map[string]string) *ArchGraph {
	return archgraph.Build(runID, serviceRepoDirs)
}

func (s *Server) fullArchGraphForRun(pid, rid string) (*ArchGraph, error) {
	mft, err := s.store.GetRun(pid, rid)
	if err != nil {
		return nil, err
	}
	if graph, err := s.persistedArchGraphForRunFast(pid, rid, &http.Request{URL: &url.URL{RawQuery: "view=full"}}); err == nil {
		if archGraphNeedsResourceIndex(graph) {
			if rebuilt, rebuildErr := s.archGraphForRun(pid, mft); rebuildErr == nil {
				graph = rebuilt
				_ = s.persistArchGraphFiles(pid, rid, rebuilt)
			}
		}
		return graph, nil
	}
	graph, err := s.archGraphForRun(pid, mft)
	if err != nil {
		return nil, err
	}
	_ = s.persistArchGraphFiles(pid, rid, graph)
	return graph, nil
}

func (s *Server) handleRunArchGraphTeam(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	graph, err := s.fullArchGraphForRun(pid, rid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	team := pathValue(r, "team")
	view, ok := archgraph.BuildTeamView(graph, team, r.URL.Query().Get("scope"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("team not found in architecture graph"))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleRunArchGraphService(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	graph, err := s.fullArchGraphForRun(pid, rid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	view, ok := archgraph.BuildServiceView(graph, pathValue(r, "service"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("service not found in architecture graph"))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleRunArchGraphResource(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	graph, err := s.fullArchGraphForRun(pid, rid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	view, ok := archgraph.BuildResourceView(graph, pathValue(r, "resource"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("resource not found in architecture graph"))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleRunArchGraphTrace(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	graph, err := s.fullArchGraphForRun(pid, rid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	serviceName := firstNonEmpty(r.URL.Query().Get("service"), r.URL.Query().Get("service_name"))
	objectID := firstNonEmpty(r.URL.Query().Get("object_id"), r.URL.Query().Get("entrypoint_id"), r.URL.Query().Get("flow_id"))
	view, ok := archgraph.BuildTraceView(graph, serviceName, objectID)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("trace service not found in architecture graph"))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleRunArchGraphFlow(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	graph, err := s.fullArchGraphForRun(pid, rid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	serviceName := firstNonEmpty(r.URL.Query().Get("service"), r.URL.Query().Get("service_name"))
	objectID := firstNonEmpty(r.URL.Query().Get("object_id"), r.URL.Query().Get("entrypoint_id"), r.URL.Query().Get("flow_id"))
	view, ok := archgraph.BuildFlowView(graph, serviceName, objectID, archgraph.FlowOptions{
		Depth:    queryInt(r, "depth", 6),
		MaxNodes: queryInt(r, "max_nodes", 500),
		Expand:   r.URL.Query().Get("expand"),
	})
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("flow service not found in architecture graph"))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleRunArchGraphImpact(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	graph, err := s.fullArchGraphForRun(pid, rid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	target := firstNonEmpty(r.URL.Query().Get("node"), r.URL.Query().Get("service"))
	view, ok := archgraph.BuildImpactView(graph, target, archgraph.FlowOptions{
		Depth:    queryInt(r, "depth", 6),
		MaxNodes: queryInt(r, "max_nodes", 500),
	})
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("impact target not found in architecture graph"))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleRunArchGraphEntrypoints(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	graph, err := s.fullArchGraphForRun(pid, rid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	refs := archgraph.SearchEntrypoints(graph, r.URL.Query().Get("q"), queryInt(r, "limit", 50))
	if refs == nil {
		refs = []archgraph.EntrypointRef{}
	}
	writeJSON(w, http.StatusOK, refs)
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func pathValue(r *http.Request, key string) string {
	value := r.PathValue(key)
	if decoded, err := url.PathUnescape(value); err == nil {
		return decoded
	}
	return value
}

func looksLikeHTTPMethodSlug(raw string) bool {
	return archgraph.LooksLikeHTTPMethodSlug(raw)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}
