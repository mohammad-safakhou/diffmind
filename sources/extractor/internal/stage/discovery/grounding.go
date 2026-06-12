package discovery

import (
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// grounding.go turns the deterministic tree-sitter AST index into compact,
// objective-filtered HINTS for the discovery/reexamine/detail prompts.
//
// These hints are ADVISORY ONLY. The LLM remains the authority on what is and
// is not a real entity: we never filter the model's returned items against the
// hint set, and we never keep a hint the model rejects. The hints exist to
// raise recall on the mechanical majority (annotated routes, repository
// classes, datasource config) — the LLM's weak spot at scale — while the model
// still searches for everything the static analysis can't see (reflection,
// dynamic registration, custom frameworks). See the AST_HINTS prompt header.
//
// The whole file is pure (no I/O, no orchestrator) so it is trivially
// unit-testable against a synthetic ProjectIndex.

// Per-list caps. Kept as private consts (mirroring grouping.go's convention):
// raising them is a real token-cost/quality tradeoff, not an operator knob.
// At these sizes the rendered block stays well under ~4 KB even on big repos.
const (
	MaxSymbolHints = 80
	MaxConfigHints = 40
)

// Hint DTOs are extraction contracts shared with prompt rendering.
type (
	objectiveHints = extraction.ObjectiveHints
	symbolHint     = extraction.SymbolHint
	bindingHint    = extraction.BindingHint
	configHint     = extraction.ConfigHint
)

// objectiveMatcher declares what AST facts are relevant to one objective type.
// Matching is case-insensitive substring matching, deliberately loose: a hint
// that turns out irrelevant costs only a prompt line, and the model verifies
// everything anyway.
type objectiveMatcher struct {
	annPatterns    []string // matched against SymbolDef.Annotation names
	bindingKinds   []string // matched against FrameworkBinding.Kind
	classNameHints []string // matched against SymbolDef.Receiver / Name
	configKeyHints []string // matched against ConfigEntry.Key
	// clientLibs name the client libraries whose presence in a file marks it
	// as candidate-bearing for SHARDING weights only (S2): imports +2, call
	// receivers/callees +1. This lets objectives without framework detectors
	// (queue_publish, command_exec, outbound_rpc, ...) accumulate evidence and
	// shard, instead of one whole-repo "find ALL X" call — the highest-variance,
	// truncation-prone prompt shape. Matched one-way (text contains lib), unlike
	// the loose two-way hint matching.
	clientLibs []string
}

// objectiveMatchers maps objective.Type -> its matcher. One entry per of the
// 13 objectives; adding a language/annotation is a one-line edit here.
var objectiveMatchers = map[string]objectiveMatcher{
	"http_route": {
		annPatterns:    []string{"requestmapping", "getmapping", "postmapping", "putmapping", "patchmapping", "deletemapping", "controller", "restcontroller", "path", "route", "app.get", "app.post", "router"},
		bindingKinds:   []string{"http_route", "route", "controller", "endpoint"},
		classNameHints: []string{"controller", "resource", "handler", "router", "api"},
	},
	"webhook": {
		annPatterns:    []string{"postmapping", "requestmapping", "path", "route"},
		bindingKinds:   []string{"webhook", "http_route"},
		classNameHints: []string{"webhook", "callback", "hook", "notify"},
	},
	"rpc_endpoint": {
		annPatterns:    []string{"grpc", "grpcservice", "service", "rpc"},
		bindingKinds:   []string{"rpc", "grpc", "rpc_endpoint"},
		classNameHints: []string{"grpc", "service", "rpc", "servicegrpc"},
		clientLibs:     []string{"grpc", "protobuf", "implbase", "twirp"},
	},
	"queue_consumer": {
		annPatterns:    []string{"sqslistener", "kafkalistener", "rabbitlistener", "streamlistener", "jmslistener", "eventlistener", "listener", "subscribe"},
		bindingKinds:   []string{"queue_consumer", "consumer", "listener", "subscriber"},
		classNameHints: []string{"listener", "consumer", "subscriber", "handler"},
		clientLibs:     []string{"sqsclient", "kafkaconsumer", "rabbitmq", "amqp", "pubsub", "nats"},
	},
	"scheduled_job": {
		annPatterns:    []string{"scheduled", "schedule", "cron", "scheduledjob"},
		bindingKinds:   []string{"scheduled", "scheduled_job", "cron", "timer"},
		classNameHints: []string{"job", "scheduler", "task", "cron"},
	},
	"cli_command": {
		annPatterns:    []string{"command", "cli", "requesthandler"},
		bindingKinds:   []string{"cli_command", "command", "lambda_handler", "entrypoint"},
		classNameHints: []string{"command", "handler", "main", "cli", "cmd"},
		clientLibs:     []string{"cobra", "picocli", "click", "argparse", "commander", "yargs"},
	},
	"db_operation": {
		annPatterns:    []string{"repository", "query", "mapper", "select", "insert", "update", "delete", "entity", "table", "cacheable", "cacheevict"},
		bindingKinds:   []string{"db_operation", "repository"},
		classNameHints: []string{"repository", "dao", "mapper", "store", "entity"},
		configKeyHints: []string{"datasource", "jdbc", "database", "db.", "dynamodb", "mongo", "elasticsearch", "redis"},
		clientLibs:     []string{"gorm", "sqlx", "prisma", "sequelize", "typeorm", "mongoose", "sqlalchemy", "activerecord"},
	},
	"outbound_http": {
		annPatterns:    []string{"feignclient", "httpexchange", "getexchange", "postexchange", "retrofit"},
		bindingKinds:   []string{"outbound_http", "http_client", "feign"},
		classNameHints: []string{"client", "gateway", "connector", "feign", "api"},
		configKeyHints: []string{"url", "endpoint", "host", "base-url", "baseurl"},
	},
	"outbound_rpc": {
		annPatterns:    []string{"grpcclient", "grpc", "stub"},
		bindingKinds:   []string{"outbound_rpc", "grpc_client"},
		classNameHints: []string{"client", "stub", "grpc", "channel"},
		clientLibs:     []string{"grpc", "protobuf", "stub", "twirp"},
	},
	"queue_publish": {
		annPatterns:    []string{"sqstemplate", "kafkatemplate", "rabbittemplate", "publish", "send"},
		bindingKinds:   []string{"queue_publish", "producer", "publisher"},
		classNameHints: []string{"publisher", "producer", "sender", "template", "gateway"},
		configKeyHints: []string{"queue", "topic", "sns", "sqs", "kafka", "exchange"},
		clientLibs:     []string{"sqsclient", "sqstemplate", "kafkatemplate", "kafkaproducer", "rabbittemplate", "snsclient", "pubsub", "nats", "amqp", "jmstemplate"},
	},
	"command_exec": {
		annPatterns:    []string{},
		bindingKinds:   []string{"command_exec", "process"},
		classNameHints: []string{"exec", "process", "shell", "runtime", "command"},
		clientLibs:     []string{"processbuilder", "runtime.exec", "os/exec", "subprocess", "child_process"},
	},
	"cache_operation": {
		annPatterns:    []string{"cacheable", "cacheevict", "cacheput", "cache"},
		bindingKinds:   []string{"cache_operation", "cache"},
		classNameHints: []string{"cache", "redis", "memcached"},
		configKeyHints: []string{"redis", "cache", "memcached"},
		clientLibs:     []string{"redis", "jedis", "lettuce", "memcached", "caffeine", "hazelcast"},
	},
	"stream_consume": {
		annPatterns:    []string{"kinesis", "streamlistener", "streamconsumer"},
		bindingKinds:   []string{"stream_consume", "stream"},
		classNameHints: []string{"stream", "kinesis", "consumer", "processor"},
		configKeyHints: []string{"kinesis", "stream"},
		clientLibs:     []string{"kinesis", "kafkastreams", "kstream", "flink", "spark"},
	},
}

// buildObjectiveHints filters the index down to symbols / framework bindings /
// config entries relevant to obj.Type. It is deterministic (stable sort) and
// token-bounded (per-list caps). Returns a zero-value (empty) objectiveHints
// when idx is nil or no matcher exists, so callers never special-case.
//
// When subDir is non-empty (monorepo), anything outside subDir/ is dropped.
// fileScope, when non-empty, further restricts hints to files whose path has
// one of the given prefixes (used by Phase B to make a shard's hints cover
// only that shard's directories).
func BuildObjectiveHints(idx *astpkg.ProjectIndex, obj objectives.Objective, subDir string, fileScope []string) objectiveHints {
	var h objectiveHints
	if idx == nil {
		return h
	}
	m, ok := objectiveMatchers[obj.Type]
	if !ok {
		return h
	}

	inScope := func(file string) bool {
		if file == "" {
			return false
		}
		if subDir != "" && !strings.HasPrefix(file, strings.TrimSuffix(subDir, "/")+"/") {
			return false
		}
		if len(fileScope) > 0 {
			for _, p := range fileScope {
				if strings.HasPrefix(file, p) {
					return true
				}
			}
			return false
		}
		return true
	}

	// 1. Symbols: keep when an annotation OR the class/receiver name matches.
	for _, defs := range idx.Symbols {
		for _, def := range defs {
			if !inScope(def.File) {
				continue
			}
			anns := annotationNames(def.Annotations)
			if symbolMatches(def, anns, m) {
				h.Symbols = append(h.Symbols, symbolHint{
					Qualified:   def.Qualified,
					File:        def.File,
					Line:        def.Range.StartLine,
					Annotations: anns,
				})
			}
		}
	}
	sort.Slice(h.Symbols, func(i, j int) bool { return lessSymbolHint(h.Symbols[i], h.Symbols[j]) })
	h.Symbols = dedupSymbolHints(h.Symbols)
	if len(h.Symbols) > MaxSymbolHints {
		h.Symbols = h.Symbols[:MaxSymbolHints]
		h.Truncated = true
	}

	// 2. Framework bindings of a relevant kind.
	for _, b := range idx.Frameworks {
		if !inScope(b.File) {
			continue
		}
		if containsFold(m.bindingKinds, b.Kind) {
			h.Bindings = append(h.Bindings, bindingHint{
				Framework: b.Framework, Kind: b.Kind, Symbol: b.Symbol,
				Trigger: b.Trigger, File: b.File, Line: b.Range.StartLine,
			})
		}
	}
	sort.Slice(h.Bindings, func(i, j int) bool {
		if h.Bindings[i].File != h.Bindings[j].File {
			return h.Bindings[i].File < h.Bindings[j].File
		}
		return h.Bindings[i].Line < h.Bindings[j].Line
	})

	// 3. Config entries whose key hints at a relevant resource.
	if len(m.configKeyHints) > 0 {
		for _, cf := range idx.Configs {
			if cf == nil {
				continue
			}
			for _, e := range cf.Entries {
				if anyContainsFold(m.configKeyHints, e.Key) {
					h.Configs = append(h.Configs, configHint{File: cf.Path, Key: e.Key, Value: e.Value})
				}
			}
		}
		sort.Slice(h.Configs, func(i, j int) bool {
			if h.Configs[i].File != h.Configs[j].File {
				return h.Configs[i].File < h.Configs[j].File
			}
			return h.Configs[i].Key < h.Configs[j].Key
		})
		if len(h.Configs) > MaxConfigHints {
			h.Configs = h.Configs[:MaxConfigHints]
			h.Truncated = true
		}
	}

	return h
}

// objectiveCandidateWeights returns the per-file count of this objective's
// static-analysis candidates (matching symbols + framework bindings), UNCAPPED
// — unlike buildObjectiveHints, which caps and dedups for prompt rendering.
// Sharding uses these weights to decide both whether to split (total weight)
// and how to cluster files (per-file weight). Files with no candidates are
// absent from the map, so sharding naturally ignores them. subDir scoping is
// honoured; fileScope is intentionally not applied (sharding works over the
// whole in-scope tree).
func objectiveCandidateWeights(idx *astpkg.ProjectIndex, obj objectives.Objective, subDir string) map[string]int {
	out := map[string]int{}
	if idx == nil {
		return out
	}
	m, ok := objectiveMatchers[obj.Type]
	if !ok {
		return out
	}
	inScope := func(file string) bool {
		if file == "" {
			return false
		}
		if subDir != "" && !strings.HasPrefix(file, strings.TrimSuffix(subDir, "/")+"/") {
			return false
		}
		return true
	}
	for _, defs := range idx.Symbols {
		for _, def := range defs {
			if !inScope(def.File) {
				continue
			}
			if symbolMatches(def, annotationNames(def.Annotations), m) {
				out[def.File]++
			}
		}
	}
	for _, b := range idx.Frameworks {
		if !inScope(b.File) {
			continue
		}
		if containsFold(m.bindingKinds, b.Kind) {
			out[b.File]++
		}
	}
	// S2: keyword-seeded weights from client-library usage, so objectives the
	// framework detectors don't cover still accumulate evidence and shard.
	// Imports are the strongest signal (+2); call receivers/callees +1.
	if len(m.clientLibs) > 0 {
		for file, fa := range idx.Files {
			if fa == nil || !inScope(file) {
				continue
			}
			for _, imp := range fa.Imports {
				if anyLibMatch(m.clientLibs, imp.Path) || anyLibMatch(m.clientLibs, imp.Alias) {
					out[file] += 2
				}
			}
			for _, cs := range fa.Calls {
				if anyLibMatch(m.clientLibs, cs.ReceiverRaw) || anyLibMatch(m.clientLibs, cs.CalleeRaw) {
					out[file]++
				}
			}
		}
	}
	return out
}

// anyLibMatch reports whether the text mentions one of the library names.
// One-way contains only — the two-way hint matching would let a one-letter
// receiver match everything.
func anyLibMatch(libs []string, s string) bool {
	if s == "" {
		return false
	}
	ls := strings.ToLower(s)
	for _, lib := range libs {
		if strings.Contains(ls, lib) {
			return true
		}
	}
	return false
}

func symbolMatches(def astpkg.SymbolDef, anns []string, m objectiveMatcher) bool {
	for _, a := range anns {
		if anyContainsFold(m.annPatterns, a) {
			return true
		}
	}
	if anyContainsFold(m.classNameHints, def.Receiver) || anyContainsFold(m.classNameHints, def.Name) {
		return true
	}
	return false
}

func annotationNames(anns []astpkg.Annotation) []string {
	if len(anns) == 0 {
		return nil
	}
	out := make([]string, 0, len(anns))
	for _, a := range anns {
		if a.Name != "" {
			out = append(out, a.Name)
		}
	}
	return out
}

// anyContainsFold reports whether s (lowercased) contains any of the patterns,
// or whether any pattern contains s — a loose two-way substring match so e.g.
// matcher "controller" matches symbol "OrderController" and matcher
// "getmapping" matches annotation "GetMapping".
func anyContainsFold(patterns []string, s string) bool {
	if s == "" {
		return false
	}
	ls := strings.ToLower(s)
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.Contains(ls, p) || strings.Contains(p, ls) {
			return true
		}
	}
	return false
}

// containsFold reports whether vals contains s, case-insensitively (exact, used
// for binding Kind which is an enum-like value).
func containsFold(vals []string, s string) bool {
	for _, v := range vals {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func lessSymbolHint(a, b symbolHint) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Qualified < b.Qualified
}

func dedupSymbolHints(in []symbolHint) []symbolHint {
	if len(in) <= 1 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		last := out[len(out)-1]
		if s.Qualified == last.Qualified && s.File == last.File && s.Line == last.Line {
			continue
		}
		out = append(out, s)
	}
	return out
}
