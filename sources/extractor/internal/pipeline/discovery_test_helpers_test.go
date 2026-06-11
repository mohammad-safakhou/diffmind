package pipeline

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func objByType(t *testing.T, typ string) objectives.Objective {
	t.Helper()
	return objectiveByType(t, typ)
}

func testSym(qualified, name, recv, file string, line uint32, anns ...string) astpkg.SymbolDef {
	a := make([]astpkg.Annotation, 0, len(anns))
	for _, n := range anns {
		a = append(a, astpkg.Annotation{Name: n})
	}
	return astpkg.SymbolDef{
		Qualified:   qualified,
		Name:        name,
		Receiver:    recv,
		File:        file,
		Range:       astpkg.Range{StartLine: line},
		Annotations: a,
	}
}

func bigIndex(modules []string, perModule int) *astpkg.ProjectIndex {
	idx := &astpkg.ProjectIndex{
		Symbols: map[string][]astpkg.SymbolDef{},
		Files:   map[string]*astpkg.FileAST{},
	}
	for _, m := range modules {
		for i := 0; i < perModule; i++ {
			file := m + "/C" + Itoa(i) + ".java"
			q := m + ".C" + Itoa(i)
			idx.Symbols[q] = []astpkg.SymbolDef{testSym(q, "C"+Itoa(i), "FooController", file, uint32(i+1), "RestController", "GetMapping")}
			idx.Files[file] = &astpkg.FileAST{Path: file}
		}
	}
	return idx
}
