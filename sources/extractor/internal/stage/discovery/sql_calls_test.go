package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

func TestParseSQLStatement(t *testing.T) {
	cases := []struct {
		sql, opKind, table string
	}{
		{"SELECT id, total FROM orders WHERE id = $1", "read", "orders"},
		{"select * from public.orders o join lines l on ...", "read", "public.orders"},
		{"WITH recent AS (...) SELECT * FROM recent", "read", "recent"},
		{"INSERT INTO order_events (id) VALUES ($1)", "write", "order_events"},
		{"UPDATE orders SET total = $1 WHERE id = $2", "write", "orders"},
		{"DELETE FROM order_events WHERE created < $1", "write", "order_events"},
		{"update `orders` set x = 1", "write", "orders"},
		{"SELECT 1", "read", ""},                  // no table
		{"select * from " + "$1", "read", ""},     // placeholder table
		{"TRUNCATE orders", "", ""},               // not a curated statement
		{"selected items from cart", "", ""},      // prose, not SQL
		{"", "", ""},
	}
	for _, c := range cases {
		op, table := parseSQLStatement(c.sql)
		if op != c.opKind || table != c.table {
			t.Errorf("parseSQLStatement(%q): want (%q,%q), got (%q,%q)", c.sql, c.opKind, c.table, op, table)
		}
	}
}

func TestDeterministicSQLOperations(t *testing.T) {
	call := func(file, callee string, args ...string) astpkg.CallSite {
		cs := astpkg.CallSite{Caller: "main.getOrder", CalleeRaw: callee, File: file,
			Range: astpkg.Range{StartLine: 9, EndLine: 9}}
		for i, a := range args {
			cs.Arguments = append(cs.Arguments, astpkg.ArgumentExpr{Index: i, Source: a})
		}
		return cs
	}
	idx := &astpkg.ProjectIndex{Files: map[string]*astpkg.FileAST{
		"main.go": {Path: "main.go", Language: "go", Calls: []astpkg.CallSite{
			call("main.go", "db.QueryContext", "ctx", `"SELECT id FROM orders WHERE id = $1"`),
			call("main.go", "db.QueryContext", "ctx", `"SELECT total FROM orders WHERE id = $1"`), // same fact
			call("main.go", "db.ExecContext", "ctx", `"INSERT INTO order_events (id) VALUES ($1)"`),
			call("main.go", "log.Printf", `"select failed: %v"`, "err"),      // not a query API
			call("main.go", "db.QueryContext", "ctx", "buildQuery(filter)"), // not a literal
		}},
	}}
	got := DeterministicSQLOperations(idx)
	if len(got) != 2 {
		t.Fatalf("want 2 facts (read orders, write order_events), got %d: %+v", len(got), got)
	}
	if got[0].Name != "read orders" || got[1].Name != "write order_events" {
		t.Errorf("got %q, %q", got[0].Name, got[1].Name)
	}
	for _, e := range got {
		if e.Confidence != 1.0 || e.Details["discovered_by"] != "ast_sql_literal" {
			t.Errorf("entity not marked deterministic: %+v", e)
		}
	}
}
