package gin

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/internal/frameworkutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

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

// Gin (Go)

type detector struct{}

func (d *detector) Name() string { return "gin" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "go" {
			continue
		}
		echoPrefixes := echoGroupPrefixes(idx, fa)
		for _, call := range fa.Calls {
			if echoRouteReceiver(call, echoPrefixes) {
				continue
			}
			b := ginCallToBinding(call)
			if b != nil {
				out = append(out, *b)
			}
		}
	}
	return out
}

func echoRouteReceiver(call ast.CallSite, prefixes map[string]string) bool {
	raw := call.CalleeRaw
	receiver := strings.TrimSpace(call.ReceiverRaw)
	if dot := strings.LastIndex(raw, "."); dot >= 0 && receiver == "" {
		receiver = strings.TrimSpace(raw[:dot])
	}
	if receiver == "" {
		return false
	}
	if prefixes != nil {
		if _, ok := prefixes[receiver]; ok {
			return true
		}
	}
	return strings.Contains(strings.ToLower(receiver), ".echo")
}

func ginCallToBinding(call ast.CallSite) *ast.FrameworkBinding {
	methods := map[string]string{
		"GET": "GET", "POST": "POST", "PUT": "PUT",
		"PATCH": "PATCH", "DELETE": "DELETE", "Handle": "ANY",
	}
	raw := call.CalleeRaw
	verb := raw
	if dot := strings.LastIndex(verb, "."); dot >= 0 {
		verb = verb[dot+1:]
	}
	method, ok := methods[verb]
	if !ok || len(call.Arguments) < 2 || !frameworkutil.IsLiteralPathArg(call.Arguments, 0) {
		return nil
	}
	path := frameworkutil.LiteralPathArg(call.Arguments, 0)
	return &ast.FrameworkBinding{
		Framework:        "gin",
		Kind:             "http_handler",
		Direction:        "inbound",
		Symbol:           call.Caller,
		Trigger:          method + " " + path,
		TriggerSource:    raw + "(" + path + ", ...)",
		File:             call.File,
		Range:            call.Range,
		ConfidenceReason: "go_route_literal_path_handler",
	}
}
