package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

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
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return SessionState{}, err
	}
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
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Message{}, err
	}
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
