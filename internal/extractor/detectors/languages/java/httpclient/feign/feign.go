package feign

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/internal/frameworkutil"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (d *detector) Name() string { return "feign" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "java" && fa.Language != "kotlin" {
			continue
		}
		classes := frameworkutil.ClassesByName(fa)
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			cls := frameworkutil.EnclosingClassForSymbol(fa, sym, classes)
			if cls == nil || !frameworkutil.HasAnyAnnotation(*cls, "FeignClient") {
				continue
			}
			for _, ann := range sym.Annotations {
				if method, ok := springHTTPMethodForAnnotation(ann); ok {
					out = append(out, feignHTTPBindings(sym, *cls, ann, method)...)
				}
			}
		}
	}
	return out
}

func springHTTPMethodForAnnotation(ann ast.Annotation) (string, bool) {
	switch ann.Name {
	case "GetMapping":
		return "GET", true
	case "PostMapping":
		return "POST", true
	case "PutMapping":
		return "PUT", true
	case "PatchMapping":
		return "PATCH", true
	case "DeleteMapping":
		return "DELETE", true
	case "RequestMapping":
		return springRequestMethod(ann.Arguments), true
	default:
		return "", false
	}
}

func feignHTTPBindings(sym ast.SymbolDef, cls ast.SymbolDef, ann ast.Annotation, method string) []ast.FrameworkBinding {
	paths := extractRoutePaths(ann.Arguments)
	if len(paths) == 0 {
		paths = []string{""}
	}
	classPrefixes := []string{""}
	if p := classRequestMappingPrefixes(cls); len(p) > 0 {
		classPrefixes = p
	}
	out := make([]ast.FrameworkBinding, 0, len(classPrefixes)*len(paths))
	for _, prefix := range classPrefixes {
		for _, path := range paths {
			routePath := frameworkutil.JoinPath(prefix, path)
			out = append(out, ast.FrameworkBinding{
				Framework:        "spring",
				Kind:             "http_client",
				Direction:        "outbound",
				Symbol:           sym.Qualified,
				Trigger:          method + " " + routePath,
				TriggerSource:    "@" + ann.Name + "(" + ann.Arguments + ")",
				File:             sym.File,
				Range:            sym.Range,
				ConfidenceReason: "feign_client_mapping_literal",
			})
		}
	}
	return out
}

func extractRoutePaths(args string) []string {
	named, positional, hasPositional := frameworkutil.ParseAnnotationArgs(args)
	if v, ok := named["value"]; ok {
		return frameworkutil.ExtractStringArgs(v)
	}
	if v, ok := named["path"]; ok {
		return frameworkutil.ExtractStringArgs(v)
	}
	if hasPositional {
		return frameworkutil.ExtractStringArgs(positional)
	}
	return nil
}

func springRequestMethod(args string) string {
	if strings.Contains(args, "RequestMethod.GET") {
		return "GET"
	}
	if strings.Contains(args, "RequestMethod.POST") {
		return "POST"
	}
	if strings.Contains(args, "RequestMethod.PUT") {
		return "PUT"
	}
	if strings.Contains(args, "RequestMethod.PATCH") {
		return "PATCH"
	}
	if strings.Contains(args, "RequestMethod.DELETE") {
		return "DELETE"
	}
	return "ANY"
}

func classRequestMappingPrefixes(cls ast.SymbolDef) []string {
	for _, ann := range cls.Annotations {
		if ann.Name == "RequestMapping" {
			return extractRoutePaths(ann.Arguments)
		}
	}
	return nil
}
