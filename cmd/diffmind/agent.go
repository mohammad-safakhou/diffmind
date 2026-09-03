package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/agenthost"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/mcpserver"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func inheritedAgentListener() (net.Listener, error) {
	file := os.NewFile(3, "agent-listener")
	if file == nil {
		return nil, errors.New("agent-service requires an inherited listener")
	}
	defer file.Close()
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, err
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		listener.Close()
		return nil, errors.New("agent listener must be TCP loopback")
	}
	return listener, nil
}
func runAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	project := fs.String("project", "", "optional default project ID; no project needs to exist")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: diffmind agent [--project ID]")
	}
	home, err := filepath.Abs(config.Home())
	if err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	// One controller owns lifecycle/maintenance. Its lock remains held when the
	// backend is stopped for an offline operation. Never remove lock files.
	release, err := homelock.AcquireServer(filepath.Join(home, "agent-controller"))
	if err != nil {
		return fmt.Errorf("another local agent controller owns this home; use its HTTP /mcp endpoint for additional agents or a separate home: %w", err)
	}
	defer release()
	host, err := agenthost.New(binary, home)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = host.Start(ctx); err != nil {
		return err
	}
	defer host.Stop()
	st, err := store.New(home)
	if err != nil {
		return err
	}
	server := mcpserver.New(query.New(st), *project, version).WithManagement(host.Invoke).MCPServer()
	addAgentHostTools(server, host)
	if err = server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
func addAgentHostTools(server *mcp.Server, host *agenthost.Host) {
	mcp.AddTool(server, &mcp.Tool{Name: "agent_runtime", Description: "Manage the agent-owned local service without terminal commands. action: status, start, stop, restart, configure. status includes the optional dashboard URL. configure requires the complete settings object (read status first), persists settings and restarts. Stopping pauses refresh; subsequent workspace management starts the backend again. Disconnecting the owning agent stops it; use a shared deployment for always-on operation."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			Action   string              `json:"action"`
			Settings *agenthost.Settings `json:"settings,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			var err error
			switch in.Action {
			case "status":
			case "start":
				err = host.Start(ctx)
			case "stop":
				err = host.Stop()
			case "restart":
				if err = host.Stop(); err == nil {
					err = host.Start(ctx)
				}
			case "configure":
				if in.Settings == nil {
					return nil, nil, errors.New("settings required")
				}
				err = host.Configure(ctx, *in.Settings)
			default:
				return nil, nil, errors.New("action must be status/start/stop/restart/configure")
			}
			return nil, host.Status(), err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "agent_command", Description: "Agent-only local maintenance/knowledge-pack tooling. Executes this installed DiffMind binary, NEVER a shell. args starts with doctor, version, pack, backup or storage. Use [command, --help] to discover exact flags. Backend is stopped and restarted around the command, including on failure; commands have a 5-minute timeout. For backup restore/rotate or storage migrate, confirm must repeat that command (e.g. backup rotate). Backup create/rotate/restore and storage operations still require --offline. Use only user-authorized paths; no automatic retries after uncertain mutation outcomes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			Args    []string `json:"args"`
			Confirm string   `json:"confirm,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			out, err := host.Command(ctx, in.Args, in.Confirm)
			if err != nil {
				return nil, out, err
			}
			if out.ExitCode != 0 {
				raw, _ := json.Marshal(out)
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: out}, nil, nil
			}
			return nil, out, nil
		})
}
