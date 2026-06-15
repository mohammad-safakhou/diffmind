package pipeline

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// extractHTTPStatus and shouldReportHTTPStatus must combine such that
// a token-count number embedded in a schema-class error never leaks
// into the report as a fake HTTP status.
func TestHTTPStatusNotReportedForSchemaErrors(t *testing.T) {
	// This is the EXACT error message shape from the real failed run.
	// It contains "output":419 inside a token-count object and ends
	// with the well-known "no structured payload" sentinel.
	msg := `detail.dependency.queue_publish.foo prompt: no structured payload in response; raw={"info":{"tokens":{"total":12485,"input":2345,"output":419,"reasoning":121}},"parts":[]}`
	err := errors.New(msg)
	class := ClassifyError(err)
	if class != "schema" {
		t.Fatalf("classifyError = %q, want schema", class)
	}
	if ShouldReportHTTPStatus(class) {
		t.Fatalf("shouldReportHTTPStatus(%q) must be false", class)
	}
	// extractHTTPStatus itself should also no longer match arbitrary
	// 3-digit numbers tucked inside JSON. The previous regex matched
	// "419" via the ":" lookbehind; our tightened pattern requires a
	// status-line context (HTTP/, status code, "got 502", etc.).
	if got := ExtractHTTPStatus(msg); got != 0 {
		t.Errorf("extractHTTPStatus(token-count embedded msg) = %d, want 0 — regex too greedy", got)
	}
}

// Sanity: legitimate HTTP-status messages still match.
func TestHTTPStatusReportedForRealStatusLines(t *testing.T) {
	cases := map[string]int{
		"prompt failed: 502 Bad Gateway":          502,
		"server returned 503 Service Unavailable": 503,
		"got 504 from upstream":                   504,
		"HTTP 429 Too Many Requests":              429,
		"status code 401":                         401,
	}
	for msg, want := range cases {
		if got := ExtractHTTPStatus(msg); got != want {
			t.Errorf("extractHTTPStatus(%q) = %d, want %d", msg, got, want)
		}
	}
}

// renderFailureMarkdown must only list inspection files that exist on
// disk. A non-existent .json response file (the common case for
// schema-class failures) must NOT appear in the output.
func TestMarkdownOnlyListsExistingFiles(t *testing.T) {
	tmp := t.TempDir()
	captureDir := filepath.Join(tmp, "prompts")
	if err := os.MkdirAll(captureDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	// Write only .prompt.txt and .response.raw (no .json, no .text).
	promptPath := filepath.Join(captureDir, "detail.foo.prompt.txt")
	rawPath := filepath.Join(captureDir, "detail.foo.response.raw")
	if err := os.WriteFile(promptPath, []byte("prompt"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(rawPath, []byte("raw"), 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	f := &Failure{
		Stage:        "detail",
		JobID:        "detail.foo",
		Error:        "boom",
		ErrorClass:   "schema",
		PromptPath:   promptPath,
		ResponsePath: filepath.Join(captureDir, "detail.foo.response.json"),
		OccurredAt:   time.Now().UTC(),
	}
	md := renderFailureMarkdown(f, tmp, "")
	// Should contain the prompt and the raw response.
	if !strings.Contains(md, promptPath) {
		t.Errorf("markdown missing prompt path")
	}
	if !strings.Contains(md, rawPath) {
		t.Errorf("markdown missing raw response path")
	}
	// Should NOT mention the non-existent .json or .text paths.
	jsonPath := filepath.Join(captureDir, "detail.foo.response.json")
	textPath := filepath.Join(captureDir, "detail.foo.response.text")
	if strings.Contains(md, jsonPath) {
		t.Errorf("markdown lists non-existent JSON response: %s", md)
	}
	if strings.Contains(md, textPath) {
		t.Errorf("markdown lists non-existent text response: %s", md)
	}
}
