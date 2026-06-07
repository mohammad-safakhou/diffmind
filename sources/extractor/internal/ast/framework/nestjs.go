package framework

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func init() { register(&nestjsDetector{}) }

// nestjsDetector detects NestJS (TypeScript/JavaScript) implicit invocations.
type nestjsDetector struct{}

func (d *nestjsDetector) Name() string { return "nestjs" }

func (d *nestjsDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "typescript" && fa.Language != "javascript" && fa.Language != "tsx" {
			continue
		}
		classes := classesByName(fa)
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			cls := enclosingClassForSymbol(fa, sym, classes)
			for _, ann := range sym.Annotations {
				b := nestAnnotationToBinding(sym, cls, ann)
				if b != nil {
					out = append(out, *b)
				}
			}
		}
	}
	return out
}

func nestAnnotationToBinding(sym ast.SymbolDef, cls *ast.SymbolDef, ann ast.Annotation) *ast.FrameworkBinding {
	name := ann.Name
	args := ann.Arguments

	httpMap := map[string]string{
		"Get":    "GET",
		"Post":   "POST",
		"Put":    "PUT",
		"Patch":  "PATCH",
		"Delete": "DELETE",
	}
	if method, ok := httpMap[name]; ok {
		prefix := ""
		controller := false
		if cls != nil {
			controller = hasAnyAnnotation(*cls, "Controller")
			prefix = nestControllerPrefix(*cls)
		}
		path := joinPath(prefix, extractFirstStringArg(strings.TrimSpace(args)))
		rejection := ""
		if !controller {
			rejection = "nestjs_http_decorator_without_controller_context"
		}
		return &ast.FrameworkBinding{
			Framework:        "nestjs",
			Kind:             "http_handler",
			Direction:        "inbound",
			Symbol:           sym.Qualified,
			Trigger:          method + " " + path,
			TriggerSource:    "@" + name + "(" + args + ")",
			File:             sym.File,
			Range:            sym.Range,
			ConfidenceReason: "nestjs_controller_http_decorator",
			RejectionReason:  rejection,
		}
	}

	if name == "Cron" {
		return &ast.FrameworkBinding{
			Framework:     "nestjs",
			Kind:          "scheduler",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "cron: " + extractFirstStringArg(args),
			TriggerSource: "@Cron(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	if name == "MessagePattern" || name == "EventPattern" {
		return &ast.FrameworkBinding{
			Framework:     "nestjs",
			Kind:          "queue_consumer",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "message: " + extractFirstStringArg(args),
			TriggerSource: "@" + name + "(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	return nil
}

func nestControllerPrefix(cls ast.SymbolDef) string {
	for _, ann := range cls.Annotations {
		if ann.Name == "Controller" {
			return extractFirstStringArg(ann.Arguments)
		}
	}
	return ""
}
