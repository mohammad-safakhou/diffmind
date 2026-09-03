package nethttp

import (
	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"testing"
)

func TestLiteralHTTPClientCalls(t *testing.T) {
	for _, tc := range []struct{ callee, argument, want string }{
		{"http.Get", `"https://catalog/products"`, "GET https://catalog/products"},
		{"http.Head", "`http://catalog/health`", "HEAD http://catalog/health"},
		{"http.Post", `"https://billing/invoices"`, "POST https://billing/invoices"},
		{"http.PostForm", `"https://billing/invoices"`, "POST https://billing/invoices"},
		{"http.NewRequest", `"GET"`, ""},
		{"other.Get", `"https://catalog/products"`, ""},
		{"http.Get", `baseURL + "/products"`, ""},
		{"http.Get", `"/products"`, ""},
		{"http.Get", `"ftp://catalog/products"`, ""},
		{"http.Get", `"https://user:secret@catalog/products"`, ""},
	} {
		t.Run(tc.callee+tc.argument, func(t *testing.T) {
			b := clientBinding(ast.CallSite{CalleeRaw: tc.callee, Arguments: []ast.ArgumentExpr{{Source: tc.argument}}, File: "main.go"}, "http")
			if tc.want == "" {
				if b != nil {
					t.Fatalf("false positive: %+v", b)
				}
				return
			}
			if b == nil || b.Trigger != tc.want {
				t.Fatalf("binding: %+v", b)
			}
		})
	}
}

func TestClientDetectorRequiresNetHTTPImportAndSupportsAlias(t *testing.T) {
	file := &ast.FileAST{Language: "go", Calls: []ast.CallSite{{CalleeRaw: "web.Get", Arguments: []ast.ArgumentExpr{{Source: `"https://catalog/products"`}}}}}
	idx := &ast.ProjectIndex{Files: map[string]*ast.FileAST{"main.go": file}}
	d := &detector{}
	if len(d.Detect(idx)) != 0 {
		t.Fatal("unimported package matched")
	}
	file.Imports = []ast.ImportDecl{{Path: "net/http", Alias: "web"}}
	if len(d.Detect(idx)) != 1 {
		t.Fatal("net/http alias did not match")
	}
	file.LocalTypes = map[string]string{".web": "Cache"}
	if len(d.Detect(idx)) != 0 {
		t.Fatal("shadowed package alias matched")
	}
}
