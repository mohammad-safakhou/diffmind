package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for name, value := range t.headers {
		clone.Header.Set(name, value)
	}
	return t.base.RoundTrip(clone)
}

func TestRemoteMCPProtocolRequiresAuthAndListsTools(t *testing.T) {
	srv := newAuthTestServer(t)
	srv.SetAuthToken("company-secret")
	srv.SetTrustedProxySecret("proxy-secret")
	srv.SetVersion("test-version")
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()

	unauthorized, err := http.Get(httpServer.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized MCP status=%d", unauthorized.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "diffmind-http-test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: headerRoundTripper{headers: map[string]string{
			proxySecretHeader: "proxy-secret",
			proxyUserHeader:   "viewer@example.test",
			proxyRoleHeader:   "viewer",
		}, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 11 {
		t.Fatalf("tool count=%d, want 11", len(listed.Tools))
	}
}
