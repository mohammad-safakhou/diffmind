package connections

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

func TestIsLowSignalRepositoryOwner(t *testing.T) {
	cases := map[string]bool{
		"EntityManager":               true,
		"com.example.EntityManager":   true,
		"SessionFactory":              true,
		"jdbcTemplate":                true,
		"OrderRepository":             false,
		"com.example.OrderRepository": false,
		"UserDao":                     false,
	}
	for in, want := range cases {
		if got := IsLowSignalRepositoryOwner(in); got != want {
			t.Errorf("isLowSignalRepositoryOwner(%q)=%v want %v", in, got, want)
		}
	}
}

func TestIsJunkTableName(t *testing.T) {
	cases := map[string]bool{
		"entity_manager":        true,
		"order_id_seq":          true,
		"campaign_seq":          true,
		"foo_sequence":          true,
		"":                      true,
		"order":                 false,
		"traffic_configuration": false,
		"account_assignments":   false,
	}
	for in, want := range cases {
		if got := IsJunkTableName(in); got != want {
			t.Errorf("isJunkTableName(%q)=%v want %v", in, got, want)
		}
	}
}

func TestIsRepositoryOperationSymbolRejectsEntityManager(t *testing.T) {
	if IsRepositoryOperationSymbol("EntityManager.persist") {
		t.Error("EntityManager.persist must not be a repository operation")
	}
	if !IsRepositoryOperationSymbol("OrderRepository.save") {
		t.Error("OrderRepository.save should be a repository operation")
	}
}

func TestInferDBOperationKindAST(t *testing.T) {
	idx := &astpkg.ProjectIndex{Symbols: map[string][]astpkg.SymbolDef{
		"OrderRepository.bulkClose": {{
			Name: "bulkClose", Qualified: "OrderRepository.bulkClose", Kind: astpkg.SymbolKindMethod,
			Annotations: []astpkg.Annotation{{Name: "Modifying"}, {Name: "Query", Arguments: "\"update orders set ...\""}},
		}},
		"OrderRepository.reportRows": {{
			Name: "reportRows", Qualified: "OrderRepository.reportRows", Kind: astpkg.SymbolKindMethod,
			Annotations: []astpkg.Annotation{{Name: "Query", Arguments: "\"select * from orders\""}},
		}},
	}}
	cases := map[string]string{
		"OrderRepository.bulkClose":  "write",
		"OrderRepository.reportRows": "read",
		"OrderRepository.findByName": "read",
		"OrderRepository.save":       "write",
		"OrderRepository.loadAll":    "read",
		"OrderRepository.frobnicate": "unknown",
	}
	for sym, want := range cases {
		if got := InferDBOperationKind(idx, sym); got != want {
			t.Errorf("inferDBOperationKindAST(%q)=%q want %q", sym, got, want)
		}
	}
}
