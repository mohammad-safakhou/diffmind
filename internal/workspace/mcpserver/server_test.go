package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func testMCPServer(t *testing.T) (*Server, string) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(store.Project{Name: "Platform"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateRun(project.ID, store.RunManifest{Status: store.RunCompleted, StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	graph := archgraph.ArchGraph{RunID: run.ID,
		Services: []*archgraph.ServiceNode{{Name: "api", Known: true, Team: "core", HTTPRoutes: []archgraph.EntitySummary{{ID: "health", Name: "GET /health", Summary: "Health endpoint"}}}, {Name: "worker", Known: true, Team: "core"}},
		Edges:    []*archgraph.GraphEdge{{From: "worker", To: "api", Type: "http", Label: "GET /health"}},
	}
	data, _ := json.Marshal(graph)
	if err := os.WriteFile(filepath.Join(st.RunDir(project.ID, run.ID), "graph.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return New(query.New(st), "", "test"), project.ID
}

func TestMCPProtocolListsAndCallsTools(t *testing.T) {
	server, projectID := testMCPServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCPServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "diffmind-test", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s is not marked read-only", tool.Name)
		}
	}
	sort.Strings(names)
	want := []string{"get_dependencies", "get_graph_summary", "get_impact", "get_service", "list_projects", "list_services", "search_architecture"}
	if len(names) != len(want) {
		t.Fatalf("tools=%v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tools=%v", names)
		}
	}

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "list_services", Arguments: map[string]any{"project": projectID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool error: %+v", result.Content)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content type=%T", result.StructuredContent)
	}
	services, ok := structured["services"].([]any)
	if !ok || len(services) != 2 {
		t.Fatalf("structured=%#v", structured)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected text compatibility content")
	}

	bad, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "get_service", Arguments: map[string]any{"project": projectID, "service": "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatalf("missing service should be a tool error: %#v", bad)
	}
}
