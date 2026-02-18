package analyzers

import (
	"regexp"
	"sort"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

var (
	jstsMethodOptionRe  = regexp.MustCompile(`(?i)\bmethod\s*:\s*["']([A-Za-z]+)["']`)
	jstsURLFieldRe      = regexp.MustCompile(`(?i)\burl\s*:\s*["']([^"']+)["']`)
	jstsTopicFieldRe    = regexp.MustCompile(`(?i)\btopic\s*:\s*["']([^"']+)["']`)
	jstsQueueURLFieldRe = regexp.MustCompile(`(?i)\bqueueurl\s*:\s*["']([^"']+)["']`)
	jstsQuotedStringRe  = regexp.MustCompile(`["']([^"']+)["']`)
	jstsPrismaModelRe   = regexp.MustCompile(`(?i)\bprisma\.([a-zA-Z0-9_]+)\.`)
)

func detectJSTSInboundEndpointsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJSTSAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	routerObjects, _ := collectJSTSSymbols(root, content)
	routerMounts := collectJSTSRouterMounts(root, content, routerObjects)

	scanJSTSCallExpressions(root, func(call *sitter.Node) {
		member, callName, args := parseJSTSCallShape(call, content)
		if member == "" || len(args) == 0 {
			return
		}
		method := strings.ToUpper(strings.TrimSpace(callName))
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
			if !routerObjects[member] {
				return
			}
			path := normalizeJSTSPath(normalizeJSTSArgument(args[0], content))
			if path == "" {
				return
			}
			basePaths := routerMounts[member]
			if len(basePaths) == 0 {
				basePaths = []string{""}
			}
			line, col := jstsLineCol(call)
			snippet := lineSnippet(file, line)
			for _, base := range basePaths {
				fullPath := joinJSTSPaths(base, path)
				c.addFactWithEvidence("Endpoint", map[string]any{
					"direction": "inbound",
					"method":    method,
					"path":      fullPath,
					"framework": "express-semantic",
				}, file, line, col, snippet, func() { c.report.Endpoints++ })
			}
		}
	})

	detectJSTSNestInboundEndpoints(c, file, root, content)
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
			fnName := strings.TrimSpace(fn.Utf8Text(content))
			switch fnName {
			case "fetch":
				target := normalizeJSTSArgument(args[0], content)
				if target == "" {
					target = "unknown-target"
				}
				method := parseJSTSMethodFromArgs(args, content, "UNKNOWN")
				line, col := jstsLineCol(call)
				snippet := lineSnippet(file, line)
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "http",
					"method":   method,
					"target":   target,
					"library":  "fetch-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			case "axios":
				method, target := parseJSTSAxiosIdentifierCall(args, content)
				line, col := jstsLineCol(call)
				snippet := lineSnippet(file, line)
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "http",
					"method":   method,
					"target":   target,
					"library":  "axios-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
			return
		}

		member, callName, _ := parseJSTSCallShape(call, content)
		if member == "" {
			return
		}
		method := strings.ToUpper(strings.TrimSpace(callName))
		line, col := jstsLineCol(call)
		snippet := lineSnippet(file, line)
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
			if !(member == "axios" || axiosClients[member]) {
				return
			}
			target := normalizeJSTSArgument(args[0], content)
			if target == "" {
				target = "unknown-target"
			}
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
		case "REQUEST":
			if !(member == "axios" || axiosClients[member]) {
				return
			}
			method := parseJSTSMethodFromArgs(args, content, "UNKNOWN")
			target := parseJSTSURLFromArgs(args, content)
			if target == "" {
				target = "unknown-target"
			}
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

func detectJSTSQueueAndDBCallsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJSTSAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	scanJSTSCallExpressions(root, func(call *sitter.Node) {
		text := strings.TrimSpace(call.Utf8Text(content))
		lower := strings.ToLower(text)
		line, col := jstsLineCol(call)
		snippet := lineSnippet(file, line)

		if strings.Contains(lower, ".send(") {
			if topic := firstMatchGroup(text, jstsTopicFieldRe); topic != "" {
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":        "queue",
					"method":          "PUBLISH",
					"target":          topic,
					"library":         "kafkajs-semantic",
					"queue_operation": "publish",
					"queue_kind":      "kafka",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		}
		if strings.Contains(lower, ".subscribe(") {
			if topic := firstMatchGroup(text, jstsTopicFieldRe); topic != "" {
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":        "queue",
					"method":          "CONSUME",
					"target":          topic,
					"library":         "kafkajs-semantic",
					"queue_operation": "consume",
					"queue_kind":      "kafka",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		}
		if strings.Contains(lower, ".sendtoqueue(") || strings.Contains(lower, ".publish(") {
			target := firstQuotedFromText(text)
			if target != "" {
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":        "queue",
					"method":          "PUBLISH",
					"target":          target,
					"library":         "amqplib-semantic",
					"queue_operation": "publish",
					"queue_kind":      "rabbitmq",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		}
		if strings.Contains(lower, ".consume(") {
			target := firstQuotedFromText(text)
			if target != "" {
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":        "queue",
					"method":          "CONSUME",
					"target":          target,
					"library":         "amqplib-semantic",
					"queue_operation": "consume",
					"queue_kind":      "rabbitmq",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		}
		if strings.Contains(lower, "sendmessage(") {
			target := firstMatchGroup(text, jstsQueueURLFieldRe)
			if target == "" {
				target = "sqs:unknown-queue"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "PUBLISH",
				"target":          target,
				"library":         "aws-sdk-js-sqs-semantic",
				"queue_operation": "publish",
				"queue_kind":      "sqs",
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		}
		if strings.Contains(lower, "receivemessage(") {
			target := firstMatchGroup(text, jstsQueueURLFieldRe)
			if target == "" {
				target = "sqs:unknown-queue"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "CONSUME",
				"target":          target,
				"library":         "aws-sdk-js-sqs-semantic",
				"queue_operation": "consume",
				"queue_kind":      "sqs",
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		}

		if strings.Contains(lower, "prisma.") {
			target := "prisma"
			if m := jstsPrismaModelRe.FindStringSubmatch(text); len(m) >= 2 {
				target = "prisma." + strings.TrimSpace(m[1])
			}
			switch {
			case strings.Contains(lower, ".findmany("), strings.Contains(lower, ".findfirst("), strings.Contains(lower, ".findunique("), strings.Contains(lower, ".count("), strings.Contains(lower, ".aggregate("):
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":     "db",
					"method":       "READ",
					"target":       target,
					"library":      "prisma-semantic",
					"db_operation": "read",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			case strings.Contains(lower, ".create("), strings.Contains(lower, ".createmany("), strings.Contains(lower, ".update("), strings.Contains(lower, ".updatemany("), strings.Contains(lower, ".upsert("), strings.Contains(lower, ".delete("), strings.Contains(lower, ".deletemany("):
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":     "db",
					"method":       "WRITE",
					"target":       target,
					"library":      "prisma-semantic",
					"db_operation": "write",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		}

		member, callName, args := parseJSTSCallShape(call, content)
		if member != "" {
			m := strings.ToLower(strings.TrimSpace(callName))
			if m == "exec" || m == "execsync" || m == "spawn" {
				target := "unknown-command"
				if len(args) > 0 {
					target = normalizeJSTSArgument(args[0], content)
					if target == "" {
						target = "unknown-command"
					}
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "command",
					"method":   "EXEC",
					"target":   target,
					"library":  "node-process-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
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
		"app":     true,
		"router":  true,
		"server":  true,
		"fastify": true,
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

		if fn.Kind() == "member_expression" {
			obj := fn.ChildByFieldName("object")
			prop := fn.ChildByFieldName("property")
			if obj != nil && prop != nil {
				objName := objectTokenFromMemberObject(obj, content)
				propName := strings.TrimSpace(prop.Utf8Text(content))
				if objName == "express" && propName == "Router" {
					routerObjects[name] = true
				}
				if objName == "axios" && propName == "create" {
					axiosClients[name] = true
				}
			}
		}

		if fn.Kind() == "identifier" {
			fnName := strings.TrimSpace(fn.Utf8Text(content))
			if fnName == "express" || fnName == "fastify" {
				routerObjects[name] = true
			}
		}
	})
	return routerObjects, axiosClients
}

func collectJSTSRouterMounts(root *sitter.Node, content []byte, routerObjects map[string]bool) map[string][]string {
	mounts := map[string][]string{}
	scanJSTSCallExpressions(root, func(call *sitter.Node) {
		member, callName, args := parseJSTSCallShape(call, content)
		if member == "" || strings.ToUpper(strings.TrimSpace(callName)) != "USE" || len(args) < 2 {
			return
		}
		routerName := strings.TrimSpace(args[1].Utf8Text(content))
		if !routerObjects[routerName] {
			return
		}
		base := normalizeJSTSPath(normalizeJSTSArgument(args[0], content))
		if base == "" {
			base = "/"
		}
		mounts[routerName] = append(mounts[routerName], base)
	})
	for k := range mounts {
		mounts[k] = dedupeJSTSStrings(mounts[k])
	}
	return mounts
}

func detectJSTSNestInboundEndpoints(c *collector, file sourceFile, root *sitter.Node, content []byte) {
	walkJSTS(root, func(n *sitter.Node) {
		if n == nil || n.Kind() != "class_declaration" {
			return
		}

		classDecorators := collectJSTSClassDecorators(n, content)
		basePaths := []string{""}
		for _, d := range classDecorators {
			name, arg, ok := parseJSTSDecorator(d)
			if !ok || name != "Controller" {
				continue
			}
			basePaths = []string{normalizeJSTSPath(arg)}
			if basePaths[0] == "" {
				basePaths[0] = "/"
			}
		}
		if len(basePaths) == 0 {
			basePaths = []string{"/"}
		}

		body := n.ChildByFieldName("body")
		if body == nil {
			return
		}
		pendingDecorators := make([]string, 0, 4)
		for i := uint(0); i < body.NamedChildCount(); i++ {
			ch := body.NamedChild(i)
			if ch == nil {
				continue
			}
			if ch.Kind() == "decorator" {
				pendingDecorators = append(pendingDecorators, strings.TrimSpace(ch.Utf8Text(content)))
				continue
			}
			if ch.Kind() != "method_definition" {
				pendingDecorators = pendingDecorators[:0]
				continue
			}
			methodRoutes := make([][2]string, 0, 2)
			for _, d := range pendingDecorators {
				name, arg, ok := parseJSTSDecorator(d)
				if !ok {
					continue
				}
				httpMethod := nestDecoratorMethod(name)
				if httpMethod == "" {
					continue
				}
				methodRoutes = append(methodRoutes, [2]string{httpMethod, normalizeJSTSPath(arg)})
			}
			pendingDecorators = pendingDecorators[:0]
			if len(methodRoutes) == 0 {
				continue
			}
			line, col := jstsLineCol(ch)
			snippet := lineSnippet(file, line)
			for _, bp := range basePaths {
				for _, route := range methodRoutes {
					path := joinJSTSPaths(bp, route[1])
					c.addFactWithEvidence("Endpoint", map[string]any{
						"direction": "inbound",
						"method":    route[0],
						"path":      path,
						"framework": "nestjs-semantic",
					}, file, line, col, snippet, func() { c.report.Endpoints++ })
				}
			}
		}
	})
}

func collectJSTSClassDecorators(classNode *sitter.Node, content []byte) []string {
	out := make([]string, 0, 4)
	for i := uint(0); i < classNode.NamedChildCount(); i++ {
		ch := classNode.NamedChild(i)
		if ch == nil {
			continue
		}
		if ch.Kind() == "decorator" {
			out = append(out, strings.TrimSpace(ch.Utf8Text(content)))
			continue
		}
		break
	}
	parent := classNode.Parent()
	if parent == nil || parent.Kind() != "export_statement" {
		return out
	}
	for i := uint(0); i < parent.NamedChildCount(); i++ {
		ch := parent.NamedChild(i)
		if ch == nil {
			continue
		}
		if ch == classNode {
			break
		}
		if ch.Kind() == "decorator" {
			out = append(out, strings.TrimSpace(ch.Utf8Text(content)))
		}
	}
	return out
}

func parseJSTSDecorator(text string) (name string, arg string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "@") {
		return "", "", false
	}
	name = strings.TrimPrefix(text, "@")
	if idx := strings.Index(name, "("); idx >= 0 {
		name = strings.TrimSpace(name[:idx])
	}
	if name == "" {
		return "", "", false
	}
	arg = firstQuotedFromText(text)
	return name, arg, true
}

func nestDecoratorMethod(name string) string {
	switch strings.TrimSpace(name) {
	case "Get":
		return "GET"
	case "Post":
		return "POST"
	case "Put":
		return "PUT"
	case "Patch":
		return "PATCH"
	case "Delete":
		return "DELETE"
	case "Head":
		return "HEAD"
	case "Options":
		return "OPTIONS"
	case "All":
		return "ANY"
	default:
		return ""
	}
}

func parseJSTSAxiosIdentifierCall(args []*sitter.Node, content []byte) (method string, target string) {
	method = "UNKNOWN"
	target = "unknown-target"
	if len(args) == 0 {
		return method, target
	}
	if args[0].Kind() == "object" {
		m := jstsObjectFieldString(args[0], "method", content)
		u := jstsObjectFieldString(args[0], "url", content)
		if m != "" {
			method = strings.ToUpper(m)
		}
		if u != "" {
			target = u
		}
		return method, target
	}
	target = normalizeJSTSArgument(args[0], content)
	if target == "" {
		target = "unknown-target"
	}
	if len(args) > 1 {
		if m := parseJSTSMethodFromArgs(args[1:], content, ""); m != "" {
			method = m
		}
	}
	return method, target
}

func parseJSTSMethodFromArgs(args []*sitter.Node, content []byte, fallback string) string {
	for _, a := range args {
		if a == nil {
			continue
		}
		if a.Kind() == "object" {
			if m := jstsObjectFieldString(a, "method", content); m != "" {
				return strings.ToUpper(m)
			}
		}
		raw := strings.TrimSpace(a.Utf8Text(content))
		if m := jstsMethodOptionRe.FindStringSubmatch(raw); len(m) >= 2 {
			return strings.ToUpper(strings.TrimSpace(m[1]))
		}
	}
	return fallback
}

func parseJSTSURLFromArgs(args []*sitter.Node, content []byte) string {
	for _, a := range args {
		if a == nil {
			continue
		}
		if a.Kind() == "object" {
			if u := jstsObjectFieldString(a, "url", content); u != "" {
				return u
			}
		}
		raw := strings.TrimSpace(a.Utf8Text(content))
		if m := jstsURLFieldRe.FindStringSubmatch(raw); len(m) >= 2 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func jstsObjectFieldString(node *sitter.Node, key string, content []byte) string {
	if node == nil || node.Kind() != "object" {
		return ""
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		pair := node.NamedChild(i)
		if pair == nil || pair.Kind() != "pair" {
			continue
		}
		kNode := pair.ChildByFieldName("key")
		vNode := pair.ChildByFieldName("value")
		if kNode == nil || vNode == nil {
			continue
		}
		k := strings.TrimSpace(kNode.Utf8Text(content))
		k = strings.Trim(k, "\"'")
		if !strings.EqualFold(k, key) {
			continue
		}
		return normalizeJSTSArgument(vNode, content)
	}
	return ""
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
	object = objectTokenFromMemberObject(obj, content)
	if object == "" {
		return "", "", nil
	}
	return object, strings.TrimSpace(prop.Utf8Text(content)), namedChildren(argsNode)
}

func objectTokenFromMemberObject(node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "identifier":
		return strings.TrimSpace(node.Utf8Text(content))
	case "member_expression":
		prop := node.ChildByFieldName("property")
		if prop != nil {
			return strings.TrimSpace(prop.Utf8Text(content))
		}
	}
	return ""
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

func normalizeJSTSPath(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	if len(v) > 1 {
		v = strings.TrimRight(v, "/")
	}
	return v
}

func joinJSTSPaths(base string, route string) string {
	base = normalizeJSTSPath(base)
	route = normalizeJSTSPath(route)
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
		return normalizeJSTSPath(strings.TrimRight(base, "/") + "/" + strings.TrimLeft(route, "/"))
	}
}

func firstMatchGroup(text string, re *regexp.Regexp) string {
	if re == nil {
		return ""
	}
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func firstQuotedFromText(text string) string {
	m := jstsQuotedStringRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func dedupeJSTSStrings(values []string) []string {
	set := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, exists := set[v]; exists {
			continue
		}
		set[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
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
