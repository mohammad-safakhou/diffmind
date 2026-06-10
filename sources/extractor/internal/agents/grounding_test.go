package agents

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func objByType(t *testing.T, typ string) objectives.Objective {
	t.Helper()
	for _, o := range objectives.Default() {
		if o.Type == typ {
			return o
		}
	}
	t.Fatalf("no objective with type %q", typ)
	return objectives.Objective{}
}

func sym(qualified, name, recv, file string, line uint32, anns ...string) astpkg.SymbolDef {
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

func fixtureIndex() *astpkg.ProjectIndex {
	return &astpkg.ProjectIndex{
		Symbols: map[string][]astpkg.SymbolDef{
			"OrderController.create": {sym("OrderController.create", "create", "OrderController", "src/api/OrderController.java", 34, "RestController", "PostMapping")},
			"OrderController.get":    {sym("OrderController.get", "get", "OrderController", "src/api/OrderController.java", 50, "GetMapping")},
			"OrderRepository.save":   {sym("OrderRepository.save", "save", "OrderRepository", "src/db/OrderRepository.java", 12, "Repository")},
			"PlainHelper.format":     {sym("PlainHelper.format", "format", "PlainHelper", "src/util/PlainHelper.java", 5)},
			"OtherModule.handler":    {sym("OtherModule.handler", "handler", "OtherController", "other/api/OtherController.java", 9, "GetMapping")},
		},
		Frameworks: []astpkg.FrameworkBinding{
			{Framework: "spring", Kind: "scheduled", Symbol: "ReportJob.run", Trigger: "@Scheduled", File: "src/jobs/ReportJob.java", Range: astpkg.Range{StartLine: 20}},
			{Framework: "spring", Kind: "http_route", Symbol: "OrderController.create", Trigger: "@PostMapping", File: "src/api/OrderController.java", Range: astpkg.Range{StartLine: 34}},
		},
		Configs: map[string]*astpkg.ConfigFile{
			"application.yml": {Path: "application.yml", Entries: []astpkg.ConfigEntry{
				{Key: "spring.datasource.url", Value: "jdbc:postgresql://db/orders"},
				{Key: "server.port", Value: "8080"},
			}},
		},
	}
}

func TestBuildObjectiveHints_HTTPRoute(t *testing.T) {
	idx := fixtureIndex()
	h := buildObjectiveHints(idx, objByType(t, "http_route"), "", nil)

	gotFiles := map[string]bool{}
	for _, s := range h.Symbols {
		gotFiles[s.Qualified] = true
	}
	// Controllers must be selected.
	for _, want := range []string{"OrderController.create", "OrderController.get", "OtherModule.handler"} {
		if !gotFiles[want] {
			t.Errorf("http_route hints missing %s; got %+v", want, h.Symbols)
		}
	}
	// Repository and plain helper must NOT be selected for http_route.
	if gotFiles["OrderRepository.save"] || gotFiles["PlainHelper.format"] {
		t.Errorf("http_route hints wrongly included a non-route symbol: %+v", h.Symbols)
	}
	// The http_route framework binding should appear; the scheduled one should not.
	if len(h.Bindings) != 1 || h.Bindings[0].Kind != "http_route" {
		t.Fatalf("expected 1 http_route binding, got %+v", h.Bindings)
	}
}

func TestBuildObjectiveHints_DBOperation(t *testing.T) {
	idx := fixtureIndex()
	h := buildObjectiveHints(idx, objByType(t, "db_operation"), "", nil)
	found := false
	for _, s := range h.Symbols {
		if s.Qualified == "OrderRepository.save" {
			found = true
		}
		if s.Qualified == "OrderController.create" {
			t.Errorf("db_operation hints wrongly included a controller: %+v", h.Symbols)
		}
	}
	if !found {
		t.Fatalf("db_operation hints missing OrderRepository.save: %+v", h.Symbols)
	}
	// The datasource config entry must be surfaced; server.port must not.
	if len(h.Configs) != 1 || h.Configs[0].Key != "spring.datasource.url" {
		t.Fatalf("db_operation config hints = %+v", h.Configs)
	}
}

func TestBuildObjectiveHints_ScheduledBinding(t *testing.T) {
	idx := fixtureIndex()
	h := buildObjectiveHints(idx, objByType(t, "scheduled_job"), "", nil)
	if len(h.Bindings) != 1 || h.Bindings[0].Kind != "scheduled" {
		t.Fatalf("expected the scheduled binding, got %+v", h.Bindings)
	}
}

func TestBuildObjectiveHints_SubDirFilter(t *testing.T) {
	idx := fixtureIndex()
	h := buildObjectiveHints(idx, objByType(t, "http_route"), "src", nil)
	for _, s := range h.Symbols {
		if s.Qualified == "OtherModule.handler" {
			t.Fatalf("subDir=src should exclude other/api/...: %+v", h.Symbols)
		}
	}
	// Bindings outside src must also be excluded (both fixture bindings are under src/, so still present).
	if len(h.Symbols) == 0 {
		t.Fatal("expected some in-scope symbols")
	}
}

func TestBuildObjectiveHints_FileScope(t *testing.T) {
	idx := fixtureIndex()
	h := buildObjectiveHints(idx, objByType(t, "http_route"), "", []string{"other/"})
	for _, s := range h.Symbols {
		if s.File != "" && s.File[:6] != "other/" {
			t.Fatalf("fileScope=other/ leaked %s", s.File)
		}
	}
	if len(h.Symbols) != 1 || h.Symbols[0].Qualified != "OtherModule.handler" {
		t.Fatalf("fileScope hints = %+v", h.Symbols)
	}
}

func TestBuildObjectiveHints_NilIndexAndUnknownType(t *testing.T) {
	if h := buildObjectiveHints(nil, objByType(t, "http_route"), "", nil); !h.Empty() {
		t.Fatalf("nil index should yield empty hints, got %+v", h)
	}
	// Unknown objective type → no matcher → empty.
	if h := buildObjectiveHints(fixtureIndex(), objectives.Objective{Type: "totally_unknown"}, "", nil); !h.Empty() {
		t.Fatalf("unknown type should yield empty hints, got %+v", h)
	}
}

func TestBuildObjectiveHints_CapAndTruncate(t *testing.T) {
	idx := &astpkg.ProjectIndex{Symbols: map[string][]astpkg.SymbolDef{}}
	for i := 0; i < maxSymbolHints+25; i++ {
		q := "Ctl.m" + Itoa(i)
		idx.Symbols[q] = []astpkg.SymbolDef{sym(q, "m"+Itoa(i), "OrderController", "src/api/C"+Itoa(i)+".java", uint32(i), "GetMapping")}
	}
	h := buildObjectiveHints(idx, objByType(t, "http_route"), "", nil)
	if len(h.Symbols) != maxSymbolHints {
		t.Fatalf("expected cap at %d, got %d", maxSymbolHints, len(h.Symbols))
	}
	if !h.Truncated {
		t.Fatal("Truncated should be true when capped")
	}
}

func TestBuildObjectiveHints_Deterministic(t *testing.T) {
	idx := fixtureIndex()
	a := buildObjectiveHints(idx, objByType(t, "http_route"), "", nil)
	b := buildObjectiveHints(idx, objByType(t, "http_route"), "", nil)
	if len(a.Symbols) != len(b.Symbols) {
		t.Fatalf("non-deterministic length")
	}
	for i := range a.Symbols {
		if a.Symbols[i].Qualified != b.Symbols[i].Qualified || a.Symbols[i].File != b.Symbols[i].File {
			t.Fatalf("non-deterministic order at %d: %v vs %v", i, a.Symbols[i], b.Symbols[i])
		}
	}
}
