package ui

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/agentapi"
)

// Do not capture credentials from MCP initialization. Stateful clients may
// change identity; each tool call must re-authenticate its CURRENT HTTP headers.
func (s *Server) invokeAgentOperation(ctx context.Context, call *mcp.CallToolRequest, req *http.Request) (agentapi.Result, error) {
	if call == nil || call.Extra == nil {
		return agentapi.Result{}, errors.New("HTTP management requires current request authentication context")
	}
	req = req.WithContext(ctx)
	for _, key := range []string{"Authorization", "X-DiffMind-Token", proxySecretHeader, proxyUserHeader, proxyRoleHeader} {
		for _, value := range call.Extra.Header.Values(key) {
			req.Header.Add(key, value)
		}
	}
	req.RemoteAddr = "mcp"
	result := &agentResponse{header: make(http.Header)}
	// All role/membership, mutation-idle, quota and audit behavior stays identical
	// to the UI. Only catalogued finite JSON routes reach this adapter.
	s.Handler().ServeHTTP(result, req)
	if result.overflow {
		return agentapi.Result{}, errors.New("response exceeds 8 MiB; narrow or paginate")
	}
	status := result.status
	if status == 0 {
		status = 200
	}
	return agentapi.Decode(status, result.header, &result.body)
}

type agentResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (r *agentResponse) Header() http.Header { return r.header }
func (r *agentResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *agentResponse) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	if r.body.Len()+len(b) > agentapi.MaxResponse {
		r.overflow = true
		return 0, errors.New("agent response limit")
	}
	return r.body.Write(b)
}
