// Package opencode provides an HTTP client for the OpenCode LLM server.
package opencode

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client communicates with the OpenCode server.
type Client struct {
	baseURL    string
	httpClient *http.Client
	authHeader string
	providerID string
	modelID    string
	variant    string
}

// NewClient creates a new OpenCode client.
func NewClient(baseURL, providerID, modelID, variant, username, password string, timeout time.Duration) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: timeout},
		providerID: providerID,
		modelID:    modelID,
		variant:    variant,
	}
	if username != "" && password != "" {
		creds := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		c.authHeader = "Basic " + creds
	}
	return c
}

// Health checks if the server is reachable.
func (c *Client) Health() error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/global/health", nil)
	if err != nil {
		return err
	}
	c.applyAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opencode health check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("opencode health check returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Session represents an OpenCode session.
type Session struct {
	ID string `json:"id"`
}

// CreateSession creates a new session scoped to a directory.
func (c *Client) CreateSession(directory string) (*Session, error) {
	url := c.baseURL + "/session?directory=" + directory
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create session returned %d: %s", resp.StatusCode, string(body))
	}
	var s Session
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("parse session response: %w", err)
	}
	return &s, nil
}

// DeleteSession deletes a session.
func (c *Client) DeleteSession(sessionID string) error {
	url := c.baseURL + "/session/" + sessionID
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	c.applyAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	defer resp.Body.Close()
	return nil
}

// MessageRequest is the payload for sending a prompt.
type MessageRequest struct {
	Format  MessageFormat `json:"format,omitempty"`
	Parts   []MessagePart `json:"parts"`
	Model   *MessageModel `json:"model,omitempty"`
	Variant string        `json:"variant,omitempty"`
}

type MessageFormat struct {
	Type       string          `json:"type,omitempty"`
	Schema     json.RawMessage `json:"schema,omitempty"`
	RetryCount int             `json:"retryCount,omitempty"`
}

type MessagePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type MessageModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// MessageResponse is the response from sending a prompt.
type MessageResponse struct {
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
	Info struct {
		Structured json.RawMessage `json:"structured,omitempty"`
	} `json:"info"`
}

// Prompt sends a text prompt and returns the text response.
func (c *Client) Prompt(sessionID, prompt string) (string, error) {
	payload := MessageRequest{
		Parts: []MessagePart{{Type: "text", Text: prompt}},
	}
	if c.providerID != "" && c.modelID != "" {
		payload.Model = &MessageModel{
			ProviderID: c.providerID,
			ModelID:    c.modelID,
		}
	}
	if c.variant != "" {
		payload.Variant = c.variant
	}
	return c.sendMessage(sessionID, payload)
}

// PromptStructured sends a prompt with a JSON schema for structured output.
func (c *Client) PromptStructured(sessionID, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	payload := MessageRequest{
		Format: MessageFormat{
			Type:       "json_schema",
			Schema:     schema,
			RetryCount: 1,
		},
		Parts: []MessagePart{{Type: "text", Text: prompt}},
	}
	if c.providerID != "" && c.modelID != "" {
		payload.Model = &MessageModel{
			ProviderID: c.providerID,
			ModelID:    c.modelID,
		}
	}
	if c.variant != "" {
		payload.Variant = c.variant
	}

	text, err := c.sendMessage(sessionID, payload)
	if err != nil {
		return nil, err
	}
	// Try to extract JSON from the response.
	raw := extractJSON(text)
	if raw == nil {
		return nil, fmt.Errorf("no JSON found in response: %.200s", text)
	}
	return raw, nil
}

func (c *Client) sendMessage(sessionID string, payload MessageRequest) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}
	url := c.baseURL + "/session/" + sessionID + "/message"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("send message returned %d: %s", resp.StatusCode, string(respBody))
	}

	var mr MessageResponse
	if err := json.Unmarshal(respBody, &mr); err != nil {
		return "", fmt.Errorf("parse message response: %w", err)
	}

	// Prefer structured output.
	if mr.Info.Structured != nil && len(mr.Info.Structured) > 0 {
		return string(mr.Info.Structured), nil
	}
	// Fall back to text parts.
	var texts []string
	for _, p := range mr.Parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func (c *Client) applyAuth(req *http.Request) {
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
}

// extractJSON tries to find a JSON object or array in the text.
func extractJSON(text string) json.RawMessage {
	// Try fenced code blocks first.
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if json.Valid([]byte(candidate)) {
				return json.RawMessage(candidate)
			}
		}
	}
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + len("```")
		// skip optional language tag on same line
		if nl := strings.Index(text[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if json.Valid([]byte(candidate)) {
				return json.RawMessage(candidate)
			}
		}
	}
	// Try raw JSON.
	text = strings.TrimSpace(text)
	if (strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[")) && json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	return nil
}
