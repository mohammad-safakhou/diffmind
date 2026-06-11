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

func declaredVariable(line string) (name, typ string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.Contains(line, ";") {
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
