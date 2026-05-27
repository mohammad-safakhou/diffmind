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
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			for _, ann := range sym.Annotations {
				b := nestAnnotationToBinding(sym, ann)
				if b != nil {
					out = append(out, *b)
				}
			}
		}
	}
	return out
}

func nestAnnotationToBinding(sym ast.SymbolDef, ann ast.Annotation) *ast.FrameworkBinding {
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
		path := extractFirstStringArg(strings.TrimSpace(args))
		return &ast.FrameworkBinding{
			Framework:     "nestjs",
			Kind:          "http_handler",
			Symbol:        sym.Qualified,
			Trigger:       method + " " + path,
			TriggerSource: "@" + name + "(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	if name == "Cron" {
		return &ast.FrameworkBinding{
			Framework:     "nestjs",
			Kind:          "scheduler",
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
			Symbol:        sym.Qualified,
			Trigger:       "message: " + extractFirstStringArg(args),
			TriggerSource: "@" + name + "(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	return nil
}
