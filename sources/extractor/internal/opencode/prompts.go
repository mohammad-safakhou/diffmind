package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

var diffmindPromptTools = map[string]bool{
	"task": false,
}

func (c *Client) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("opencode disabled")
	}
	util.Debug("opencode.client", "prompt text request", map[string]any{
		"session_id": sessionID, "directory": directory, "prompt_len": len(prompt),
	})
	endpoint := fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID)
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	body := c.promptBody(prompt)
	buffer, _ := json.Marshal(body)
	req, err := c.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(buffer))
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
		responseBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("prompt failed: %s %s", resp.Status, string(responseBody))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var response struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return string(raw), nil
	}
	var texts []string
	for _, part := range response.Parts {
		if part.Type == "text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func (c *Client) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	result, err := c.PromptStructuredVerbose(ctx, sessionID, directory, prompt, schema)
	if err != nil {
		return nil, err
	}
	return result.Payload, nil
}

type StructuredResult struct {
	Payload  map[string]any
	RawBody  []byte
	TextBody string
	Status   int
}

func (c *Client) PromptStructuredVerbose(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (StructuredResult, error) {
	if !c.Enabled() {
		return StructuredResult{}, fmt.Errorf("opencode disabled")
	}
	util.Debug("opencode.client", "prompt structured request", map[string]any{
		"session_id": sessionID, "directory": directory, "prompt_len": len(prompt),
	})
	endpoint := fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID)
	if directory != "" {
		endpoint += "?directory=" + url.QueryEscape(directory)
	}
	body := c.promptBody(prompt)
	body["format"] = map[string]any{
		"type": "json_schema", "schema": schema, "retryCount": 1,
	}
	buffer, _ := json.Marshal(body)
	req, err := c.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(buffer))
	if err != nil {
		return StructuredResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return StructuredResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	result := StructuredResult{RawBody: raw, Status: resp.StatusCode}
	if resp.StatusCode >= 300 {
		return result, fmt.Errorf("prompt failed: %s %s", resp.Status, string(raw))
	}
	util.Debug("opencode.client", "prompt structured response", map[string]any{
		"session_id": sessionID, "status": resp.StatusCode,
	})
	payload, details, text := parseStructuredResponseVerbose(raw)
	result.Payload = payload
	result.TextBody = text
	if payload != nil {
		util.Trace("opencode.client", "parsed structured payload", map[string]any{"session_id": sessionID})
		return result, nil
	}
	preview := previewBody(raw, 320)
	if details != "" {
		return result, fmt.Errorf("no structured payload in response (%s) raw=%s", details, preview)
	}
	return result, fmt.Errorf("no structured payload in response; raw=%s", preview)
}

func (c *Client) promptBody(prompt string) map[string]any {
	body := map[string]any{
		"tools": diffmindPromptTools,
		"parts": []map[string]any{{"type": "text", "text": prompt}},
	}
	if c.providerID != "" && c.modelID != "" {
		body["model"] = map[string]string{"providerID": c.providerID, "modelID": c.modelID}
	}
	if variant := strings.TrimSpace(c.variant); variant != "" {
		body["variant"] = variant
	}
	return body
}
