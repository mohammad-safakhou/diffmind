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

// AbortSession asks the server to stop any in-flight prompt on the given
// session. We use this on context cancellation/timeout to keep the server
// from holding a paused agent (typically waiting on a permission prompt
// that no human will ever answer in our headless setup).
func (c *Client) AbortSession(ctx context.Context, sessionID, directory string) error {
	if !c.Enabled() || sessionID == "" {
		return nil
	}
	u := fmt.Sprintf("%s/session/%s/abort", c.baseURL, sessionID)
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("abort session failed: %s %s", resp.Status, string(b))
	}
	util.Trace("opencode.client", "abort session ok", map[string]any{"session_id": sessionID})
	return nil
}

// PendingPermission describes a permission request the server is waiting on.
// The orchestrator's watchdog uses this to auto-reply so prompts cannot
// deadlock waiting for human input that will never come.
//
// OpenCode's actual record carries a "patterns" array (file globs the agent
// wants access to) for path-scoped permissions like external_directory,
// edit, read; we surface it so the watchdog can decide allow vs deny based
// on what is actually being asked. Title and Type are sometimes empty
// (e.g. external_directory checks emit no title), which is why patterns is
// the more reliable diagnostic.
type PendingPermission struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"sessionID"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Permission string   `json:"permission"`
	Patterns   []string `json:"patterns"`
}

// ListPermissions returns outstanding permission requests across the server.
// DiffMind scopes the result to its own session ids in the orchestrator.
func (c *Client) ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error) {
	if !c.Enabled() {
		return nil, nil
	}
	u := c.baseURL + "/permission"
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list permissions failed: %s %s", resp.Status, string(b))
	}
	// The server returns either an array or an object with an items array
	// depending on version; tolerate both shapes.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var arr []PendingPermission
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var wrap struct {
		Items []PendingPermission `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrap); err == nil {
		return wrap.Items, nil
	}
	return nil, nil
}

// RespondPermission replies to a pending permission. response should be one
// of "allow", "deny" (server-supported additions like "allow_once",
// "allow_always" are accepted as opaque strings).
func (c *Client) RespondPermission(ctx context.Context, sessionID, permissionID, directory, response string) error {
	if !c.Enabled() || sessionID == "" || permissionID == "" {
		return nil
	}
	u := fmt.Sprintf("%s/session/%s/permissions/%s", c.baseURL, sessionID, permissionID)
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	body, _ := json.Marshal(map[string]string{"response": response})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("respond permission failed: %s %s", resp.Status, string(b))
	}
	return nil
}

// PendingQuestion describes a clarification request the agent has emitted.
type PendingQuestion struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Question  string `json:"question"`
}

// ListQuestions returns outstanding clarification questions.
func (c *Client) ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error) {
	if !c.Enabled() {
		return nil, nil
	}
	u := c.baseURL + "/question"
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list questions failed: %s %s", resp.Status, string(b))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var arr []PendingQuestion
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var wrap struct {
		Items []PendingQuestion `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrap); err == nil {
		return wrap.Items, nil
	}
	return nil, nil
}

// RejectQuestion tells the server we are not going to answer the question.
// The agent receives the rejection and continues (typically with reduced
// information).
func (c *Client) RejectQuestion(ctx context.Context, requestID, directory string) error {
	if !c.Enabled() || requestID == "" {
		return nil
	}
	u := fmt.Sprintf("%s/question/%s/reject", c.baseURL, requestID)
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reject question failed: %s %s", resp.Status, string(b))
	}
	return nil
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

func (c *Client) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("opencode disabled")
	}
	util.Debug("opencode.client", "prompt text request", map[string]any{
		"session_id": sessionID,
		"directory":  directory,
		"prompt_len": len(prompt),
	})
	u := fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID)
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	body := map[string]any{
		"parts": []map[string]any{{
			"type": "text",
			"text": prompt,
		}},
	}
	if c.providerID != "" && c.modelID != "" {
		body["model"] = map[string]string{"providerID": c.providerID, "modelID": c.modelID}
	}
	if strings.TrimSpace(c.variant) != "" {
		body["variant"] = strings.TrimSpace(c.variant)
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(buf))
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
		return "", fmt.Errorf("prompt failed: %s %s", resp.Status, string(b))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// Extract text from response parts
	var out struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw), nil
	}
	var texts []string
	for _, p := range out.Parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func (c *Client) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	res, err := c.PromptStructuredVerbose(ctx, sessionID, directory, prompt, schema)
	if err != nil {
		return nil, err
	}
	return res.Payload, nil
}

// StructuredResult carries everything PromptStructuredVerbose observed,
// whether or not the structured payload could be parsed. The orchestrator
// uses this to (a) persist the raw body even on parse failure and (b)
// decide whether a free-text fallback is worth attempting.
type StructuredResult struct {
	Payload  map[string]any // nil when parsing failed
	RawBody  []byte         // full HTTP body, or empty on transport error
	TextBody string         // concatenation of any parts[].text fields
	Status   int
}

// PromptStructuredVerbose is like PromptStructured but exposes the raw
// response body and any free-text part it observed. Callers needing the
// transparent diagnostic + fallback path should prefer this.
func (c *Client) PromptStructuredVerbose(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (StructuredResult, error) {
	if !c.Enabled() {
		return StructuredResult{}, fmt.Errorf("opencode disabled")
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
	if strings.TrimSpace(c.variant) != "" {
		body["variant"] = strings.TrimSpace(c.variant)
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(buf))
	if err != nil {
		return StructuredResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return StructuredResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	res := StructuredResult{RawBody: raw, Status: resp.StatusCode}
	if resp.StatusCode >= 300 {
		return res, fmt.Errorf("prompt failed: %s %s", resp.Status, string(raw))
	}
	util.Debug("opencode.client", "prompt structured response", map[string]any{"session_id": sessionID, "status": resp.StatusCode})
	payload, details, text := parseStructuredResponseVerbose(raw)
	res.Payload = payload
	res.TextBody = text
	if payload != nil {
		util.Trace("opencode.client", "parsed structured payload", map[string]any{"session_id": sessionID})
		return res, nil
	}
	preview := previewBody(raw, 320)
	if details != "" {
		return res, fmt.Errorf("no structured payload in response (%s) raw=%s", details, preview)
	}
	return res, fmt.Errorf("no structured payload in response; raw=%s", preview)
}

// previewBody returns a quoted, length-capped string representation of raw
// suitable for embedding in error messages. Newlines are escaped so the
// preview stays on a single line.
func previewBody(raw []byte, maxLen int) string {
	if len(raw) == 0 {
		return "<empty>"
	}
	s := string(raw)
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	if len(s) > maxLen {
		s = s[:maxLen] + "\u2026"
	}
	return s
}

func parseStructuredResponse(raw []byte) (map[string]any, string) {
	m, detail, _ := parseStructuredResponseVerbose(raw)
	return m, detail
}

// parseStructuredResponseVerbose extends parseStructuredResponse with the
// concatenated text parts the response contained. Callers use that text
// for free-text JSON-extraction fallback when the structured slot is
// empty.
func parseStructuredResponseVerbose(raw []byte) (map[string]any, string, string) {
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
		return nil, "", ""
	}
	var texts []string
	for _, p := range out.Parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, p.Text)
		}
	}
	textBody := strings.Join(texts, "\n")
	if out.Info.Structured != nil {
		if m, ok := toMap(out.Info.Structured); ok {
			return m, "", textBody
		}
	}
	for _, p := range out.Parts {
		if p.Type != "text" || strings.TrimSpace(p.Text) == "" {
			continue
		}
		if m, ok := parseAnyJSONMap(p.Text); ok {
			return m, "", textBody
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
		return nil, detail, textBody
	}
	return nil, "", textBody
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
