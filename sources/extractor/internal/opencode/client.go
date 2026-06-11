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

var diffmindPromptTools = map[string]bool{
	"task": false,
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

// RespondPermission replies to a pending permission via OpenCode's new
// `/permission/{requestID}/reply` endpoint. The legacy
// `/session/{id}/permissions/{permID}` endpoint is deprecated upstream and
// in practice silently no-ops `"deny"` responses on recent OpenCode
// builds, leaving the agent stuck and the GET /permission listing full of
// the same record on every poll.
//
// reply must be one of:
//   - "once"   — allow this single request only
//   - "always" — allow forever (use with care)
//   - "reject" — deny this request (the agent receives a denial result)
//
// We accept the legacy "allow" / "deny" strings for backwards compat with
// older callers and silently translate them.
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
	u := fmt.Sprintf("%s/permission/%s/reply", c.baseURL, permissionID)
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	body, _ := json.Marshal(map[string]string{"reply": reply})
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
		// Fall back to the legacy session-scoped endpoint if the new one
		// isn't available (older OpenCode releases). The legacy endpoint
		// accepts the same enum and a different body shape.
		if resp.StatusCode == http.StatusNotFound && sessionID != "" {
			return c.respondPermissionLegacy(ctx, sessionID, permissionID, directory, reply)
		}
		return fmt.Errorf("respond permission failed: %s %s", resp.Status, string(b))
	}
	return nil
}

// respondPermissionLegacy hits the deprecated session-scoped endpoint. We
// only call it from RespondPermission's 404 fallback path.
func (c *Client) respondPermissionLegacy(ctx context.Context, sessionID, permissionID, directory, response string) error {
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
		return fmt.Errorf("respond permission (legacy) failed: %s %s", resp.Status, string(b))
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
		"tools": diffmindPromptTools,
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
		"tools": diffmindPromptTools,
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

// SessionState is the minimal subset of OpenCode's session header
// record that the liveness watchdog and dashboard need to reason about
// progress. Counters here move when the model emits new content or
// runs new tools, so they are a coarse but unambiguous "I made
// progress" signal independent of part-counting on the message side.
type SessionState struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// ParentID is set when the session was spawned as a subagent via
	// the parent's `task` tool. We surface it because the orchestrator's
	// permission watchdog only tracks sessions IT created, but OpenCode
	// can transitively create subagent sessions that emit their own
	// permission requests (e.g. external_directory /tmp/* when the
	// explore subagent tries to write a summary file). Without
	// ParentID we cannot recognise those permissions as ours and they
	// hang forever — see run 20260521T112326Z for the symptom.
	ParentID string `json:"parentID,omitempty"`
	Time     struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
	Cost   float64 `json:"cost"`
	Tokens struct {
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

// Activity returns a single integer that increases monotonically as
// the agent makes progress. Liveness checks can compare two snapshots
// and treat any change as "still alive". We sum every counter that
// the server advances on real work, plus the raw time.updated tick.
func (s SessionState) Activity() int64 {
	if s.ID == "" {
		return 0
	}
	return s.Time.Updated +
		int64(s.Tokens.Input) +
		int64(s.Tokens.Output) +
		int64(s.Tokens.Reasoning) +
		int64(s.Tokens.Cache.Read) +
		int64(s.Tokens.Cache.Write)
}

// MessagePart describes one fragment of an assistant message. The
// type field is the discriminator (step-start, reasoning, text,
// tool, step-finish, ...). For tool parts the State sub-object
// carries the tool name, status (running|completed|error), and the
// timestamps we need to tell "actively working" from "stuck waiting
// for a frozen tool".
type MessagePart struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Tool      string `json:"tool,omitempty"`
	Text      string `json:"text,omitempty"`
	CallID    string `json:"callID,omitempty"`
	MessageID string `json:"messageID,omitempty"`
	Time      *struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	} `json:"time,omitempty"`
	State *struct {
		Status string `json:"status"`
		Time   *struct {
			Start int64 `json:"start"`
			End   int64 `json:"end"`
		} `json:"time,omitempty"`
		Input  map[string]any `json:"input,omitempty"`
		Output string         `json:"output,omitempty"`
		Title  string         `json:"title,omitempty"`
	} `json:"state,omitempty"`
}

// Message is one message in a session — either user or assistant.
// We only ever care about the latest assistant message during a live
// prompt, since that's the one growing parts in real time.
type Message struct {
	Info struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionID"`
		Role      string `json:"role"`
		Time      struct {
			Created int64 `json:"created"`
		} `json:"time"`
		ModelID    string `json:"modelID,omitempty"`
		ProviderID string `json:"providerID,omitempty"`
	} `json:"info"`
	Parts []MessagePart `json:"parts"`
}

// GetSession fetches the cheap (~500B) session header for one session.
// Used by the liveness watchdog as its lowest-cost heartbeat: even if
// the message endpoint is slow to update, any movement in tokens or
// time.updated proves the agent is still working.
//
// Returns a zero value (ID == "") on 404 so callers can treat the
// session as absent without inspecting the error.
func (c *Client) GetSession(ctx context.Context, sessionID, directory string) (SessionState, error) {
	if !c.Enabled() || sessionID == "" {
		return SessionState{}, nil
	}
	u := fmt.Sprintf("%s/session/%s", c.baseURL, sessionID)
	if directory != "" {
		u += "?directory=" + url.QueryEscape(directory)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return SessionState{}, err
	}
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SessionState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return SessionState{}, nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return SessionState{}, fmt.Errorf("get session %s failed: %s %s", sessionID, resp.Status, string(b))
	}
	var s SessionState
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return SessionState{}, fmt.Errorf("decode session: %w", err)
	}
	return s, nil
}

// GetLatestMessage fetches the most recent message in a session,
// using `?limit=1` so the response is bounded regardless of how
// many historical messages exist (we observed ~4KB on a session
// with 1000+ messages, vs. 13MB for the unfiltered list).
//
// Returns a zero-value Message (Info.ID == "") when the session
// exists but has no messages yet, so callers can poll a freshly-
// created session without special-casing 404s.
func (c *Client) GetLatestMessage(ctx context.Context, sessionID, directory string) (Message, error) {
	if !c.Enabled() || sessionID == "" {
		return Message{}, nil
	}
	u := fmt.Sprintf("%s/session/%s/message?limit=1", c.baseURL, sessionID)
	if directory != "" {
		u += "&directory=" + url.QueryEscape(directory)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Message{}, err
	}
	c.addAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Message{}, nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("get latest message %s failed: %s %s", sessionID, resp.Status, string(b))
	}
	// The server returns a one-element array. Decode tolerantly:
	// some upstream versions are documented to switch to a single
	// object when ?limit=1 is set; honour both shapes.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, err
	}
	var arr []Message
	if err := json.Unmarshal(raw, &arr); err == nil {
		if len(arr) == 0 {
			return Message{}, nil
		}
		return arr[0], nil
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err == nil {
		return m, nil
	}
	return Message{}, fmt.Errorf("decode latest message: unexpected payload (%d bytes)", len(raw))
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
