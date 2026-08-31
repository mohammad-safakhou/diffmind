package ast

import "strings"

func extractLocalTypes(src []byte, symbols []SymbolDef) map[string]string {
	out := map[string]string{}
	lines := strings.Split(string(src), "\n")
	for _, method := range symbols {
		if method.Kind != SymbolKindMethod && method.Kind != SymbolKindFunction && method.Kind != SymbolKindConstructor {
			continue
		}
		for lineNo := int(method.Range.StartLine); lineNo <= int(method.Range.EndLine) && lineNo < len(lines); lineNo++ {
			line := strings.TrimSpace(lines[lineNo])
			name, typ, ok := declaredVariable(line)
			if !ok {
				continue
			}
			out[method.Qualified+"."+name] = typ
		}
	}
	return out
}

func extractImplements(src []byte, symbols []SymbolDef) map[string][]string {
	out := map[string][]string{}
	lines := strings.Split(string(src), "\n")
	for _, cls := range symbols {
		if cls.Kind != SymbolKindClass {
			continue
		}
		decl := ""
		for lineNo := int(cls.Range.StartLine); lineNo < len(lines) && lineNo <= int(cls.Range.StartLine)+6; lineNo++ {
			decl += " " + strings.TrimSpace(lines[lineNo])
			if strings.Contains(lines[lineNo], "{") {
				break
			}
		}
		idx := strings.Index(decl, " implements ")
		if idx < 0 {
			continue
		}
		tail := decl[idx+len(" implements "):]
		if brace := strings.Index(tail, "{"); brace >= 0 {
			tail = tail[:brace]
		}
		for _, raw := range strings.Split(tail, ",") {
			iface := cleanTypeName(raw)
			if iface == "" {
				continue
			}
			out[iface] = append(out[iface], cls.Qualified)
		}
	}
	return out
}

func extractFieldTypes(src []byte, symbols []SymbolDef) map[string]string {
	out := map[string]string{}
	lines := strings.Split(string(src), "\n")
	for _, cls := range symbols {
		if cls.Kind != SymbolKindClass && cls.Kind != SymbolKindInterface {
			continue
		}
		statement := ""
		for lineNo := int(cls.Range.StartLine); lineNo <= int(cls.Range.EndLine) && lineNo < len(lines); lineNo++ {
			line := strings.TrimSpace(lines[lineNo])
			if line == "" || strings.HasPrefix(line, "@") || strings.HasPrefix(line, "//") {
				continue
			}
			statement = strings.TrimSpace(statement + " " + line)
			if !strings.Contains(line, ";") {
				continue
			}
			candidate := statement
			statement = ""
			name, typ, ok := declaredVariable(candidate)
			if !ok {
				continue
			}
			out[cls.Qualified+"."+name] = typ
		}
	}
	return out
}

func extractGoFieldTypes(src []byte, lang, relPath string) map[string]string {
	out := map[string]string{}
	if lang != "go" {
		return out
	}
	scope := goPackageScope(relPath)
	if scope == "" {
		return out
	}
	lines := strings.Split(string(src), "\n")
	for lineNo := 0; lineNo < len(lines); lineNo++ {
		line := strings.TrimSpace(lines[lineNo])
		if !strings.HasPrefix(line, "type ") || !strings.Contains(line, " struct") || !strings.Contains(line, "{") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		typeName := sanitizeSymbolSegment(parts[1])
		if typeName == "" {
			continue
		}
		for lineNo++; lineNo < len(lines); lineNo++ {
			fieldLine := strings.TrimSpace(lines[lineNo])
			if strings.HasPrefix(fieldLine, "}") {
				break
			}
			if fieldLine == "" || strings.HasPrefix(fieldLine, "//") {
				continue
			}
			if tag := strings.Index(fieldLine, "`"); tag >= 0 {
				fieldLine = strings.TrimSpace(fieldLine[:tag])
			}
			name, typ, ok := declaredVariable(fieldLine)
			if !ok {
				continue
			}
			out[scope+"."+typeName+"."+name] = typ
		}
	}
	return out
}

func extractGoImplements(src []byte, lang, relPath string) map[string][]string {
	out := map[string][]string{}
	if lang != "go" {
		return out
	}
	scope := goPackageScope(relPath)
	if scope == "" {
		return out
	}
	for _, rawLine := range strings.Split(string(src), "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "var _ ") || !strings.Contains(line, "= (*") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		iface := strings.TrimSpace(strings.TrimPrefix(line[:eq], "var _ "))
		rest := line[eq+1:]
		start := strings.Index(rest, "(*")
		if start < 0 {
			continue
		}
		rest = rest[start+2:]
		end := strings.Index(rest, ")")
		if end < 0 {
			continue
		}
		impl := sanitizeSymbolSegment(strings.TrimSpace(rest[:end]))
		if iface == "" || impl == "" {
			continue
		}
		out[iface] = append(out[iface], scope+"."+impl)
	}
	return out
}

func extractGoWireBinds(src []byte, lang string, imports []ImportDecl) map[string][]string {
	out := map[string][]string{}
	if lang != "go" || !strings.Contains(string(src), "wire.Bind") {
		return out
	}
	importScopes := map[string]string{}
	for _, imp := range imports {
		alias := imp.Alias
		if alias == "" {
			alias = importPackageName(imp.Path)
		}
		if alias == "" {
			continue
		}
		if scope := goImportPathScope(imp.Path); scope != "" {
			importScopes[alias] = scope
		}
	}
	for _, rawLine := range strings.Split(string(src), "\n") {
		line := strings.TrimSpace(rawLine)
		for strings.Contains(line, "wire.Bind(") {
			start := strings.Index(line, "wire.Bind(")
			tail := line[start+len("wire.Bind("):]
			end := strings.Index(tail, "))")
			if end < 0 {
				break
			}
			args := tail[:end+2]
			iface, impl, ok := parseWireBindArgs(args, importScopes)
			if ok {
				out[iface] = append(out[iface], impl)
			}
			line = tail[end+2:]
		}
	}
	return out
}

func parseWireBindArgs(args string, importScopes map[string]string) (iface, impl string, ok bool) {
	parts := strings.Split(args, "),")
	if len(parts) < 2 {
		return "", "", false
	}
	iface = parseWireNewType(parts[0], importScopes)
	impl = parseWireNewType(parts[1], importScopes)
	if iface == "" || impl == "" {
		return "", "", false
	}
	return iface, impl, true
}

func parseWireNewType(raw string, importScopes map[string]string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "new(")
	raw = strings.TrimPrefix(raw, "*")
	raw = strings.TrimSuffix(raw, ")")
	raw = strings.TrimSpace(raw)
	alias, name, ok := strings.Cut(raw, ".")
	if !ok || name == "" {
		return sanitizeSymbolSegment(raw)
	}
	scope := importScopes[alias]
	if scope == "" {
		return cleanTypeName(raw)
	}
	return scope + "." + sanitizeSymbolSegment(name)
}

func importPackageName(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		return path[slash+1:]
	}
	return path
}

func goImportPathScope(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	for _, marker := range []string{"/internal/", "/cmd/", "/pkg/"} {
		if idx := strings.Index(path, marker); idx >= 0 {
			trimmed := strings.TrimPrefix(path[idx+1:], "/")
			parts := strings.Split(trimmed, "/")
			out := make([]string, 0, len(parts))
			for _, part := range parts {
				if seg := sanitizeSymbolSegment(part); seg != "" {
					out = append(out, seg)
				}
			}
			return strings.Join(out, ".")
		}
	}
	return ""
}

func declaredVariable(line string) (name, typ string, ok bool) {
	line = strings.TrimSpace(line)
	// Go declarations (no semicolon, name-first): "var order Order" and the
	// short composite-literal form "order := Order{...}" / "order := &Order{...}".
	if rest, found := strings.CutPrefix(line, "var "); found && !strings.Contains(rest, "=") {
		parts := strings.Fields(rest)
		if len(parts) == 2 && !strings.ContainsAny(parts[0], "<>[]()=,") {
			if typ = cleanTypeName(strings.TrimPrefix(parts[1], "*")); typ != "" {
				return parts[0], typ, true
			}
		}
		return "", "", false
	}
	if i := strings.Index(line, ":="); i > 0 {
		name = strings.TrimSpace(line[:i])
		rhs := strings.TrimPrefix(strings.TrimSpace(line[i+2:]), "&")
		if brace := strings.IndexByte(rhs, '{'); brace > 0 && !strings.ContainsAny(name, " ,()") {
			if typ = cleanTypeName(rhs[:brace]); typ != "" && !strings.ContainsAny(typ, "<>()") {
				return name, typ, true
			}
		}
		return "", "", false
	}
	if line == "" {
		return "", "", false
	}
	if !strings.Contains(line, ";") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && !strings.ContainsAny(parts[0], "<>[]()=,{}:") && !isModifierLike(parts[0]) {
			if typ = cleanTypeName(strings.TrimPrefix(parts[1], "*")); typ != "" && !isModifierLike(typ) && !strings.ContainsAny(typ, "<>(){},") {
				return parts[0], typ, true
			}
		}
		return "", "", false
	}
	if eq := strings.Index(line, "="); eq >= 0 {
		line = strings.TrimSpace(line[:eq])
	} else {
		line = strings.TrimSuffix(line, ";")
	}
	if strings.Contains(line, "(") || strings.Contains(line, ")") {
		return "", "", false
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", "", false
	}
	name = strings.Trim(parts[len(parts)-1], " ;")
	typ = cleanTypeName(parts[len(parts)-2])
	if name == "" || typ == "" || strings.ContainsAny(name, "<>[]()") || isModifierLike(name) || isModifierLike(typ) {
		return "", "", false
	}
	return name, typ, true
}

func cleanTypeName(raw string) string {
	raw = strings.TrimSpace(strings.Trim(raw, " ;{}"))
	if idx := strings.Index(raw, "<"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimPrefix(raw, "?")
	raw = strings.TrimSuffix(raw, "[]")
	if dot := strings.LastIndex(raw, "."); dot >= 0 {
		raw = raw[dot+1:]
	}
	return strings.TrimSpace(raw)
}

func isModifierLike(s string) bool {
	switch s {
	case "private", "protected", "public", "final", "static", "volatile", "transient", "var", "val", "let", "const":
		return true
	}
	return false
}
