package wire

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

func TestDetectsWireOpenAIProvider(t *testing.T) {
	idx := &ast.ProjectIndex{Files: map[string]*ast.FileAST{
		"cmd/app/wire_set.go": {
			Path:     "cmd/app/wire_set.go",
			Language: "go",
			Imports:  []ast.ImportDecl{{Path: "github.com/google/wire"}},
			Calls: []ast.CallSite{{
				Caller:    "",
				CalleeRaw: "wire.NewSet",
				File:      "cmd/app/wire_set.go",
				Arguments: []ast.ArgumentExpr{
					{Index: 0, Source: "infra.NewChatGPTClient"},
					{Index: 1, Source: "infra.NewRedisClient"},
				},
			}},
		},
	}}

	got := (&detector{}).Detect(idx)
	if len(got) != 1 {
		t.Fatalf("Detect() count = %d, want 1: %#v", len(got), got)
	}
	if got[0].Framework != "go-wire" || got[0].Kind != "http_client" || got[0].Trigger != "POST /v1/chat/completions" {
		t.Fatalf("unexpected binding: %#v", got[0])
	}
	if got[0].Symbol != "infra.NewChatGPTClient" {
		t.Fatalf("unexpected symbol: %q", got[0].Symbol)
	}
}
