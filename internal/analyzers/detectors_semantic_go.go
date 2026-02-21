package analyzers

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"strings"
)

func detectGoInboundEndpointsSemantic(c *collector, file sourceFile) bool {
	fset, node, ok := parseGoAST(file)
	if !ok {
		return false
	}

	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		method := strings.ToUpper(strings.TrimSpace(sel.Sel.Name))
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
			if len(call.Args) == 0 {
				return true
			}
			path := normalizeGoExprAsPath(call.Args[0], fset)
			if path == "" {
				return true
			}
			line, col, snippet := lineColSnippet(fset, file, call.Pos())
			c.addFactWithEvidence("Endpoint", map[string]any{
				"direction": "inbound",
				"method":    method,
				"path":      path,
				"framework": "go-router-semantic",
			}, file, line, col, snippet, func() { c.report.Endpoints++ })
		case "HANDLE", "HANDLEFUNC":
			if len(call.Args) == 0 {
				return true
			}
			path := normalizeGoExprAsPath(call.Args[0], fset)
			if path == "" {
				return true
			}
			line, col, snippet := lineColSnippet(fset, file, call.Pos())
			c.addFactWithEvidence("Endpoint", map[string]any{
				"direction": "inbound",
				"method":    "ANY",
				"path":      path,
				"framework": "go-net-http-semantic",
			}, file, line, col, snippet, func() { c.report.Endpoints++ })
		case "METHODS":
			if len(call.Args) == 0 {
				return true
			}
			nested, ok := sel.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			nestedSel, ok := nested.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			nestedMethod := strings.ToUpper(strings.TrimSpace(nestedSel.Sel.Name))
			if nestedMethod != "HANDLE" && nestedMethod != "HANDLEFUNC" {
				return true
			}
			if len(nested.Args) == 0 {
				return true
			}
			path := normalizeGoExprAsPath(nested.Args[0], fset)
			if path == "" {
				return true
			}
			methods := goStringArgs(call.Args, fset)
			if len(methods) == 0 {
				methods = []string{"ANY"}
			}
			line, col, snippet := lineColSnippet(fset, file, call.Pos())
			for _, m := range methods {
				c.addFactWithEvidence("Endpoint", map[string]any{
					"direction": "inbound",
					"method":    strings.ToUpper(m),
					"path":      path,
					"framework": "gorilla-mux-semantic",
				}, file, line, col, snippet, func() { c.report.Endpoints++ })
			}
		}
		return true
	})
	return true
}

func detectGoOutboundCallsSemantic(c *collector, file sourceFile) bool {
	fset, node, ok := parseGoAST(file)
	if !ok {
		return false
	}
	httpAliases := goImportAliasesForPath(node, "net/http")

	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		name := strings.TrimSpace(sel.Sel.Name)

		if httpAliases[recv.Name] {
			switch name {
			case "Get", "Post":
				if len(call.Args) == 0 {
					return true
				}
				target := normalizeGoExprAsTarget(call.Args[0], fset)
				method := strings.ToUpper(name)
				line, col, snippet := lineColSnippet(fset, file, call.Pos())
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "http",
					"method":   method,
					"target":   target,
					"library":  "go-net-http-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			case "NewRequest":
				if len(call.Args) < 2 {
					return true
				}
				method := normalizeGoMethod(call.Args[0], fset, httpAliases)
				target := normalizeGoExprAsTarget(call.Args[1], fset)
				line, col, snippet := lineColSnippet(fset, file, call.Pos())
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "http",
					"method":   method,
					"target":   target,
					"library":  "go-net-http-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
			return true
		}

		if name == "Do" && len(call.Args) > 0 {
			line, col, snippet := lineColSnippet(fset, file, call.Pos())
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol": "http",
				"method":   "UNKNOWN",
				"target":   "request-object",
				"library":  "go-net-http-semantic",
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		}
		return true
	})
	return true
}

func detectGoQueueAndDBCallsSemantic(c *collector, file sourceFile) bool {
	fset, node, ok := parseGoAST(file)
	if !ok {
		return false
	}

	sqlAliases := goImportAliasesForPath(node, "database/sql")
	execAliases := goImportAliasesForPath(node, "os/exec")

	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method := strings.TrimSpace(sel.Sel.Name)
		methodUpper := strings.ToUpper(method)
		line, col, snippet := lineColSnippet(fset, file, call.Pos())

		switch methodUpper {
		case "WRITEMESSAGES":
			target, queueKind := goQueueTargetFromCallArgs(call.Args, fset)
			if target == "" {
				target = "kafka:unknown-topic"
			}
			if queueKind == "" {
				queueKind = "kafka"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "PUBLISH",
				"target":          target,
				"library":         "go-queue-semantic",
				"queue_operation": "publish",
				"queue_kind":      queueKind,
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		case "SENDMESSAGE":
			target, queueKind := goQueueTargetFromCallArgs(call.Args, fset)
			if target == "" {
				target = "queue:unknown"
			}
			if queueKind == "" {
				queueKind = "queue"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "PUBLISH",
				"target":          target,
				"library":         "go-queue-semantic",
				"queue_operation": "publish",
				"queue_kind":      queueKind,
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		case "CONSUME", "READMESSAGE", "RECEIVEMESSAGE":
			target, queueKind := goQueueTargetFromCallArgs(call.Args, fset)
			if target == "" {
				target = "queue:unknown"
			}
			if queueKind == "" {
				queueKind = "queue"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "CONSUME",
				"target":          target,
				"library":         "go-queue-semantic",
				"queue_operation": "consume",
				"queue_kind":      queueKind,
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		case "PUBLISH":
			target, queueKind := goQueueTargetFromCallArgs(call.Args, fset)
			if target == "" {
				target = "queue:unknown"
			}
			if queueKind == "" {
				queueKind = "queue"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "PUBLISH",
				"target":          target,
				"library":         "go-queue-semantic",
				"queue_operation": "publish",
				"queue_kind":      queueKind,
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		case "OPEN":
			if recv, ok := sel.X.(*ast.Ident); ok && sqlAliases[recv.Name] {
				target := "db:unknown"
				if len(call.Args) > 0 {
					target = normalizeGoExprAsTarget(call.Args[0], fset)
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":     "db",
					"method":       "CONNECT",
					"target":       target,
					"library":      "database/sql-semantic",
					"db_operation": "connect",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		case "QUERY", "QUERYROW", "SELECT", "FIND", "GET":
			target := goSQLTargetFromCallArgs(call.Args, fset)
			if target == "" {
				target = "db"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":     "db",
				"method":       "READ",
				"target":       target,
				"library":      "go-db-semantic",
				"db_operation": "read",
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		case "EXEC", "CREATE", "UPDATE", "DELETE", "INSERT":
			target := goSQLTargetFromCallArgs(call.Args, fset)
			if target == "" {
				target = "db"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":     "db",
				"method":       "WRITE",
				"target":       target,
				"library":      "go-db-semantic",
				"db_operation": "write",
			}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
		case "COMMAND", "COMMANDCONTEXT":
			if recv, ok := sel.X.(*ast.Ident); ok && execAliases[recv.Name] {
				target := "unknown-command"
				if len(call.Args) > 0 {
					target = normalizeGoExprAsTarget(call.Args[0], fset)
				}
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol": "command",
					"method":   "EXEC",
					"target":   target,
					"library":  "os-exec-semantic",
				}, file, line, col, snippet, func() { c.report.ExternalCalls++ })
			}
		}
		return true
	})
	return true
}

func parseGoAST(file sourceFile) (*token.FileSet, *ast.File, bool) {
	if strings.ToLower(file.Ext) != ".go" {
		return nil, nil, false
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, file.Path, file.Text, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, false
	}
	return fset, node, true
}

func goImportAliasesForPath(file *ast.File, importPath string) map[string]bool {
	out := map[string]bool{}
	for _, spec := range file.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil || p != importPath {
			continue
		}
		if spec.Name != nil && spec.Name.Name != "" && spec.Name.Name != "." && spec.Name.Name != "_" {
			out[spec.Name.Name] = true
			continue
		}
		parts := strings.Split(p, "/")
		out[parts[len(parts)-1]] = true
	}
	return out
}

func normalizeGoMethod(expr ast.Expr, fset *token.FileSet, httpAliases map[string]bool) string {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if s, err := strconv.Unquote(lit.Value); err == nil && strings.TrimSpace(s) != "" {
			return strings.ToUpper(strings.TrimSpace(s))
		}
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok && httpAliases[id.Name] {
			switch sel.Sel.Name {
			case "MethodGet":
				return "GET"
			case "MethodPost":
				return "POST"
			case "MethodPut":
				return "PUT"
			case "MethodPatch":
				return "PATCH"
			case "MethodDelete":
				return "DELETE"
			}
		}
	}
	v := strings.TrimSpace(exprString(expr, fset))
	if v == "" {
		return "UNKNOWN"
	}
	return strings.ToUpper(v)
}

func normalizeGoExprAsPath(expr ast.Expr, fset *token.FileSet) string {
	switch v := expr.(type) {
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			return normalizeGoExprAsPath(v.X, fset)
		}
	case *ast.CallExpr:
		// Common helper wrappers such as aws.String("..."), ptr("..."), etc.
		if len(v.Args) > 0 {
			first := normalizeGoExprAsPath(v.Args[0], fset)
			if first != "" && first != "unknown-target" {
				return first
			}
		}
	}
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if s, err := strconv.Unquote(lit.Value); err == nil {
			return strings.TrimSpace(s)
		}
	}
	v := strings.TrimSpace(exprString(expr, fset))
	return strings.Trim(v, "\"")
}

func normalizeGoExprAsTarget(expr ast.Expr, fset *token.FileSet) string {
	v := normalizeGoExprAsPath(expr, fset)
	if v == "" {
		return "unknown-target"
	}
	return v
}

func exprString(expr ast.Expr, fset *token.FileSet) string {
	if expr == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return ""
	}
	return buf.String()
}

func lineColSnippet(fset *token.FileSet, file sourceFile, pos token.Pos) (int, int, string) {
	p := fset.Position(pos)
	line := p.Line
	col := p.Column
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	if line <= len(file.Lines) {
		return line, col, file.Lines[line-1]
	}
	return line, col, ""
}

func goStringArgs(args []ast.Expr, fset *token.FileSet) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		v := normalizeGoExprAsPath(a, fset)
		if strings.TrimSpace(v) == "" {
			continue
		}
		out = append(out, strings.ToUpper(strings.TrimSpace(v)))
	}
	return out
}

func goTopicFromCallArgs(args []ast.Expr, fset *token.FileSet) string {
	for _, arg := range args {
		if arg == nil {
			continue
		}
		if kv, ok := arg.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok && strings.EqualFold(strings.TrimSpace(key.Name), "Topic") {
				return normalizeGoExprAsTarget(kv.Value, fset)
			}
		}
		if comp, ok := arg.(*ast.CompositeLit); ok {
			for _, el := range comp.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					key := strings.TrimSpace(exprString(kv.Key, fset))
					key = strings.TrimPrefix(key, "*")
					if strings.HasSuffix(strings.ToLower(key), ".topic") || strings.EqualFold(key, "Topic") {
						return normalizeGoExprAsTarget(kv.Value, fset)
					}
				}
			}
		}
	}
	if len(args) > 0 {
		v := normalizeGoExprAsTarget(args[0], fset)
		if strings.Contains(v, ".") || strings.HasPrefix(v, "topic") || strings.HasPrefix(v, "kafka:") {
			return v
		}
	}
	return ""
}

func goQueueTargetFromCallArgs(args []ast.Expr, fset *token.FileSet) (string, string) {
	keyTarget, keyKind := goQueueTargetFromStructuredArgs(args, fset)
	if keyTarget != "" {
		return keyTarget, keyKind
	}
	target := goTopicFromCallArgs(args, fset)
	if target == "" {
		for _, arg := range args {
			if _, ok := arg.(*ast.Ident); ok {
				continue
			}
			candidate := normalizeGoExprAsTarget(arg, fset)
			if candidate == "" || candidate == "unknown-target" {
				continue
			}
			target = candidate
			break
		}
	}
	kind := inferQueueKindFromTarget(target)
	return target, kind
}

func goQueueTargetFromStructuredArgs(args []ast.Expr, fset *token.FileSet) (string, string) {
	var visitExpr func(ast.Expr) (string, string)
	visitExpr = func(expr ast.Expr) (string, string) {
		switch n := expr.(type) {
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				return visitExpr(n.X)
			}
		case *ast.CompositeLit:
			for _, el := range n.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key := strings.ToLower(strings.TrimSpace(exprString(kv.Key, fset)))
				target := normalizeGoExprAsTarget(kv.Value, fset)
				switch {
				case strings.Contains(key, "queueurl"), strings.HasSuffix(key, "queue_url"):
					return target, "sqs"
				case strings.Contains(key, "topicarn"), strings.HasSuffix(key, "topic_arn"):
					return target, "sns"
				case strings.Contains(key, "topic"):
					return target, "kafka"
				case strings.Contains(key, "queue"):
					return target, "queue"
				}
			}
		}
		return "", ""
	}
	for _, arg := range args {
		if arg == nil {
			continue
		}
		if kv, ok := arg.(*ast.KeyValueExpr); ok {
			key := strings.ToLower(strings.TrimSpace(exprString(kv.Key, fset)))
			target := normalizeGoExprAsTarget(kv.Value, fset)
			switch {
			case strings.Contains(key, "queueurl"), strings.HasSuffix(key, "queue_url"):
				return target, "sqs"
			case strings.Contains(key, "topicarn"), strings.HasSuffix(key, "topic_arn"):
				return target, "sns"
			case strings.Contains(key, "topic"):
				return target, "kafka"
			case strings.Contains(key, "queue"):
				return target, "queue"
			}
		}
		if target, kind := visitExpr(arg); target != "" {
			return target, kind
		}
	}
	return "", ""
}

func inferQueueKindFromTarget(target string) string {
	v := strings.ToLower(strings.TrimSpace(target))
	if v == "" || v == "unknown-target" {
		return ""
	}
	switch {
	case strings.Contains(v, "arn:aws:sns"), strings.Contains(v, "sns"):
		return "sns"
	case strings.Contains(v, "sqs"), strings.Contains(v, "queueurl"):
		return "sqs"
	case strings.Contains(v, "kafka"), strings.Contains(v, "topic"):
		return "kafka"
	case strings.Contains(v, "rabbit"), strings.Contains(v, "amqp"):
		return "rabbitmq"
	default:
		return "queue"
	}
}

func goSQLTargetFromCallArgs(args []ast.Expr, fset *token.FileSet) string {
	if len(args) == 0 {
		return ""
	}
	v := normalizeGoExprAsTarget(args[0], fset)
	upper := strings.ToUpper(strings.TrimSpace(v))
	if strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "INSERT ") || strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ") {
		return v
	}
	if strings.Contains(strings.ToLower(v), "db") || strings.Contains(strings.ToLower(v), "sql") {
		return v
	}
	return ""
}
