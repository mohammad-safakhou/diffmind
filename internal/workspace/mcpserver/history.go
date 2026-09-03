package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type graphRunsInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project ID. Defaults to the configured or sole accessible project."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Nonnegative pagination offset, default 0."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Page size 1 to 500, default 100."`
}

type compareInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project ID. Both runs must belong to this project."`
	From    string `json:"from" jsonschema:"Exact baseline completed run ID from list_graph_runs."`
	To      string `json:"to" jsonschema:"Exact comparison completed run ID from list_graph_runs."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Nonnegative change offset, default 0."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Page size 1 to 500, default 100. Totals cover all changes."`
}

type pathInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project ID. Defaults to the configured or sole accessible project."`
	Run     string `json:"run,omitempty" jsonschema:"Completed graph run ID. Omit for latest completed graph."`
	From    string `json:"from" jsonschema:"Exact source service name or resource graph ID."`
	To      string `json:"to" jsonschema:"Exact destination service name or resource graph ID."`
	Depth   int    `json:"depth,omitempty" jsonschema:"Maximum directed hops, 1 to 20, default 6."`
}

type traceInput struct {
	Project  string `json:"project,omitempty" jsonschema:"Project ID. Defaults to the configured or sole accessible project."`
	Run      string `json:"run,omitempty" jsonschema:"Completed graph run ID. Use the run returned by get_service to keep IDs consistent."`
	Service  string `json:"service" jsonschema:"Exact service name from list_services."`
	ObjectID string `json:"object_id" jsonschema:"Exact object or flow ID from get_service. Names and fuzzy matches are not accepted."`
}

func (s *Server) addHistoryTools(server *mcp.Server) {
	mcp.AddTool(server, tool("list_graph_runs", "List graph runs", "Discover saved graph runs, timestamps, availability and knowledge-pack digests. Use exact completed IDs for comparison. graph_available checks artifact presence; reading validates content."),
		func(_ context.Context, _ *mcp.CallToolRequest, in graphRunsInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.GraphRuns(project, in.Offset, in.Limit)
			return nil, out, err
		})
	mcp.AddTool(server, tool("compare_graphs", "Compare graph versions", "Compare two saved graph snapshots: services, objects, local flows, resources, external systems and typed relationships. Includes before/after evidence and artifact/pack context, not causal claims or source-code diffs. Follow next_offset for all changes."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in compareInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.CompareGraphs(ctx, project, in.From, in.To, in.Offset, in.Limit)
			return nil, out, err
		})
	mcp.AddTool(server, tool("find_dependency_path", "Find dependency path", "Find one deterministic shortest directed dependency path between services/resources, retaining edge evidence. This is graph topology, not proof of execution. limited means the search was incomplete; not_found means no directed path in the examined saved graph."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in pathInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.FindPath(ctx, project, in.Run, in.From, in.To, in.Depth)
			return nil, out, err
		})
	mcp.AddTool(server, tool("get_object_trace", "Get object trace", "Inspect exact-ID local flow connections, evidence and directly matched dependency edges for a saved object. Missing local flows remain partial; service adjacency never proves cross-service execution continuity."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in traceInput) (*mcp.CallToolResult, any, error) {
			project, err := s.project(in.Project)
			if err != nil {
				return nil, nil, err
			}
			out, err := s.query.TraceObject(ctx, project, in.Run, in.Service, in.ObjectID)
			return nil, out, err
		})
}
