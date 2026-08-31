package echo

import (
	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/internal/frameworkutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

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
type detector struct{}

func (d *detector) Name() string { return "echo" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "go" {
			continue
		}
		prefixes := echoGroupPrefixes(idx, fa)
		if !fileImportsEcho(fa) && len(prefixes) == 0 {
			continue
		}
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
	if !ok || len(call.Arguments) < 2 || !frameworkutil.IsLiteralPathArg(call.Arguments, 0) {
		return nil
	}
	receiver = strings.TrimSpace(receiver)
	if receiver == "" {
		return nil
	}
	path := frameworkutil.JoinPath(prefixes[receiver], frameworkutil.LiteralPathArg(call.Arguments, 0))
	symbol := call.Caller
	if h := frameworkutil.HandlerIdentifierArg(call.Arguments, 1); h != "" {
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
	return frameworkutil.JoinPath(parts...)
}
