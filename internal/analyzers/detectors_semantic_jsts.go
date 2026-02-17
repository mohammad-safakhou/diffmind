package analyzers

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func detectJSTSInboundEndpointsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJSTSAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	routerObjects, _ := collectJSTSSymbols(root, content)
	scanJSTSCallExpressions(root, func(call *sitter.Node) {
		member, callName, args := parseJSTSCallShape(call, content)
		if member == "" || len(args) == 0 {
			return
		}
		method := strings.ToUpper(strings.TrimSpace(callName))
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
			if !routerObjects[member] {
				return
			}
			path := normalizeJSTSArgument(args[0], content)
			if path == "" {
				return
			}
			line, col := jstsLineCol(call)
			snippet := lineSnippet(file, line)
			c.addFactWithEvidence("Endpoint", map[string]any{
				"direction": "inbound",
				"method":    method,
				"path":      path,
				"framework": "express-semantic",
			}, file, line, col, snippet, func() { c.report.Endpoints++ })
		}
	})
	return true
}

func detectJSTSOutboundCallsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJSTSAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	_, axiosClients := collectJSTSSymbols(root, content)
	scanJSTSCallExpressions(root, func(call *sitter.Node) {
		fn := call.ChildByFieldName("function")
		argsNode := call.ChildByFieldName("arguments")
		if fn == nil || argsNode == nil {
			return
		}
		args := namedChildren(argsNode)
		if len(args) == 0 {
			return
		}

		if fn.Kind() == "identifier" {
			if strings.TrimSpace(fn.Utf8Text(content)) == "fetch" {
				target := normalizeJSTSArgument(args[0], content)
				if target == "" {
					target = "unknown-target"
				}
				line, col := jstsLineCol(call)
				snippet := lineSnippet(file, line)
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "http",
					"method":   "UNKNOWN",
					"target":   target,
					"library":  "fetch-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
			return
		}

		member, callName, _ := parseJSTSCallShape(call, content)
		if member == "" {
			return
		}
		method := strings.ToUpper(strings.TrimSpace(callName))
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
			if !(member == "axios" || axiosClients[member]) {
				return
			}
			target := normalizeJSTSArgument(args[0], content)
			if target == "" {
				target = "unknown-target"
			}
			line, col := jstsLineCol(call)
			snippet := lineSnippet(file, line)
			library := "axios-semantic"
			if axiosClients[member] {
				library = "axios-client-semantic"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol": "http",
				"method":   method,
				"target":   target,
				"library":  library,
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		}
	})
	return true
}

func parseJSTSAST(file sourceFile) (*sitter.Tree, []byte, bool) {
	ext := strings.ToLower(strings.TrimSpace(file.Ext))
	var language *sitter.Language
	switch ext {
	case ".js", ".jsx":
		language = sitter.NewLanguage(javascript.Language())
	case ".ts":
		language = sitter.NewLanguage(typescript.LanguageTypescript())
	case ".tsx":
		language = sitter.NewLanguage(typescript.LanguageTSX())
	default:
		return nil, nil, false
	}

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(language); err != nil {
		return nil, nil, false
	}
	content := []byte(file.Text)
	tree := parser.Parse(content, nil)
	if tree == nil || tree.RootNode() == nil {
		return nil, nil, false
	}
	return tree, content, true
}

func collectJSTSSymbols(root *sitter.Node, content []byte) (map[string]bool, map[string]bool) {
	routerObjects := map[string]bool{
		"app":    true,
		"router": true,
	}
	axiosClients := map[string]bool{}

	walkJSTS(root, func(n *sitter.Node) {
		if n.Kind() != "variable_declarator" {
			return
		}
		nameNode := n.ChildByFieldName("name")
		valueNode := n.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil || nameNode.Kind() != "identifier" || valueNode.Kind() != "call_expression" {
			return
		}
		name := strings.TrimSpace(nameNode.Utf8Text(content))
		if name == "" {
			return
		}

		fn := valueNode.ChildByFieldName("function")
		if fn == nil {
			return
		}

		// const api = express.Router()
		if fn.Kind() == "member_expression" {
			obj := fn.ChildByFieldName("object")
			prop := fn.ChildByFieldName("property")
			if obj != nil && prop != nil && obj.Kind() == "identifier" {
				objName := strings.TrimSpace(obj.Utf8Text(content))
				propName := strings.TrimSpace(prop.Utf8Text(content))
				if objName == "express" && propName == "Router" {
					routerObjects[name] = true
				}
				if objName == "axios" && propName == "create" {
					axiosClients[name] = true
				}
			}
		}

		// const app = express()
		if fn.Kind() == "identifier" && strings.TrimSpace(fn.Utf8Text(content)) == "express" {
			routerObjects[name] = true
		}
	})
	return routerObjects, axiosClients
}

func parseJSTSCallShape(call *sitter.Node, content []byte) (object string, method string, args []*sitter.Node) {
	fn := call.ChildByFieldName("function")
	argsNode := call.ChildByFieldName("arguments")
	if fn == nil || argsNode == nil || fn.Kind() != "member_expression" {
		return "", "", nil
	}
	obj := fn.ChildByFieldName("object")
	prop := fn.ChildByFieldName("property")
	if obj == nil || prop == nil {
		return "", "", nil
	}
	if obj.Kind() != "identifier" {
		return "", "", nil
	}
	return strings.TrimSpace(obj.Utf8Text(content)), strings.TrimSpace(prop.Utf8Text(content)), namedChildren(argsNode)
}

func namedChildren(node *sitter.Node) []*sitter.Node {
	if node == nil {
		return nil
	}
	out := make([]*sitter.Node, 0, node.NamedChildCount())
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child != nil {
			out = append(out, child)
		}
	}
	return out
}

func scanJSTSCallExpressions(root *sitter.Node, fn func(call *sitter.Node)) {
	walkJSTS(root, func(n *sitter.Node) {
		if n.Kind() == "call_expression" {
			fn(n)
		}
	})
}

func walkJSTS(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		walkJSTS(node.Child(i), fn)
	}
}

func normalizeJSTSArgument(node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	raw := strings.TrimSpace(node.Utf8Text(content))
	raw = strings.TrimPrefix(raw, "`")
	raw = strings.TrimSuffix(raw, "`")
	raw = strings.Trim(raw, "\"")
	raw = strings.Trim(raw, "'")
	return strings.TrimSpace(raw)
}

func jstsLineCol(node *sitter.Node) (int, int) {
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

func lineSnippet(file sourceFile, line int) string {
	if line < 1 || line > len(file.Lines) {
		return ""
	}
	return file.Lines[line-1]
}
