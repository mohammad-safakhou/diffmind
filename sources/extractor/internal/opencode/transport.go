package opencode

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type Client struct {
	baseURL    string
	providerID string
	modelID    string
	variant    string
	username   string
	password   string
	httpClient *http.Client
}

func New(baseURL, providerID, modelID, variant, username, password string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		providerID: providerID,
		modelID:    modelID,
		variant:    variant,
		username:   username,
		password:   password,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) Enabled() bool {
	return c.baseURL != ""
}

func (c *Client) Health(ctx context.Context) error {
	if !c.Enabled() {
		return fmt.Errorf("opencode disabled")
	}
	util.Debug("opencode.client", "health request", map[string]any{"url": c.baseURL + "/global/health"})
	req, err := c.newRequest(ctx, http.MethodGet, c.baseURL+"/global/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("health failed: %s %s", resp.Status, string(body))
	}
	util.Debug("opencode.client", "health response", map[string]any{"status": resp.StatusCode})
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	c.addAuth(req)
	return req, nil
}

func (c *Client) addAuth(req *http.Request) {
	if c.password == "" {
		return
	}
	user := c.username
	if user == "" {
		user = "opencode"
	}
	token := base64.StdEncoding.EncodeToString([]byte(user + ":" + c.password))
	req.Header.Set("Authorization", "Basic "+token)
}
