package laravel

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// Laravel (PHP)

type detector struct{}

func (d *detector) Name() string { return "laravel" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for path, fa := range idx.Files {
		if fa.Language != "php" {
			continue
		}
		if !strings.Contains(path, "routes/") {
			continue
		}
		for _, call := range fa.Calls {
			raw := call.CalleeRaw
			methods := map[string]string{
				"Route.get": "GET", "Route.post": "POST", "Route.put": "PUT",
				"Route.patch": "PATCH", "Route.delete": "DELETE",
				"get": "GET", "post": "POST",
			}
			method, ok := methods[raw]
			if !ok {
				continue
			}
			routePath := ""
			if len(call.Arguments) > 0 {
				routePath = strings.Trim(call.Arguments[0].Source, `"'`)
			}
			out = append(out, ast.FrameworkBinding{
				Framework:     "laravel",
				Kind:          "http_handler",
				Symbol:        call.Caller,
				Trigger:       method + " " + routePath,
				TriggerSource: raw + "(" + routePath + ", ...)",
				File:          call.File,
				Range:         call.Range,
			})
		}
	}
	return out
}
