// Package mcpserver exposes DiffMind's deterministic architecture graph to
// coding agents over the Model Context Protocol.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
)

type Server struct {
	query          *query.Service
	defaultProject string
	version        string
}

func New(q *query.Service, defaultProject, version string) *Server {
	if version == "" {
		version = "dev"
	}
	return &Server{query: q, defaultProject: defaultProject, version: version}
}

type projectInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project ID. Defaults to the configured or sole accessible project."`
	Run     string `json:"run,omitempty" jsonschema:"Completed graph run ID. Omit to use the latest completed run."`
}

type serviceInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project ID. Defaults to the configured or sole accessible project."`
	Run     string `json:"run,omitempty" jsonschema:"Completed graph run ID. Omit to use the latest completed run."`
	Service string `json:"service" jsonschema:"Exact service name from list_services."`
}

type dependenciesInput struct {
	Project   string `json:"project,omitempty" jsonschema:"Project ID. Defaults to the configured or sole accessible project."`
	Run       string `json:"run,omitempty" jsonschema:"Completed graph run ID. Omit to use the latest completed run."`
	Service   string `json:"service" jsonschema:"Exact service name from list_services."`
	Direction string `json:"direction,omitempty" jsonschema:"Dependency direction: inbound, outbound, or both. Defaults to both."`
}

type searchInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project ID. Defaults to the configured or sole accessible project."`
	Run     string `json:"run,omitempty" jsonschema:"Completed graph run ID. Omit to use the latest completed run."`
	Query   string `json:"query" jsonschema:"Case-insensitive text to find in services, entrypoints, dependencies, and resources."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum matches, from 1 to 200. Defaults to 50."`
}

type impactInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project ID. Defaults to the configured or sole accessible project."`
	Run     string `json:"run,omitempty" jsonschema:"Completed graph run ID. Omit to use the latest completed run."`
	Target  string `json:"target" jsonschema:"Service name or resource graph ID whose blast radius should be calculated."`
	Depth   int    `json:"depth,omitempty" jsonschema:"Maximum graph traversal depth, from 1 to 20. Defaults to 6."`
}

func (s *Server) MCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "diffmind", Title: "DiffMind Architecture Graph", Version: s.version, WebsiteURL: "https://github.com/mohammad-safakhou/diffmind"}, nil)
	readOnly := &mcp.ToolAnnotations{Title: "List DiffMind projects", ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	mcp.AddTool(server, &mcp.Tool{Name: "list_projects", Title: "List projects", Description: "List DiffMind projects accessible to this connection and whether each has a queryable architecture graph.", Annotations: readOnly},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			projects, err := s.query.Projects()
			return nil, map[string]any{"projects": projects}, err
		})

	mcp.AddTool(server, tool("get_graph_summary", "Get graph summary", "Return counts, teams, connectivity, and quality warnings for a project's architecture graph."),
		func(_ context.Context, _ *mcp.CallToolRequest, in projectInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.Summary(project, in.Run)
			return nil, out, err
		})
	mcp.AddTool(server, tool("list_services", "List services", "List services with team, repository, freshness, entrypoint, and dependency counts."),
		func(_ context.Context, _ *mcp.CallToolRequest, in projectInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			services, err := s.query.Services(project, in.Run)
			return nil, map[string]any{"project_id": project, "services": services}, err
		})
	mcp.AddTool(server, tool("get_service", "Get service", "Inspect one service, its entrypoints, evidence summaries, neighbors, resources, and inbound/outbound edges."),
		func(_ context.Context, _ *mcp.CallToolRequest, in serviceInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.Service(project, in.Run, in.Service)
			return nil, out, err
		})
	mcp.AddTool(server, tool("get_dependencies", "Get dependencies", "Return typed inbound, outbound, or bidirectional graph edges for a service."),
		func(_ context.Context, _ *mcp.CallToolRequest, in dependenciesInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.Dependencies(project, in.Run, in.Service, in.Direction)
			return nil, out, err
		})
	mcp.AddTool(server, tool("search_architecture", "Search architecture", "Search services, endpoints, dependencies, resources, and external systems in the current graph."),
		func(_ context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.Search(project, in.Run, in.Query, in.Limit)
			return nil, out, err
		})
	mcp.AddTool(server, tool("get_impact", "Get impact", "Calculate the deterministic blast radius of changing a service or resource."),
		func(_ context.Context, _ *mcp.CallToolRequest, in impactInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.Impact(project, in.Run, in.Target, in.Depth)
			return nil, out, err
		})
	s.addHistoryTools(server)
	return server
}

func (s *Server) Run(ctx context.Context) error {
	return s.MCPServer().Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) project(requested string) (string, error) {
	if requested == "" {
		requested = s.defaultProject
	}
	p, err := s.query.ResolveProject(requested)
	if err != nil {
		return "", fmt.Errorf("select project: %w", err)
	}
	return p.ID, nil
}

func tool(name, title, description string) *mcp.Tool {
	return &mcp.Tool{Name: name, Title: title, Description: description, Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}}
}

func boolPtr(v bool) *bool { return &v }
