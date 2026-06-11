package pipeline

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	connectionstage "github.com/mohammad-safakhou/diffmind/internal/stage/connections"
)

// ── D1: junk-table / generic-owner denylist ─────────────────────────────────

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
		if got := connectionstage.IsLowSignalRepositoryOwner(in); got != want {
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
		if got := connectionstage.IsJunkTableName(in); got != want {
			t.Errorf("isJunkTableName(%q)=%v want %v", in, got, want)
		}
	}
}

func TestIsRepositoryOperationSymbolRejectsEntityManager(t *testing.T) {
	if connectionstage.IsRepositoryOperationSymbol("EntityManager.persist") {
		t.Error("EntityManager.persist must not be a repository operation")
	}
	if !connectionstage.IsRepositoryOperationSymbol("OrderRepository.save") {
		t.Error("OrderRepository.save should be a repository operation")
	}
}

// ── D3: operation-kind inference ────────────────────────────────────────────

func TestInferDBOperationKindAST(t *testing.T) {
	// Build a tiny index with an annotated repository method.
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
		"OrderRepository.bulkClose":  "write", // @Modifying
		"OrderRepository.reportRows": "read",  // select @Query
		"OrderRepository.findByName": "read",  // name prefix
		"OrderRepository.save":       "write", // name prefix
		"OrderRepository.loadAll":    "read",  // finder fallback (load*)
		"OrderRepository.frobnicate": "unknown",
	}
	for sym, want := range cases {
		if got := connectionstage.InferDBOperationKind(idx, sym); got != want {
			t.Errorf("inferDBOperationKindAST(%q)=%q want %q", sym, got, want)
		}
	}
}

// ── D2: config placeholder resolution ───────────────────────────────────────

func configIndex(entries map[string]string) *astpkg.ProjectIndex {
	var ce []astpkg.ConfigEntry
	for k, v := range entries {
		ce = append(ce, astpkg.ConfigEntry{Key: k, Value: v})
	}
	return &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": {Path: "application.yml", Format: "yaml", Entries: ce},
	}}
}

func TestResolveResourceName(t *testing.T) {
	t.Run("non-placeholder passes through", func(t *testing.T) {
		if got := ResolveResourceName(nil, "orders-created"); got != "orders-created" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("key-segment fallback when value unresolved", func(t *testing.T) {
		// The exact run defect: ${...sqs.catalogue-target-response-sqs.url} with
		// no config value should still yield the queue name.
		got := ResolveResourceName(configIndex(nil), "${services.aws.sqs.catalogue-target-response-sqs.url}")
		if got != "catalogue-target-response-sqs" {
			t.Errorf("key-segment fallback got %q", got)
		}
	})
	t.Run("resolves URL value to trailing segment", func(t *testing.T) {
		idx := configIndex(map[string]string{"app.queue.url": "https://sqs.eu-central-1.amazonaws.com/123456789012/my-real-queue"})
		if got := ResolveResourceName(idx, "${app.queue.url}"); got != "my-real-queue" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("resolves ARN value to trailing segment", func(t *testing.T) {
		idx := configIndex(map[string]string{"app.q": "arn:aws:sqs:eu-central-1:123456789012:campaign-changes.fifo"})
		if got := ResolveResourceName(idx, "${app.q}"); got != "campaign-changes.fifo" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("default value when key absent", func(t *testing.T) {
		if got := ResolveResourceName(configIndex(nil), "${missing.key:fallback-queue}"); got != "fallback-queue" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("one level of indirection", func(t *testing.T) {
		idx := configIndex(map[string]string{"a": "${b}", "b": "inner-queue"})
		if got := ResolveResourceName(idx, "${a}"); got != "inner-queue" {
			t.Errorf("got %q", got)
		}
	})
}

func TestEntityFromFrameworkBindingResolvesQueuePlaceholder(t *testing.T) {
	idx := configIndex(nil)
	obj := objectiveByType(t, "queue_consumer")
	e, ok := entityFromFrameworkBinding(idx, obj, astpkg.FrameworkBinding{
		Framework: "spring", Kind: "queue_consumer",
		Symbol:  "com.example.Listener.onMessage",
		Trigger: "sqs: ${services.aws.sqs.catalogue-target-response-sqs.url}",
		File:    "src/main/java/com/example/Listener.java",
		Range:   astpkg.Range{StartLine: 10, EndLine: 10},
	})
	if !ok {
		t.Fatal("expected entity")
	}
	if e.Name != "catalogue-target-response-sqs" || e.Details["queue"] != "catalogue-target-response-sqs" {
		t.Fatalf("placeholder not resolved: name=%q queue=%v", e.Name, e.Details["queue"])
	}
	if e.Details["platform"] != "sqs" {
		t.Fatalf("platform=%v", e.Details["platform"])
	}
}
