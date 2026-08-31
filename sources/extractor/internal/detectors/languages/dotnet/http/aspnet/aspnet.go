package aspnet

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/internal/frameworkutil"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// ASP.NET (C#)

type detector struct{}

func (d *detector) Name() string { return "aspnet" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "csharp" {
			continue
		}
		classes := frameworkutil.ClassesByName(fa)
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			cls := frameworkutil.EnclosingClassForSymbol(fa, sym, classes)
			for _, ann := range sym.Annotations {
				b := aspnetAnnotationToBinding(sym, cls, ann)
				if b != nil {
					out = append(out, *b)
				}
			}
		}
	}
	return out
}

func aspnetAnnotationToBinding(sym ast.SymbolDef, cls *ast.SymbolDef, ann ast.Annotation) *ast.FrameworkBinding {
	methods := map[string]string{
		"HttpGet": "GET", "HttpPost": "POST", "HttpPut": "PUT",
		"HttpPatch": "PATCH", "HttpDelete": "DELETE",
	}
	if method, ok := methods[ann.Name]; ok {
		prefix := ""
		controller := false
		if cls != nil {
			controller = strings.HasSuffix(cls.Name, "Controller") || frameworkutil.HasAnyAnnotation(*cls, "ApiController", "Controller")
			prefix = aspnetClassRoutePrefix(*cls)
		}
		path := frameworkutil.JoinPath(prefix, frameworkutil.ExtractFirstStringArg(ann.Arguments))
		rejection := ""
		if !controller {
			rejection = "aspnet_http_attribute_without_controller_context"
		}
		return &ast.FrameworkBinding{
			Framework:        "aspnet",
			Kind:             "http_handler",
			Direction:        "inbound",
			Symbol:           sym.Qualified,
			Trigger:          method + " " + path,
			TriggerSource:    "[" + ann.Name + "(" + ann.Arguments + ")]",
			File:             sym.File,
			Range:            sym.Range,
			ConfidenceReason: "aspnet_controller_http_attribute",
			RejectionReason:  rejection,
		}
	}
	return nil
}

func aspnetClassRoutePrefix(cls ast.SymbolDef) string {
	for _, ann := range cls.Annotations {
		if ann.Name == "Route" {
			return frameworkutil.ExtractFirstStringArg(ann.Arguments)
		}
	}
	return ""
}
