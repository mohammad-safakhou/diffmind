package ast_test

import (
	"context"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
	_ "github.com/mohammad-safakhou/diffmind/internal/detectors/register"
)

// TestBuildMultiLanguageIndex proves the index is genuinely multi-language:
// a single repo containing Go, Java, and Python source is parsed in full,
// idx.Languages lists every language present, and framework HTTP routes from
// BOTH Go (net/http) and Java (Spring) are detected within the one index — no
// "primary language" gate.
func TestBuildMultiLanguageIndex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
import "net/http"
func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	http.ListenAndServe(":8080", mux)
}
func health(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, dir, "OrderController.java", `package com.example;
import org.springframework.web.bind.annotation.*;

@RestController
class OrderController {
	@GetMapping("/orders")
	public String list() { return ""; }
}
`)
	writeFile(t, dir, "views.py", `def index(request):
	return None
`)

	idx, err := ast.Build(context.Background(), dir, "", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	langs := map[string]bool{}
	for _, l := range idx.Languages {
		langs[l] = true
	}
	for _, want := range []string{"go", "java", "python"} {
		if !langs[want] {
			t.Errorf("idx.Languages missing %q; got %v", want, idx.Languages)
		}
	}

	gotFW := map[string]bool{}
	for _, b := range idx.Frameworks {
		if b.Kind == "http_handler" {
			gotFW[b.Framework] = true
		}
	}
	if !gotFW["net/http"] {
		t.Errorf("missing net/http handler binding; frameworks=%+v", idx.Frameworks)
	}
	if !gotFW["spring"] {
		t.Errorf("missing spring handler binding; frameworks=%+v", idx.Frameworks)
	}
}
