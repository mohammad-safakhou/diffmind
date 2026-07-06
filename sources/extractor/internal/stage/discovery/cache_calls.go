package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	"del":       "evict",
	"unlink":    "evict",
	"flushdb":   "evict",
	"flushall":  "evict",
	"expire":    "expire",
	"pexpire":   "expire",
	"persist":   "expire",
}

func DeterministicCacheOperations(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	type agg struct {
		op        string
		locations []candidateLocation
		evidence  []candidateEvidence
		seenLoc   map[string]struct{}
	}
	seen := map[string]*agg{}
	var order []string
	forEachCall(idx, func(cs astpkg.CallSite) {
		fa := idx.Files[cs.File]
		if fa == nil || (fa.Language != "python" && fa.Language != "go") {
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
	for _, e := range deterministicGoRedisSourceOperations(idx) {
		op := stringAny(e.Details["operation"])
		if op == "" {
			continue
		}
		key := "redis|" + op
		if _, exists := seen[key]; exists {
			continue
		}
		a := &agg{op: op, seenLoc: map[string]struct{}{}}
		a.locations = append(a.locations, e.Locations...)
		a.evidence = append(a.evidence, e.Evidence...)
		seen[key] = a
		order = append(order, key)
	}
	for _, e := range deterministicS3StorageOperations(idx) {
		op := stringAny(e.Details["operation"])
		cache := stringAny(e.Details["cache"])
		if op == "" || cache == "" {
			continue
		}
		key := "s3|" + cache + "|" + op
		if _, exists := seen[key]; exists {
			continue
		}
		a := &agg{op: op, seenLoc: map[string]struct{}{}}
		a.locations = append(a.locations, e.Locations...)
		a.evidence = append(a.evidence, e.Evidence...)
		seen[key] = a
		order = append(order, key)
	}
	sort.Strings(order)
	out := make([]candidate, 0, len(order))
	for _, key := range order {
		a := seen[key]
		if strings.HasPrefix(key, "s3|") {
			parts := strings.Split(key, "|")
			cache := "s3"
			if len(parts) > 1 && parts[1] != "" {
				cache = parts[1]
			}
			out = append(out, candidate{
				Type:       "cache_operation",
				Name:       a.op + " " + cache,
				Summary:    fmt.Sprintf("AST-derived S3 object storage %s", a.op),
				Confidence: 1.0,
				Tags:       []string{"deterministic", "object-storage:s3", "aws-sdk"},
				Details: map[string]any{
					"cache":         cache,
					"cache_type":    "object_storage",
					"operation":     a.op,
					"platform":      "s3",
					"discovered_by": "ast_aws_s3_call",
				},
				Locations: a.locations,
				Evidence:  a.evidence,
			})
			continue
		}
		out = append(out, candidate{
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

func deterministicS3StorageOperations(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	forEachCall(idx, func(cs astpkg.CallSite) {
		fa := idx.Files[cs.File]
		if fa == nil || (fa.Language != "java" && fa.Language != "kotlin") || isLowSignalCacheArtifactPath(cs.File) {
			return
		}
		op, ok := s3Operation(cs)
		if !ok {
			return
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		bucket := s3BucketFromCall(idx, cs)
		if bucket == "" {
			bucket = "s3"
		}
		key := bucket + "|" + op
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, candidate{
			Type:       "cache_operation",
			Name:       op + " " + bucket,
			Summary:    fmt.Sprintf("AST-derived S3 object storage %s", op),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "object-storage:s3", "aws-sdk"},
			Details: map[string]any{
				"cache":         bucket,
				"cache_type":    "object_storage",
				"operation":     op,
				"platform":      "s3",
				"discovered_by": "ast_aws_s3_call",
			},
			Locations: []candidateLocation{loc},
			Evidence:  []candidateEvidence{callEvidence(cs)},
		})
	})
	return out
}

func s3Operation(cs astpkg.CallSite) (string, bool) {
	receiver, callee := splitCall(cs)
	r := strings.ToLower(receiver)
	c := strings.ToLower(callee)
	if !strings.Contains(r, "s3") {
		return "", false
	}
	switch c {
	case "putobject", "putobjectlegalhold", "putobjecttagging":
		return "write", true
	case "getobject", "headobject", "listobjects", "listobjectsv2":
		return "read", true
	case "deleteobject", "deleteobjects":
		return "evict", true
	default:
		return "", false
	}
}

func s3BucketFromCall(idx *astpkg.ProjectIndex, cs astpkg.CallSite) string {
	window := callWindowSource(idx, cs, 10)
	if value := javaBuilderStringValue(window, "bucket"); value != "" {
		return normalizeResourceToken(ResolveResourceName(idx, value))
	}
	if value := javaAssignedStringValue(window, "bucketName"); value != "" {
		return normalizeResourceToken(ResolveResourceName(idx, value))
	}
	return canonicalS3Bucket(idx)
}

func canonicalS3Bucket(idx *astpkg.ProjectIndex) string {
	if idx == nil {
		return ""
	}
	distinct := map[string]struct{}{}
	for _, path := range sortedConfigPaths(idx) {
		cf := idx.Configs[path]
		if cf == nil {
			continue
		}
		for _, e := range cf.Entries {
			k := strings.ToLower(strings.TrimSpace(e.Key))
			if !strings.Contains(k, "bucket") && !strings.Contains(k, "s3") {
				continue
			}
			v := normalizeResourceToken(stripPlaceholderDefault(e.Value))
			if v == "" || IsPlaceholder(v) || strings.Contains(v, "example") {
				continue
			}
			distinct[v] = struct{}{}
		}
	}
	if len(distinct) != 1 {
		return ""
	}
	for v := range distinct {
		return v
	}
	return ""
}

var goRedisCallRE = regexp.MustCompile(`\b(?:[A-Za-z_][A-Za-z0-9_]*\.)?(?:redisClient|cacheRepo|cache)\s*\.\s*(Get|Set|Del|Delete|Exists|Expire)\s*\(`)

func deterministicGoRedisSourceOperations(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil || idx.RepoRoot == "" {
		return nil
	}
	var out []candidate
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(idx.Files))
	for p := range idx.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fa := idx.Files[path]
		if fa == nil || fa.Language != "go" || isLowSignalCacheArtifactPath(path) {
			continue
		}
		if !fileImportsRedis(fa) && !strings.Contains(strings.ToLower(path), "/cache") && !strings.Contains(strings.ToLower(path), "/redis") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(idx.RepoRoot, path))
		if err != nil {
			continue
		}
		src := string(b)
		for _, m := range goRedisCallRE.FindAllStringSubmatchIndex(src, -1) {
			method := src[m[2]:m[3]]
			op, ok := redisCacheOps[strings.ToLower(method)]
			if !ok {
				continue
			}
			key := "redis|" + op
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			line := 1 + strings.Count(src[:m[0]], "\n")
			loc := candidateLocation{File: path, StartLine: line, EndLine: line}
			out = append(out, candidate{
				Type:       "cache_operation",
				Name:       op + " redis",
				Summary:    fmt.Sprintf("AST-derived Redis cache %s", op),
				Confidence: 1.0,
				Tags:       []string{"deterministic", "cache:redis", "go"},
				Details: map[string]any{
					"cache":         "redis",
					"cache_type":    "redis",
					"operation":     op,
					"platform":      "redis",
					"discovered_by": "ast_go_redis_source",
				},
				Locations: []candidateLocation{loc},
				Evidence: []candidateEvidence{{
					File:      loc.File,
					StartLine: loc.StartLine,
					EndLine:   loc.EndLine,
					Snippet:   src[m[0]:m[1]],
					Source:    "deterministic_ast",
				}},
			})
		}
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
		p := strings.ToLower(strings.Trim(imp.Path, `"`))
		if p == "redis" || strings.HasPrefix(p, "redis.") ||
			strings.Contains(p, "/redis") || strings.Contains(p, "go-redis") {
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
