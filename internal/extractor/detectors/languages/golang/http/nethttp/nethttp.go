package nethttp

import (
	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/detectors/languages/internal/frameworkutil"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// net/http (Go standard library)
//
// Detects mux registrations — http.HandleFunc / http.Handle and the same
// methods on a *ServeMux variable — with a literal route pattern. Go 1.22
// patterns may carry a method prefix ("GET /orders/{id}"); without one the
// route accepts any method. The handler argument names the binding's symbol so
// the connection walk starts at the handler, not at the registration site
// (registration never CALLS the handler).
type detector struct{}

func (d *detector) Name() string { return "net/http" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
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
	if h := frameworkutil.HandlerIdentifierArg(call.Arguments, 1); h != "" {
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
