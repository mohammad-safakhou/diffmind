package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/artifacts"
)

type ArchGraph = archgraph.ArchGraph
type SchedulerNode = archgraph.SchedulerNode
type ServiceNode = archgraph.ServiceNode
type ConnectionSummary = archgraph.ConnectionSummary
type ExternalNode = archgraph.ExternalNode
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

	serviceRepoDirs := map[string]string{}
	for _, ref := range mft.Repos {
		repo, err := s.store.GetRepo(pid, ref.RepoID)
		if err != nil {
			continue
		}
		if repo.Kind == "infra_repo" || ref.DiffMindRunID == "" {
			continue
		}
		if ref.DiffMindRunID == artifacts.RepoArchfileRunID {
			serviceRepoDirs[repo.Name] = artifacts.RepoArchfilePath(repo.Path)
		} else {
			serviceRepoDirs[repo.Name] = filepath.Join(s.diffmindRunsDir, ref.DiffMindRunID)
		}
	}
	if len(serviceRepoDirs) == 0 {
		writeErr(w, http.StatusNotFound, errors.New("no service repos with diffmind artifacts in this run"))
		return
	}

	g := buildArchitectureGraph(rid, serviceRepoDirs)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(g)
}

func buildArchitectureGraph(runID string, serviceRepoDirs map[string]string) *ArchGraph {
	return archgraph.Build(runID, serviceRepoDirs)
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
