package spring

import (
	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/detectors/languages/internal/frameworkutil"
	"strings"
)

func init() { ast.RegisterFrameworkDetector(&detector{}) }

// detector implements ast.FrameworkDetector for Spring Framework HTTP and
// Spring-owned activation/cache/listener annotations.
type detector struct{}

func (d *detector) Name() string { return "spring" }

func (d *detector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "java" && fa.Language != "kotlin" {
			continue
		}
		classes := frameworkutil.ClassesByName(fa)
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			cls := frameworkutil.EnclosingClassForSymbol(fa, sym, classes)
			for _, ann := range sym.Annotations {
				bindings := springAnnotationToBindings(fa, sym, cls, ann)
				if len(bindings) > 0 {
					out = append(out, bindings...)
				}
			}
		}
	}
	// @Cacheable/@CachePut/@CacheEvict only count as external cache_operations
	// when the repo configures an external cache backing (Redis/Hazelcast/…).
	// Without that signal the annotation may be an in-memory cache, so we drop
	// the cache bindings until a detector can ground them (precision over recall).
	if !springHasExternalCache(idx) {
		out = dropBindingsOfKind(out, "cache_operation")
	}
	return out
}

func dropBindingsOfKind(in []ast.FrameworkBinding, kind string) []ast.FrameworkBinding {
	out := in[:0]
	for _, b := range in {
		if b.Kind == kind {
			continue
		}
		out = append(out, b)
	}
	return out
}

// firstCacheName returns the first cache name from a @Cacheable/@CachePut/
// @CacheEvict annotation (cacheNames= / value= / positional / array).
func firstCacheName(args string) string {
	if v := frameworkutil.NamedOrPositionalValues(args, "cacheNames", "value"); len(v) > 0 {
		return v[0]
	}
	return ""
}

// springHasExternalCache reports whether the repo configures an external cache
// manager (Redis/Hazelcast/Infinispan/Memcached/JCache/Couchbase). Used to gate
// cache_operation bindings so in-memory caches (caffeine/simple) are excluded.
func springHasExternalCache(idx *ast.ProjectIndex) bool {
	if idx == nil {
		return false
	}
	external := map[string]bool{"redis": true, "hazelcast": true, "infinispan": true, "memcached": true, "jcache": true, "couchbase": true}
	for _, cf := range idx.Configs {
		for _, e := range cf.Entries {
			k := strings.ToLower(e.Key)
			v := strings.ToLower(strings.TrimSpace(e.Value))
			if strings.Contains(k, "spring.cache.type") && external[v] {
				return true
			}
			if strings.Contains(k, "spring.redis") || strings.Contains(k, "spring.data.redis") || strings.Contains(k, "redis.host") {
				return true
			}
		}
	}
	return false
}

func springAnnotationToBindings(fa *ast.FileAST, sym ast.SymbolDef, cls *ast.SymbolDef, ann ast.Annotation) []ast.FrameworkBinding {
	name := ann.Name
	args := ann.Arguments

	// HTTP route mappings.
	httpMethods := map[string]string{
		"GetMapping":     "GET",
		"PostMapping":    "POST",
		"PutMapping":     "PUT",
		"PatchMapping":   "PATCH",
		"DeleteMapping":  "DELETE",
		"RequestMapping": "ANY",
	}
	if method, ok := httpMethods[name]; ok {
		return springHTTPBindings(sym, cls, ann, method)
	}

	// Scheduled jobs.
	if name == "Scheduled" {
		return []ast.FrameworkBinding{{
			Framework:     "spring",
			Kind:          "scheduler",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "scheduled: " + args,
			TriggerSource: "@Scheduled(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}}
	}

	// Event listeners.
	if name == "EventListener" {
		return []ast.FrameworkBinding{{
			Framework:     "spring",
			Kind:          "event_listener",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "event: " + args,
			TriggerSource: "@EventListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}}
	}

	// Message-queue listeners. Each declares its destination(s) under a
	// framework-specific attribute; the value may be a single literal or an
	// array ({"a","b"}). We emit ONE consumer binding per destination so array
	// forms aren't mangled or dropped (E2).
	// Each listener names its destination(s) under a framework-specific
	// attribute (with version aliases), or positionally. We try the known
	// attribute names in order so e.g. Spring Cloud AWS's @SqsListener(queueNames=…)
	// and the older value= form both resolve.
	queueListeners := map[string]struct {
		platform string
		attrs    []string
	}{
		"KafkaListener":  {"kafka", []string{"topics"}},
		"RabbitListener": {"rabbitmq", []string{"queues"}},
		"SqsListener":    {"sqs", []string{"queueNames", "value"}},
		"JmsListener":    {"jms", []string{"destination"}},
	}
	if ql, ok := queueListeners[name]; ok {
		dests := frameworkutil.NamedOrPositionalValues(args, ql.attrs...)
		return queueConsumerBindings(sym, ql.platform, dests, "@"+name+"("+args+")")
	}

	// Cache operations. Only kept when an external cache backing is configured
	// (see Detect); @Cacheable on an in-memory cache is not a cache_operation.
	cacheOps := map[string]string{"Cacheable": "read", "CachePut": "write", "CacheEvict": "evict"}
	if op, ok := cacheOps[name]; ok {
		cache := firstCacheName(args)
		return []ast.FrameworkBinding{{
			Framework:     "spring",
			Kind:          "cache_operation",
			Direction:     "outbound",
			Symbol:        sym.Qualified,
			Trigger:       "cache: " + op + " " + cache,
			TriggerSource: "@" + name + "(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}}
	}

	// Async dispatch.
	if name == "Async" {
		return []ast.FrameworkBinding{{
			Framework:     "spring",
			Kind:          "async_dispatch",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "async",
			TriggerSource: "@Async",
			File:          sym.File,
			Range:         sym.Range,
		}}
	}

	return nil
}

func springHTTPBindings(sym ast.SymbolDef, cls *ast.SymbolDef, ann ast.Annotation, defaultMethod string) []ast.FrameworkBinding {
	method := defaultMethod
	if ann.Name == "RequestMapping" {
		method = springRequestMethod(ann.Arguments)
	}
	paths := extractRoutePaths(ann.Arguments)
	if len(paths) == 0 {
		paths = []string{""}
	}
	classPrefixes := []string{""}
	controller := false
	feign := false
	if cls != nil {
		controller = frameworkutil.HasAnyAnnotation(*cls, "RestController", "Controller")
		feign = frameworkutil.HasAnyAnnotation(*cls, "FeignClient")
		if p := classRequestMappingPrefixes(*cls); len(p) > 0 {
			classPrefixes = p
		}
	}
	if feign {
		return nil
	}
	kind := "http_handler"
	direction := "inbound"
	reason := "controller_mapping_literal"
	rejection := ""
	if !controller {
		rejection = "spring_mapping_without_controller_context"
	}
	out := make([]ast.FrameworkBinding, 0, len(classPrefixes)*len(paths))
	for _, prefix := range classPrefixes {
		for _, path := range paths {
			routePath := frameworkutil.JoinPath(prefix, path)
			out = append(out, ast.FrameworkBinding{
				Framework:        "spring",
				Kind:             kind,
				Direction:        direction,
				Symbol:           sym.Qualified,
				Trigger:          method + " " + routePath,
				TriggerSource:    "@" + ann.Name + "(" + ann.Arguments + ")",
				File:             sym.File,
				Range:            sym.Range,
				ConfidenceReason: reason,
				RejectionReason:  rejection,
			})
		}
	}
	return out
}

// queueConsumerBindings emits one queue_consumer binding per destination name.
// When no destination resolves it still emits a single binding with an empty
// name so the consumer is not lost (destination resolution happens downstream).
func queueConsumerBindings(sym ast.SymbolDef, platform string, names []string, source string) []ast.FrameworkBinding {
	if len(names) == 0 {
		names = []string{""}
	}
	out := make([]ast.FrameworkBinding, 0, len(names))
	for _, n := range names {
		out = append(out, ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       platform + ": " + n,
			TriggerSource: source,
			File:          sym.File,
			Range:         sym.Range,
		})
	}
	return out
}

// extractRoutePaths returns ONLY the route path literal(s) from a Spring mapping
// annotation's argument text: the `value=`/`path=` attribute, or the leading
// positional argument. It deliberately ignores other string-valued attributes
// (produces/consumes/headers/params/name) so they are never mistaken for routes
// (E1). Handles both single (`"/x"`) and array (`{"/a","/b"}`) forms.
func extractRoutePaths(args string) []string {
	named, positional, hasPositional := frameworkutil.ParseAnnotationArgs(args)
	if v, ok := named["value"]; ok {
		return frameworkutil.ExtractStringArgs(v)
	}
	if v, ok := named["path"]; ok {
		return frameworkutil.ExtractStringArgs(v)
	}
	if hasPositional {
		return frameworkutil.ExtractStringArgs(positional)
	}
	return nil
}

func springRequestMethod(args string) string {
	if strings.Contains(args, "RequestMethod.GET") {
		return "GET"
	}
	if strings.Contains(args, "RequestMethod.POST") {
		return "POST"
	}
	if strings.Contains(args, "RequestMethod.PUT") {
		return "PUT"
	}
	if strings.Contains(args, "RequestMethod.PATCH") {
		return "PATCH"
	}
	if strings.Contains(args, "RequestMethod.DELETE") {
		return "DELETE"
	}
	return "ANY"
}

func classRequestMappingPrefixes(cls ast.SymbolDef) []string {
	for _, ann := range cls.Annotations {
		if ann.Name == "RequestMapping" {
			return extractRoutePaths(ann.Arguments)
		}
	}
	return nil
}
