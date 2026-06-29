package fastapi

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/internal/frameworkutil"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// FastAPI (Python)

type detector struct{}

func (d *detector) Name() string { return "fastapi" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "python" {
			continue
		}
		for _, sym := range fa.Symbols {
			for _, ann := range sym.Annotations {
				b := fastAPIAnnotationToBinding(sym, ann)
				if b != nil {
					out = append(out, *b)
				}
			}
		}
	}
	return out
}

func fastAPIAnnotationToBinding(sym ast.SymbolDef, ann ast.Annotation) *ast.FrameworkBinding {
	methods := map[string]string{
		"get": "GET", "post": "POST", "put": "PUT",
		"patch": "PATCH", "delete": "DELETE", "api_route": "ANY",
	}
	// Annotations like @app.get("/path") or @router.post("/path")
	name := ann.Name
	// Strip receiver prefix.
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	if method, ok := methods[name]; ok {
		path := frameworkutil.ExtractFirstStringArg(ann.Arguments)
		return &ast.FrameworkBinding{
			Framework:     "fastapi",
			Kind:          "http_handler",
			Symbol:        sym.Qualified,
			Trigger:       method + " " + path,
			TriggerSource: "@" + ann.Name + "(" + ann.Arguments + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}
	return nil
}
