package fiber

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/internal/frameworkutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// Fiber (Go)
//
// Fiber registers direct and grouped routes through title-case verbs:
//
//	app.Get("/health", handler)
//	r := dep.Router.Use(auth)
//	r.Post("/metadata", c.get)
//
// Controllers often receive a fiber.Router through a shared dependency type, so
// individual route files do not always import Fiber directly. The detector is
// therefore project-gated on any Fiber import, then keeps the call match itself
// literal-path only.
type detector struct{}

func (d *detector) Name() string { return "fiber" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	if !projectImportsFiber(idx) {
		return out
	}
	for _, fa := range idx.Files {
		if fa.Language != "go" {
			continue
		}
		prefixes := fiberGroupPrefixes(idx, fa)
		for _, call := range fa.Calls {
			if b := fiberCallToBinding(call, prefixes); b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func projectImportsFiber(idx *ast.ProjectIndex) bool {
	if idx == nil {
		return false
	}
	for _, fa := range idx.Files {
		if fileImportsFiber(fa) {
			return true
		}
	}
	return false
}

func fileImportsFiber(fa *ast.FileAST) bool {
	if fa == nil {
		return false
	}
	for _, imp := range fa.Imports {
		p := strings.Trim(imp.Path, `"`)
		if p == "github.com/gofiber/fiber/v2" || strings.HasPrefix(p, "github.com/gofiber/fiber/") {
			return true
		}
	}
	return false
}

func fiberCallToBinding(call ast.CallSite, prefixes map[string]string) *ast.FrameworkBinding {
	raw := call.CalleeRaw
	receiver, verb := call.ReceiverRaw, raw
	if dot := strings.LastIndex(raw, "."); dot >= 0 {
		if receiver == "" {
			receiver = raw[:dot]
		}
		verb = raw[dot+1:]
	}
	methods := map[string]string{
		"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH",
		"Delete": "DELETE", "Head": "HEAD", "Options": "OPTIONS", "All": "ANY",
	}
	pathPos, handlerPos := 0, 1
	method, ok := methods[verb]
	if !ok && verb == "Add" {
		if len(call.Arguments) < 3 {
			return nil
		}
		method = goHTTPMethodFromExpr(call.Arguments[0].Source)
		pathPos, handlerPos = 1, 2
		ok = method != ""
	}
	if !ok || len(call.Arguments) <= handlerPos || !frameworkutil.IsLiteralPathArg(call.Arguments, pathPos) {
		return nil
	}
	receiver = strings.TrimSpace(receiver)
	if receiver == "" || strings.Contains(receiver, "client") {
		return nil
	}
	path := frameworkutil.JoinPath(prefixes[receiver], frameworkutil.LiteralPathArg(call.Arguments, pathPos))
	symbol := call.Caller
	if h := frameworkutil.HandlerIdentifierArg(call.Arguments, handlerPos); h != "" {
		symbol = h
	}
	return &ast.FrameworkBinding{
		Framework:        "fiber",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           symbol,
		Trigger:          method + " " + path,
		TriggerSource:    raw + "(" + call.Arguments[pathPos].Source + ", ...)",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "go_fiber_literal_route",
	}
}

var fiberGroupAssignRE = regexp.MustCompile(`(?m)\b([A-Za-z_][A-Za-z0-9_]*)\s*:=\s*[^;\n]*\.Group\s*\(([^)\n]*)\)`)

func fiberGroupPrefixes(idx *ast.ProjectIndex, fa *ast.FileAST) map[string]string {
	out := map[string]string{}
	if idx == nil || fa == nil || idx.RepoRoot == "" {
		return out
	}
	b, err := os.ReadFile(filepath.Join(idx.RepoRoot, fa.Path))
	if err != nil {
		return out
	}
	for _, match := range fiberGroupAssignRE.FindAllStringSubmatch(string(b), -1) {
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

func goHTTPMethodFromExpr(src string) string {
	src = strings.Trim(strings.TrimSpace(src), `"'`+"`")
	if src == "" {
		return ""
	}
	switch strings.ToLower(src) {
	case "get", "http.methodget", "fiber.methodget":
		return "GET"
	case "post", "http.methodpost", "fiber.methodpost":
		return "POST"
	case "put", "http.methodput", "fiber.methodput":
		return "PUT"
	case "patch", "http.methodpatch", "fiber.methodpatch":
		return "PATCH"
	case "delete", "http.methoddelete", "fiber.methoddelete":
		return "DELETE"
	case "head", "http.methodhead", "fiber.methodhead":
		return "HEAD"
	case "options", "http.methodoptions", "fiber.methodoptions":
		return "OPTIONS"
	default:
		return ""
	}
}
