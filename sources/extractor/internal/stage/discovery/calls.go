package discovery

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// deterministic_calls.go derives high-precision dependencies from call sites the
// AST already recorded — process execution and message publishing — the same
// way deterministicDBOperations derives db ops. Matching is gated on a curated
// set of (receiver, callee) patterns tied to a known library/template, so a
// generic ".send()" or ".exec()" is never mistaken for one (invariant #6).

// DeterministicCommandExec finds process-execution dependencies (Runtime.exec,
// ProcessBuilder, Go os/exec.Command, Python subprocess/os.system).
func DeterministicCommandExec(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	forEachCall(idx, func(cs astpkg.CallSite) {
		if !MatchCommandExec(cs) {
			return
		}
		cmd := firstLiteralArg(cs.Arguments)
		name := cmd
		if name == "" {
			name = "exec in " + lastIdentOf(cs.Caller)
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		key := strings.ToLower(name) + "|" + loc.File
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		details := map[string]any{"discovered_by": "ast_exec_call"}
		if cmd != "" {
			details["command"] = cmd
		} else {
			details["command"] = name
		}
		out = append(out, candidate{
			Type:       "command_exec",
			Name:       name,
			Summary:    "AST-derived process execution",
			Confidence: 1.0,
			Tags:       []string{"deterministic", "exec"},
			Details:    details,
			Locations:  []candidateLocation{loc},
			Evidence:   []candidateEvidence{callEvidence(cs)},
		})
	})
	return out
}

// DeterministicQueuePublish finds message-publish dependencies through known
// publisher templates/clients (Spring Sqs/Sns/Kafka/Rabbit templates,
// StreamBridge, AWS SDK SQS/SNS clients). Only emitted when the destination is
// resolvable from a literal argument (or a ${...} config placeholder), so the
// emitted name is a real queue/topic, never a guess.
func DeterministicQueuePublish(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	forEachCall(idx, func(cs astpkg.CallSite) {
		platform, ok := MatchQueuePublish(cs)
		if !ok {
			return
		}
		dest := ResolveResourceName(idx, firstLiteralArg(cs.Arguments))
		if dest == "" {
			return // destination not statically resolvable; leave to the LLM
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		key := platform + "|" + strings.ToLower(dest)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, candidate{
			Type:       "queue_publish",
			Name:       dest,
			Summary:    fmt.Sprintf("AST-derived %s publish to %s", platform, dest),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "messaging"},
			Details: map[string]any{
				"platform":      platform,
				"destination":   dest,
				"discovered_by": "ast_publish_call",
			},
			Locations: []candidateLocation{loc},
			Evidence:  []candidateEvidence{callEvidence(cs)},
		})
	})
	return out
}

// DeterministicOutboundRPC finds gRPC client calls through generated blocking/
// future stubs (e.g. fooServiceBlockingStub.getThing(req)). Matched on the
// generated stub naming convention, which is gRPC-specific (never a plain var).
func DeterministicOutboundRPC(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	forEachCall(idx, func(cs astpkg.CallSite) {
		service, method, ok := MatchGRPCStubCall(cs)
		if !ok {
			service, method, ok = matchGoGRPCClientCall(cs)
		}
		if !ok {
			return
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		key := strings.ToLower(service + "." + method)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, candidate{
			Type:       "outbound_rpc",
			Name:       service + "." + method,
			Summary:    "AST-derived gRPC client call",
			Confidence: 1.0,
			Tags:       []string{"deterministic", "grpc"},
			Details: map[string]any{
				"platform":      "grpc",
				"service":       service,
				"method":        method,
				"discovered_by": "ast_grpc_stub_call",
			},
			Locations: []candidateLocation{loc},
			Evidence:  []candidateEvidence{callEvidence(cs)},
		})
	})
	return out
}

// DeterministicOutboundHTTP finds high-precision Go HTTP client operations. It
// accepts Resty's explicit verbs and stdlib net/http request construction, and
// only emits calls from source paths/import contexts that look like outbound
// HTTP adapters.
func DeterministicOutboundHTTP(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	forEachCall(idx, func(cs astpkg.CallSite) {
		fa := idx.Files[cs.File]
		if fa == nil || fa.Language != "go" {
			return
		}
		var method, path, discoveredBy, tag, summary string
		window := callWindowSource(idx, cs, 10)
		if looksLikeRestyOperationContext(fa, cs.File) {
			method, path = restyHTTPCall(cs, window)
			discoveredBy = "ast_go_resty_call"
			tag = "resty"
			summary = "AST-derived Resty outbound HTTP call"
		}
		if method == "" && looksLikeNetHTTPOperationContext(fa, cs.File) {
			method, path = netHTTPRequestCall(cs, window)
			discoveredBy = "ast_go_nethttp_request"
			tag = "nethttp"
			summary = "AST-derived net/http outbound HTTP call"
		}
		if method == "" || path == "" {
			return
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		targetName := serviceNameFromOutboundHTTPPath(cs.File)
		details := map[string]any{
			"method":         method,
			"path":           path,
			"url_template":   path,
			"target_service": targetName,
			"discovered_by":  discoveredBy,
		}
		target := configuredHTTPTargetForOperation(idx, cs.Caller, path)
		if target.serviceRef == "" && targetName != "" {
			target = configuredHTTPTargetForOperation(idx, targetName, path)
		}
		if target.serviceRef != "" {
			applyConfiguredHTTPTargetDetails(details, target)
			if target.host != "" && strings.HasPrefix(path, "/") {
				details["url_template"] = strings.TrimRight(target.urlTemplate, "/") + path
			}
		}
		name := strings.TrimSpace(method + " " + path)
		if targetName != "" {
			name = targetName + " " + name
		}
		key := strings.ToLower(firstNonEmptyString(stringAny(details["target_service"]), targetName) + "|" + method + "|" + path)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, candidate{
			Type:       "outbound_http",
			Name:       name,
			Summary:    summary,
			Confidence: 1.0,
			Tags:       []string{"deterministic", tag, "go"},
			Details:    details,
			Locations:  []candidateLocation{loc},
			Evidence:   []candidateEvidence{callEvidence(cs)},
		})
	})
	return out
}

// DeterministicStreamConsume finds Kafka Streams sources (streamsBuilder.stream
// ("topic")). Gated on a StreamsBuilder receiver so the ubiquitous Java
// Collection.stream() never matches, and only when the topic is a literal.
func DeterministicStreamConsume(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	forEachCall(idx, func(cs astpkg.CallSite) {
		r, m := splitCall(cs)
		if strings.ToLower(m) != "stream" || !strings.Contains(strings.ToLower(r), "streamsbuilder") {
			return
		}
		topic := ResolveResourceName(idx, firstLiteralArg(cs.Arguments))
		if topic == "" {
			return
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		if _, dup := seen[strings.ToLower(topic)]; dup {
			return
		}
		seen[strings.ToLower(topic)] = struct{}{}
		out = append(out, candidate{
			Type:       "stream_consume",
			Name:       topic,
			Summary:    "AST-derived Kafka Streams source for " + topic,
			Confidence: 1.0,
			Tags:       []string{"deterministic", "streaming"},
			Details: map[string]any{
				"platform":      "kafka-streams",
				"stream":        topic,
				"discovered_by": "ast_kafka_streams",
			},
			Locations: []candidateLocation{loc},
			Evidence:  []candidateEvidence{callEvidence(cs)},
		})
	})
	return out
}

// MatchGRPCStubCall returns (service, method) when the call is on a generated
// gRPC blocking/future stub. The "BlockingStub"/"FutureStub" naming is specific
// to gRPC codegen, so this stays high-precision.
func MatchGRPCStubCall(cs astpkg.CallSite) (service, method string, ok bool) {
	r, m := splitCall(cs)
	rl := strings.ToLower(r)
	if !strings.Contains(rl, "blockingstub") && !strings.Contains(rl, "futurestub") {
		return "", "", false
	}
	if m == "" {
		return "", "", false
	}
	return deriveGRPCService(r), m, true
}

func matchGoGRPCClientCall(cs astpkg.CallSite) (service, method string, ok bool) {
	r, m := splitCall(cs)
	rl := strings.ToLower(r)
	if !strings.HasSuffix(rl, "serviceclient") {
		return "", "", false
	}
	if m == "" {
		return "", "", false
	}
	service = deriveGRPCService(r)
	if fromPath := serviceNameFromGRPCPath(cs.File); fromPath != "" {
		service = fromPath
	}
	return service, m, true
}

// deriveGRPCService strips the stub suffix from a stub variable/type to recover
// the service name (fooServiceBlockingStub -> fooService).
func deriveGRPCService(stub string) string {
	s := strings.TrimSpace(stub)
	for _, suf := range []string{"BlockingStub", "FutureStub", "ServiceClient", "serviceClient", "Stub", "blockingStub", "futureStub", "stub"} {
		if strings.HasSuffix(s, suf) {
			return strings.TrimSuffix(s, suf)
		}
	}
	return s
}

func looksLikeRestyOperationContext(fa *astpkg.FileAST, path string) bool {
	if fa != nil {
		for _, imp := range fa.Imports {
			if strings.Contains(strings.ToLower(strings.Trim(imp.Path, `"`)), "go-resty/resty") {
				return true
			}
		}
	}
	p := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return strings.Contains(p, "/adapter/outbound/") && (strings.Contains(p, "_http/") || strings.Contains(p, "/http/"))
}

func looksLikeNetHTTPOperationContext(fa *astpkg.FileAST, path string) bool {
	if fa == nil {
		return false
	}
	importsNetHTTP := false
	for _, imp := range fa.Imports {
		if strings.Trim(imp.Path, `"`) == "net/http" {
			importsNetHTTP = true
			break
		}
	}
	if !importsNetHTTP {
		return false
	}
	p := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return strings.Contains(p, "/adapter/outbound/") || strings.Contains(p, "/outbound/")
}

func restyHTTPCall(cs astpkg.CallSite, src string) (method, path string) {
	_, callee := splitCall(cs)
	switch strings.ToLower(callee) {
	case "execute":
		if len(cs.Arguments) > 0 {
			method = strings.ToUpper(strings.Trim(strings.TrimSpace(cs.Arguments[0].Source), "\"'`"))
		}
		if len(cs.Arguments) > 1 {
			path = strings.Trim(strings.TrimSpace(cs.Arguments[1].Source), "\"'`")
		}
	case "get", "post", "put", "patch", "delete":
		method = strings.ToUpper(callee)
		if len(cs.Arguments) > 0 {
			path = strings.Trim(strings.TrimSpace(cs.Arguments[0].Source), "\"'`")
		}
	default:
		return "", ""
	}
	if path == "" || strings.ContainsAny(path, "()") || isPlainIdent(path) {
		if p := restyJoinedPath(src); p != "" {
			path = p
		}
	}
	if method == "" || path == "" {
		return "", ""
	}
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(strings.ToLower(path), "http://") && !strings.HasPrefix(strings.ToLower(path), "https://") {
		return "", ""
	}
	return method, path
}

func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

var urlJoinPathRE = regexp.MustCompile(`url\.JoinPath\s*\([^,\n]+,\s*"([^"]+)"`)

func restyJoinedPath(src string) string {
	if m := urlJoinPathRE.FindStringSubmatch(src); len(m) == 2 {
		return m[1]
	}
	return ""
}

func netHTTPRequestCall(cs astpkg.CallSite, src string) (method, path string) {
	raw := strings.TrimSpace(cs.CalleeRaw)
	_, callee := splitCall(cs)
	if raw != "http.NewRequestWithContext" && raw != "http.NewRequest" && callee != "NewRequestWithContext" && callee != "NewRequest" {
		return "", ""
	}
	methodPos, urlPos := 0, 1
	if strings.HasSuffix(raw, "NewRequestWithContext") || callee == "NewRequestWithContext" {
		methodPos, urlPos = 1, 2
	}
	if len(cs.Arguments) <= urlPos {
		return "", ""
	}
	method = httpMethodFromExpr(cs.Arguments[methodPos].Source)
	path = httpURLTemplateFromExpr(cs.Arguments[urlPos].Source, src)
	if method == "" || path == "" {
		return "", ""
	}
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(strings.ToLower(path), "http://") && !strings.HasPrefix(strings.ToLower(path), "https://") {
		return "", ""
	}
	return method, path
}

func httpMethodFromExpr(src string) string {
	src = strings.Trim(strings.TrimSpace(src), `"'`+"`")
	switch strings.ToLower(src) {
	case "get", "http.methodget":
		return "GET"
	case "post", "http.methodpost":
		return "POST"
	case "put", "http.methodput":
		return "PUT"
	case "patch", "http.methodpatch":
		return "PATCH"
	case "delete", "http.methoddelete":
		return "DELETE"
	case "head", "http.methodhead":
		return "HEAD"
	case "options", "http.methodoptions":
		return "OPTIONS"
	default:
		return ""
	}
}

func httpURLTemplateFromExpr(expr, src string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if strings.HasPrefix(expr, `"`) || strings.HasPrefix(expr, "'") || strings.HasPrefix(expr, "`") {
		return unquoteGoString(expr)
	}
	if strings.HasPrefix(expr, "fmt.Sprintf") {
		return templateFromFmtSprintf(expr)
	}
	if isPlainIdent(expr) {
		return templateFromAssignedFmtSprintf(expr, src)
	}
	return ""
}

func templateFromAssignedFmtSprintf(name, src string) string {
	if name == "" || src == "" {
		return ""
	}
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\s*(?::=|=)\s*fmt\.Sprintf\s*\(\s*(` + goStringLiteralPattern + `)`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return templateFromFormatLiteral(m[1])
	}
	return ""
}

func templateFromFmtSprintf(expr string) string {
	re := regexp.MustCompile(`(?s)fmt\.Sprintf\s*\(\s*(` + goStringLiteralPattern + `)`)
	if m := re.FindStringSubmatch(expr); len(m) > 1 {
		return templateFromFormatLiteral(m[1])
	}
	return ""
}

const goStringLiteralPattern = `"([^"\\]|\\.)*"|'([^'\\]|\\.)*'|` + "`" + `([^` + "`" + `])*` + "`"

var fmtVerbRE = regexp.MustCompile(`%[+#0 \-]*(?:\d+|\*)?(?:\.(?:\d+|\*))?[bcdeEfFgGosqtxXUvpT]`)

func templateFromFormatLiteral(lit string) string {
	tpl := unquoteGoString(lit)
	if tpl == "" {
		return ""
	}
	tpl = fmtVerbRE.ReplaceAllString(tpl, "{value}")
	tpl = strings.TrimPrefix(tpl, "{value}")
	return tpl
}

func unquoteGoString(lit string) string {
	lit = strings.TrimSpace(lit)
	if lit == "" {
		return ""
	}
	if v, err := strconv.Unquote(lit); err == nil {
		return v
	}
	return strings.Trim(lit, `"'`+"`")
}

func serviceNameFromOutboundHTTPPath(path string) string {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasSuffix(part, "_http") {
			return strings.TrimSuffix(part, "_http")
		}
		if part == "provider" && i+1 < len(parts) {
			return strings.TrimSpace(parts[i+1])
		}
	}
	return ""
}

func serviceNameFromGRPCPath(path string) string {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	for _, part := range strings.Split(path, "/") {
		if strings.HasSuffix(part, "_grpc") {
			return strings.TrimSuffix(part, "_grpc")
		}
	}
	return ""
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func stringAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// MatchCommandExec reports whether a call site is a process execution, by a
// curated (receiver, callee) set. High precision: the receiver must tie to a
// known exec API, never a bare ".exec"/".run".
func MatchCommandExec(cs astpkg.CallSite) bool {
	rr, cc := splitCall(cs)
	r := strings.ToLower(rr)
	c := strings.ToLower(cc)
	switch {
	case c == "exec" && strings.Contains(r, "runtime"): // Java Runtime.getRuntime().exec
		return true
	case c == "start" && strings.Contains(r, "processbuilder"): // Java new ProcessBuilder(...).start()
		return true
	case c == "command" && r == "exec": // Go os/exec.Command
		return true
	case r == "subprocess" && (c == "run" || c == "call" || c == "popen" || c == "check_output" || c == "check_call"): // Python
		return true
	case r == "os" && (c == "system" || c == "popen"): // Python os.system / os.popen
		return true
	}
	return false
}

// MatchQueuePublish reports the messaging platform when a call site is a publish
// through a known template/client, else ("", false).
func MatchQueuePublish(cs astpkg.CallSite) (string, bool) {
	rr, cc := splitCall(cs)
	r := strings.ToLower(rr)
	c := strings.ToLower(cc)
	type pat struct {
		token    string
		platform string
		callees  []string
	}
	pats := []pat{
		{"sqstemplate", "sqs", []string{"send", "sendmany"}},
		{"snstemplate", "sns", []string{"send", "sendnotification"}},
		{"kafkatemplate", "kafka", []string{"send"}},
		{"streambridge", "kafka", []string{"send"}},
		{"rabbittemplate", "rabbitmq", []string{"convertandsend", "send"}},
		{"sqsclient", "sqs", []string{"sendmessage", "sendmessagebatch"}},
		{"amazonsqs", "sqs", []string{"sendmessage", "sendmessagebatch"}},
		{"snsclient", "sns", []string{"publish"}},
		{"amazonsns", "sns", []string{"publish"}},
	}
	for _, p := range pats {
		if strings.Contains(r, p.token) && containsStr(p.callees, c) {
			return p.platform, true
		}
	}
	return "", false
}

// splitCall normalizes a call site into (receiver, method). Some language
// parsers fold the receiver into CalleeRaw (e.g. Go records "exec.Command"), so
// the method is the last dotted segment and the receiver is recovered from the
// prefix when ReceiverRaw is empty.
func splitCall(cs astpkg.CallSite) (receiver, method string) {
	method = strings.TrimSpace(cs.CalleeRaw)
	receiver = strings.TrimSpace(cs.ReceiverRaw)
	if i := strings.LastIndex(method, "."); i >= 0 {
		if receiver == "" {
			receiver = method[:i]
		}
		method = method[i+1:]
	}
	return receiver, method
}

// forEachCall visits every recorded call site — including ones without an
// enclosing named caller (top-level / arrow-function code, the norm in Node),
// which idx.CallGraph drops — in sorted file order, so "first seen wins"
// tie-breaks inside the derivers are stable run-to-run (the V3a lesson).
func forEachCall(idx *astpkg.ProjectIndex, fn func(astpkg.CallSite)) {
	paths := make([]string, 0, len(idx.Files))
	for p := range idx.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fa := idx.Files[p]
		if fa == nil {
			continue
		}
		for _, cs := range fa.Calls {
			fn(cs)
		}
	}
}

func firstLiteralArg(args []astpkg.ArgumentExpr) string {
	for _, a := range args {
		if a.Kind == "literal" {
			return strings.Trim(strings.TrimSpace(a.Source), "\"'`")
		}
	}
	return ""
}

func callLoc(cs astpkg.CallSite) candidateLocation {
	return candidateLocation{File: cs.File, StartLine: int(cs.Range.StartLine) + 1, EndLine: int(cs.Range.EndLine) + 1}
}

func callEvidence(cs astpkg.CallSite) candidateEvidence {
	snippet := strings.TrimSpace(strings.TrimSpace(cs.ReceiverRaw) + "." + strings.TrimSpace(cs.CalleeRaw))
	return candidateEvidence{
		File:      cs.File,
		StartLine: int(cs.Range.StartLine) + 1,
		EndLine:   int(cs.Range.EndLine) + 1,
		Snippet:   strings.TrimPrefix(snippet, "."),
		Source:    "deterministic_ast",
	}
}

func lastIdentOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, "./#"); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	if s == "" {
		return "handler"
	}
	return s
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
