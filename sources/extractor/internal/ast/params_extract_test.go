package ast_test

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

// TestJavaParamsExtracted verifies handler formal parameters (name, type, and
// parameter-level annotations) are captured — the raw material for the
// deterministic IO-contract backfill.
func TestJavaParamsExtracted(t *testing.T) {
	src := `
package com.example;
import org.springframework.web.bind.annotation.*;

@RestController
class OrderController {
    @GetMapping("/orders/{id}")
    public Order get(@PathVariable("id") Long id, @RequestParam(required = false) String filter) {
        return null;
    }

    @PostMapping("/orders")
    public Order create(@RequestBody OrderRequest body, javax.servlet.http.HttpServletRequest req) {
        return null;
    }
}`
	fa := parseInline(t, src, "java", ".java")
	byName := map[string]ast.SymbolDef{}
	for _, s := range fa.Symbols {
		byName[s.Name] = s
	}

	get, ok := byName["get"]
	if !ok {
		t.Fatal("method get not found")
	}
	if len(get.Parameters) != 2 {
		t.Fatalf("get params = %d, want 2: %+v", len(get.Parameters), get.Parameters)
	}
	p0 := get.Parameters[0]
	if p0.Name != "id" || p0.Type != "Long" {
		t.Errorf("get param0 = %+v, want name=id type=Long", p0)
	}
	if len(p0.Annotations) == 0 || p0.Annotations[0].Name != "PathVariable" {
		t.Errorf("get param0 annotations = %+v, want PathVariable", p0.Annotations)
	}
	p1 := get.Parameters[1]
	if p1.Name != "filter" || len(p1.Annotations) == 0 || p1.Annotations[0].Name != "RequestParam" {
		t.Errorf("get param1 = %+v, want filter/@RequestParam", p1)
	}

	create, ok := byName["create"]
	if !ok {
		t.Fatal("method create not found")
	}
	if len(create.Parameters) != 2 {
		t.Fatalf("create params = %d, want 2", len(create.Parameters))
	}
	if create.Parameters[0].Type != "OrderRequest" || create.Parameters[0].Annotations[0].Name != "RequestBody" {
		t.Errorf("create param0 = %+v, want OrderRequest/@RequestBody", create.Parameters[0])
	}
}
