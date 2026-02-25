package opencode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type Client struct {
	baseURL    string
	providerID string
	modelID    string
	username   string
	password   string
	httpClient *http.Client
}

func New(baseURL, providerID, modelID, username, password string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		providerID: providerID,
		modelID:    modelID,
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/global/health", nil)
	if err != nil {
		return err
	}
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("health failed: %s %s", resp.Status, string(b))
	}
	util.Debug("opencode.client", "health response", map[string]any{"status": resp.StatusCode})
	return nil
}

func (c *Client) CreateSession(ctx context.Context, directory string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("opencode disabled")
	}
	util.Debug("opencode.client", "create session request", map[string]any{"directory": directory})
	u := c.baseURL + "/session"
	if directory != "" {
		u = u + "?directory=" + url.QueryEscape(directory)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session failed: %s %s", resp.Status, string(b))
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

func (c *Client) DeleteSession(ctx context.Context, sessionID, directory string) error {
	if !c.Enabled() || sessionID == "" {
		return nil
	}
	util.Trace("opencode.client", "delete session request", map[string]any{"session_id": sessionID})
	u := fmt.Sprintf("%s/session/%s", c.baseURL, sessionID)
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete session failed: %s %s", resp.Status, string(b))
	}
	util.Trace("opencode.client", "delete session response", map[string]any{"session_id": sessionID, "status": resp.StatusCode})
	return nil
}

func (c *Client) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("opencode disabled")
	}
	util.Debug("opencode.client", "prompt structured request", map[string]any{
		"session_id": sessionID,
		"directory":  directory,
		"prompt_len": len(prompt),
	})
	u := fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID)
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	body := map[string]any{
		"format": map[string]any{
			"type":       "json_schema",
			"schema":     schema,
			"retryCount": 1,
		},
		"parts": []map[string]any{{
			"type": "text",
			"text": prompt,
		}},
	}
	if c.providerID != "" && c.modelID != "" {
		body["model"] = map[string]string{"providerID": c.providerID, "modelID": c.modelID}
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("prompt failed: %s %s", resp.Status, string(b))
	}
	util.Debug("opencode.client", "prompt structured response", map[string]any{"session_id": sessionID, "status": resp.StatusCode})
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	payload, details := parseStructuredResponse(raw)
	if payload != nil {
		util.Trace("opencode.client", "parsed structured payload", map[string]any{"session_id": sessionID})
		return payload, nil
	}
	if details != "" {
		return nil, fmt.Errorf("no structured payload in response (%s)", details)
	}
	return nil, fmt.Errorf("no structured payload in response")
}

func parseStructuredResponse(raw []byte) (map[string]any, string) {
	var out struct {
		Info struct {
			Structured any `json:"structured"`
			Error      struct {
				Name    string         `json:"name"`
				Message string         `json:"message"`
				Data    map[string]any `json:"data"`
			} `json:"error"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, ""
	}
	if out.Info.Structured != nil {
		if m, ok := toMap(out.Info.Structured); ok {
			return m, ""
		}
	}
	for _, p := range out.Parts {
		if p.Type != "text" || strings.TrimSpace(p.Text) == "" {
			continue
		}
		if m, ok := parseAnyJSONMap(p.Text); ok {
			return m, ""
		}
	}
	if strings.TrimSpace(out.Info.Error.Name) != "" || strings.TrimSpace(out.Info.Error.Message) != "" || len(out.Info.Error.Data) > 0 {
		detail := strings.TrimSpace(out.Info.Error.Name + ": " + out.Info.Error.Message)
		if msg, ok := out.Info.Error.Data["message"].(string); ok && strings.TrimSpace(msg) != "" {
			if detail == "" {
				detail = strings.TrimSpace(msg)
			} else {
				detail = detail + " (" + strings.TrimSpace(msg) + ")"
			}
		}
		return nil, detail
	}
	return nil, ""
}

func toMap(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	return m, true
}

func parseAnyJSONMap(s string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(s)
	if m, ok := tryJSONMap(trimmed); ok {
		return m, true
	}
	for _, block := range extractCodeFenceBlocks(trimmed) {
		if m, ok := tryJSONMap(block); ok {
			return m, true
		}
	}
	if candidate, ok := extractFirstJSONObject(trimmed); ok {
		if m, ok := tryJSONMap(candidate); ok {
			return m, true
		}
	}
	return nil, false
}

func tryJSONMap(s string) (map[string]any, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		return m, true
	}
	return nil, false
}

var fencedJSONRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

func extractCodeFenceBlocks(s string) []string {
	matches := fencedJSONRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) >= 2 {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}

func extractFirstJSONObject(s string) (string, bool) {
	start := strings.Index(s, "{")
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '{' {
			depth++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
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
