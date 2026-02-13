package analyzers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type llmClient interface {
	CompleteJSON(ctx context.Context, model string, system string, user string) (string, error)
}

type openAIClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

func newOpenAIClientFromEnv() (*openAIClient, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required when --llm-augment is enabled")
	}
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com"
	}
	return &openAIClient{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		apiKey:     key,
		baseURL:    base,
	}, nil
}

func (c *openAIClient) CompleteJSON(ctx context.Context, model string, system string, user string) (string, error) {
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"response_format": map[string]any{"type": "json_object"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal openai payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create openai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read openai response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("openai status %d: %s", resp.StatusCode, string(respBody))
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return "", fmt.Errorf("decode openai envelope: %w", err)
	}
	if len(envelope.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}
	content := envelope.Choices[0].Message.Content
	if content == "" {
		return "", fmt.Errorf("openai returned empty content")
	}
	return content, nil
}
