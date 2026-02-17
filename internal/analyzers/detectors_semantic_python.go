package analyzers

import (
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

var pythonRouteMethodsRe = regexp.MustCompile(`(?i)methods\s*=\s*\[\s*["']([A-Za-z]+)["']`)

func detectPythonInboundEndpointsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parsePythonAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	routerNames := map[string]bool{
		"app":    true,
		"router": true,
		"api":    true,
	}

	scanPythonCallExpressions(root, func(call *sitter.Node) {
		object, method, args := parsePythonCallShape(call, content)
		if object == "" || !routerNames[object] {
			return
		}
		method = strings.ToUpper(strings.TrimSpace(method))
		if len(args) == 0 {
			return
		}

		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
			path := normalizePythonArgument(args[0], content)
			if path == "" {
				return
			}
			line, col := pythonLineCol(call)
			c.addFactWithEvidence("Endpoint", map[string]any{
				"direction": "inbound",
				"method":    method,
				"path":      path,
				"framework": "python-semantic",
			}, file, line, col, pythonLineSnippet(file, line), func() { c.report.Endpoints++ })
		case "ROUTE":
			path := normalizePythonArgument(args[0], content)
			if path == "" {
				return
			}
			httpMethod := extractPythonRouteMethod(call, content)
			line, col := pythonLineCol(call)
			c.addFactWithEvidence("Endpoint", map[string]any{
				"direction": "inbound",
				"method":    httpMethod,
				"path":      path,
				"framework": "python-semantic",
			}, file, line, col, pythonLineSnippet(file, line), func() { c.report.Endpoints++ })
		}
	})
	return true
}

func detectPythonOutboundCallsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parsePythonAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	requestsAliases, directFuncs := collectPythonRequestsSymbols(root, content)
	scanPythonCallExpressions(root, func(call *sitter.Node) {
		fn := call.ChildByFieldName("function")
		argsNode := call.ChildByFieldName("arguments")
		if fn == nil || argsNode == nil {
			return
		}
		args := pythonArgs(argsNode)
		if len(args) == 0 {
			return
		}

		method := ""
		if fn.Kind() == "attribute" {
			obj := fn.ChildByFieldName("object")
			attr := fn.ChildByFieldName("attribute")
			if obj == nil || attr == nil || obj.Kind() != "identifier" {
				return
			}
			objName := strings.TrimSpace(obj.Utf8Text(content))
			if !requestsAliases[objName] {
				return
			}
			method = strings.ToUpper(strings.TrimSpace(attr.Utf8Text(content)))
		} else if fn.Kind() == "identifier" {
			name := strings.TrimSpace(fn.Utf8Text(content))
			m, ok := directFuncs[name]
			if !ok {
				return
			}
			method = m
		} else {
			return
		}

		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
			target := normalizePythonArgument(args[0], content)
			if target == "" {
				target = "unknown-target"
			}
			line, col := pythonLineCol(call)
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol": "http",
				"method":   method,
				"target":   target,
				"library":  "python-requests-semantic",
			}, file, line, col, pythonLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}
	})
	return true
}

func parsePythonAST(file sourceFile) (*sitter.Tree, []byte, bool) {
	if strings.ToLower(strings.TrimSpace(file.Ext)) != ".py" {
		return nil, nil, false
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(python.Language())); err != nil {
		return nil, nil, false
	}
	content := []byte(file.Text)
	tree := parser.Parse(content, nil)
	if tree == nil || tree.RootNode() == nil {
		return nil, nil, false
	}
	return tree, content, true
}

func collectPythonRequestsSymbols(root *sitter.Node, content []byte) (map[string]bool, map[string]string) {
	aliases := map[string]bool{
		"requests": true,
	}
	direct := map[string]string{}

	walkPython(root, func(n *sitter.Node) {
		switch n.Kind() {
		case "import_statement":
			text := strings.TrimSpace(n.Utf8Text(content))
			// Supports: import requests / import requests as rq
			if !strings.HasPrefix(text, "import ") {
				return
			}
			text = strings.TrimSpace(strings.TrimPrefix(text, "import "))
			if text == "requests" {
				aliases["requests"] = true
				return
			}
			if strings.HasPrefix(text, "requests as ") {
				alias := strings.TrimSpace(strings.TrimPrefix(text, "requests as "))
				if alias != "" {
					aliases[alias] = true
				}
			}
		case "import_from_statement":
			text := strings.TrimSpace(n.Utf8Text(content))
			// Supports: from requests import get / from requests import post as p
			if !strings.HasPrefix(text, "from requests import ") {
				return
			}
			names := strings.TrimSpace(strings.TrimPrefix(text, "from requests import "))
			for _, part := range strings.Split(names, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				aliasName := part
				baseName := part
				if strings.Contains(part, " as ") {
					p := strings.SplitN(part, " as ", 2)
					baseName = strings.TrimSpace(p[0])
					aliasName = strings.TrimSpace(p[1])
				}
				switch strings.ToLower(baseName) {
				case "get", "post", "put", "patch", "delete":
					direct[aliasName] = strings.ToUpper(baseName)
				}
			}
		}
	})
	return aliases, direct
}

func scanPythonCallExpressions(root *sitter.Node, fn func(call *sitter.Node)) {
	walkPython(root, func(n *sitter.Node) {
		if n.Kind() == "call" {
			fn(n)
		}
	})
}

func walkPython(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		walkPython(node.Child(i), fn)
	}
}

func parsePythonCallShape(call *sitter.Node, content []byte) (object string, method string, args []*sitter.Node) {
	fn := call.ChildByFieldName("function")
	argsNode := call.ChildByFieldName("arguments")
	if fn == nil || argsNode == nil || fn.Kind() != "attribute" {
		return "", "", nil
	}
	obj := fn.ChildByFieldName("object")
	attr := fn.ChildByFieldName("attribute")
	if obj == nil || attr == nil || obj.Kind() != "identifier" {
		return "", "", nil
	}
	return strings.TrimSpace(obj.Utf8Text(content)), strings.TrimSpace(attr.Utf8Text(content)), pythonArgs(argsNode)
}

func pythonArgs(node *sitter.Node) []*sitter.Node {
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

func normalizePythonArgument(node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	text := strings.TrimSpace(node.Utf8Text(content))
	text = strings.Trim(text, "\"")
	text = strings.Trim(text, "'")
	return strings.TrimSpace(text)
}

func extractPythonRouteMethod(call *sitter.Node, content []byte) string {
	argsNode := call.ChildByFieldName("arguments")
	if argsNode == nil {
		return "ANY"
	}
	text := argsNode.Utf8Text(content)
	m := pythonRouteMethodsRe.FindStringSubmatch(text)
	if len(m) >= 2 {
		return strings.ToUpper(strings.TrimSpace(m[1]))
	}
	return "ANY"
}

func pythonLineCol(node *sitter.Node) (int, int) {
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

func pythonLineSnippet(file sourceFile, line int) string {
	if line < 1 || line > len(file.Lines) {
		return ""
	}
	return file.Lines[line-1]
}
