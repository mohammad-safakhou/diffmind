package retrofit

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/internal/frameworkutil"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (d *detector) Name() string { return "retrofit" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "java" && fa.Language != "kotlin" {
			continue
		}
		if !fileImportsRetrofitHTTP(fa) {
			continue
		}
		classes := frameworkutil.ClassesByName(fa)
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			cls := frameworkutil.EnclosingClassForSymbol(fa, sym, classes)
			if cls == nil || cls.Kind != ast.SymbolKindInterface {
				continue
			}
			for _, ann := range sym.Annotations {
				if method, ok := retrofitHTTPMethod(ann.Name); ok {
					out = append(out, retrofitHTTPBinding(sym, ann, method))
				}
			}
		}
	}
	return out
}

func retrofitHTTPMethod(name string) (string, bool) {
	switch name {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return name, true
	default:
		return "", false
	}
}

func fileImportsRetrofitHTTP(fa *ast.FileAST) bool {
	if fa == nil {
		return false
	}
	for _, imp := range fa.Imports {
		if strings.HasPrefix(imp.Path, "retrofit2.http") {
			return true
		}
	}
	return false
}

func retrofitHTTPBinding(sym ast.SymbolDef, ann ast.Annotation, method string) ast.FrameworkBinding {
	path := frameworkutil.ExtractFirstStringArg(ann.Arguments)
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return ast.FrameworkBinding{
		Framework:        "retrofit",
		Kind:             "http_client",
		Direction:        "outbound",
		Symbol:           sym.Qualified,
		Trigger:          method + " " + path,
		TriggerSource:    "@" + ann.Name + "(" + ann.Arguments + ")",
		File:             sym.File,
		Range:            sym.Range,
		ConfidenceReason: "retrofit_client_mapping_literal",
	}
}
