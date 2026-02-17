package analyzers

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	golang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func detectSemanticSymbolsAndCalls(c *collector, file sourceFile) {
	language, grammar, ok := semanticGrammarForExt(file.Ext)
	if !ok {
		return
	}

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(grammar); err != nil {
		return
	}
	content := []byte(file.Text)
	tree := parser.Parse(content, nil)
	if tree == nil || tree.RootNode() == nil {
		return
	}
	defer tree.Close()

	root := tree.RootNode()
	collectSemanticSymbols(c, file, language, root, content)
	collectSemanticCalls(c, file, language, root, content)
}

func semanticGrammarForExt(ext string) (string, *sitter.Language, bool) {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".go":
		return "go", sitter.NewLanguage(golang.Language()), true
	case ".js", ".jsx":
		return "javascript", sitter.NewLanguage(javascript.Language()), true
	case ".ts":
		return "typescript", sitter.NewLanguage(typescript.LanguageTypescript()), true
	case ".tsx":
		return "typescript", sitter.NewLanguage(typescript.LanguageTSX()), true
	case ".py":
		return "python", sitter.NewLanguage(python.Language()), true
	case ".java":
		return "java", sitter.NewLanguage(java.Language()), true
	default:
		return "", nil, false
	}
}

func collectSemanticSymbols(c *collector, file sourceFile, language string, root *sitter.Node, content []byte) {
	symbolKinds := semanticSymbolKinds(language)
	if len(symbolKinds) == 0 {
		return
	}
	walkSemantic(root, func(n *sitter.Node) {
		kind := strings.TrimSpace(n.Kind())
		symbolKind, ok := symbolKinds[kind]
		if !ok {
			return
		}
		name := semanticSymbolName(language, n, content)
		if name == "" {
			return
		}
		line, col := semanticLineCol(n)
		c.addFactWithEvidence("CodeSymbol", map[string]any{
			"language":    language,
			"symbol_kind": symbolKind,
			"name":        name,
			"file":        file.Path,
			"line":        line,
			"col":         col,
		}, file, line, col, semanticLineSnippet(file, line), func() { c.report.CodeSymbols++ })
	})
}

func collectSemanticCalls(c *collector, file sourceFile, language string, root *sitter.Node, content []byte) {
	callKind := semanticCallKind(language)
	if callKind == "" {
		return
	}
	walkSemantic(root, func(n *sitter.Node) {
		if n.Kind() != callKind {
			return
		}
		callee := semanticCallName(language, n, content)
		if callee == "" {
			return
		}
		line, col := semanticLineCol(n)
		c.addFactWithEvidence("CodeCall", map[string]any{
			"language": language,
			"callee":   callee,
			"file":     file.Path,
			"line":     line,
			"col":      col,
		}, file, line, col, semanticLineSnippet(file, line), func() { c.report.CodeCalls++ })
	})
}

func semanticSymbolKinds(language string) map[string]string {
	switch language {
	case "go":
		return map[string]string{
			"function_declaration": "function",
			"method_declaration":   "method",
			"type_declaration":     "type",
		}
	case "javascript", "typescript":
		return map[string]string{
			"function_declaration": "function",
			"method_definition":    "method",
			"class_declaration":    "class",
		}
	case "python":
		return map[string]string{
			"function_definition": "function",
			"class_definition":    "class",
		}
	case "java":
		return map[string]string{
			"method_declaration":    "method",
			"class_declaration":     "class",
			"interface_declaration": "interface",
		}
	default:
		return nil
	}
}

func semanticCallKind(language string) string {
	switch language {
	case "go":
		return "call_expression"
	case "javascript", "typescript":
		return "call_expression"
	case "python":
		return "call"
	case "java":
		return "method_invocation"
	default:
		return ""
	}
}

func semanticSymbolName(language string, node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	if child := node.ChildByFieldName("name"); child != nil {
		return strings.TrimSpace(child.Utf8Text(content))
	}

	switch language {
	case "go":
		// type_declaration keeps identifiers under named children.
		for i := uint(0); i < node.NamedChildCount(); i++ {
			ch := node.NamedChild(i)
			if ch == nil {
				continue
			}
			if ch.Kind() == "type_spec" {
				if n := ch.ChildByFieldName("name"); n != nil {
					return strings.TrimSpace(n.Utf8Text(content))
				}
			}
		}
	}
	return ""
}

func semanticCallName(language string, node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	switch language {
	case "go", "javascript", "typescript", "python":
		if fn := node.ChildByFieldName("function"); fn != nil {
			return strings.TrimSpace(fn.Utf8Text(content))
		}
	case "java":
		if name := node.ChildByFieldName("name"); name != nil {
			return strings.TrimSpace(name.Utf8Text(content))
		}
		return strings.TrimSpace(node.Utf8Text(content))
	}
	return ""
}

func walkSemantic(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		walkSemantic(node.Child(i), fn)
	}
}

func semanticLineCol(node *sitter.Node) (int, int) {
	if node == nil {
		return 1, 1
	}
	sp := node.StartPosition()
	line := int(sp.Row) + 1
	col := int(sp.Column) + 1
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	return line, col
}

func semanticLineSnippet(file sourceFile, line int) string {
	if line < 1 || line > len(file.Lines) {
		return ""
	}
	return file.Lines[line-1]
}
