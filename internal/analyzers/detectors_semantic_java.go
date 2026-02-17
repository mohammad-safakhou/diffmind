package analyzers

import (
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
)

var (
	javaQuotedStringRe     = regexp.MustCompile(`"([^"]+)"`)
	javaNamedPathValueRe   = regexp.MustCompile(`(?i)(path|value)\s*=\s*"([^"]+)"`)
	javaRequestMethodRefRe = regexp.MustCompile(`RequestMethod\.([A-Z]+)`)
	javaRestTemplateCallRe = regexp.MustCompile(`(?i)\.(getForObject|getForEntity|postForObject|postForEntity|put|delete|exchange)\(\s*"([^"]+)"`)
	javaRestExchangeVerbRe = regexp.MustCompile(`HttpMethod\.([A-Z]+)`)
)

func detectJavaInboundEndpointsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJavaAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	walkJava(root, func(n *sitter.Node) {
		if n == nil || (n.Kind() != "annotation" && n.Kind() != "marker_annotation") {
			return
		}
		text := strings.TrimSpace(n.Utf8Text(content))
		method, path, matched := parseSpringMappingAnnotation(text)
		if !matched || path == "" {
			return
		}
		line, col := javaLineCol(n)
		c.addFactWithEvidence("Endpoint", map[string]any{
			"direction": "inbound",
			"method":    method,
			"path":      path,
			"framework": "spring-semantic",
		}, file, line, col, javaLineSnippet(file, line), func() { c.report.Endpoints++ })
	})
	return true
}

func detectJavaOutboundCallsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJavaAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	walkJava(root, func(n *sitter.Node) {
		if n == nil || n.Kind() != "method_invocation" {
			return
		}
		text := strings.TrimSpace(n.Utf8Text(content))
		m := javaRestTemplateCallRe.FindStringSubmatch(text)
		if len(m) < 3 {
			return
		}
		callName := strings.ToLower(strings.TrimSpace(m[1]))
		target := strings.TrimSpace(m[2])
		if target == "" {
			target = "unknown-target"
		}

		method := "UNKNOWN"
		switch callName {
		case "getforobject", "getforentity":
			method = "GET"
		case "postforobject", "postforentity":
			method = "POST"
		case "put":
			method = "PUT"
		case "delete":
			method = "DELETE"
		case "exchange":
			if mm := javaRestExchangeVerbRe.FindStringSubmatch(text); len(mm) >= 2 {
				method = strings.ToUpper(strings.TrimSpace(mm[1]))
			}
		}

		line, col := javaLineCol(n)
		c.addFactWithEvidence("ExternalCall", map[string]any{
			"protocol": "http",
			"method":   method,
			"target":   target,
			"library":  "resttemplate-semantic",
		}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
	})
	return true
}

func parseJavaAST(file sourceFile) (*sitter.Tree, []byte, bool) {
	if strings.ToLower(strings.TrimSpace(file.Ext)) != ".java" {
		return nil, nil, false
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(java.Language())); err != nil {
		return nil, nil, false
	}
	content := []byte(file.Text)
	tree := parser.Parse(content, nil)
	if tree == nil || tree.RootNode() == nil {
		return nil, nil, false
	}
	return tree, content, true
}

func walkJava(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		walkJava(node.Child(i), fn)
	}
}

func parseSpringMappingAnnotation(text string) (method string, path string, matched bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "@") {
		return "", "", false
	}

	name := strings.TrimPrefix(text, "@")
	if i := strings.Index(name, "("); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", false
	}

	switch name {
	case "GetMapping":
		method = "GET"
	case "PostMapping":
		method = "POST"
	case "PutMapping":
		method = "PUT"
	case "PatchMapping":
		method = "PATCH"
	case "DeleteMapping":
		method = "DELETE"
	case "RequestMapping":
		method = "ANY"
		if m := javaRequestMethodRefRe.FindStringSubmatch(text); len(m) >= 2 {
			method = strings.ToUpper(strings.TrimSpace(m[1]))
		}
	default:
		return "", "", false
	}

	if m := javaNamedPathValueRe.FindStringSubmatch(text); len(m) >= 3 {
		path = strings.TrimSpace(m[2])
	} else if m := javaQuotedStringRe.FindStringSubmatch(text); len(m) >= 2 {
		path = strings.TrimSpace(m[1])
	}
	return method, path, true
}

func javaLineCol(node *sitter.Node) (int, int) {
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

func javaLineSnippet(file sourceFile, line int) string {
	if line < 1 || line > len(file.Lines) {
		return ""
	}
	return file.Lines[line-1]
}
