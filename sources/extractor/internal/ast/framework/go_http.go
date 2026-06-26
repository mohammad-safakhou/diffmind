package framework

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func init() {
	register(&netHTTPDetector{})
	register(&echoDetector{})
}

// net/http (Go standard library)
//
// Detects mux registrations — http.HandleFunc / http.Handle and the same
// methods on a *ServeMux variable — with a literal route pattern. Go 1.22
// patterns may carry a method prefix ("GET /orders/{id}"); without one the
// route accepts any method. The handler argument names the binding's symbol so
// the connection walk starts at the handler, not at the registration site
// (registration never CALLS the handler).
type netHTTPDetector struct{}

func (d *netHTTPDetector) Name() string { return "net/http" }

func (d *netHTTPDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "go" {
			continue
		}
		for _, call := range fa.Calls {
			if b := netHTTPCallToBinding(call); b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

var goHTTPMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

func netHTTPCallToBinding(call ast.CallSite) *ast.FrameworkBinding {
	raw := call.CalleeRaw
	receiver, verb := call.ReceiverRaw, raw
	if dot := strings.LastIndex(raw, "."); dot >= 0 {
		if receiver == "" {
			receiver = raw[:dot]
		}
		verb = raw[dot+1:]
	}
	if verb != "HandleFunc" && verb != "Handle" {
		return nil
	}
	// Receiver gate: the stdlib package itself or a mux/router variable. A
	// literal pattern alone is not enough — frameworks share these verbs.
	rl := strings.ToLower(receiver)
	if rl != "http" && !strings.Contains(rl, "mux") && !strings.Contains(rl, "router") {
		return nil
	}
	if len(call.Arguments) < 2 {
		return nil
	}
	pattern := strings.Trim(strings.TrimSpace(call.Arguments[0].Source), "\"'`")
	if pattern == "" || strings.ContainsAny(pattern, "()\n") {
		return nil // expression, not a route literal ({...} wildcards are fine)
	}
	method, path := "ANY", pattern
	if sp := strings.IndexByte(pattern, ' '); sp > 0 && goHTTPMethods[strings.ToUpper(pattern[:sp])] {
		method = strings.ToUpper(pattern[:sp])
		path = strings.TrimSpace(pattern[sp+1:])
	}
	if !strings.HasPrefix(path, "/") {
		return nil // host-prefixed or non-literal pattern; not worth a guess
	}
	symbol := call.Caller
	if h := handlerIdentifierArg(call.Arguments, 1); h != "" {
		symbol = h
	}
	return &ast.FrameworkBinding{
		Framework:        "net/http",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           symbol,
		Trigger:          method + " " + path,
		TriggerSource:    raw + "(" + call.Arguments[0].Source + ", ...)",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "go_stdlib_mux_literal_pattern",
	}
}

// handlerIdentifierArg returns the handler function name when the argument is a
// plain identifier (possibly receiver-qualified, "s.handleOrders" →
// "handleOrders"). Closures and wrapped handlers return "" so the caller keeps
// the registration site as the symbol.
func handlerIdentifierArg(args []ast.ArgumentExpr, pos int) string {
	if len(args) <= pos {
		return ""
	}
	src := strings.TrimSpace(args[pos].Source)
	if src == "" || strings.ContainsAny(src, "({\" \t") {
		return ""
	}
	if dot := strings.LastIndex(src, "."); dot >= 0 {
		src = src[dot+1:]
	}
	for _, r := range src {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return src
}

// Echo (Go)
//
// Echo registers routes through direct router calls and grouped routers:
//
//	e.GET("/health", h)
//	api := e.Group(prefix + "/financial")
//	api.POST("/pay", c.Pay)
//
// The call-site AST gives us the route calls. Group prefixes are recovered from
// the source text with a conservative assignment regex, because the LHS of
// short variable declarations is not currently part of CallSite.
type echoDetector struct{}

func (d *echoDetector) Name() string { return "echo" }

func (d *echoDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "go" || !fileImportsEcho(fa) {
			continue
		}
		prefixes := echoGroupPrefixes(idx, fa)
		for _, call := range fa.Calls {
			if b := echoCallToBinding(call, prefixes); b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func fileImportsEcho(fa *ast.FileAST) bool {
	for _, imp := range fa.Imports {
		p := strings.Trim(imp.Path, `"`)
		if p == "github.com/labstack/echo/v4" || strings.HasPrefix(p, "github.com/labstack/echo/") {
			return true
		}
	}
	return false
}

func echoCallToBinding(call ast.CallSite, prefixes map[string]string) *ast.FrameworkBinding {
	raw := call.CalleeRaw
	receiver, verb := call.ReceiverRaw, raw
	if dot := strings.LastIndex(raw, "."); dot >= 0 {
		if receiver == "" {
			receiver = raw[:dot]
		}
		verb = raw[dot+1:]
	}
	methods := map[string]string{
		"GET": "GET", "POST": "POST", "PUT": "PUT", "PATCH": "PATCH",
		"DELETE": "DELETE", "HEAD": "HEAD", "OPTIONS": "OPTIONS", "Any": "ANY",
	}
	method, ok := methods[verb]
	if !ok || len(call.Arguments) < 2 || !isLiteralPathArg(call.Arguments, 0) {
		return nil
	}
	receiver = strings.TrimSpace(receiver)
	if receiver == "" {
		return nil
	}
	path := joinPath(prefixes[receiver], literalPathArg(call.Arguments, 0))
	symbol := call.Caller
	if h := handlerIdentifierArg(call.Arguments, 1); h != "" {
		symbol = h
	}
	return &ast.FrameworkBinding{
		Framework:        "echo",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           symbol,
		Trigger:          method + " " + path,
		TriggerSource:    raw + "(" + call.Arguments[0].Source + ", ...)",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "go_echo_literal_route",
	}
}

var echoGroupAssignRE = regexp.MustCompile(`(?m)\b([A-Za-z_][A-Za-z0-9_]*)\s*:=\s*[^;\n]*\.Group\s*\(([^)\n]*)\)`)

func echoGroupPrefixes(idx *ast.ProjectIndex, fa *ast.FileAST) map[string]string {
	out := map[string]string{}
	if idx == nil || fa == nil || idx.RepoRoot == "" {
		return out
	}
	b, err := os.ReadFile(filepath.Join(idx.RepoRoot, fa.Path))
	if err != nil {
		return out
	}
	for _, match := range echoGroupAssignRE.FindAllStringSubmatch(string(b), -1) {
		if len(match) != 3 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name == "" {
			continue
		}
		out[name] = stringLiteralsAsPath(match[2])
	}
	return out
}

func stringLiteralsAsPath(expr string) string {
	lits := regexp.MustCompile(`"([^"]*)"|'([^']*)'|`+"`([^`]*)`").FindAllStringSubmatch(expr, -1)
	var parts []string
	for _, lit := range lits {
		for i := 1; i < len(lit); i++ {
			if lit[i] != "" {
				parts = append(parts, lit[i])
				break
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return joinPath(parts...)
}
