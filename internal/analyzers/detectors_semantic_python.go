package analyzers

import (
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

var (
	pythonRouteMethodsRe = regexp.MustCompile(`(?i)methods\s*=\s*\[\s*["']([A-Za-z]+)["']`)
	pythonKeywordArgRe   = regexp.MustCompile(`(?i)\b([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*["']([^"']+)["']`)
)

func detectPythonInboundEndpointsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parsePythonAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	routerNames, routerPrefixes := collectPythonRouterSymbols(root, content)

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
			fullPath := joinPythonPaths(routerPrefixes[object], path)
			c.addFactWithEvidence("Endpoint", map[string]any{
				"direction": "inbound",
				"method":    method,
				"path":      fullPath,
				"framework": "python-semantic",
			}, file, line, col, pythonLineSnippet(file, line), func() { c.report.Endpoints++ })
		case "ROUTE":
			path := normalizePythonArgument(args[0], content)
			if path == "" {
				return
			}
			httpMethod := extractPythonRouteMethod(call, content)
			line, col := pythonLineCol(call)
			fullPath := joinPythonPaths(routerPrefixes[object], path)
			c.addFactWithEvidence("Endpoint", map[string]any{
				"direction": "inbound",
				"method":    httpMethod,
				"path":      fullPath,
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

	httpAliases, directFuncs := collectPythonHTTPClientSymbols(root, content)
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
			clientKind, ok := httpAliases[objName]
			if !ok {
				return
			}
			method = strings.ToUpper(strings.TrimSpace(attr.Utf8Text(content)))
			if clientKind == "httpx" && method == "REQUEST" {
				method = extractPythonHTTPXRequestMethod(args, content)
			}
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
			target := pythonTargetFromArgs(args, content)
			if target == "" {
				target = "unknown-target"
			}
			line, col := pythonLineCol(call)
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol": "http",
				"method":   method,
				"target":   target,
				"library":  "python-http-semantic",
			}, file, line, col, pythonLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}
	})
	return true
}

func detectPythonQueueAndDBCallsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parsePythonAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()
	moduleAliases := collectPythonModuleAliases(root, content)
	serviceClients := collectPythonServiceClients(root, content, moduleAliases)

	scanPythonCallExpressions(root, func(call *sitter.Node) {
		fn := call.ChildByFieldName("function")
		argsNode := call.ChildByFieldName("arguments")
		if fn == nil || argsNode == nil {
			return
		}
		args := pythonArgs(argsNode)
		line, col := pythonLineCol(call)
		snippet := pythonLineSnippet(file, line)

		if fn.Kind() == "attribute" {
			object := fn.ChildByFieldName("object")
			attr := fn.ChildByFieldName("attribute")
			if attr == nil || object == nil {
				return
			}
			objectName := strings.TrimSpace(object.Utf8Text(content))
			name := strings.ToLower(strings.TrimSpace(attr.Utf8Text(content)))

			if service := strings.ToLower(strings.TrimSpace(serviceClients[objectName])); service != "" {
				switch service {
				case "sqs":
					switch name {
					case "send_message", "sendmessage":
						target := pythonServiceTargetFromArgs(argsNode.Utf8Text(content), args, content, "queue_url", "QueueUrl")
						if target == "" {
							target = "sqs:unknown-queue"
						}
						c.addFactWithEvidence("ExternalCall", map[string]any{
							"protocol":        "queue",
							"method":          "PUBLISH",
							"target":          target,
							"library":         "python-boto3-sqs-semantic",
							"queue_operation": "publish",
							"queue_kind":      "sqs",
						}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
						return
					case "receive_message", "receivemessage":
						target := pythonServiceTargetFromArgs(argsNode.Utf8Text(content), args, content, "queue_url", "QueueUrl")
						if target == "" {
							target = "sqs:unknown-queue"
						}
						c.addFactWithEvidence("ExternalCall", map[string]any{
							"protocol":        "queue",
							"method":          "CONSUME",
							"target":          target,
							"library":         "python-boto3-sqs-semantic",
							"queue_operation": "consume",
							"queue_kind":      "sqs",
						}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
						return
					}
				case "sns":
					if name == "publish" {
						target := pythonServiceTargetFromArgs(argsNode.Utf8Text(content), args, content, "topic_arn", "TopicArn")
						if target == "" {
							target = "sns:unknown-topic"
						}
						c.addFactWithEvidence("ExternalCall", map[string]any{
							"protocol":        "queue",
							"method":          "PUBLISH",
							"target":          target,
							"library":         "python-boto3-sns-semantic",
							"queue_operation": "publish",
							"queue_kind":      "sns",
						}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
						return
					}
				}
			}

			switch name {
			case "send", "publish", "send_task", "apply_async", "send_message":
				target := pythonTargetFromArgs(args, content)
				if target == "" {
					target = "queue:unknown"
				}
				queueKind := "queue"
				if strings.Contains(strings.ToLower(target), "sqs") {
					queueKind = "sqs"
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":        "queue",
					"method":          "PUBLISH",
					"target":          target,
					"library":         "python-queue-semantic",
					"queue_operation": "publish",
					"queue_kind":      queueKind,
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			case "consume", "subscribe", "receive_message", "get_message":
				target := pythonTargetFromArgs(args, content)
				if target == "" {
					target = "queue:unknown"
				}
				queueKind := "queue"
				if strings.Contains(strings.ToLower(target), "sqs") {
					queueKind = "sqs"
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":        "queue",
					"method":          "CONSUME",
					"target":          target,
					"library":         "python-queue-semantic",
					"queue_operation": "consume",
					"queue_kind":      queueKind,
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			case "execute":
				target := pythonTargetFromArgs(args, content)
				method := "READ"
				op := strings.ToUpper(strings.TrimSpace(target))
				if strings.HasPrefix(op, "INSERT ") || strings.HasPrefix(op, "UPDATE ") || strings.HasPrefix(op, "DELETE ") {
					method = "WRITE"
				}
				if target == "" {
					target = "db"
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":     "db",
					"method":       method,
					"target":       target,
					"library":      "python-db-semantic",
					"db_operation": strings.ToLower(method),
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			case "query", "find", "get":
				target := pythonTargetFromArgs(args, content)
				if target == "" {
					target = "db"
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":     "db",
					"method":       "READ",
					"target":       target,
					"library":      "python-db-semantic",
					"db_operation": "read",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			case "create", "save", "update", "delete", "insert":
				target := pythonTargetFromArgs(args, content)
				if target == "" {
					target = "db"
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":     "db",
					"method":       "WRITE",
					"target":       target,
					"library":      "python-db-semantic",
					"db_operation": "write",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			case "run", "popen", "call", "check_output":
				target := pythonTargetFromArgs(args, content)
				if target == "" {
					target = "unknown-command"
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "command",
					"method":   "EXEC",
					"target":   target,
					"library":  "python-process-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		}
		if fn.Kind() == "identifier" {
			name := strings.ToLower(strings.TrimSpace(fn.Utf8Text(content)))
			if name == "system" || name == "popen" {
				target := pythonTargetFromArgs(args, content)
				if target == "" {
					target = "unknown-command"
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "command",
					"method":   "EXEC",
					"target":   target,
					"library":  "python-process-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		}
	})
	return true
}

func pythonServiceTargetFromArgs(argsText string, args []*sitter.Node, content []byte, keys ...string) string {
	for _, key := range keys {
		if v := extractPythonKeywordArgValue(argsText, key); v != "" {
			return v
		}
	}
	return pythonTargetFromArgs(args, content)
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

func collectPythonHTTPClientSymbols(root *sitter.Node, content []byte) (map[string]string, map[string]string) {
	aliases := map[string]string{
		"requests": "requests",
		"httpx":    "httpx",
	}
	direct := map[string]string{}

	walkPython(root, func(n *sitter.Node) {
		switch n.Kind() {
		case "import_statement":
			text := strings.TrimSpace(n.Utf8Text(content))
			// Supports: import requests / import requests as rq / import httpx as hx
			if !strings.HasPrefix(text, "import ") {
				return
			}
			text = strings.TrimSpace(strings.TrimPrefix(text, "import "))
			if text == "requests" {
				aliases["requests"] = "requests"
				return
			}
			if strings.HasPrefix(text, "requests as ") {
				alias := strings.TrimSpace(strings.TrimPrefix(text, "requests as "))
				if alias != "" {
					aliases[alias] = "requests"
				}
			}
			if text == "httpx" {
				aliases["httpx"] = "httpx"
				return
			}
			if strings.HasPrefix(text, "httpx as ") {
				alias := strings.TrimSpace(strings.TrimPrefix(text, "httpx as "))
				if alias != "" {
					aliases[alias] = "httpx"
				}
			}
		case "import_from_statement":
			text := strings.TrimSpace(n.Utf8Text(content))
			// Supports: from requests import get / from httpx import post as p
			provider := ""
			switch {
			case strings.HasPrefix(text, "from requests import "):
				provider = "requests"
				text = strings.TrimSpace(strings.TrimPrefix(text, "from requests import "))
			case strings.HasPrefix(text, "from httpx import "):
				provider = "httpx"
				text = strings.TrimSpace(strings.TrimPrefix(text, "from httpx import "))
			default:
				return
			}
			names := text
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
				if strings.EqualFold(baseName, "request") {
					direct[aliasName] = "UNKNOWN"
					continue
				}
				switch strings.ToLower(baseName) {
				case "get", "post", "put", "patch", "delete":
					direct[aliasName] = strings.ToUpper(baseName)
				case "request":
					if provider == "httpx" || provider == "requests" {
						direct[aliasName] = "UNKNOWN"
					}
				}
			}
		}
	})
	return aliases, direct
}

func collectPythonModuleAliases(root *sitter.Node, content []byte) map[string]string {
	aliases := map[string]string{}
	walkPython(root, func(n *sitter.Node) {
		switch n.Kind() {
		case "import_statement":
			text := strings.TrimSpace(n.Utf8Text(content))
			if !strings.HasPrefix(text, "import ") {
				return
			}
			text = strings.TrimSpace(strings.TrimPrefix(text, "import "))
			for _, part := range strings.Split(text, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				base := part
				alias := part
				if strings.Contains(part, " as ") {
					p := strings.SplitN(part, " as ", 2)
					base = strings.TrimSpace(p[0])
					alias = strings.TrimSpace(p[1])
				}
				if alias == "" {
					continue
				}
				aliases[alias] = base
			}
		case "import_from_statement":
			text := strings.TrimSpace(n.Utf8Text(content))
			if !strings.HasPrefix(text, "from ") {
				return
			}
			remainder := strings.TrimSpace(strings.TrimPrefix(text, "from "))
			idx := strings.Index(remainder, " import ")
			if idx < 0 {
				return
			}
			module := strings.TrimSpace(remainder[:idx])
			items := strings.TrimSpace(remainder[idx+len(" import "):])
			for _, part := range strings.Split(items, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				base := part
				alias := part
				if strings.Contains(part, " as ") {
					p := strings.SplitN(part, " as ", 2)
					base = strings.TrimSpace(p[0])
					alias = strings.TrimSpace(p[1])
				}
				if alias == "" {
					continue
				}
				aliases[alias] = module + "." + base
			}
		}
	})
	return aliases
}

func collectPythonServiceClients(root *sitter.Node, content []byte, moduleAliases map[string]string) map[string]string {
	clients := map[string]string{}
	walkPython(root, func(n *sitter.Node) {
		if n.Kind() != "assignment" {
			return
		}
		left := n.ChildByFieldName("left")
		right := n.ChildByFieldName("right")
		if left == nil || right == nil || left.Kind() != "identifier" || right.Kind() != "call" {
			return
		}
		fn := right.ChildByFieldName("function")
		argsNode := right.ChildByFieldName("arguments")
		if fn == nil || argsNode == nil || fn.Kind() != "attribute" {
			return
		}
		obj := fn.ChildByFieldName("object")
		attr := fn.ChildByFieldName("attribute")
		if obj == nil || attr == nil {
			return
		}
		objName := strings.TrimSpace(obj.Utf8Text(content))
		module := strings.ToLower(strings.TrimSpace(moduleAliases[objName]))
		if module == "" {
			module = strings.ToLower(strings.TrimSpace(objName))
		}
		action := strings.ToLower(strings.TrimSpace(attr.Utf8Text(content)))
		if action != "client" && action != "resource" {
			return
		}
		if !(strings.Contains(module, "boto3") || strings.Contains(module, "aioboto3")) {
			return
		}
		service := normalizePythonArgument(firstPythonArg(argsNode), content)
		service = strings.ToLower(strings.TrimSpace(service))
		if service == "" {
			return
		}
		target := strings.TrimSpace(left.Utf8Text(content))
		if target == "" {
			return
		}
		clients[target] = service
	})
	return clients
}

func firstPythonArg(node *sitter.Node) *sitter.Node {
	args := pythonArgs(node)
	if len(args) == 0 {
		return nil
	}
	return args[0]
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

func collectPythonRouterSymbols(root *sitter.Node, content []byte) (map[string]bool, map[string]string) {
	names := map[string]bool{"app": true, "router": true, "api": true}
	prefixes := map[string]string{}
	walkPython(root, func(n *sitter.Node) {
		if n.Kind() != "assignment" {
			return
		}
		left := n.ChildByFieldName("left")
		right := n.ChildByFieldName("right")
		if left == nil || right == nil || left.Kind() != "identifier" || right.Kind() != "call" {
			return
		}
		name := strings.TrimSpace(left.Utf8Text(content))
		if name == "" {
			return
		}
		fn := right.ChildByFieldName("function")
		if fn == nil {
			return
		}
		fnText := strings.TrimSpace(fn.Utf8Text(content))
		if fnText == "FastAPI" || strings.HasSuffix(fnText, ".FastAPI") || fnText == "Flask" || strings.HasSuffix(fnText, ".Flask") || fnText == "APIRouter" || strings.HasSuffix(fnText, ".APIRouter") || fnText == "Blueprint" || strings.HasSuffix(fnText, ".Blueprint") {
			names[name] = true
			if args := right.ChildByFieldName("arguments"); args != nil {
				if p := extractPythonKeywordArgValue(args.Utf8Text(content), "prefix"); p != "" {
					prefixes[name] = normalizePythonPath(p)
				}
				if p := extractPythonKeywordArgValue(args.Utf8Text(content), "url_prefix"); p != "" {
					prefixes[name] = normalizePythonPath(p)
				}
			}
		}
	})
	scanPythonCallExpressions(root, func(call *sitter.Node) {
		object, method, args := parsePythonCallShape(call, content)
		if object == "" || strings.ToUpper(strings.TrimSpace(method)) != "INCLUDE_ROUTER" || len(args) == 0 {
			return
		}
		routerName := strings.TrimSpace(args[0].Utf8Text(content))
		if routerName == "" {
			return
		}
		names[routerName] = true
		if argsNode := call.ChildByFieldName("arguments"); argsNode != nil {
			if p := extractPythonKeywordArgValue(argsNode.Utf8Text(content), "prefix"); p != "" {
				prefixes[routerName] = joinPythonPaths(normalizePythonPath(p), prefixes[routerName])
			}
		}
	})
	return names, prefixes
}

func extractPythonKeywordArgValue(argsText string, key string) string {
	for _, m := range pythonKeywordArgRe.FindAllStringSubmatch(argsText, -1) {
		if len(m) < 3 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(m[1]), key) {
			return strings.TrimSpace(m[2])
		}
	}
	return ""
}

func normalizePythonPath(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	v = strings.Trim(v, "\"'")
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	if len(v) > 1 {
		v = strings.TrimRight(v, "/")
	}
	return v
}

func joinPythonPaths(base string, route string) string {
	base = normalizePythonPath(base)
	route = normalizePythonPath(route)
	switch {
	case base == "" && route == "":
		return "/"
	case base == "":
		return route
	case route == "":
		return base
	case base == "/":
		return route
	case route == "/":
		return base
	default:
		return normalizePythonPath(strings.TrimRight(base, "/") + "/" + strings.TrimLeft(route, "/"))
	}
}

func extractPythonHTTPXRequestMethod(args []*sitter.Node, content []byte) string {
	for _, a := range args {
		if a == nil {
			continue
		}
		text := strings.TrimSpace(a.Utf8Text(content))
		if m := extractPythonKeywordArgValue(text, "method"); m != "" {
			return strings.ToUpper(strings.TrimSpace(m))
		}
	}
	return "UNKNOWN"
}

func pythonTargetFromArgs(args []*sitter.Node, content []byte) string {
	if len(args) == 0 {
		return ""
	}
	// First pass: resolve known keyword arguments regardless of their order.
	for _, a := range args {
		if a == nil {
			continue
		}
		text := strings.TrimSpace(a.Utf8Text(content))
		if m := extractPythonKeywordArgValue(text, "topic"); m != "" {
			return m
		}
		if m := extractPythonKeywordArgValue(text, "queue_url"); m != "" {
			return m
		}
		if m := extractPythonKeywordArgValue(text, "QueueUrl"); m != "" {
			return m
		}
		if m := extractPythonKeywordArgValue(text, "url"); m != "" {
			return m
		}
	}
	// Second pass: fall back to the first positional/literal-like argument.
	for _, a := range args {
		if a == nil {
			continue
		}
		if a.Kind() == "keyword_argument" {
			// Skip keyword arguments in fallback mode.
			continue
		}
		v := normalizePythonArgument(a, content)
		if v != "" {
			return v
		}
	}
	return ""
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
