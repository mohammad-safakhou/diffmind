package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func symIndex(syms ...astpkg.SymbolDef) *astpkg.ProjectIndex {
	idx := &astpkg.ProjectIndex{Symbols: map[string][]astpkg.SymbolDef{}, Implements: map[string][]string{}}
	for _, s := range syms {
		idx.Symbols[s.Qualified] = append(idx.Symbols[s.Qualified], s)
	}
	return idx
}

// The AST client detector detects clients deterministically from the registry: a
// repository interface (db, fixed datasource anchor), a Feign client whose url is
// a ${placeholder} (anchor = the resolved key), and a Feign client whose url is a
// literal endpoint (harvested straight into an InstanceRef).
func TestDetectClientsRegistry(t *testing.T) {
	idx := symIndex(
		astpkg.SymbolDef{Name: "OrderRepository", Qualified: "com.app.OrderRepository", Kind: astpkg.SymbolKindInterface, File: "src/OrderRepository.java"},
		astpkg.SymbolDef{Name: "BillingClient", Qualified: "com.app.BillingClient", Kind: astpkg.SymbolKindInterface, File: "src/BillingClient.java",
			Annotations: []astpkg.Annotation{{Name: "FeignClient", Arguments: `(name = "billing", url = "${services.billing.url}")`}}},
		astpkg.SymbolDef{Name: "ScoringClient", Qualified: "com.app.ScoringClient", Kind: astpkg.SymbolKindInterface, File: "src/ScoringClient.java",
			Annotations: []astpkg.Annotation{{Name: "FeignClient", Arguments: `(url = "https://scoring.internal/api")`}}},
	)
	byName := map[string]model.ConnectionClient{}
	for _, c := range DetectClients(idx) {
		byName[c.LogicalName] = c
	}
	if r := byName["OrderRepository"]; r.Kind != "db" || r.ConfigAnchor != "spring.datasource.url" {
		t.Fatalf("repository client = %+v", r)
	}
	if b := byName["BillingClient"]; b.Kind != "http" || b.ConfigAnchor != "services.billing.url" {
		t.Fatalf("feign placeholder client = %+v", b)
	}
	if s := byName["ScoringClient"]; s.Kind != "http" || s.InstanceRef == nil || s.InstanceRef.Host != "scoring.internal" {
		t.Fatalf("feign literal client should harvest the literal endpoint, got %+v", s.InstanceRef)
	}
}

// HarvestPhysicalTables corrects an entity-class resource to the physical table
// from the class's @Table(name=...) annotation.
func TestHarvestPhysicalTables(t *testing.T) {
	idx := symIndex(
		astpkg.SymbolDef{Name: "TrafficData", Qualified: "com.app.TrafficData", Kind: astpkg.SymbolKindClass, File: "src/TrafficData.java",
			Annotations: []astpkg.Annotation{{Name: "Table", Arguments: `(name = "traffic-info")`}}},
	)
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "db_operation", Name: "write traffic-info", Details: map[string]any{"entity": "TrafficData", "operation": "write"}}},
	}
	HarvestPhysicalTables(idx, deps)
	if got := deps[0].Details["table"]; got != "traffic-info" {
		t.Fatalf("table should be harvested from @Table, got %v", got)
	}
}

// MergeClients drops a detected client that duplicates an existing one by
// config anchor, keeping genuinely new ones.
func TestMergeClientsDedupsByAnchor(t *testing.T) {
	base := []model.ConnectionClient{{LogicalName: "dataSource", Kind: "db", ConfigAnchor: "spring.datasource.url"}}
	ast := []model.ConnectionClient{
		{LogicalName: "OrderRepository", Symbol: "com.app.OrderRepository", Kind: "db", ConfigAnchor: "spring.datasource.url"},
		{LogicalName: "RedisClient", Kind: "cache", ConfigAnchor: "spring.redis.url"},
	}
	out := MergeClients(base, ast)
	if len(out) != 2 {
		t.Fatalf("expected 2 clients (dataSource + RedisClient) after anchor dedup, got %d: %+v", len(out), out)
	}
}
