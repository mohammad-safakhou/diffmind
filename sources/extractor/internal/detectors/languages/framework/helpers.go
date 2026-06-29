package framework

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func classesByName(fa *ast.FileAST) map[string]*ast.SymbolDef {
	out := map[string]*ast.SymbolDef{}
	for i := range fa.Symbols {
		sym := &fa.Symbols[i]
		if sym.Kind != ast.SymbolKindClass && sym.Kind != ast.SymbolKindInterface {
			continue
		}
		out[sym.Name] = sym
		out[sym.Qualified] = sym
	}
	return out
}

func enclosingClassForSymbol(fa *ast.FileAST, sym ast.SymbolDef, classes map[string]*ast.SymbolDef) *ast.SymbolDef {
	if sym.Receiver != "" {
		if cls := classes[sym.Receiver]; cls != nil {
			return cls
		}
	}
	var best *ast.SymbolDef
	for i := range fa.Symbols {
		cls := &fa.Symbols[i]
		if cls.Kind != ast.SymbolKindClass && cls.Kind != ast.SymbolKindInterface {
			continue
		}
		if cls.Range.StartLine <= sym.Range.StartLine && cls.Range.EndLine >= sym.Range.EndLine {
			if best == nil || cls.Range.StartLine >= best.Range.StartLine {
				best = cls
			}
		}
	}
	return best
}

func hasAnyAnnotation(sym ast.SymbolDef, names ...string) bool {
	for _, ann := range sym.Annotations {
		for _, name := range names {
			if ann.Name == name {
				return true
			}
		}
	}
	return false
}

func joinPath(paths ...string) string {
	parts := []string{}
	for _, p := range paths {
		p = strings.TrimSpace(strings.Trim(p, `"'`))
		p = strings.Trim(p, "/")
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

func isLiteralPathArg(args []ast.ArgumentExpr, pos int) bool {
	if len(args) <= pos {
		return false
	}
	arg := strings.TrimSpace(args[pos].Source)
	if arg == "" {
		return false
	}
	if !(strings.HasPrefix(arg, `"`) || strings.HasPrefix(arg, "'") || strings.HasPrefix(arg, "`")) {
		return false
	}
	return strings.HasPrefix(strings.Trim(arg, `"'`+"`"), "/")
}

func literalPathArg(args []ast.ArgumentExpr, pos int) string {
	if !isLiteralPathArg(args, pos) {
		return ""
	}
	return strings.Trim(strings.TrimSpace(args[pos].Source), `"'`+"`")
}
