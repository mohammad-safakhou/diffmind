package parser

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	golang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type treeSitterTarget struct {
	Language string
	Parser   string
	Grammar  *sitter.Language
}

func parseSourceWithTreeSitter(ctx context.Context, ext string, content []byte) (*ParseArtifact, bool, error) {
	target, ok := treeSitterForExt(ext)
	if !ok {
		return nil, false, nil
	}

	parser := sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(target.Grammar); err != nil {
		return nil, true, fmt.Errorf("tree-sitter set language: %w", err)
	}

	tree := parser.ParseCtx(ctx, content, nil)
	if tree == nil || tree.RootNode() == nil {
		return nil, true, fmt.Errorf("tree-sitter returned empty tree")
	}
	defer tree.Close()

	root := tree.RootNode()
	symbols := extractSymbols(root, content)
	artifact := &ParseArtifact{
		ArtifactType: "tree_sitter",
		Language:     target.Language,
		ParserName:   target.Parser,
		Tree: &TreeSummary{
			RootType:  root.Kind(),
			NodeCount: countNodes(root),
		},
		Symbols: symbols,
	}
	return artifact, true, nil
}

func treeSitterForExt(ext string) (treeSitterTarget, bool) {
	switch ext {
	case ".go":
		return treeSitterTarget{Language: "go", Parser: "tree-sitter-go", Grammar: sitter.NewLanguage(golang.Language())}, true
	case ".js", ".jsx":
		return treeSitterTarget{Language: "javascript", Parser: "tree-sitter-javascript", Grammar: sitter.NewLanguage(javascript.Language())}, true
	case ".ts":
		return treeSitterTarget{Language: "typescript", Parser: "tree-sitter-typescript", Grammar: sitter.NewLanguage(typescript.LanguageTypescript())}, true
	case ".tsx":
		return treeSitterTarget{Language: "typescript", Parser: "tree-sitter-tsx", Grammar: sitter.NewLanguage(typescript.LanguageTSX())}, true
	case ".py":
		return treeSitterTarget{Language: "python", Parser: "tree-sitter-python", Grammar: sitter.NewLanguage(python.Language())}, true
	case ".java":
		return treeSitterTarget{Language: "java", Parser: "tree-sitter-java", Grammar: sitter.NewLanguage(java.Language())}, true
	default:
		return treeSitterTarget{}, false
	}
}

func extractSymbols(node *sitter.Node, content []byte) []Symbol {
	out := make([]Symbol, 0, 64)
	walk(node, func(n *sitter.Node) {
		kind := symbolKind(n.Kind())
		if kind == "" {
			return
		}
		name := extractSymbolName(n, content)
		if name == "" {
			return
		}
		sp := n.StartPosition()
		ep := n.EndPosition()
		out = append(out, Symbol{
			Kind:      kind,
			Name:      name,
			StartLine: int(sp.Row) + 1,
			StartCol:  int(sp.Column) + 1,
			EndLine:   int(ep.Row) + 1,
			EndCol:    int(ep.Column) + 1,
		})
	})
	return out
}

func symbolKind(nodeType string) string {
	switch nodeType {
	case "function_declaration", "method_declaration", "function_definition":
		return "function"
	case "class_declaration", "class_definition":
		return "class"
	case "interface_declaration":
		return "interface"
	case "import_declaration", "import_statement":
		return "import"
	default:
		return ""
	}
}

func extractSymbolName(n *sitter.Node, content []byte) string {
	if child := n.ChildByFieldName("name"); child != nil {
		return strings.TrimSpace(child.Utf8Text(content))
	}
	text := strings.TrimSpace(n.Utf8Text(content))
	if text == "" {
		return ""
	}
	if len(text) > 160 {
		return text[:160]
	}
	return text
}

func walk(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		walk(node.Child(i), fn)
	}
}

func countNodes(node *sitter.Node) int {
	count := 0
	walk(node, func(*sitter.Node) { count++ })
	return count
}
