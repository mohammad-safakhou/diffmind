package django

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

func TestDjangoURLCallToBinding(t *testing.T) {
	cases := []struct {
		name    string
		call    ast.CallSite
		trigger string
		symbol  string
	}{
		{
			name:    "function view",
			call:    goCall("path", `"orders/"`, "views.order_list"),
			trigger: "ANY /orders",
			symbol:  "views.order_list",
		},
		{
			name:    "class-based view strips as_view",
			call:    goCall("path", `"orders/<int:pk>/"`, "OrderDetail.as_view"),
			trigger: "ANY /orders/<int:pk>",
			symbol:  "OrderDetail",
		},
		{
			name:    "re_path raw string",
			call:    goCall("re_path", `r"^reports/$"`, "views.reports"),
			trigger: "ANY /reports",
			symbol:  "views.reports",
		},
		{
			name: "regex group rejected",
			call: goCall("re_path", `r"^orders/(?P<pk>\d+)/$"`, "views.detail"),
		},
		{
			name: "non-route call rejected",
			call: goCall("os.path", `"orders/"`, "x"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := djangoURLCallToBinding(c.call)
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
