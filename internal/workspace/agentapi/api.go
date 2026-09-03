// Package agentapi defines the finite, discoverable management contract shared
// by local agents and authenticated remote MCP. It is not an arbitrary HTTP proxy.
package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const MaxBody = 1 << 20
const MaxResponse = 8 << 20

type Operation struct {
	Name        string `json:"name"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	BodyExample any    `json:"body_example,omitempty"`
	Destructive bool   `json:"destructive"`
}
type Input struct {
	Operation string            `json:"operation" jsonschema:"Exact operation name from describe_management. Never an HTTP URL or arbitrary command."`
	Selectors map[string]string `json:"selectors,omitempty" jsonschema:"Path placeholders from the operation, e.g. pid (project ID), rid (repository or graph run ID), jid, pack_id, tid."`
	Query     map[string]string `json:"query,omitempty" jsonschema:"Query parameters documented by the operation."`
	Body      map[string]any    `json:"body,omitempty" jsonschema:"JSON request body, using describe_management's example and description."`
	Confirm   string            `json:"confirm,omitempty" jsonschema:"For destructive operations, repeat the exact operation name to confirm its intended scope."`
}
type Result struct {
	Status     int    `json:"status"`
	Data       any    `json:"data"`
	RetryAfter string `json:"retry_after,omitempty"`
}
type Invoke func(context.Context, *mcp.CallToolRequest, *http.Request) (Result, error)

func Find(name string) (Operation, bool) {
	for _, op := range Operations {
		if op.Name == name {
			return op, true
		}
	}
	return Operation{}, false
}

func Request(ctx context.Context, in Input, readOnly bool) (*http.Request, error) {
	op, ok := Find(in.Operation)
	if !ok {
		return nil, fmt.Errorf("unknown operation %q; call describe_management", in.Operation)
	}
	if readOnly && op.Method != http.MethodGet {
		return nil, fmt.Errorf("use manage_workspace for %s", op.Name)
	}
	if !readOnly && op.Method == http.MethodGet {
		return nil, fmt.Errorf("use inspect_workspace for %s", op.Name)
	}
	if op.Destructive && in.Confirm != op.Name {
		return nil, fmt.Errorf("%s requires confirm=%q; inspect the exact target before deleting/revoking", op.Name, op.Name)
	}
	p := op.Path
	for key, value := range in.Selectors {
		placeholder := "{" + key + "}"
		if !strings.Contains(p, placeholder) {
			return nil, fmt.Errorf("unexpected selector %q", key)
		}
		if value == "" || len(value) > 512 || value == "." || value == ".." || strings.ContainsAny(value, "/\\%?#\x00\r\n") {
			return nil, fmt.Errorf("invalid selector %q", key)
		}
		p = strings.ReplaceAll(p, placeholder, url.PathEscape(value))
	}
	if strings.Contains(p, "{") {
		return nil, fmt.Errorf("missing selectors for %s", p)
	}
	values := url.Values{}
	for k, v := range in.Query {
		if k == "" || len(k) > 100 || len(v) > 4096 {
			return nil, fmt.Errorf("invalid query parameter")
		}
		values.Set(k, v)
	}
	if len(values) > 32 {
		return nil, fmt.Errorf("too many query parameters")
	}
	if len(values) > 0 {
		p += "?" + values.Encode()
	}
	body, err := json.Marshal(in.Body)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBody {
		return nil, fmt.Errorf("body exceeds 1 MiB")
	}
	if op.Method == http.MethodGet && len(in.Body) > 0 {
		return nil, fmt.Errorf("read operations do not accept a body")
	}
	if in.Body == nil {
		body = []byte("{}")
	}
	req, err := http.NewRequestWithContext(ctx, op.Method, p, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func Decode(status int, headers http.Header, body io.Reader) (Result, error) {
	b, err := io.ReadAll(io.LimitReader(body, MaxResponse+1))
	if err != nil {
		return Result{}, err
	}
	if len(b) > MaxResponse {
		return Result{}, fmt.Errorf("response exceeds 8 MiB; narrow the query or paginate")
	}
	var data any
	if len(bytes.TrimSpace(b)) > 0 {
		if err = json.Unmarshal(b, &data); err != nil {
			data = string(b)
		}
	}
	return Result{Status: status, Data: data, RetryAfter: headers.Get("Retry-After")}, nil
}

func AddTools(server *mcp.Server, invoke Invoke) {
	mcp.AddTool(server, &mcp.Tool{Name: "describe_management", Description: "Discover all project, repository, ingestion, job, pack, configuration, permissions, token and quota operations. Call before using inspect_workspace/manage_workspace. Operations remain subject to the authenticated caller's permissions.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			Operation string `json:"operation,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			if in.Operation != "" {
				op, ok := Find(in.Operation)
				if !ok {
					return nil, nil, fmt.Errorf("unknown operation")
				}
				return nil, op, nil
			}
			return nil, map[string]any{"operations": Operations, "workflow": []string{"list projects before creating; do not retry creation blindly after a lost response", "create_project", "import_repositories with dry_run=true to preview", "start_ingestion with import or {} for incremental refresh", "get_ingestion until completed; inspect failures before retry", "query graph and verify source evidence"}}, nil
		})
	for _, readOnly := range []bool{true, false} {
		name, desc := "manage_workspace", "Create/configure projects, import repositories, build/update/cancel/retry graphs, teach packs, and administer access/tokens/limits. Use describe_management first. Accepted async work is NOT completed: inspect its persisted status. Mutations are audited and permission checked. No automatic mutation retries."
		if readOnly {
			name = "inspect_workspace"
			desc = "Inspect configuration, repositories, work status/history, capabilities, access, packs, and limits using an operation from describe_management. Does not mutate workspace state."
		}
		mcp.AddTool(server, &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: boolPointer(!readOnly), OpenWorldHint: boolPointer(!readOnly)}},
			func(ctx context.Context, call *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
				req, err := Request(ctx, in, readOnly)
				if err != nil {
					return nil, nil, err
				}
				result, err := invoke(ctx, call, req)
				if err != nil {
					return nil, nil, err
				}
				if result.Status >= 400 {
					raw, _ := json.Marshal(result)
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: result}, nil, nil
				}
				return nil, result, nil
			})
	}
}
func boolPointer(v bool) *bool { return &v }
