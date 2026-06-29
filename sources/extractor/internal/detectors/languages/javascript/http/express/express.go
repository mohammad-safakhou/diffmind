package express

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/internal/frameworkutil"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// Express / Node.js

type detector struct{}

func (d *detector) Name() string { return "express" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "javascript" && fa.Language != "typescript" && fa.Language != "tsx" && fa.Language != "jsx" {
			continue
		}
		for _, call := range fa.Calls {
			b := expressCallToBinding(call)
			if b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func expressCallToBinding(call ast.CallSite) *ast.FrameworkBinding {
	raw := call.CalleeRaw
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return nil
	}
	receiver, verb := parts[0], parts[1]
	if receiver != "app" && receiver != "router" {
		return nil
	}
	methods := map[string]string{
		"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH", "delete": "DELETE",
	}
	method, ok := methods[verb]
	if !ok || len(call.Arguments) < 2 || !frameworkutil.IsLiteralPathArg(call.Arguments, 0) {
		return nil
	}
	path := frameworkutil.LiteralPathArg(call.Arguments, 0)
	return &ast.FrameworkBinding{
		Framework:        "express",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           call.Caller,
		Trigger:          method + " " + path,
		TriggerSource:    raw + "(" + path + ", ...)",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "express_receiver_literal_path_handler",
	}
}
