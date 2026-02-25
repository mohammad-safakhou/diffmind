package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPromptStructured(t *testing.T) {
	c := New("http://opencode.local", "", "", "opencode", "secret", 3*time.Second)
	c.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got == "" {
			t.Fatalf("expected authorization header")
		}
		status := 200
		body := "{}"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			body = `{"ok":true}`
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			body = `{"id":"s1"}`
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/s1/message"):
			resp := map[string]any{
				"info": map[string]any{
					"structured": map[string]any{
						"summary":    "ok",
						"confidence": 0.91,
					},
				},
				"parts": []any{},
			}
			b, _ := json.Marshal(resp)
			body = string(b)
		case r.Method == http.MethodDelete && r.URL.Path == "/session/s1":
			body = `true`
		default:
			status = 404
			body = `{"error":"not found"}`
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}, nil
	})}

	ctx := context.Background()
	if err := c.Health(ctx); err != nil {
		t.Fatalf("health failed: %v", err)
	}
	sid, err := c.CreateSession(ctx, "/tmp/repo")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sid != "s1" {
		t.Fatalf("unexpected session id: %s", sid)
	}
	data, err := c.PromptStructured(ctx, sid, "/tmp/repo", "prompt", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if data["summary"] != "ok" {
		t.Fatalf("unexpected payload: %#v", data)
	}
}

func TestPromptStructuredParsesFencedJSONText(t *testing.T) {
	c := New("http://opencode.local", "", "", "", "", 3*time.Second)
	c.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := 200
		body := "{}"
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			body = `{"id":"s1"}`
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/s1/message"):
			body = "{\"info\":{\"role\":\"assistant\"},\"parts\":[{\"type\":\"text\",\"text\":\"Here is JSON:\\n```json\\n{\\\"summary\\\":\\\"ok\\\",\\\"confidence\\\":0.91}\\n```\"}]}"
		case r.Method == http.MethodDelete && r.URL.Path == "/session/s1":
			body = `true`
		default:
			status = 404
			body = `{"error":"not found"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewBufferString(body)), Header: make(http.Header)}, nil
	})}

	ctx := context.Background()
	sid, err := c.CreateSession(ctx, "/tmp/repo")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	data, err := c.PromptStructured(ctx, sid, "/tmp/repo", "prompt", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if data["summary"] != "ok" {
		t.Fatalf("unexpected payload: %#v", data)
	}
}

func TestPromptStructuredIncludesServerErrorDetails(t *testing.T) {
	c := New("http://opencode.local", "", "", "", "", 3*time.Second)
	c.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := 200
		body := "{}"
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			body = `{"id":"s1"}`
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/s1/message"):
			body = `{"info":{"error":{"name":"APIError","message":"","data":{"message":"Incorrect API key provided"}}},"parts":[]}`
		case r.Method == http.MethodDelete && r.URL.Path == "/session/s1":
			body = `true`
		default:
			status = 404
			body = `{"error":"not found"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewBufferString(body)), Header: make(http.Header)}, nil
	})}

	ctx := context.Background()
	sid, err := c.CreateSession(ctx, "/tmp/repo")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, err = c.PromptStructured(ctx, sid, "/tmp/repo", "prompt", map[string]any{"type": "object"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "APIError") || !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Fatalf("expected detailed structured output error, got: %v", err)
	}
}
