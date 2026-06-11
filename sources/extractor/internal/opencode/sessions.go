package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func (c *Client) CreateSession(ctx context.Context, directory string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("opencode disabled")
	}
	util.Debug("opencode.client", "create session request", map[string]any{"directory": directory})
	endpoint := c.baseURL + "/session"
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	req, err := c.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session failed: %s %s", resp.Status, string(body))
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.ID == "" {
		return "", fmt.Errorf("session response missing id")
	}
	util.Debug("opencode.client", "create session response", map[string]any{"session_id": payload.ID})
	return payload.ID, nil
}

func (c *Client) AbortSession(ctx context.Context, sessionID, directory string) error {
	if !c.Enabled() || sessionID == "" {
		return nil
	}
	endpoint := fmt.Sprintf("%s/session/%s/abort", c.baseURL, sessionID)
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	req, err := c.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("abort session failed: %s %s", resp.Status, string(body))
	}
	util.Trace("opencode.client", "abort session ok", map[string]any{"session_id": sessionID})
	return nil
}

func (c *Client) DeleteSession(ctx context.Context, sessionID, directory string) error {
	if !c.Enabled() || sessionID == "" {
		return nil
	}
	util.Trace("opencode.client", "delete session request", map[string]any{"session_id": sessionID})
	endpoint := fmt.Sprintf("%s/session/%s", c.baseURL, sessionID)
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	req, err := c.newRequest(ctx, http.MethodDelete, endpoint, nil)
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
		return fmt.Errorf("delete session failed: %s %s", resp.Status, string(body))
	}
	util.Trace("opencode.client", "delete session response", map[string]any{
		"session_id": sessionID, "status": resp.StatusCode,
	})
	return nil
}
