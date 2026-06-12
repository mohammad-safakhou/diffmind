package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

func ormIdx(lang, file string, calls ...astpkg.CallSite) *astpkg.ProjectIndex {
	for i := range calls {
		calls[i].File = file
		calls[i].Range = astpkg.Range{StartLine: uint32(10 + i)}
	}
	return &astpkg.ProjectIndex{
		Files: map[string]*astpkg.FileAST{file: {Path: file, Language: lang, Calls: calls}},
	}
}

func ormCall(receiver, callee string, args ...string) astpkg.CallSite {
	cs := astpkg.CallSite{Caller: "caller", ReceiverRaw: receiver, CalleeRaw: receiver + "." + callee}
	for i, a := range args {
		cs.Arguments = append(cs.Arguments, astpkg.ArgumentExpr{Index: i, Source: a})
	}
	return cs
}

func factNames(out []llmEntity) []string {
	names := make([]string, 0, len(out))
	for _, e := range out {
		names = append(names, e.Name)
	}
	return names
}

func TestDeterministicORMOperationsGORM(t *testing.T) {
	idx := ormIdx("go", "store.go",
		ormCall("s.db", "Create", "&Order{Total: t}"),
		ormCall("db", "Find", "&[]OrderItem{}"),
		ormCall("tx", "Delete", "&pkg.Order{}"),
		ormCall("db", "First", "&invoice", "id"), // local var, resolved via LocalTypes
		ormCall("db", "Find", "&found"),          // local var WITHOUT a recorded type -> skipped
		ormCall("svc", "Create", "&Order{}"),     // non-db receiver -> skipped
		ormCall("db", "Where", `"total > ?"`),    // not a terminal op -> skipped
	)
	idx.LocalTypes = map[string]string{"caller.invoice": "Invoice"}
	got := factNames(DeterministicORMOperations(idx))
	want := []string{"read invoices", "read order_items", "write orders"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestDeterministicORMOperationsDjango(t *testing.T) {
	idx := ormIdx("python", "views.py",
		ormCall("Order.objects", "filter", "status=status"),
		ormCall("models.Order.objects", "create", "total=total"),
		ormCall("queryset.objects", "filter"), // lowercase model -> skipped
		ormCall("Order", "filter"),            // no .objects -> skipped
	)
	got := factNames(DeterministicORMOperations(idx))
	want := []string{"read order", "write order"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestDeterministicORMOperationsPrismaAndSequelize(t *testing.T) {
	idx := ormIdx("typescript", "service.ts",
		ormCall("prisma.order", "findMany"),
		ormCall("this.prisma.userAccount", "create", "{ data }"),
		ormCall("client.order", "findMany"), // owner is not a prisma client -> skipped
		// Sequelize: findByPk corroborates the Invoice receiver, so its
		// generic create counts too; Widget only uses generic verbs -> skipped.
		ormCall("Invoice", "findByPk", "id"),
		ormCall("Invoice", "create", "{ total }"),
		ormCall("Widget", "create", "{ name }"),
	)
	got := factNames(DeterministicORMOperations(idx))
	// Sorted by (table, op-kind) key: invoices/read, invoices/write, order/read, user_account/write.
	want := []string{"read invoices", "write invoices", "read order", "write user_account"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("want %v, got %v", want, got)
			break
		}
	}
}

func TestDeterministicORMOperationsActiveRecord(t *testing.T) {
	idx := ormIdx("ruby", "app/models/job.rb",
		ormCall("Order", "find_by", "id: id"), // distinctive -> trusts Order
		ormCall("Order", "create", "attrs"),
		ormCall("Helper", "create", "attrs"), // never corroborated -> skipped
	)
	got := factNames(DeterministicORMOperations(idx))
	want := []string{"read orders", "write orders"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestORMNamingHelpers(t *testing.T) {
	cases := []struct{ in, snake, plural string }{
		{"Order", "order", "orders"},
		{"OrderItem", "order_item", "order_items"},
		{"Category", "category", "categories"},
		{"Box", "box", "boxes"},
		{"Status", "status", "statuses"},
	}
	for _, c := range cases {
		if got := snakeCase(c.in); got != c.snake {
			t.Errorf("snakeCase(%q): want %q, got %q", c.in, c.snake, got)
		}
		if got := pluralizeSnake(c.snake); got != c.plural {
			t.Errorf("pluralizeSnake(%q): want %q, got %q", c.snake, c.plural, got)
		}
	}
}
