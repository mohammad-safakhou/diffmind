package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type PendingPermission struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"sessionID"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Permission string   `json:"permission"`
	Patterns   []string `json:"patterns"`
}

func (c *Client) ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error) {
	if !c.Enabled() {
		return nil, nil
	}
	endpoint := c.baseURL + "/permission"
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list permissions failed: %s %s", resp.Status, string(body))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var permissions []PendingPermission
	if err := json.Unmarshal(raw, &permissions); err == nil {
		return permissions, nil
	}
	var wrapped struct {
		Items []PendingPermission `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return wrapped.Items, nil
	}
	return nil, nil
}

func (c *Client) RespondPermission(ctx context.Context, sessionID, permissionID, directory, reply string) error {
	if !c.Enabled() || permissionID == "" {
		return nil
	}
	switch reply {
	case "allow":
		reply = "once"
	case "deny":
		reply = "reject"
	}
	endpoint := fmt.Sprintf("%s/permission/%s/reply", c.baseURL, permissionID)
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	body, _ := json.Marshal(map[string]string{"reply": reply})
	req, err := c.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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
		responseBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusNotFound && sessionID != "" {
			return c.respondPermissionLegacy(ctx, sessionID, permissionID, directory, reply)
		}
		return fmt.Errorf("respond permission failed: %s %s", resp.Status, string(responseBody))
	}
	return nil
}

func (c *Client) respondPermissionLegacy(ctx context.Context, sessionID, permissionID, directory, response string) error {
	endpoint := fmt.Sprintf("%s/session/%s/permissions/%s", c.baseURL, sessionID, permissionID)
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	body, _ := json.Marshal(map[string]string{"response": response})
	req, err := c.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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
		responseBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("respond permission (legacy) failed: %s %s", resp.Status, string(responseBody))
	}
	return nil
}

type PendingQuestion struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Question  string `json:"question"`
}

func (c *Client) ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error) {
	if !c.Enabled() {
		return nil, nil
	}
	endpoint := c.baseURL + "/question"
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list questions failed: %s %s", resp.Status, string(body))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var questions []PendingQuestion
	if err := json.Unmarshal(raw, &questions); err == nil {
		return questions, nil
	}
	var wrapped struct {
		Items []PendingQuestion `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return wrapped.Items, nil
	}
	return nil, nil
}

func (c *Client) RejectQuestion(ctx context.Context, requestID, directory string) error {
	if !c.Enabled() || requestID == "" {
		return nil
	}
	endpoint := fmt.Sprintf("%s/question/%s/reject", c.baseURL, requestID)
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
		return fmt.Errorf("reject question failed: %s %s", resp.Status, string(body))
	}
	return nil
}
