// Package query provides the read-only architecture query surface shared by
// DiffMind's HTTP API and MCP server.
package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

var (
	ErrNoProjects       = errors.New("no DiffMind projects exist")
	ErrProjectRequired  = errors.New("project is required when more than one project exists")
	ErrNoCompletedGraph = errors.New("project has no completed graph run")
	ErrServiceNotFound  = errors.New("service not found")
)

type Service struct {
	store  *store.Store
	access func(string) error
}

func New(st *store.Store) *Service { return &Service{store: st} }

// NewWithAccess filters discovery before reading project contents and checks
// explicit/default project selection. A nil policy is trusted local access.
func NewWithAccess(st *store.Store, access func(string) error) *Service {
	return &Service{store: st, access: access}
}

func (s *Service) visibleProjects() ([]store.Project, error) {
	projects, err := s.store.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]store.Project, 0, len(projects))
	for _, p := range projects {
		if s.access != nil {
			if err := s.access(p.ID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				return nil, err
			}
		}
		out = append(out, p)
	}
	return out, nil
}

type Project struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	RepositoryCount int    `json:"repository_count"`
	LatestRunID     string `json:"latest_run_id,omitempty"`
	GraphReady      bool   `json:"graph_ready"`
}

type GraphSummary struct {
	ProjectID     string                       `json:"project_id"`
	RunID         string                       `json:"run_id"`
	ServiceCount  int                          `json:"service_count"`
	EdgeCount     int                          `json:"edge_count"`
	ExternalCount int                          `json:"external_count"`
	ResourceCount int                          `json:"resource_count"`
	Teams         []string                     `json:"teams"`
	Connectivity  *archgraph.ConnectivityStats `json:"connectivity,omitempty"`
	Quality       *store.GraphQuality          `json:"quality,omitempty"`
}

type ServiceSummary struct {
	Name          string `json:"name"`
	Team          string `json:"team,omitempty"`
	RepoID        string `json:"repo_id,omitempty"`
	RepoPath      string `json:"repo_path,omitempty"`
	ComponentKind string `json:"component_kind,omitempty"`
	Freshness     string `json:"freshness,omitempty"`
	Entrypoints   int    `json:"entrypoints"`
	Dependencies  int    `json:"dependencies"`
	InboundEdges  int    `json:"inbound_edges"`
	OutboundEdges int    `json:"outbound_edges"`
}

type DependencyResult struct {
	ProjectID string                 `json:"project_id"`
	RunID     string                 `json:"run_id"`
	Service   string                 `json:"service"`
	Direction string                 `json:"direction"`
	Edges     []*archgraph.GraphEdge `json:"edges"`
}

type SearchResult struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service,omitempty"`
	Team    string `json:"team,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type SearchResponse struct {
	ProjectID string         `json:"project_id"`
	RunID     string         `json:"run_id"`
	Query     string         `json:"query"`
	Results   []SearchResult `json:"results"`
}

func (s *Service) ResolveProject(requested string) (*store.Project, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		if !store.ValidID(requested) {
			return nil, store.ErrNotFound
		}
		if s.access != nil {
			if err := s.access(requested); err != nil {
				return nil, err
			}
		}
		p, err := s.store.GetProject(requested)
		if err != nil {
			return nil, fmt.Errorf("project %q: %w", requested, err)
		}
		return p, nil
	}
	projects, err := s.visibleProjects()
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, ErrNoProjects
	}
	if len(projects) > 1 {
		ids := make([]string, 0, len(projects))
		for _, p := range projects {
			ids = append(ids, p.ID)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("%w: choose one of %s", ErrProjectRequired, strings.Join(ids, ", "))
	}
	return &projects[0], nil
}

func (s *Service) Projects() ([]Project, error) {
	projects, err := s.visibleProjects()
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(projects))
	for _, p := range projects {
		repos, err := s.store.ListRepos(p.ID)
		if err != nil {
			return nil, err
		}
		item := Project{ID: p.ID, Name: p.Name, RepositoryCount: len(repos)}
		if run, _, err := s.loadGraph(p.ID, ""); err == nil {
			item.LatestRunID, item.GraphReady = run.ID, true
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Service) Load(projectID, runID string) (*store.RunManifest, *archgraph.ArchGraph, error) {
	p, err := s.ResolveProject(projectID)
	if err != nil {
		return nil, nil, err
	}
	return s.loadGraph(p.ID, runID)
}

func (s *Service) loadGraph(projectID, runID string) (*store.RunManifest, *archgraph.ArchGraph, error) {
	if !store.ValidID(projectID) || (runID != "" && !store.ValidID(runID)) {
		return nil, nil, store.ErrNotFound
	}
	var run *store.RunManifest
	if strings.TrimSpace(runID) != "" {
		var err error
		run, err = s.store.GetRun(projectID, runID)
		if err != nil {
			return nil, nil, fmt.Errorf("run %q: %w", runID, err)
		}
		if run.Status != store.RunCompleted {
			return nil, nil, fmt.Errorf("run %q is %s, not completed", run.ID, run.Status)
		}
	} else {
		runs, err := s.store.ListRuns(projectID)
		if err != nil {
			return nil, nil, err
		}
		for i := range runs {
			if runs[i].Status != store.RunCompleted {
				continue
			}
			if _, err := os.Stat(filepath.Join(s.store.RunDir(projectID, runs[i].ID), "graph.json")); err == nil {
				copy := runs[i]
				run = &copy
				break
			}
		}
		if run == nil {
			return nil, nil, ErrNoCompletedGraph
		}
	}
	data, err := os.ReadFile(filepath.Join(s.store.RunDir(projectID, run.ID), "graph.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("%w: graph artifact is missing for run %s", ErrNoCompletedGraph, run.ID)
		}
		return nil, nil, err
	}
	var graph *archgraph.ArchGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, nil, fmt.Errorf("decode graph for run %s: %w", run.ID, err)
	}
	if graph == nil {
		return nil, nil, fmt.Errorf("graph for run %s is null", run.ID)
	}
	if graph.RunID != "" && graph.RunID != run.ID {
		return nil, nil, fmt.Errorf("graph run ID %q does not match requested run %q", graph.RunID, run.ID)
	}
	graph.RunID = run.ID
	return run, graph, nil
}

func (s *Service) Summary(projectID, runID string) (*GraphSummary, error) {
	run, graph, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	teams := map[string]bool{}
	for _, svc := range graph.Services {
		if svc != nil && strings.TrimSpace(svc.Team) != "" {
			teams[svc.Team] = true
		}
	}
	teamList := make([]string, 0, len(teams))
	for team := range teams {
		teamList = append(teamList, team)
	}
	sort.Strings(teamList)
	return &GraphSummary{
		ProjectID: run.ProjectID, RunID: run.ID, ServiceCount: len(graph.Services), EdgeCount: len(graph.Edges),
		ExternalCount: len(graph.ExternalNodes), ResourceCount: len(graph.ResourceNodes), Teams: teamList,
		Connectivity: graph.Connectivity, Quality: run.GraphQuality,
	}, nil
}

func (s *Service) Services(projectID, runID string) ([]ServiceSummary, error) {
	_, graph, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	inbound, outbound := map[string]int{}, map[string]int{}
	for _, edge := range graph.Edges {
		if edge != nil {
			outbound[edge.From]++
			inbound[edge.To]++
		}
	}
	out := make([]ServiceSummary, 0, len(graph.Services))
	for _, svc := range graph.Services {
		if svc == nil {
			continue
		}
		entrypoints := svc.EntrypointCount
		if entrypoints == 0 {
			entrypoints = len(svc.HTTPRoutes) + len(svc.RPCEndpoints) + len(svc.QueueConsumers) + len(svc.ScheduledJobs) + len(svc.Webhooks) + len(svc.CLICommands)
		}
		out = append(out, ServiceSummary{Name: svc.Name, Team: svc.Team, RepoID: svc.RepoID, RepoPath: svc.RepoPath,
			ComponentKind: svc.ComponentKind, Freshness: svc.DiffMindFreshness, Entrypoints: entrypoints,
			Dependencies: len(svc.Dependencies), InboundEdges: inbound[svc.Name], OutboundEdges: outbound[svc.Name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Service) Service(projectID, runID, name string) (*archgraph.ServiceView, error) {
	_, graph, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	view, ok := archgraph.BuildServiceView(graph, strings.TrimSpace(name))
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, name)
	}
	return view, nil
}

func (s *Service) Dependencies(projectID, runID, name, direction string) (*DependencyResult, error) {
	run, graph, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction == "" {
		direction = "both"
	}
	if direction != "inbound" && direction != "outbound" && direction != "both" {
		return nil, fmt.Errorf("direction must be inbound, outbound, or both")
	}
	if _, ok := archgraph.BuildServiceView(graph, name); !ok {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, name)
	}
	edges := make([]*archgraph.GraphEdge, 0)
	for _, edge := range graph.Edges {
		if edge == nil {
			continue
		}
		if (direction == "outbound" || direction == "both") && edge.From == name ||
			(direction == "inbound" || direction == "both") && edge.To == name {
			edges = append(edges, edge)
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Type < edges[j].Type
	})
	return &DependencyResult{ProjectID: run.ProjectID, RunID: run.ID, Service: name, Direction: direction, Edges: edges}, nil
}

func (s *Service) Impact(projectID, runID, target string, depth int) (*archgraph.FlowView, error) {
	_, graph, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	if depth <= 0 {
		depth = 6
	}
	if depth > 20 {
		depth = 20
	}
	view, ok := archgraph.BuildImpactView(graph, strings.TrimSpace(target), archgraph.FlowOptions{Depth: depth, MaxNodes: 500})
	if !ok {
		return nil, fmt.Errorf("impact target not found: %s", target)
	}
	return view, nil
}

func (s *Service) Search(projectID, runID, text string, limit int) (*SearchResponse, error) {
	run, graph, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(text))
	if q == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	results := make([]SearchResult, 0)
	match := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), q) {
				return true
			}
		}
		return false
	}
	add := func(r SearchResult) {
		if len(results) < limit {
			results = append(results, r)
		}
	}
	for _, svc := range graph.Services {
		if svc == nil || len(results) >= limit {
			continue
		}
		if match(svc.Name, svc.Team, svc.ComponentKind, svc.ComponentType) {
			add(SearchResult{Kind: "service", ID: svc.Name, Name: svc.Name, Service: svc.Name, Team: svc.Team})
		}
		groups := []struct {
			kind  string
			items []archgraph.EntitySummary
		}{
			{"http_endpoint", svc.HTTPRoutes}, {"rpc_endpoint", svc.RPCEndpoints}, {"queue_consumer", svc.QueueConsumers},
			{"scheduled_job", svc.ScheduledJobs}, {"webhook", svc.Webhooks}, {"cli_command", svc.CLICommands}, {"dependency", svc.Dependencies},
		}
		for _, group := range groups {
			for _, item := range group.items {
				if len(results) >= limit {
					break
				}
				if match(item.ID, item.Name, item.Summary) {
					add(SearchResult{Kind: group.kind, ID: item.ID, Name: item.Name, Service: svc.Name, Team: svc.Team, Summary: item.Summary})
				}
			}
		}
	}
	for _, node := range graph.ResourceNodes {
		if node != nil && len(results) < limit && match(node.ID, node.GraphID, node.Name, node.Kind, node.Platform) {
			add(SearchResult{Kind: node.Kind, ID: first(node.GraphID, node.ID), Name: node.Name, Service: node.OwnerService, Team: node.OwnerTeam})
		}
	}
	for _, node := range graph.ExternalNodes {
		if node != nil && len(results) < limit && match(node.Name, node.Kind) {
			add(SearchResult{Kind: "external_" + node.Kind, ID: node.Name, Name: node.Name})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Kind != results[j].Kind {
			return results[i].Kind < results[j].Kind
		}
		if results[i].Service != results[j].Service {
			return results[i].Service < results[j].Service
		}
		return results[i].Name < results[j].Name
	})
	return &SearchResponse{ProjectID: run.ProjectID, RunID: run.ID, Query: text, Results: results}, nil
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
