package openai

import (
	"regexp"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (d *detector) Name() string { return "openai" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa == nil || fa.Language != "go" || !importsOpenAI(fa) {
			continue
		}
		for _, call := range fa.Calls {
			if b := openAIChatCompletionBinding(call); b != nil {
				key := b.File + "|" + b.Symbol + "|" + b.Trigger
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, *b)
			}
		}
	}
	return out
}

func importsOpenAI(fa *ast.FileAST) bool {
	for _, imp := range fa.Imports {
		if strings.Trim(imp.Path, `"`) == "github.com/sashabaranov/go-openai" {
			return true
		}
	}
	return false
}

func openAIChatCompletionBinding(call ast.CallSite) *ast.FrameworkBinding {
	callee := strings.ToLower(strings.TrimSpace(call.CalleeRaw))
	switch {
	case strings.Contains(callee, "createchatcompletionstream"):
	case strings.Contains(callee, "createchatcompletion"):
	default:
		return nil
	}
	reason := "go_openai_chat_completion_call"
	if model := openAIModelFromArgs(call.Arguments); model != "" {
		reason += " model=" + model
	}
	return &ast.FrameworkBinding{
		Framework:        "openai",
		Kind:             "http_client",
		Direction:        "outbound",
		Symbol:           call.Caller,
		Trigger:          "POST /v1/chat/completions",
		TriggerSource:    callSource(call),
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: reason,
	}
}

func openAIModelFromArgs(args []ast.ArgumentExpr) string {
	for _, arg := range args {
		src := arg.Source
		if m := regexp.MustCompile(`openai\.([A-Za-z0-9_]*GPT[A-Za-z0-9_]*)`).FindStringSubmatch(src); len(m) == 2 {
			return m[1]
		}
		if m := regexp.MustCompile(`Model\s*:\s*"([^"]+)"`).FindStringSubmatch(src); len(m) == 2 {
			return m[1]
		}
	}
	return ""
}

func callSource(call ast.CallSite) string {
	parts := make([]string, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		parts = append(parts, strings.TrimSpace(arg.Source))
	}
	if len(parts) == 0 {
		return strings.TrimSpace(call.CalleeRaw)
	}
	return strings.TrimSpace(call.CalleeRaw) + "(" + strings.Join(parts, ", ") + ")"
}
