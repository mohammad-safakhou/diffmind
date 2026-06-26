package discovery

import (
	"fmt"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

var redisCacheOps = map[string]string{
	"get":       "read",
	"mget":      "read",
	"hget":      "read",
	"hgetall":   "read",
	"scan_iter": "read",
	"keys":      "read",
	"exists":    "read",
	"set":       "write",
	"setex":     "write",
	"mset":      "write",
	"hset":      "write",
	"hmset":     "write",
	"incr":      "write",
	"decr":      "write",
	"delete":    "evict",
	"unlink":    "evict",
	"flushdb":   "evict",
	"flushall":  "evict",
	"expire":    "expire",
	"pexpire":   "expire",
	"persist":   "expire",
}

func DeterministicCacheOperations(idx *astpkg.ProjectIndex) []llmEntity {
	if idx == nil {
		return nil
	}
	type agg struct {
		op        string
		locations []llmLocation
		evidence  []llmEvidence
		seenLoc   map[string]struct{}
	}
	seen := map[string]*agg{}
	var order []string
	forEachCall(idx, func(cs astpkg.CallSite) {
		fa := idx.Files[cs.File]
		if fa == nil || fa.Language != "python" {
			return
		}
		if isLowSignalCacheArtifactPath(cs.File) {
			return
		}
		receiver, callee := splitCall(cs)
		op, ok := redisCacheOps[strings.ToLower(callee)]
		if !ok || !looksLikeRedisReceiver(receiver, fa) {
			return
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		key := "redis|" + op
		a, exists := seen[key]
		if !exists {
			a = &agg{op: op, seenLoc: map[string]struct{}{}}
			seen[key] = a
			order = append(order, key)
		}
		locKey := fmt.Sprintf("%s:%d-%d", loc.File, loc.StartLine, loc.EndLine)
		if _, dup := a.seenLoc[locKey]; dup {
			return
		}
		a.seenLoc[locKey] = struct{}{}
		a.locations = append(a.locations, loc)
		a.evidence = append(a.evidence, callEvidence(cs))
	})
	sort.Strings(order)
	out := make([]llmEntity, 0, len(order))
	for _, key := range order {
		a := seen[key]
		out = append(out, llmEntity{
			Type:       "cache_operation",
			Name:       a.op + " redis",
			Summary:    fmt.Sprintf("AST-derived Redis cache %s", a.op),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "cache:redis"},
			Details: map[string]any{
				"cache":         "redis",
				"cache_type":    "redis",
				"operation":     a.op,
				"platform":      "redis",
				"discovered_by": "ast_redis_call",
			},
			Locations: a.locations,
			Evidence:  a.evidence,
		})
	}
	return out
}

func looksLikeRedisReceiver(receiver string, fa *astpkg.FileAST) bool {
	r := strings.ToLower(strings.TrimSpace(receiver))
	if r == "" {
		return false
	}
	tokens := []string{"redis", "cache"}
	for _, tok := range tokens {
		if r == tok || strings.Contains(r, "_"+tok) || strings.Contains(r, tok+"_") || strings.HasSuffix(r, tok) {
			return true
		}
	}
	return strings.Contains(r, "pipeline") && fileImportsRedis(fa)
}

func fileImportsRedis(fa *astpkg.FileAST) bool {
	if fa == nil {
		return false
	}
	for _, imp := range fa.Imports {
		if strings.EqualFold(imp.Path, "redis") || strings.HasPrefix(strings.ToLower(imp.Path), "redis.") {
			return true
		}
	}
	return false
}

func isLowSignalCacheArtifactPath(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	base := path
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "test", "tests", "__tests__", "fixture", "fixtures", "script", "scripts", "tool", "tools", "dev", "development":
			return true
		}
		if strings.HasPrefix(segment, "local_") || strings.HasSuffix(segment, "_local") {
			return true
		}
	}
	return strings.Contains(base, "_test.") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}
