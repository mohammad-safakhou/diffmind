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

	return fa, nil
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

	var out []SymbolDef
	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var name, receiver string
		var kind SymbolKind
		var r Range
		var node *sitter.Node

		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "name":
				name = text
				node = c.Node
				r = nodeRange(c.Node)
			case "receiver", "class", "type":
				receiver = text
			case "def":
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

		qualified := qualifiedName(receiver, name, lang)

		// Collect annotations from the preceding lines (annotations/decorators
		// appear above the function definition, not on the same line as the
		// identifier). Search up to 10 lines above.
		var annots []Annotation
		for lineOff := uint32(1); lineOff <= 10 && r.StartLine >= lineOff; lineOff++ {
			lineAnnots := annotsByLine[r.StartLine-lineOff]
			if len(lineAnnots) > 0 {
				annots = append(lineAnnots, annots...)
			}
		}
		// Also include annotations on the same line (rare but valid).
		if same := annotsByLine[r.StartLine]; len(same) > 0 {
			annots = append(annots, same...)
		}

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
	return out
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
		var line uint32
		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "name":
				annotName = text
				line = c.Node.StartPoint().Row
			case "args":
				args = trimOuterParens(text)
			}
		}
		if annotName != "" {
			out[line] = append(out[line], Annotation{Name: annotName, Arguments: args})
		}
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

	var out []CallSite
	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var calleeRaw, argsText string
		var callNode *sitter.Node

		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "callee":
				calleeRaw = text
				callNode = c.Node
			case "args":
				argsText = text
			}
		}

		if calleeRaw == "" || callNode == nil {
			continue
		}

		r := nodeRange(callNode)

		// Find the enclosing function.
		caller := enclosingSymbol(r.StartLine, symbolsInFile)

		// Build the enclosing control-flow path.
		enclosing := buildEnclosingPath(callNode, src)

		// Parse arguments.
		args := parseArguments(argsText)

		out = append(out, CallSite{
			Caller:        caller,
			CalleeRaw:     calleeRaw,
			File:          relPath,
			Range:         r,
			Arguments:     args,
			EnclosingPath: enclosing,
		})
	}
	return out
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

// ─── Helpers ─────────────────────────────────────────────────────────────────

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

// ─── Config file parsing ──────────────────────────────────────────────────────

// extractConfigEntries extracts flat key-value pairs from a configuration file.
// The parser is format-specific but the output is always a flat []ConfigEntry.
func extractConfigEntries(src []byte, format string) []ConfigEntry {
	switch format {
	case "yaml":
		return parseYAMLEntries(src)
	case "json":
		return parseJSONEntries(src)
	case "properties", "env":
		return parsePropertiesEntries(src)
	case "toml":
		return parseTOMLEntries(src)
	default:
		return nil
	}
}

// parseYAMLEntries parses a YAML file into flat key-value pairs using a simple
// line-by-line parser. We deliberately avoid full YAML libraries to keep the
// dependency tree minimal — all we need is the key:value structure.
func parseYAMLEntries(src []byte) []ConfigEntry {
	var entries []ConfigEntry
	lines := strings.Split(string(src), "\n")
	var prefixStack []string
	var indentStack []int

	for lineNum, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		indent := len(line) - len(trimmed)

		// Pop prefix stack when indentation decreases.
		for len(indentStack) > 0 && indent <= indentStack[len(indentStack)-1] {
			indentStack = indentStack[:len(indentStack)-1]
			prefixStack = prefixStack[:len(prefixStack)-1]
		}

		colonIdx := strings.Index(trimmed, ":")
		if colonIdx < 0 {
			continue
		}

		key := strings.TrimSpace(trimmed[:colonIdx])
		value := strings.TrimSpace(trimmed[colonIdx+1:])

		// Skip list markers.
		if key == "-" || strings.HasPrefix(key, "- ") {
			continue
		}

		// Remove YAML string quotes.
		value = strings.Trim(value, `"'`)

		fullKey := strings.Join(append(prefixStack, key), ".")

		if value == "" || value == "{}" || value == "[]" {
			// This is a mapping parent — push onto prefix stack.
			prefixStack = append(prefixStack, key)
			indentStack = append(indentStack, indent)
		} else {
			entries = append(entries, ConfigEntry{
				Key:   fullKey,
				Value: value,
				Line:  lineNum + 1,
			})
		}
	}
	return entries
}

// parsePropertiesEntries parses .properties / .env files.
func parsePropertiesEntries(src []byte) []ConfigEntry {
	var entries []ConfigEntry
	for lineNum, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		// Support both = and : separators.
		sep := strings.IndexAny(line, "=:")
		if sep < 0 {
			continue
		}
		key := strings.TrimSpace(line[:sep])
		value := strings.TrimSpace(line[sep+1:])
		if key == "" {
			continue
		}
		entries = append(entries, ConfigEntry{Key: key, Value: value, Line: lineNum + 1})
	}
	return entries
}

// parseJSONEntries does a recursive descent parse of a JSON object to extract
// all leaf key-value pairs as dot-separated keys.
func parseJSONEntries(src []byte) []ConfigEntry {
	// Simple approach: find "key": "value" patterns using string scanning.
	// A full JSON parser would be better, but for our purposes (finding
	// connection strings and endpoints) this is sufficient.
	var entries []ConfigEntry
	lines := strings.Split(string(src), "\n")
	for lineNum, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, ":") {
			continue
		}
		// Try to extract "key": "value" or "key": value patterns.
		if !strings.HasPrefix(line, `"`) {
			continue
		}
		closeQuote := strings.Index(line[1:], `"`)
		if closeQuote < 0 {
			continue
		}
		key := line[1 : closeQuote+1]
		rest := strings.TrimSpace(line[closeQuote+2:])
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(rest, ":"))
		value = strings.TrimRight(value, ",")
		value = strings.Trim(value, `"`)
		if value == "" || value == "{" || value == "[" || value == "}" || value == "]" {
			continue
		}
		entries = append(entries, ConfigEntry{Key: key, Value: value, Line: lineNum + 1})
	}
	return entries
}

// parseTOMLEntries parses a TOML file into flat key-value pairs.
func parseTOMLEntries(src []byte) []ConfigEntry {
	var entries []ConfigEntry
	var section string
	for lineNum, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			end := strings.Index(line, "]")
			section = line[1:end] + "."
			continue
		}
		if !strings.Contains(line, "=") {
			continue
		}
		eqIdx := strings.Index(line, "=")
		key := strings.TrimSpace(line[:eqIdx])
		value := strings.TrimSpace(line[eqIdx+1:])
		value = strings.Trim(value, `"'`)
		entries = append(entries, ConfigEntry{
			Key:   section + key,
			Value: value,
			Line:  lineNum + 1,
		})
	}
	return entries
}
