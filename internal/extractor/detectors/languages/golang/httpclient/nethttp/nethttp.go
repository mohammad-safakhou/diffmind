package nethttp

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

type detector struct{}

func (*detector) Name() string { return "net/http-client" }

// Detect only direct package calls with literal destinations. Constructing a
// Request does not send it, and similarly named methods on unknown receivers
// are not evidence of HTTP traffic.
func (*detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	if idx == nil {
		return out
	}
	for _, file := range idx.Files {
		if file == nil || file.Language != "go" {
			continue
		}
		alias := ""
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path, `"`) == "net/http" {
				alias = imp.Alias
				if alias == "" {
					alias = "http"
				}
			}
		}
		if alias == "" || alias == "." || alias == "_" {
			continue
		}
		for _, call := range file.Calls {
			if file.LocalTypes[call.Caller+"."+alias] != "" || idx.LocalTypes[call.Caller+"."+alias] != "" {
				continue
			}
			shadowed := false
			for _, symbol := range file.Symbols {
				if symbol.Qualified == call.Caller || symbol.Name == call.Caller {
					for _, param := range symbol.Parameters {
						if param.Name == alias {
							shadowed = true
						}
					}
				}
			}
			if shadowed {
				continue
			}
			if binding := clientBinding(call, alias); binding != nil {
				out = append(out, *binding)
			}
		}
	}
	return out
}

func clientBinding(call ast.CallSite, alias string) *ast.FrameworkBinding {
	verb, ok := strings.CutPrefix(call.CalleeRaw, alias+".")
	if !ok || len(call.Arguments) == 0 {
		return nil
	}
	method := ""
	switch verb {
	case "Get":
		method = "GET"
	case "Head":
		method = "HEAD"
	case "Post", "PostForm":
		method = "POST"
	default:
		return nil
	}
	raw, err := strconv.Unquote(strings.TrimSpace(call.Arguments[0].Source))
	if err != nil {
		return nil
	}
	target, err := url.Parse(raw)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil {
		return nil
	}
	return &ast.FrameworkBinding{Framework: "net/http", Kind: "http_client", Direction: "outbound", Symbol: call.Caller,
		Trigger: method + " " + raw, TriggerSource: call.CalleeRaw + "(" + call.Arguments[0].Source + ", ...)", File: call.File, Range: call.Range,
		ConfidenceReason: "go_stdlib_http_literal_destination"}
}
