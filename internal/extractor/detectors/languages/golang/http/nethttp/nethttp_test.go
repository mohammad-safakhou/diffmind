package nethttp

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

func goCall(callee string, args ...string) ast.CallSite {
	cs := ast.CallSite{Caller: "main.routes", CalleeRaw: callee, File: "main.go"}
	for i, a := range args {
		cs.Arguments = append(cs.Arguments, ast.ArgumentExpr{Index: i, Source: a})
	}
	return cs
}

func TestNetHTTPCallToBinding(t *testing.T) {
	cases := []struct {
		name    string
		call    ast.CallSite
		trigger string // "" = no binding expected
		symbol  string
	}{
		{
			name:    "go 1.22 method pattern",
			call:    goCall("mux.HandleFunc", `"GET /orders/{id}"`, "getOrder"),
			trigger: "GET /orders/{id}",
			symbol:  "getOrder",
		},
		{
			name:    "classic pattern is ANY",
			call:    goCall("http.HandleFunc", `"/healthz"`, "healthHandler"),
			trigger: "ANY /healthz",
			symbol:  "healthHandler",
		},
		{
			name:    "receiver-qualified handler keeps method name",
			call:    goCall("apiMux.HandleFunc", `"POST /orders"`, "s.createOrder"),
			trigger: "POST /orders",
			symbol:  "createOrder",
		},
		{
			name:    "closure handler falls back to registration caller",
			call:    goCall("mux.HandleFunc", `"/x"`, "func(w http.ResponseWriter, r *http.Request) {}"),
			trigger: "ANY /x",
			symbol:  "main.routes",
		},
		{
			name: "non-mux receiver rejected",
			call: goCall("server.HandleFunc", `"/x"`, "h"),
		},
		{
			name: "non-literal pattern rejected",
			call: goCall("mux.HandleFunc", "buildPath()", "h"),
		},
		{
			name: "host-prefixed pattern rejected",
			call: goCall("mux.HandleFunc", `"example.com/admin"`, "h"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := netHTTPCallToBinding(c.call)
			if c.trigger == "" {
				if b != nil {
					t.Fatalf("want no binding, got %+v", b)
				}
				return
			}
			if b == nil {
				t.Fatal("want binding, got nil")
			}
			if b.Trigger != c.trigger || b.Symbol != c.symbol {
				t.Errorf("want (%q,%q), got (%q,%q)", c.trigger, c.symbol, b.Trigger, b.Symbol)
			}
		})
	}
}
