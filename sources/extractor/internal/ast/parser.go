package ast

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// ParseFile parses one source file and returns its FileAST.
// The language is determined from the file extension.
// Returns nil, nil when the file extension is not a recognised source language.
func ParseFile(ctx context.Context, repoRoot, relPath string) (*FileAST, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	lang := LanguageForExtension(ext)
	if lang == "" {
		return nil, nil
	}
	sitterLang := GetLanguage(lang)
	if sitterLang == nil {
		return nil, nil
	}
	abs := filepath.Join(repoRoot, relPath)
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relPath, err)
	}
	return parseSource(ctx, src, lang, sitterLang, relPath)
}

// ParseConfigFile parses a configuration file and returns a ConfigFile.
// Returns nil when the file extension is not a recognised config format.
func ParseConfigFile(repoRoot, relPath string) (*ConfigFile, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	format := ConfigFormatForExtension(ext)
	if format == "" {
		return nil, nil
	}
	abs := filepath.Join(repoRoot, relPath)
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", relPath, err)
	}
	entries := extractConfigEntries(src, format)
	return &ConfigFile{
		Path:    relPath,
		Format:  format,
		Entries: entries,
	}, nil
}

// parseSource runs tree-sitter on src and extracts the FileAST.
func parseSource(ctx context.Context, src []byte, lang string, sitterLang *sitter.Language, relPath string) (*FileAST, error) {
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(sitterLang)

	tree, err := p.ParseCtx(ctx, nil, src)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relPath, err)
	}
	defer tree.Close()

	root := tree.RootNode()
	fa := &FileAST{
		Path:     relPath,
		Language: lang,
	}

	// Load queries for this language.
	qs := queriesForLanguage(lang)
	if qs == nil {
		// No queries registered yet: return a skeleton FileAST without
		// symbols or calls. The walker will still handle it correctly
		// (an empty node has no outgoing edges).
		return fa, nil
	}

	// Extract imports.
	fa.Imports = extractImports(root, src, lang, sitterLang)

	// Extract symbol definitions.
	fa.Symbols = extractSymbols(root, src, lang, sitterLang, relPath)

	// Extract call sites.
	fa.Calls = extractCalls(root, src, lang, sitterLang, relPath, fa.Symbols)

	// Extract lightweight field declarations for receiver/type resolution.
	fa.FieldTypes = extractFieldTypes(src, fa.Symbols)
	fa.LocalTypes = extractLocalTypes(src, fa.Symbols)
	fa.Implements = extractImplements(src, fa.Symbols)

	return fa, nil
}

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

// extractImports runs the imports query and returns the resolved import list.
func extractImports(root *sitter.Node, src []byte, lang string, sitterLang *sitter.Language) []ImportDecl {
	q := queriesForLanguage(lang)
	if q == nil || q.imports == nil {
		return nil
	}
	query, err := sitter.NewQuery(q.imports, sitterLang)
	if err != nil {
		return nil
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	var out []ImportDecl
	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var path, alias string
		for _, c := range m.Captures {
			name := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch name {
			case "path":
				path = trimQuotes(text)
			case "alias":
				alias = text
			}
		}
		if path == "" {
			continue
		}
		if alias == "" {
			// Derive alias from the last path segment.
			parts := strings.Split(path, "/")
			alias = parts[len(parts)-1]
			// Handle dots: "com.example.Foo" → "Foo"
			if dotParts := strings.Split(alias, "."); len(dotParts) > 0 {
				alias = dotParts[len(dotParts)-1]
			}
		}
		out = append(out, ImportDecl{Alias: alias, Path: path})
	}
	return out
}

// extractSymbols runs the functions/methods query and builds SymbolDef list.
func extractSymbols(root *sitter.Node, src []byte, lang string, sitterLang *sitter.Language, relPath string) []SymbolDef {
	q := queriesForLanguage(lang)
	if q == nil || q.functions == nil {
		return nil
	}
	query, err := sitter.NewQuery(q.functions, sitterLang)
	if err != nil {
		return nil
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	// Also run the annotations query to attach annotations.
	annotsByLine := extractAnnotations(root, src, lang, sitterLang)

	// We do a two-pass approach:
	//   Pass 1: collect all symbols as-is.
	//   Pass 2: for languages that don't have explicit receiver syntax
	//           (Java, Kotlin, C#, Python classes, etc.), infer the class
	//           name from the enclosing class symbol and prepend it to
	//           every method's Qualified name.
	// This turns ambiguous names like "run" → "CIPlannedTargetDailyJob.run",
	// which prevents the walker from conflating methods across classes.

	var out []SymbolDef
	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var name, receiver string
		var kind SymbolKind
		var r Range
		var nameLine uint32
		var node *sitter.Node

		// defRange stores the FULL range of the @def node (method body).
		// nameRange stores just the identifier position.
		// We use defRange.EndLine for enclosingSymbol lookup.
		var defNode *sitter.Node

		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "name":
				name = text
				node = c.Node
				r = nodeRange(c.Node)
				nameLine = r.StartLine
			case "receiver", "class", "type":
				receiver = text
			case "def":
				defNode = c.Node
				switch c.Node.Type() {
				case "function_declaration", "function_definition", "function",
					"func_literal", "function_item", "def":
					kind = SymbolKindFunction
				case "method_declaration", "method_definition", "method",
					"function_def":
					kind = SymbolKindMethod
				case "class_declaration", "class_definition", "class",
					"class_body", "struct_item":
					kind = SymbolKindClass
				case "interface_declaration", "interface", "trait_item",
					"protocol_declaration":
					kind = SymbolKindInterface
				case "constructor_declaration", "constructor_definition":
					kind = SymbolKindConstructor
				default:
					kind = SymbolKindFunction
				}
				_ = node // used below
			}
		}

		if name == "" {
			continue
		}

		defR := r
		// If we have the full @def node, use its EndLine so that
		// enclosingSymbol() can correctly identify which method body
		// a call site belongs to. Without this, methods whose identifier
		// is on line N but body extends to line M would never match calls
		// on lines N+1..M.
		if defNode != nil {
			defR = nodeRange(defNode)
			r.EndLine = defR.EndLine
			r.EndColumn = defR.EndColumn
			r.EndByte = defR.EndByte
			// Also use the def node's start so the range covers annotations too.
			if defR.StartLine < r.StartLine {
				r.StartLine = defR.StartLine
				r.StartColumn = defR.StartColumn
				r.StartByte = defR.StartByte
			}
		}

		qualified := qualifiedName(receiver, name, lang)

		annots := annotationsForSymbol(annotsByLine, src, defR, nameLine)

		out = append(out, SymbolDef{
			Name:        name,
			Qualified:   qualified,
			Kind:        kind,
			File:        relPath,
			Range:       r,
			Receiver:    receiver,
			Annotations: annots,
		})
	}

	// Pass 2: for methods that have no explicit receiver, infer it from
	// the enclosing class. This applies to Java, Kotlin, C#, Python, etc.
	// where class membership is determined by nesting, not by receiver syntax.
	//
	// We only qualify names for languages that don't already have receiver
	// syntax in the query (Go methods get "Receiver.method" from Pass 1).
	switch lang {
	case "java", "kotlin", "csharp", "python", "typescript", "tsx", "javascript", "jsx", "php", "ruby":
		out = qualifyMethodsWithClass(out)
	}

	return out
}

// qualifyMethodsWithClass infers the enclosing class for each method that
// has an empty Receiver and prepends the class name to its Qualified symbol.
// This prevents "run", "send", "save" etc. from being ambiguous across all
// classes in the project.
func qualifyMethodsWithClass(symbols []SymbolDef) []SymbolDef {
	// Collect class symbols sorted by StartLine ascending.
	type classRange struct {
		name  string
		start uint32
		end   uint32
	}
	var classes []classRange
	for _, sym := range symbols {
		if sym.Kind == SymbolKindClass || sym.Kind == SymbolKindInterface {
			classes = append(classes, classRange{
				name:  sym.Name,
				start: sym.Range.StartLine,
				end:   sym.Range.EndLine,
			})
		}
	}

	// For each method/function without a receiver, find the innermost class
	// that contains it.
	result := make([]SymbolDef, len(symbols))
	copy(result, symbols)

	for i := range result {
		sym := &result[i]
		if sym.Kind != SymbolKindMethod && sym.Kind != SymbolKindFunction && sym.Kind != SymbolKindConstructor {
			continue
		}
		if sym.Receiver != "" {
			// Already has receiver (Go style).
			continue
		}
		// Find the innermost enclosing class.
		best := ""
		bestStart := uint32(0)
		for _, cls := range classes {
			if cls.start <= sym.Range.StartLine && cls.end >= sym.Range.EndLine {
				if cls.start >= bestStart {
					bestStart = cls.start
					best = cls.name
				}
			}
		}
		if best != "" && best != sym.Name {
			sym.Receiver = best
			sym.Qualified = best + "." + sym.Name
		}
	}
	return result
}

// extractAnnotations returns a map from line number to annotations at that line.
func extractAnnotations(root *sitter.Node, src []byte, lang string, sitterLang *sitter.Language) map[uint32][]Annotation {
	q := queriesForLanguage(lang)
	out := map[uint32][]Annotation{}
	if q == nil || q.annotations == nil {
		return out
	}
	query, err := sitter.NewQuery(q.annotations, sitterLang)
	if err != nil {
		return out
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var annotName, args string
		var r Range
		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "name":
				annotName = text
				r = nodeRange(c.Node)
			case "args":
				args = trimOuterParens(text)
				argRange := nodeRange(c.Node)
				if r.StartLine == 0 && r.EndLine == 0 {
					r = argRange
				} else {
					r.EndLine = argRange.EndLine
					r.EndColumn = argRange.EndColumn
					r.EndByte = argRange.EndByte
				}
			}
		}
		if annotName != "" {
			out[r.StartLine] = append(out[r.StartLine], Annotation{Name: annotName, Arguments: args, Range: r})
		}
	}
	return out
}

func annotationsForSymbol(annotsByLine map[uint32][]Annotation, src []byte, defR Range, nameLine uint32) []Annotation {
	var out []Annotation
	seen := map[string]struct{}{}
	add := func(anns []Annotation) {
		for _, ann := range anns {
			key := ann.Name + "\x00" + ann.Arguments + "\x00" + fmt.Sprint(ann.Range.StartLine)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ann)
		}
	}

	// Prefer annotations that tree-sitter includes in the declaration node. This
	// keeps class annotations attached to classes and method annotations attached
	// only to the method they decorate.
	for line := defR.StartLine; line <= nameLine; line++ {
		add(annotsByLine[line])
	}
	if len(out) > 0 {
		return out
	}

	// Fallback for grammars whose declaration node starts at the identifier:
	// walk upward only through immediately adjacent annotation lines. Stop at
	// blanks or code so class-level annotations cannot leak into methods.
	lines := strings.Split(string(src), "\n")
	for line := int(nameLine) - 1; line >= 0 && line < len(lines); line-- {
		if anns := annotsByLine[uint32(line)]; len(anns) > 0 {
			out = append(append([]Annotation(nil), anns...), out...)
			continue
		}
		break
	}
	return out
}

// extractCalls runs the calls query and builds CallSite list.
// symbolsInFile is used to determine the enclosing function for each call.
func extractCalls(root *sitter.Node, src []byte, lang string, sitterLang *sitter.Language, relPath string, symbolsInFile []SymbolDef) []CallSite {
	q := queriesForLanguage(lang)
	if q == nil || q.calls == nil {
		return nil
	}
	query, err := sitter.NewQuery(q.calls, sitterLang)
	if err != nil {
		return nil
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	// seen deduplicates on (callee, startByte) so the same call expression
	// is not emitted twice when multiple query patterns match it.
	seen := map[uint64]struct{}{}
	deupKey := func(callee string, startByte uint32) uint64 {
		// Simple hash: combine startByte with a hash of the callee string.
		h := uint64(startByte) * 2654435761
		for _, ch := range callee {
			h = h*31 + uint64(ch)
		}
		return h
	}

	var out []CallSite
	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var calleeRaw, receiverRaw, argsText string
		var callNode *sitter.Node
		var isMethodRef bool
		var methodRefNode *sitter.Node

		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "callee":
				calleeRaw = text
				callNode = c.Node
			case "receiver":
				receiverRaw = text
			case "args":
				argsText = text
			case "method_ref":
				// Java/Kotlin method reference: extract the method name after "::"
				isMethodRef = true
				methodRefNode = c.Node
				full := text // e.g. "service::processItem" or "CampaignMapper::map"
				if idx := strings.LastIndex(full, "::"); idx >= 0 {
					calleeRaw = full[idx+2:]
				} else {
					calleeRaw = full
				}
				callNode = c.Node
			}
		}

		if calleeRaw == "" || callNode == nil {
			continue
		}
		if receiverRaw != "" && !strings.Contains(calleeRaw, ".") {
			calleeRaw = strings.TrimSpace(receiverRaw) + "." + strings.TrimSpace(calleeRaw)
		}
		_ = isMethodRef
		_ = methodRefNode

		r := nodeRange(callNode)

		// Deduplicate: the same call expression should only be emitted once
		// even if multiple query patterns both match it.
		key := deupKey(calleeRaw, r.StartByte)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		// Find the enclosing function.
		caller := enclosingSymbol(r.StartLine, symbolsInFile)

		// Build the enclosing control-flow path.
		enclosing := buildEnclosingPath(callNode, src)

		// Parse arguments.
		args := parseArguments(argsText)

		out = append(out, CallSite{
			Caller:        caller,
			CalleeRaw:     calleeRaw,
			ReceiverRaw:   receiverRaw,
			File:          relPath,
			Range:         r,
			Arguments:     args,
			EnclosingPath: enclosing,
		})
	}

	// Post-process: scan all calls for lambda arguments that contain method
	// references (`obj::method` in the source text). This handles cases like:
	//   list.forEach(service::processItem)
	//   list.stream().map(Mapper::convert)
	// where the tree-sitter query captures `forEach`/`map` as the callee but
	// the actual callable passed as an argument is `service::processItem`.
	// We synthesise a CallSite for the method reference so the walker can
	// follow that edge.
	out = appendMethodRefArgCalls(out, src, relPath, symbolsInFile, seen, deupKey)

	return out
}

// appendMethodRefArgCalls scans the already-extracted calls for arguments that
// look like method references ("Class::method" or "obj::method") and emits
// synthetic CallSites for the referenced method. This handles:
//
//	list.forEach(service::processItem)  →  synthetic call to processItem
//	stream.map(Mapper::convert)         →  synthetic call to convert
//
// The synthetic site has the same caller/enclosing context as the surrounding
// forEach/map/filter call, so conditions and repetitions are correctly attributed.
func appendMethodRefArgCalls(
	calls []CallSite,
	src []byte, relPath string,
	symbolsInFile []SymbolDef,
	seen map[uint64]struct{},
	deupKey func(string, uint32) uint64,
) []CallSite {
	var extra []CallSite
	for _, cs := range calls {
		for _, arg := range cs.Arguments {
			s := strings.TrimSpace(arg.Source)
			if !strings.Contains(s, "::") {
				continue
			}
			// Extract the method name after "::".
			idx := strings.LastIndex(s, "::")
			if idx < 0 || idx+2 >= len(s) {
				continue
			}
			methodName := strings.TrimSpace(s[idx+2:])
			if methodName == "" || methodName == "new" {
				continue
			}
			key := deupKey(methodName, cs.Range.StartByte+uint32(arg.Index)+0xdeadbeef)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			extra = append(extra, CallSite{
				Caller:        cs.Caller,
				CalleeRaw:     methodName,
				File:          relPath,
				Range:         cs.Range,
				Arguments:     nil,
				EnclosingPath: cs.EnclosingPath,
				IsImplicit:    true,
			})
		}
	}
	return append(calls, extra...)
}

// buildEnclosingPath walks from the call node up to the enclosing function
// body and collects control-flow boundary nodes.
func buildEnclosingPath(n *sitter.Node, src []byte) []EnclosingNode {
	var path []EnclosingNode
	// Walk up the tree collecting control-flow boundaries.
	cur := n.Parent()
	for cur != nil {
		kind := NormaliseNodeKind(cur.Type())
		if kind != "" {
			source := ""
			iterates := ""
			// Extract condition text or loop header.
			source, iterates = extractNodeSource(cur, src)
			path = append(path, EnclosingNode{
				Kind:         kind,
				Range:        nodeRange(cur),
				Source:       source,
				IteratesOver: iterates,
			})
		}
		// Stop when we hit a function/method definition (we don't want
		// to capture conditions from the *calling* function's scope).
		t := cur.Type()
		if isFunctionNode(t) {
			break
		}
		cur = cur.Parent()
	}
	// Reverse so outermost is first.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// extractNodeSource returns the condition/header text and the iterated
// collection (for loops) from a control-flow node.
func extractNodeSource(n *sitter.Node, src []byte) (source, iteratesOver string) {
	// For loops and comprehensions: try to find the iterable.
	switch n.Type() {
	case "for_statement", "enhanced_for_statement", "foreach_statement",
		"for_in_statement", "for_of_statement", "range_clause",
		"list_comprehension", "for_expression":
		// The iterable is typically the last named child before the body.
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			t := child.Type()
			// Skip the body block.
			if t == "block" || t == "statement_block" || t == "body" {
				break
			}
			iteratesOver = child.Content(src)
		}
		source = extractFirstLine(n.Content(src))
		return

	case "if_statement", "if_expression", "if_let_expression", "elif_clause":
		// Condition is the first named child that isn't the consequence.
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			t := child.Type()
			if t == "block" || t == "then" || t == "consequence" ||
				t == "statement_block" || t == "body" {
				break
			}
			source = child.Content(src)
			break
		}
		return

	case "while_statement", "while_expression":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			t := child.Type()
			if t == "block" || t == "body" || t == "statement_block" {
				break
			}
			source = child.Content(src)
			break
		}
		return

	case "catch_clause", "except_clause", "rescue":
		source = extractFirstLine(n.Content(src))
		return

	case "match_arm", "case_clause", "when_expression":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			t := child.Type()
			if t == "block" || t == "body" || t == "statement_block" ||
				t == "=>" || t == ":" {
				break
			}
			source = child.Content(src)
			break
		}
		return

	default:
		source = extractFirstLine(n.Content(src))
	}
	return
}

// enclosingSymbol finds the qualified name of the innermost symbol definition
// that contains lineNum (0-based).
func enclosingSymbol(lineNum uint32, symbols []SymbolDef) string {
	best := ""
	bestStart := uint32(0)
	for _, sym := range symbols {
		if sym.Range.StartLine <= lineNum && sym.Range.EndLine >= lineNum {
			if sym.Range.StartLine >= bestStart {
				bestStart = sym.Range.StartLine
				best = sym.Qualified
			}
		}
	}
	return best
}

// parseArguments splits a raw argument list text into individual ArgumentExprs.
func parseArguments(argsText string) []ArgumentExpr {
	argsText = trimOuterParens(argsText)
	if strings.TrimSpace(argsText) == "" {
		return nil
	}
	// Simple split on commas at depth 0.
	parts := splitArgs(argsText)
	out := make([]ArgumentExpr, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, ArgumentExpr{
			Index:  i,
			Source: p,
			Kind:   classifyArgument(p),
		})
	}
	return out
}

// splitArgs splits a comma-separated argument list, respecting nested
// brackets/parens/braces.
func splitArgs(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, ch := range s {
		switch ch {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

// classifyArgument classifies an argument expression text.
func classifyArgument(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "other"
	}
	// String literal.
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) ||
		(strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`")) {
		return "literal"
	}
	// Number literal.
	if len(s) > 0 && (s[0] >= '0' && s[0] <= '9') {
		return "literal"
	}
	// Boolean / null literals.
	switch strings.ToLower(s) {
	case "true", "false", "null", "nil", "none", "undefined":
		return "literal"
	}
	// Constructor call.
	if strings.HasPrefix(s, "new ") {
		return "new"
	}
	// Nested call expression (contains parentheses).
	if strings.Contains(s, "(") {
		return "call"
	}
	// Plain identifier.
	if !strings.ContainsAny(s, " \t.+*/%-&|^!<>=") {
		return "identifier"
	}
	return "other"
}

// Helpers

func nodeRange(n *sitter.Node) Range {
	sp := n.StartPoint()
	ep := n.EndPoint()
	return Range{
		StartByte:   n.StartByte(),
		EndByte:     n.EndByte(),
		StartLine:   sp.Row,
		StartColumn: sp.Column,
		EndLine:     ep.Row,
		EndColumn:   ep.Column,
	}
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') ||
			(s[0] == '`' && s[len(s)-1] == '`') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func trimOuterParens(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' {
		return s[1 : len(s)-1]
	}
	return s
}

func extractFirstLine(s string) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

// qualifiedName builds a stable qualified symbol name from receiver + method
// name, using the dot-separated convention shared across languages.
func qualifiedName(receiver, name, lang string) string {
	if receiver == "" {
		return name
	}
	return receiver + "." + name
}

// isFunctionNode reports whether the tree-sitter node type represents a
// function or method body boundary.
func isFunctionNode(t string) bool {
	switch t {
	case "function_declaration", "function_definition", "function",
		"method_declaration", "method_definition", "method",
		"func_literal", "function_item", "arrow_function",
		"anonymous_function", "closure_expression", "lambda",
		"proc_literal", "def", "constructor_declaration",
		"constructor_definition", "function_def":
		return true
	}
	return false
}
