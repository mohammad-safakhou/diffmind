package rails

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// Ruby on Rails

type detector struct{}

func (d *detector) Name() string { return "rails" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	// Rails routes are defined in config/routes.rb. We detect them from
	// call expressions in that file.
	var out []ast.FrameworkBinding
	for path, fa := range idx.Files {
		if fa.Language != "ruby" {
			continue
		}
		if !strings.HasSuffix(path, "routes.rb") {
			continue
		}
		for _, call := range fa.Calls {
			method := strings.ToUpper(call.CalleeRaw)
			httpMethods := map[string]bool{"GET": true, "POST": true, "PUT": true,
				"PATCH": true, "DELETE": true, "RESOURCES": true, "RESOURCE": true}
			if !httpMethods[method] {
				continue
			}
			routePath := ""
			if len(call.Arguments) > 0 {
				routePath = strings.Trim(call.Arguments[0].Source, `"'`)
			}
			out = append(out, ast.FrameworkBinding{
				Framework:     "rails",
				Kind:          "http_handler",
				Symbol:        call.Caller,
				Trigger:       method + " " + routePath,
				TriggerSource: call.CalleeRaw + " " + routePath,
				File:          call.File,
				Range:         call.Range,
			})
		}
	}
	return out
}
