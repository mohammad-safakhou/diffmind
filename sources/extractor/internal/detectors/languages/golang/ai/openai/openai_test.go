package openai

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func TestDetectsOpenAIChatCompletion(t *testing.T) {
	idx := &ast.ProjectIndex{Files: map[string]*ast.FileAST{
		"internal/chatgpt/ask.go": {
			Path:     "internal/chatgpt/ask.go",
			Language: "go",
			Imports:  []ast.ImportDecl{{Path: "github.com/sashabaranov/go-openai"}},
			Calls: []ast.CallSite{{
				Caller:    "chatgpt.Service.Ask",
				CalleeRaw: "s.client.CreateChatCompletion",
				File:      "internal/chatgpt/ask.go",
				Arguments: []ast.ArgumentExpr{
					{Index: 0, Source: "ctx"},
					{Index: 1, Source: "openai.ChatCompletionRequest{Model: openai.GPT4oMini}"},
				},
			}},
		},
	}}

	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("Detect() count = %d, want 1: %#v", len(got), got)
	}
	if got[0].Framework != "openai" || got[0].Kind != "http_client" || got[0].Trigger != "POST /v1/chat/completions" {
		t.Fatalf("unexpected binding: %#v", got[0])
	}
	if got[0].ConfidenceReason != "go_openai_chat_completion_call model=GPT4oMini" {
		t.Fatalf("unexpected reason: %q", got[0].ConfidenceReason)
	}
}
