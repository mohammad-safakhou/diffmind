package spring

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func cacheIdx(serviceJava string, cfg map[string]string) *ast.ProjectIndex {
	idx := &ast.ProjectIndex{
		Files:   map[string]*ast.FileAST{},
		Configs: map[string]*ast.ConfigFile{},
	}
	// Minimal hand-built FileAST with a @Cacheable method symbol.
	_ = serviceJava
	idx.Files["CacheService.java"] = &ast.FileAST{
		Language: "java",
		Symbols: []ast.SymbolDef{{
			Name:      "lookup",
			Qualified: "com.example.CacheService.lookup",
			Kind:      ast.SymbolKindMethod,
			Annotations: []ast.Annotation{
				{Name: "Cacheable", Arguments: `cacheNames = "users"`},
			},
		}},
	}
	if cfg != nil {
		cf := &ast.ConfigFile{Path: "application.yml"}
		for k, v := range cfg {
			cf.Entries = append(cf.Entries, ast.ConfigEntry{Key: k, Value: v})
		}
		idx.Configs["application.yml"] = cf
	}
	return idx
}

func cacheBindings(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, b := range (&detector{}).Detect(idx) {
		if b.Kind == "cache_operation" {
			out = append(out, b)
		}
	}
	return out
}

// With an external cache backing configured, @Cacheable -> cache_operation.
func TestCacheableWithExternalBackingEmits(t *testing.T) {
	idx := cacheIdx("", map[string]string{"spring.cache.type": "redis"})
	bs := cacheBindings(idx)
	if len(bs) != 1 {
		t.Fatalf("expected 1 cache_operation binding, got %d", len(bs))
	}
	if bs[0].Trigger != "cache: read users" {
		t.Errorf("trigger = %q, want 'cache: read users'", bs[0].Trigger)
	}
}

// Redis host config also counts as an external backing.
func TestCacheableWithRedisHostEmits(t *testing.T) {
	idx := cacheIdx("", map[string]string{"spring.redis.host": "redis.svc"})
	if len(cacheBindings(idx)) != 1 {
		t.Fatalf("redis host config should enable cache_operation")
	}
}

// Without any external backing signal, @Cacheable is NOT emitted (could be an
// in-memory cache) — precision over recall.
func TestCacheableWithoutBackingExcluded(t *testing.T) {
	if got := len(cacheBindings(cacheIdx("", nil))); got != 0 {
		t.Fatalf("expected 0 cache bindings without external backing, got %d", got)
	}
	// caffeine is in-memory -> excluded.
	if got := len(cacheBindings(cacheIdx("", map[string]string{"spring.cache.type": "caffeine"}))); got != 0 {
		t.Fatalf("caffeine (in-memory) should not enable cache_operation, got %d", got)
	}
}
