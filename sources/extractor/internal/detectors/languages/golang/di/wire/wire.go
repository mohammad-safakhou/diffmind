package wire

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (d *detector) Name() string { return "go-wire" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa == nil || fa.Language != "go" || !importsWire(fa) {
			continue
		}
		for _, call := range fa.Calls {
			if !isWireProviderCall(call) {
				continue
			}
			for _, arg := range call.Arguments {
				provider := strings.TrimSpace(arg.Source)
				if !isOpenAIProvider(provider) {
					continue
				}
				key := call.File + "|" + provider
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, ast.FrameworkBinding{
					Framework:        "go-wire",
					Kind:             "http_client",
					Direction:        "outbound",
					Symbol:           firstNonEmpty(call.Caller, provider),
					Trigger:          "POST /v1/chat/completions",
					TriggerSource:    provider,
					File:             call.File,
					Range:            call.Range,
					ConfidenceReason: "go_wire_provider_openai_client provider=" + provider,
				})
			}
		}
	}
	return out
}

func importsWire(fa *ast.FileAST) bool {
	for _, imp := range fa.Imports {
		if strings.Trim(imp.Path, `"`) == "github.com/google/wire" {
			return true
		}
	}
	return false
}

func isWireProviderCall(call ast.CallSite) bool {
	callee := strings.TrimSpace(call.CalleeRaw)
	return callee == "wire.Build" || callee == "wire.NewSet"
}

func isOpenAIProvider(provider string) bool {
	provider = strings.ToLower(provider)
	return strings.Contains(provider, "newchatgptclient") ||
		strings.Contains(provider, "newopenaiclient") ||
		strings.Contains(provider, "openai.newclient")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
