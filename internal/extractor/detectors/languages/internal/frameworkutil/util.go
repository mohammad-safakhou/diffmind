// Package frameworkutil contains small AST helper functions shared by concrete
// detector packages. It deliberately avoids framework-specific policy.
package frameworkutil

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

func ClassesByName(fa *ast.FileAST) map[string]*ast.SymbolDef {
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

func EnclosingClassForSymbol(fa *ast.FileAST, sym ast.SymbolDef, classes map[string]*ast.SymbolDef) *ast.SymbolDef {
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

func HasAnyAnnotation(sym ast.SymbolDef, names ...string) bool {
	for _, ann := range sym.Annotations {
		for _, name := range names {
			if ann.Name == name {
				return true
			}
		}
	}
	return false
}

func JoinPath(paths ...string) string {
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

func IsLiteralPathArg(args []ast.ArgumentExpr, pos int) bool {
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

func LiteralPathArg(args []ast.ArgumentExpr, pos int) string {
	if !IsLiteralPathArg(args, pos) {
		return ""
	}
	return strings.Trim(strings.TrimSpace(args[pos].Source), `"'`+"`")
}

func HandlerIdentifierArg(args []ast.ArgumentExpr, pos int) string {
	if len(args) <= pos {
		return ""
	}
	src := strings.TrimSpace(args[pos].Source)
	if src == "" || strings.ContainsAny(src, "({\" \t") {
		return ""
	}
	if dot := strings.LastIndex(src, "."); dot >= 0 {
		src = src[dot+1:]
	}
	for _, r := range src {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return src
}

// ExtractFirstStringArg extracts the first string literal from annotation or
// decorator argument text. If no quoted value exists, it returns the trimmed
// argument body.
func ExtractFirstStringArg(args string) string {
	values := ExtractStringArgs(args)
	if len(values) > 0 {
		return values[0]
	}
	return strings.TrimSpace(strings.Trim(args, "{}"))
}

func NamedOrPositionalValues(args string, keys ...string) []string {
	named, positional, hasPositional := ParseAnnotationArgs(args)
	for _, key := range keys {
		if v, ok := named[strings.ToLower(key)]; ok {
			return ExtractStringArgs(v)
		}
	}
	if hasPositional {
		return ExtractStringArgs(positional)
	}
	return nil
}

func ParseAnnotationArgs(args string) (named map[string]string, positional string, hasPositional bool) {
	named = map[string]string{}
	for _, part := range SplitTopLevelArgs(args) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if k, v, ok := splitNamedArg(part); ok {
			named[strings.ToLower(k)] = v
		} else if !hasPositional {
			positional = strings.TrimSpace(part)
			hasPositional = true
		}
	}
	return named, positional, hasPositional
}

func SplitTopLevelArgs(s string) []string {
	var parts []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func splitNamedArg(part string) (key, value string, named bool) {
	depth := 0
	var quote byte
	for i := 0; i < len(part); i++ {
		c := part[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(part) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 {
				k := strings.TrimSpace(part[:i])
				if isAnnotationIdent(k) {
					return k, strings.TrimSpace(part[i+1:]), true
				}
				return "", strings.TrimSpace(part), false
			}
		}
	}
	return "", strings.TrimSpace(part), false
}

func isAnnotationIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func ExtractStringArgs(args string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] != '"' && args[i] != '\'' {
			continue
		}
		quote := args[i]
		start := i + 1
		i++
		for i < len(args) {
			if args[i] == '\\' && i+1 < len(args) {
				i += 2
				continue
			}
			if args[i] == quote {
				out = append(out, args[start:i])
				break
			}
			i++
		}
	}
	return out
}
