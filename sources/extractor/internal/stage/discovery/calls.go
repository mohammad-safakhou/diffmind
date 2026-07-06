package discovery

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
		dest := queuePublishDestinationFromCall(idx, cs, platform)
		if dest == "" {
			return // destination not statically resolvable; do not guess
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

func queuePublishDestinationFromCall(idx *astpkg.ProjectIndex, cs astpkg.CallSite, platform string) string {
	if dest := normalizeQueueOrTopicDestination(ResolveResourceName(idx, firstLiteralArg(cs.Arguments)), platform); dest != "" {
		return dest
	}
	fa := idx.Files[cs.File]
	if fa != nil {
		switch fa.Language {
		case "python":
			if dest := pythonQueuePublishDestination(cs, platform); dest != "" {
				return normalizeQueueOrTopicDestination(ResolveResourceName(idx, dest), platform)
			}
		case "javascript", "jsx", "typescript", "tsx":
			if dest := javascriptQueuePublishDestination(cs, platform); dest != "" {
				return normalizeQueueOrTopicDestination(ResolveResourceName(idx, dest), platform)
			}
		case "go":
			if dest := goQueuePublishDestination(idx, cs, platform); dest != "" {
				return normalizeQueueOrTopicDestination(ResolveResourceName(idx, dest), platform)
			}
		}
	}
	return awsQueueDestinationFromCall(idx, cs, platform)
}

func DeterministicAWSQueueConsumers(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	forEachCall(idx, func(cs astpkg.CallSite) {
		platform, ok := MatchQueueConsume(cs)
		if !ok {
			return
		}
		dest := ResolveResourceName(idx, firstLiteralArg(cs.Arguments))
		if dest == "" {
			dest = awsQueueDestinationFromCall(idx, cs, platform)
		}
		dest = normalizeResourceToken(dest)
		if dest == "" {
			return
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
			Type:       "queue_consumer",
			Name:       dest,
			Summary:    fmt.Sprintf("AST-derived %s queue consumer from AWS SDK receive call", platform),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "aws-sdk", platform},
			Details: map[string]any{
				"platform":      platform,
				"queue":         dest,
				"destination":   dest,
				"discovered_by": "ast_aws_queue_receive_call",
			},
			Locations: []candidateLocation{loc},
			Evidence:  []candidateEvidence{callEvidence(cs)},
		})
	})
	return out
}

// DeterministicPythonSQSConsumers finds Python SQS pull-consumers using boto3
// resource/client queue access. This covers worker-style services that are
// wired in code instead of SAM templates.
func DeterministicPythonSQSConsumers(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	forEachCall(idx, func(cs astpkg.CallSite) {
		fa := idx.Files[cs.File]
		if fa == nil || fa.Language != "python" {
			return
		}
		queue, ok := pythonSQSConsumerCall(cs)
		if !ok {
			return
		}
		queue = ResolveResourceName(idx, queue)
		queue = normalizeResourceToken(queue)
		if queue == "" {
			return
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		key := strings.ToLower("sqs|" + queue)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, candidate{
			Type:       "queue_consumer",
			Name:       queue,
			Summary:    "AST-derived Python SQS queue consumer",
			Confidence: 1.0,
			Tags:       []string{"deterministic", "python", "sqs"},
			Details: map[string]any{
				"platform":      "sqs",
				"queue":         queue,
				"destination":   queue,
				"discovered_by": "ast_python_sqs_consumer",
			},
			Locations: []candidateLocation{loc},
			Evidence:  []candidateEvidence{callEvidence(cs)},
		})
	})
	return out
}

func awsQueueDestinationFromCall(idx *astpkg.ProjectIndex, cs astpkg.CallSite, platform string) string {
	window := callWindowSource(idx, cs, 10)
	for _, name := range []string{"queueUrl", "queueName", "queue", "topicArn", "topic"} {
		if value := javaBuilderStringValue(window, name); value != "" {
			return normalizeQueueOrTopicDestination(ResolveResourceName(idx, value), platform)
		}
	}
	if platform == "sns" {
		if value := javaAssignedStringValue(window, "topicArn"); value != "" {
			return normalizeQueueOrTopicDestination(ResolveResourceName(idx, value), platform)
		}
	}
	if value := javaAssignedStringValue(window, "queueUrl"); value != "" {
		return normalizeQueueOrTopicDestination(ResolveResourceName(idx, value), platform)
	}
	return ""
}

func normalizeQueueOrTopicDestination(raw, platform string) string {
	raw = strings.TrimSpace(stripPlaceholderDefault(raw))
	if raw == "" {
		return ""
	}
	if strings.ContainsAny(raw, "+\"'") {
		return ""
	}
	if strings.HasPrefix(raw, "arn:") {
		if idx := strings.LastIndex(raw, ":"); idx >= 0 && idx+1 < len(raw) {
			raw = raw[idx+1:]
		}
	}
	if platform == "sqs" && strings.Contains(raw, "://") {
		if idx := strings.LastIndex(raw, "/"); idx >= 0 && idx+1 < len(raw) {
			raw = raw[idx+1:]
		}
	}
	return normalizeResourceToken(raw)
}

func javaBuilderStringValue(src, method string) string {
	if src == "" || method == "" {
		return ""
	}
	re := regexp.MustCompile(`(?s)\.` + regexp.QuoteMeta(method) + `\s*\(\s*(` + javaStringOrIdentPattern + `)`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return javaStringOrIdentValue(src, m[1])
	}
	return ""
}

func javaAssignedStringValue(src, name string) string {
	if src == "" || name == "" {
		return ""
	}
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\s*(?:=|:=)\s*(` + javaStringOrIdentPattern + `)`)
	if m := re.FindStringSubmatch(src); len(m) > 1 {
		return javaTerminalStringOrIdentValue(m[1])
	}
	return ""
}

const javaStringOrIdentPattern = `"([^"\\]|\\.)*"|'([^'\\]|\\.)*'|[A-Za-z_][A-Za-z0-9_$.]*`

func javaStringOrIdentValue(src, expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if strings.HasPrefix(expr, `"`) || strings.HasPrefix(expr, "'") {
		return strings.Trim(expr, `"'`)
	}
	if strings.Contains(expr, ".") {
		last := expr[strings.LastIndex(expr, ".")+1:]
		if !isJavaConstantName(last) {
			return ""
		}
		return trimResourceConstantSuffix(last)
	}
	if assigned := javaAssignedStringValue(src, expr); assigned != "" && assigned != expr {
		return assigned
	}
	if isJavaConstantName(expr) {
		return trimResourceConstantSuffix(expr)
	}
	return ""
}

func javaTerminalStringOrIdentValue(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if strings.HasPrefix(expr, `"`) || strings.HasPrefix(expr, "'") {
		return strings.Trim(expr, `"'`)
	}
	if strings.Contains(expr, ".") {
		expr = expr[strings.LastIndex(expr, ".")+1:]
	}
	if !isJavaConstantName(expr) {
		return ""
	}
	return trimResourceConstantSuffix(expr)
}

func isJavaConstantName(expr string) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}
	for _, r := range expr {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}
	return strings.Contains(expr, "_")
}

func trimResourceConstantSuffix(raw string) string {
	s := strings.TrimSpace(raw)
	for _, suffix := range []string{"_QUEUE_URL", "_QUEUE_NAME", "_TOPIC_ARN", "_TOPIC_NAME", "_BUCKET_NAME", "_BUCKET"} {
		s = strings.TrimSuffix(s, suffix)
	}
	return s
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
	add := func(e candidate) {
		method := strings.ToUpper(firstNonEmptyString(stringAny(e.Details["method"]), "GET"))
		target := firstNonEmptyString(stringAny(e.Details["target_service"]), stringAny(e.Details["service"]), e.Name)
		path := firstNonEmptyString(stringAny(e.Details["path"]), stringAny(e.Details["endpoint"]), stringAny(e.Details["url_template"]))
		key := strings.ToLower(target + "|" + method + "|" + path)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	forEachCall(idx, func(cs astpkg.CallSite) {
		fa := idx.Files[cs.File]
		if fa == nil {
			return
		}
		if fa.Language == "python" {
			method, path, targetName, ok := pythonHTTPCall(fa, cs)
			if !ok {
				return
			}
			loc := callLoc(cs)
			if loc.File == "" {
				return
			}
			details := map[string]any{
				"method":         method,
				"path":           path,
				"url_template":   path,
				"target_service": targetName,
				"discovered_by":  "ast_python_http_call",
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
				Summary:    "AST-derived Python outbound HTTP call",
				Confidence: 1.0,
				Tags:       []string{"deterministic", "requests", "python"},
				Details:    details,
				Locations:  []candidateLocation{loc},
				Evidence:   []candidateEvidence{callEvidence(cs)},
			})
			return
		}
		if fa.Language != "go" {
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
	for _, e := range DeterministicJavaWorkflowCallbackHTTP(idx) {
		add(e)
	}
	for _, e := range DeterministicSpringCloudGatewayHTTP(idx) {
		add(e)
	}
	for _, e := range DeterministicJavaMicronautHTTP(idx) {
		add(e)
	}
	for _, e := range DeterministicJavaInheritedFeignHTTP(idx) {
		add(e)
	}
	for _, e := range DeterministicJavaScriptAxiosHTTP(idx) {
		add(e)
	}
	return out
}

var (
	javaFeignClientInterfaceRe = regexp.MustCompile(`(?s)@FeignClient\s*\((.*?)\)\s*(?:public\s+)?interface\s+([A-Za-z_][A-Za-z0-9_]*)\s+extends\s+([^{]+)\{`)
	javaAnnotationAttrRe       = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*=\s*("[^"]*"|'[^']*')`)
	javaMicronautClientRe      = regexp.MustCompile(`(?s)@Client\s*\((.*?)\)\s*(?:public\s+)?(?:interface|class|abstract\s+class)\s+([A-Za-z_][A-Za-z0-9_]*)[^{]*\{`)
	javaMicronautHTTPMethodRe  = regexp.MustCompile(`@(Get|Post|Put|Patch|Delete)\s*\(\s*("[^"]*"|'[^']*')`)
	springGatewayRouteKeyRe    = regexp.MustCompile(`(?:^|\.)routes(?:\[(\d+)\]|\.(\d+))\.(.+)$`)
)

type springGatewayRoute struct {
	id         string
	uri        string
	path       string
	filter     string
	file       string
	line       int
	uriLine    int
	pathLine   int
	targetHint string
}

// DeterministicSpringCloudGatewayHTTP converts declarative Spring Cloud
// Gateway routes into outbound HTTP dependencies. Gateway services often have
// no direct HTTP client call site: the YAML route is the source of truth for
// proxying traffic to downstream services.
func DeterministicSpringCloudGatewayHTTP(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []candidate
	for _, cf := range sortedConfigFiles(idx) {
		routes := springGatewayRoutesFromConfig(cf)
		for _, route := range routes {
			if route.uri == "" || route.path == "" {
				continue
			}
			targetService := normalizeConfiguredServiceRef(route.id)
			hostTarget := serviceNameFromURLTemplate(route.uri)
			if targetService == "" {
				targetService = hostTarget
			}
			if targetService == "" {
				continue
			}
			line := firstPositive(route.uriLine, route.pathLine, route.line, 1)
			key := strings.ToLower(cf.Path + "|" + targetService + "|" + route.path + "|" + route.uri)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			targetType := "service"
			if isS3GatewayURI(route.uri) {
				targetType = "external"
			}
			details := map[string]any{
				"method":            "ANY",
				"path":              route.path,
				"url_template":      route.uri,
				"base_url":          route.uri,
				"target_service":    targetService,
				"target_type":       targetType,
				"gateway_route_id":  route.id,
				"host_target":       hostTarget,
				"invocation_mode":   "spring_cloud_gateway_route",
				"operation_kind":    "gateway_route",
				"discovered_by":     "config_spring_cloud_gateway_route",
				"config_source":     cf.Path,
				"spring_predicate":  "Path=" + route.path,
				"spring_route_path": route.path,
			}
			if route.filter != "" {
				details["spring_filter"] = route.filter
			}
			out = append(out, candidate{
				Type:       "outbound_http",
				Name:       "ANY " + route.path,
				Summary:    "Config-derived Spring Cloud Gateway route",
				Confidence: 0.95,
				Tags:       []string{"deterministic", "java", "spring-cloud-gateway", "gateway-route"},
				Details:    details,
				Locations:  []candidateLocation{{File: cf.Path, StartLine: line, EndLine: line}},
				Evidence: []candidateEvidence{{
					File:      cf.Path,
					StartLine: line,
					EndLine:   line,
					Snippet:   strings.TrimSpace(route.id + " -> " + route.uri + " " + route.path),
					Source:    "deterministic_config",
				}},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return fmt.Sprint(out[i].Details["target_service"]) < fmt.Sprint(out[j].Details["target_service"])
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedConfigFiles(idx *astpkg.ProjectIndex) []*astpkg.ConfigFile {
	if idx == nil || len(idx.Configs) == 0 {
		return nil
	}
	out := make([]*astpkg.ConfigFile, 0, len(idx.Configs))
	for _, cf := range idx.Configs {
		if cf != nil {
			out = append(out, cf)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func springGatewayRoutesFromConfig(cf *astpkg.ConfigFile) []springGatewayRoute {
	if cf == nil {
		return nil
	}
	byIndex := map[string]*springGatewayRoute{}
	for _, e := range cf.Entries {
		m := springGatewayRouteKeyRe.FindStringSubmatch(e.Key)
		if len(m) < 4 {
			continue
		}
		idx := firstNonEmptyString(m[1], m[2])
		field := strings.ToLower(m[3])
		r := byIndex[idx]
		if r == nil {
			r = &springGatewayRoute{file: cf.Path, line: e.Line}
			byIndex[idx] = r
		}
		if r.line == 0 || (e.Line > 0 && e.Line < r.line) {
			r.line = e.Line
		}
		value := strings.TrimSpace(e.Value)
		switch {
		case field == "id":
			r.id = value
		case field == "uri":
			r.uri = value
			r.uriLine = e.Line
		case strings.HasPrefix(field, "predicates") && strings.HasPrefix(value, "Path="):
			if r.path == "" {
				r.path = strings.TrimSpace(strings.TrimPrefix(value, "Path="))
				r.pathLine = e.Line
			}
		case strings.HasPrefix(field, "filters"):
			if r.filter == "" {
				r.filter = value
			}
		}
	}
	keys := make([]string, 0, len(byIndex))
	for k := range byIndex {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]springGatewayRoute, 0, len(keys))
	for _, k := range keys {
		r := byIndex[k]
		if r == nil {
			continue
		}
		out = append(out, *r)
	}
	return out
}

func isS3GatewayURI(raw string) bool {
	parseable := regexp.MustCompile(`\$\{[^}]+\}`).ReplaceAllString(strings.TrimSpace(raw), "placeholder")
	u, err := url.Parse(parseable)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.Contains(host, ".s3.") || strings.HasSuffix(host, ".amazonaws.com") && strings.Contains(host, "s3")
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// DeterministicJavaMicronautHTTP extracts Micronaut declarative HTTP clients
// declared with @Client and method-level @Get/@Post/etc. These are outbound
// calls even though the syntax looks similar to controller annotations.
func DeterministicJavaMicronautHTTP(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil || idx.RepoRoot == "" {
		return nil
	}
	configURLs := serviceURLConfigValues(idx)
	seen := map[string]struct{}{}
	var out []candidate
	for _, fa := range sortedFiles(idx, "java") {
		src, ok := readIndexedSource(idx, fa.Path)
		if !ok || !strings.Contains(src, "@Client") {
			continue
		}
		for _, m := range javaMicronautClientRe.FindAllStringSubmatchIndex(src, -1) {
			clientArgs := src[m[2]:m[3]]
			clientName := src[m[4]:m[5]]
			clientRef := javaAnnotationClientValue(clientArgs)
			targetService, baseURL, configKey := micronautClientTarget(idx, configURLs, clientRef)
			if targetService == "" {
				continue
			}
			body := javaBlockBody(src, m[1])
			if body == "" {
				continue
			}
			for _, hm := range javaMicronautHTTPMethodRe.FindAllStringSubmatchIndex(body, -1) {
				method := strings.ToUpper(body[hm[2]:hm[3]])
				path := strings.Trim(body[hm[4]:hm[5]], `"'`)
				if path == "" || !strings.HasPrefix(path, "/") {
					continue
				}
				absStart := m[1] + hm[0]
				line := lineNumberAt(src, absStart)
				key := strings.ToLower(targetService + "|" + method + "|" + path + "|" + fa.Path)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				urlTemplate := path
				if baseURL != "" {
					urlTemplate = strings.TrimRight(baseURL, "/") + path
				}
				out = append(out, candidate{
					Type:       "outbound_http",
					Name:       method + " " + path,
					Summary:    "Source-derived Micronaut HTTP client call",
					Confidence: 1.0,
					Tags:       []string{"deterministic", "java", "micronaut", "http-client"},
					Details: map[string]any{
						"method":         method,
						"path":           path,
						"url_template":   urlTemplate,
						"base_url":       baseURL,
						"target_service": targetService,
						"target_type":    "service",
						"client":         clientName,
						"client_ref":     clientRef,
						"config_source":  configKey,
						"discovered_by":  "source_java_micronaut_client",
					},
					Locations: []candidateLocation{{File: fa.Path, StartLine: line, EndLine: line}},
					Evidence: []candidateEvidence{{
						File:      fa.Path,
						StartLine: line,
						EndLine:   line,
						Snippet:   strings.TrimSpace(body[hm[0]:hm[1]]),
						Source:    "deterministic_source",
					}},
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return fmt.Sprint(out[i].Details["target_service"]) < fmt.Sprint(out[j].Details["target_service"])
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func javaAnnotationClientValue(args string) string {
	if v := javaAnnotationStringAttr(args, "value"); v != "" {
		return v
	}
	args = strings.TrimSpace(args)
	if strings.HasPrefix(args, `"`) || strings.HasPrefix(args, `'`) {
		return strings.Trim(args, `"'`)
	}
	return ""
}

func micronautClientTarget(idx *astpkg.ProjectIndex, configURLs map[string]string, raw string) (targetService, baseURL, configKey string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", ""
	}
	configKey = placeholderConfigKey(raw)
	if configKey != "" {
		baseURL = configURLs[configKey]
		targetService = serviceNameFromConfigURLKey(configKey)
		if targetService == "" {
			targetService = serviceNameFromURLTemplate(baseURL)
		}
		return targetService, firstNonEmptyString(baseURL, raw), configKey
	}
	if strings.Contains(raw, "://") {
		return serviceNameFromURLTemplate(raw), raw, ""
	}
	return normalizeConfiguredServiceRef(raw), "", ""
}

func javaBlockBody(src string, bodyStart int) string {
	if bodyStart < 0 || bodyStart >= len(src) {
		return ""
	}
	depth := 1
	for i := bodyStart; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[bodyStart:i]
			}
		}
	}
	return src[bodyStart:]
}

// DeterministicJavaInheritedFeignHTTP detects Feign clients whose endpoint
// methods are inherited from a generated/shared API interface. The local source
// still gives a precise service target through @FeignClient(url=...), but the
// route-level contract is outside the repository, so this intentionally emits a
// service-level ANY dependency rather than inventing endpoint paths.
func DeterministicJavaInheritedFeignHTTP(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil || idx.RepoRoot == "" {
		return nil
	}
	configURLs := serviceURLConfigValues(idx)
	seen := map[string]struct{}{}
	var out []candidate
	for _, fa := range sortedFiles(idx, "java") {
		src, ok := readIndexedSource(idx, fa.Path)
		if !ok || !strings.Contains(src, "@FeignClient") {
			continue
		}
		for _, m := range javaFeignClientInterfaceRe.FindAllStringSubmatchIndex(src, -1) {
			annArgs := src[m[2]:m[3]]
			interfaceName := src[m[4]:m[5]]
			extendsExpr := strings.TrimSpace(src[m[6]:m[7]])
			bodyStart := m[1]
			bodyEnd := strings.Index(src[bodyStart:], "}")
			body := ""
			if bodyEnd >= 0 {
				body = src[bodyStart : bodyStart+bodyEnd]
			}
			if hasLocalHTTPMapping(body) {
				continue
			}
			contract := inheritedFeignContractName(extendsExpr)
			if contract == "" {
				continue
			}
			urlExpr := javaAnnotationStringAttr(annArgs, "url")
			nameExpr := javaAnnotationStringAttr(annArgs, "name")
			configKey := placeholderConfigKey(urlExpr)
			urlTemplate := strings.TrimSpace(urlExpr)
			if configKey != "" {
				if configured := configURLs[configKey]; configured != "" {
					urlTemplate = configured
				}
			}
			targetService := ""
			if configKey != "" {
				targetService = serviceNameFromConfigURLKey(configKey)
			}
			if targetService == "" {
				targetService = serviceNameFromURLTemplate(urlTemplate)
			}
			if targetService == "" {
				targetService = normalizeConfiguredServiceRef(nameExpr)
			}
			if targetService == "" {
				continue
			}
			line := lineNumberAt(src, m[0])
			key := strings.ToLower(targetService + "|" + interfaceName + "|" + contract)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, candidate{
				Type:       "outbound_http",
				Name:       "ANY inherited Feign " + contract,
				Summary:    "Source-derived Feign client with inherited/generated HTTP contract",
				Confidence: 0.82,
				Tags:       []string{"deterministic", "java", "feign", "inherited-contract"},
				Details: map[string]any{
					"method":             "ANY",
					"url_template":       firstNonEmptyString(urlTemplate, urlExpr),
					"base_url":           firstNonEmptyString(urlTemplate, urlExpr),
					"target_service":     targetService,
					"target_type":        "service",
					"config_source":      configKey,
					"feign_client":       interfaceName,
					"inherited_contract": contract,
					"invocation_mode":    "generated_feign_contract",
					"operation_kind":     "inherited_contract",
					"discovered_by":      "source_java_inherited_feign_client",
				},
				Locations: []candidateLocation{{File: fa.Path, StartLine: line, EndLine: line}},
				Evidence: []candidateEvidence{{
					File:      fa.Path,
					StartLine: line,
					EndLine:   line,
					Snippet:   strings.TrimSpace(src[m[0]:m[1]]),
					Source:    "deterministic_source",
				}},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func hasLocalHTTPMapping(src string) bool {
	for _, marker := range []string{"@GetMapping", "@PostMapping", "@PutMapping", "@PatchMapping", "@DeleteMapping", "@RequestMapping"} {
		if strings.Contains(src, marker) {
			return true
		}
	}
	return false
}

func inheritedFeignContractName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimSuffix(raw, "{")
	if comma := strings.Index(raw, ","); comma >= 0 {
		raw = raw[:comma]
	}
	fields := strings.Fields(raw)
	if len(fields) > 0 {
		raw = fields[0]
	}
	if dot := strings.LastIndex(raw, "."); dot >= 0 {
		raw = raw[dot+1:]
	}
	return strings.TrimSpace(raw)
}

func javaAnnotationStringAttr(args, attr string) string {
	for _, m := range javaAnnotationAttrRe.FindAllStringSubmatch(args, -1) {
		if m[1] == attr {
			return strings.Trim(m[2], `"'`)
		}
	}
	return ""
}

func placeholderConfigKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "${") && strings.HasSuffix(raw, "}") {
		inner := strings.TrimSuffix(strings.TrimPrefix(raw, "${"), "}")
		if idx := strings.Index(inner, ":"); idx >= 0 {
			inner = inner[:idx]
		}
		return strings.TrimSpace(inner)
	}
	return ""
}

func serviceNameFromConfigURLKey(key string) string {
	key = strings.TrimSpace(key)
	key = strings.TrimPrefix(key, "services.")
	key = strings.TrimSuffix(key, ".url")
	key = strings.TrimSuffix(key, ".uri")
	key = strings.TrimSuffix(key, ".base-url")
	if strings.HasPrefix(key, "cdp.") {
		key = strings.TrimPrefix(key, "cdp.")
	}
	return normalizeConfiguredServiceRef(key)
}

// DeterministicJavaWorkflowCallbackHTTP finds service URLs that are assembled
// from typed Spring configuration and passed onward as workflow/callback
// payload values. This is not a direct client invocation, so the HTTP method is
// intentionally "ANY" unless the source code gives a verb elsewhere.
func DeterministicJavaWorkflowCallbackHTTP(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil || idx.RepoRoot == "" {
		return nil
	}
	configURLs := serviceURLConfigValues(idx)
	repoConstants := repositoryJavaStringConstants(idx)
	seen := map[string]struct{}{}
	var out []candidate
	for _, fa := range sortedFiles(idx, "java") {
		src, ok := readIndexedSource(idx, fa.Path)
		if !ok || !strings.Contains(src, ".getUrl()") {
			continue
		}
		constants := javaStringConstants(src)
		for k, v := range repoConstants {
			if _, exists := constants[k]; !exists {
				constants[k] = v
			}
		}
		for _, m := range javaWorkflowURLExprRe.FindAllStringSubmatchIndex(src, -1) {
			serviceExpr := src[m[2]:m[3]]
			pathExpr := src[m[4]:m[5]]
			serviceKey := resolveJavaStringExpr(serviceExpr, constants)
			path := resolveJavaStringExpr(pathExpr, constants)
			if serviceKey == "" || path == "" || !strings.HasPrefix(path, "/") {
				continue
			}
			configKey := "services.cdp." + serviceKey + ".url"
			baseURL := configURLs[configKey]
			targetService := normalizeConfiguredServiceRef(serviceKey)
			if baseURL != "" {
				if hostTarget := serviceNameFromURLTemplate(baseURL); hostTarget != "" {
					targetService = hostTarget
				}
			}
			if targetService == "" {
				continue
			}
			line := lineNumberAt(src, m[0])
			key := strings.ToLower(targetService + "|" + path + "|" + fa.Path)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			urlTemplate := path
			if baseURL != "" {
				urlTemplate = strings.TrimRight(baseURL, "/") + path
			}
			out = append(out, candidate{
				Type:       "outbound_http",
				Name:       "ANY " + path,
				Summary:    "Source-derived workflow callback URL to HTTP service",
				Confidence: 0.9,
				Tags:       []string{"deterministic", "java", "workflow-callback-url"},
				Details: map[string]any{
					"method":          "ANY",
					"path":            path,
					"url_template":    urlTemplate,
					"target_service":  targetService,
					"target_type":     "service",
					"config_key":      configKey,
					"invocation_mode": "workflow_callback_url",
					"discovered_by":   "source_java_workflow_callback_url",
				},
				Locations: []candidateLocation{{File: fa.Path, StartLine: line, EndLine: line}},
				Evidence: []candidateEvidence{{
					File:      fa.Path,
					StartLine: line,
					EndLine:   line,
					Snippet:   strings.TrimSpace(src[m[0]:m[1]]),
					Source:    "deterministic_source",
				}},
			})
		}
	}
	return out
}

// DeterministicJavaScriptAxiosHTTP follows simple axios.create({baseURL: ...})
// instances to instance.get/post/put/patch/delete calls in JS/TS clients.
func DeterministicJavaScriptAxiosHTTP(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil || idx.RepoRoot == "" {
		return nil
	}
	urlConstants := repositoryURLConstants(idx)
	seen := map[string]struct{}{}
	var out []candidate
	for _, fa := range sortedFiles(idx, "javascript", "jsx", "typescript", "tsx") {
		src, ok := readIndexedSource(idx, fa.Path)
		if !ok || !strings.Contains(src, "axios.create") {
			continue
		}
		localConstants := jsStringConstants(src)
		for k, v := range urlConstants {
			if _, exists := localConstants[k]; !exists {
				localConstants[k] = v
			}
		}
		applyJSURLGetters(src, localConstants)
		instances := axiosInstances(src, localConstants)
		for inst, baseURL := range instances {
			targetService := serviceNameFromURLTemplate(baseURL)
			if targetService == "" {
				continue
			}
			callRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(inst) + `\.(get|post|put|patch|delete)\s*\(\s*(['"` + "`" + `])([^'"` + "`" + `]+)`)
			for _, m := range callRe.FindAllStringSubmatchIndex(src, -1) {
				method := strings.ToUpper(src[m[2]:m[3]])
				path := strings.TrimSpace(src[m[6]:m[7]])
				if path == "" || !strings.HasPrefix(path, "/") {
					continue
				}
				line := lineNumberAt(src, m[0])
				key := strings.ToLower(inst + "|" + method + "|" + path)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, candidate{
					Type:       "outbound_http",
					Name:       method + " " + path,
					Summary:    "Source-derived axios outbound HTTP call",
					Confidence: 1.0,
					Tags:       []string{"deterministic", "javascript", "typescript", "axios"},
					Details: map[string]any{
						"method":         method,
						"path":           path,
						"url_template":   strings.TrimRight(baseURL, "/") + path,
						"base_url":       baseURL,
						"target_service": targetService,
						"target_type":    "service",
						"client":         inst,
						"discovered_by":  "source_js_axios_instance",
					},
					Locations: []candidateLocation{{File: fa.Path, StartLine: line, EndLine: line}},
					Evidence: []candidateEvidence{{
						File:      fa.Path,
						StartLine: line,
						EndLine:   line,
						Snippet:   strings.TrimSpace(src[m[0]:m[1]]),
						Source:    "deterministic_source",
					}},
				})
			}
		}
	}
	return out
}

func pythonHTTPCall(fa *astpkg.FileAST, cs astpkg.CallSite) (method, path, target string, ok bool) {
	receiver, callee := splitCall(cs)
	r := strings.ToLower(strings.TrimSpace(receiver))
	c := strings.ToLower(strings.TrimSpace(callee))
	switch {
	case r == "requests" && c == "request":
		if len(cs.Arguments) < 2 {
			return "", "", "", false
		}
		method = strings.ToUpper(strings.Trim(firstLiteralArg(cs.Arguments[:1]), "\"'`"))
		path = pythonURLTemplateFromArg(cs.Arguments[1])
	case r == "requests" && isHTTPVerb(c):
		method = strings.ToUpper(c)
		if len(cs.Arguments) > 0 {
			path = pythonURLTemplateFromArg(cs.Arguments[0])
		}
	case r == "self" && isHTTPVerb(c) && looksLikePythonAPIClientFile(cs.File):
		method = strings.ToUpper(c)
		if len(cs.Arguments) > 0 {
			path = pythonURLTemplateFromArg(cs.Arguments[0])
		}
	default:
		return "", "", "", false
	}
	if method == "" || path == "" {
		return "", "", "", false
	}
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(strings.ToLower(path), "http://") && !strings.HasPrefix(strings.ToLower(path), "https://") {
		return "", "", "", false
	}
	target = serviceNameFromPythonHTTPPath(cs.File)
	return method, path, target, true
}

func pythonSQSConsumerCall(cs astpkg.CallSite) (string, bool) {
	receiver, callee := splitCall(cs)
	r := strings.ToLower(strings.TrimSpace(receiver))
	c := strings.ToLower(strings.TrimSpace(callee))
	switch c {
	case "get_queue_by_name":
		if !strings.Contains(r, "sqs") && r != "" {
			return "", false
		}
		if queue := pythonKeywordOrFirstArg(cs.Arguments, "QueueName", "queue_name"); queue != "" {
			return queue, true
		}
	case "receive_message":
		if !strings.Contains(r, "sqs") && !strings.Contains(r, "queue") {
			return "", false
		}
		if queue := pythonKeywordOrFirstArg(cs.Arguments, "QueueUrl", "queue_url"); queue != "" {
			return queue, true
		}
	}
	return "", false
}

func pythonQueuePublishDestination(cs astpkg.CallSite, platform string) string {
	_, callee := splitCall(cs)
	c := strings.ToLower(strings.TrimSpace(callee))
	switch platform {
	case "sqs":
		if c == "send_message" || c == "send_message_batch" {
			return pythonKeywordOrFirstArg(cs.Arguments, "QueueUrl", "queue_url", "QueueName", "queue_name")
		}
	case "sns":
		if c == "publish" {
			return pythonKeywordOrFirstArg(cs.Arguments, "TopicArn", "topic_arn", "Topic", "topic")
		}
	case "kafka":
		if c == "send" || c == "produce" {
			return pythonKeywordOrFirstArg(cs.Arguments, "topic", "topic_name")
		}
	}
	return ""
}

func pythonKeywordOrFirstArg(args []astpkg.ArgumentExpr, keys ...string) string {
	for _, arg := range args {
		src := strings.TrimSpace(arg.Source)
		for _, key := range keys {
			prefix := key + "="
			if strings.HasPrefix(src, prefix) {
				return pythonResourceValue(strings.TrimSpace(strings.TrimPrefix(src, prefix)))
			}
		}
	}
	if len(args) > 0 {
		return pythonResourceValue(args[0].Source)
	}
	return ""
}

func pythonResourceValue(src string) string {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	if strings.HasPrefix(src, "${") {
		return src
	}
	if strings.HasPrefix(src, "\"") || strings.HasPrefix(src, "'") || strings.HasPrefix(src, "`") {
		return strings.Trim(src, "\"'`")
	}
	if strings.Contains(src, ".") {
		last := src[strings.LastIndex(src, ".")+1:]
		last = strings.Trim(last, "\"'`")
		if !isPythonResourceConstant(last) {
			return ""
		}
		return trimPythonResourceConstantSuffix(last)
	}
	src = strings.Trim(src, "\"'`")
	if isPythonResourceConstant(src) {
		return trimPythonResourceConstantSuffix(src)
	}
	return ""
}

func isPythonResourceConstant(src string) bool {
	src = strings.TrimSpace(src)
	if src == "" {
		return false
	}
	for _, r := range src {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}
	return strings.Contains(src, "_")
}

func trimPythonResourceConstantSuffix(src string) string {
	src = strings.TrimSuffix(src, "_QUEUE_NAME")
	src = strings.TrimSuffix(src, "_QUEUE_URL")
	src = strings.TrimSuffix(src, "_TOPIC_ARN")
	src = strings.TrimSuffix(src, "_TOPIC_NAME")
	src = strings.TrimSuffix(src, "_URL")
	src = strings.TrimSuffix(src, "_NAME")
	return src
}

func javascriptQueuePublishDestination(cs astpkg.CallSite, platform string) string {
	_, callee := splitCall(cs)
	c := strings.ToLower(strings.TrimSpace(callee))
	switch platform {
	case "sqs":
		if c == "sendmessage" || c == "sendmessagebatch" {
			return javascriptObjectStringValue(firstArgSource(cs.Arguments), "QueueUrl", "QueueName")
		}
		if c == "send" {
			return javascriptAWSCommandObjectValue(cs.Arguments, "SendMessageCommand", "QueueUrl", "QueueName")
		}
	case "sns":
		if c == "publish" {
			return javascriptObjectStringValue(firstArgSource(cs.Arguments), "TopicArn", "Topic")
		}
		if c == "send" {
			return javascriptAWSCommandObjectValue(cs.Arguments, "PublishCommand", "TopicArn", "Topic")
		}
	case "kafka":
		if c == "send" {
			return javascriptObjectStringValue(firstArgSource(cs.Arguments), "topic")
		}
		if c == "produce" {
			if value := firstLiteralArg(cs.Arguments); value != "" {
				return value
			}
			return javascriptObjectStringValue(firstArgSource(cs.Arguments), "topic")
		}
	}
	return ""
}

func javascriptAWSCommandObjectValue(args []astpkg.ArgumentExpr, command string, keys ...string) string {
	for _, arg := range args {
		src := strings.TrimSpace(arg.Source)
		if src == "" || !strings.Contains(src, command) {
			continue
		}
		if value := javascriptObjectStringValue(src, keys...); value != "" {
			return value
		}
	}
	return ""
}

func javascriptObjectStringValue(src string, keys ...string) string {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	for _, key := range keys {
		re := regexp.MustCompile(`(?s)(?:\b` + regexp.QuoteMeta(key) + `\b|["']` + regexp.QuoteMeta(key) + `["'])\s*:\s*["'` + "`" + `]([^"'` + "`" + `]+)["'` + "`" + `]`)
		if m := re.FindStringSubmatch(src); len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func goQueuePublishDestination(idx *astpkg.ProjectIndex, cs astpkg.CallSite, platform string) string {
	switch platform {
	case "sqs":
		return goCompositeStringField(cs.Arguments, "QueueUrl", "QueueName")
	case "sns":
		return goCompositeStringField(cs.Arguments, "TopicArn", "Topic")
	case "kafka":
		if value := goCompositeStringField(cs.Arguments, "Topic"); value != "" {
			return value
		}
		return goKafkaWriterTopicFromWindow(idx, cs)
	}
	return ""
}

func goCompositeStringField(args []astpkg.ArgumentExpr, fields ...string) string {
	for _, arg := range args {
		if value := goStringFieldValue(arg.Source, fields...); value != "" {
			return value
		}
	}
	return ""
}

func goStringFieldValue(src string, fields ...string) string {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	for _, field := range fields {
		re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(field) + `\s*:\s*(?:[A-Za-z_][A-Za-z0-9_./]*\.String\s*\(\s*)?(` + goStringLiteralPattern + `)`)
		if m := re.FindStringSubmatch(src); len(m) > 1 {
			return unquoteGoString(m[1])
		}
	}
	return ""
}

func goKafkaWriterTopicFromWindow(idx *astpkg.ProjectIndex, cs astpkg.CallSite) string {
	receiver, _ := splitCall(cs)
	receiver = strings.TrimSpace(receiver)
	if receiver == "" {
		return ""
	}
	window := callWindowSource(idx, cs, 20)
	if window == "" {
		return ""
	}
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(receiver) + `\s*(?::=|=)\s*&?(?:kafka\.)?Writer\s*\{[^}]*\bTopic\s*:\s*(` + goStringLiteralPattern + `)`)
	if m := re.FindStringSubmatch(window); len(m) > 1 {
		return unquoteGoString(m[1])
	}
	re = regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(receiver) + `\s*(?::=|=)\s*kafka\.NewWriter\s*\(\s*kafka\.WriterConfig\s*\{[^}]*\bTopic\s*:\s*(` + goStringLiteralPattern + `)`)
	if m := re.FindStringSubmatch(window); len(m) > 1 {
		return unquoteGoString(m[1])
	}
	return ""
}

func pythonURLTemplateFromArg(arg astpkg.ArgumentExpr) string {
	src := strings.TrimSpace(arg.Source)
	if src == "" {
		return ""
	}
	if arg.Kind == "literal" || strings.HasPrefix(src, "f\"") || strings.HasPrefix(src, "f'") || strings.HasPrefix(src, "F\"") || strings.HasPrefix(src, "F'") {
		return normalizePythonStringTemplate(src)
	}
	return ""
}

func normalizePythonStringTemplate(src string) string {
	src = strings.TrimSpace(src)
	if len(src) >= 2 && (src[0] == 'f' || src[0] == 'F') {
		src = src[1:]
	}
	src = strings.Trim(src, `"'`)
	out := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(src, "{value}")
	return out
}

func looksLikePythonAPIClientFile(path string) bool {
	p := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return strings.Contains(p, "api_client") || strings.Contains(p, "api_clients") || strings.Contains(p, "/client")
}

func serviceNameFromPythonHTTPPath(path string) string {
	p := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	base := p
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".py")
	for _, suffix := range []string{"_client", "-client"} {
		base = strings.TrimSuffix(base, suffix)
	}
	base = strings.NewReplacer("_", "-", ".", "-").Replace(base)
	base = strings.Trim(base, "-")
	if base == "" || base == "http-base" || base == "base" {
		return ""
	}
	return base
}

func isHTTPVerb(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return true
	default:
		return false
	}
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
	compactR := strings.ReplaceAll(r, "_", "")
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
		{"kafkaproducer", "kafka", []string{"send", "produce"}},
	}
	for _, p := range pats {
		if (strings.Contains(r, p.token) || strings.Contains(compactR, p.token)) && containsStr(p.callees, c) {
			return p.platform, true
		}
	}
	switch {
	case (r == "sqs" || strings.Contains(r, "sqs_client") || strings.Contains(r, "sqsclient")) && (c == "send_message" || c == "send_message_batch"):
		return "sqs", true
	case (r == "sns" || strings.Contains(r, "sns_client") || strings.Contains(r, "snsclient")) && c == "publish":
		return "sns", true
	case (strings.Contains(r, "kafka") || r == "producer") && (c == "send" || c == "produce") && queuePublishArgHasTopic(cs.Arguments):
		return "kafka", true
	case (strings.Contains(compactR, "producer") || strings.Contains(compactR, "kafka")) && c == "sendmessage" && queuePublishArgHasTopic(cs.Arguments):
		return "kafka", true
	case c == "writemessages" && (strings.Contains(compactR, "kafka") || r == "writer" || strings.HasSuffix(r, "writer")):
		return "kafka", true
	case strings.Contains(r, "sqsclient") && c == "send" && queuePublishArgMentions(cs.Arguments, "SendMessageCommand"):
		return "sqs", true
	case strings.Contains(r, "snsclient") && c == "send" && queuePublishArgMentions(cs.Arguments, "PublishCommand"):
		return "sns", true
	}
	return "", false
}

func MatchQueueConsume(cs astpkg.CallSite) (string, bool) {
	rr, cc := splitCall(cs)
	r := strings.ToLower(rr)
	c := strings.ToLower(cc)
	switch {
	case strings.Contains(r, "sqsclient") && c == "receivemessage":
		return "sqs", true
	case strings.Contains(r, "amazonsqs") && c == "receivemessage":
		return "sqs", true
	default:
		return "", false
	}
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

func firstArgSource(args []astpkg.ArgumentExpr) string {
	if len(args) == 0 {
		return ""
	}
	return strings.TrimSpace(args[0].Source)
}

func queuePublishArgMentions(args []astpkg.ArgumentExpr, needle string) bool {
	for _, arg := range args {
		if strings.Contains(arg.Source, needle) {
			return true
		}
	}
	return false
}

func queuePublishArgHasKey(args []astpkg.ArgumentExpr, key string) bool {
	for _, arg := range args {
		if javascriptObjectStringValue(arg.Source, key) != "" {
			return true
		}
	}
	return false
}

func queuePublishArgHasTopic(args []astpkg.ArgumentExpr) bool {
	return queuePublishArgHasKey(args, "topic") || queuePublishArgHasKey(args, "Topic") || goCompositeStringField(args, "Topic") != ""
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

var javaWorkflowURLExprRe = regexp.MustCompile(`servicesConfig\s*\.\s*getCdp\s*\(\s*\)\s*\.\s*get\s*\(\s*([^)]+?)\s*\)\s*\.\s*getUrl\s*\(\s*\)\s*\+\s*([A-Za-z_][A-Za-z0-9_]*|"[^"]+"|'[^']+')`)

func sortedFiles(idx *astpkg.ProjectIndex, languages ...string) []*astpkg.FileAST {
	allowed := map[string]bool{}
	for _, lang := range languages {
		allowed[lang] = true
	}
	paths := make([]string, 0, len(idx.Files))
	for path, fa := range idx.Files {
		if fa != nil && allowed[fa.Language] {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	out := make([]*astpkg.FileAST, 0, len(paths))
	for _, path := range paths {
		out = append(out, idx.Files[path])
	}
	return out
}

func readIndexedSource(idx *astpkg.ProjectIndex, rel string) (string, bool) {
	if idx == nil || idx.RepoRoot == "" || strings.TrimSpace(rel) == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(idx.RepoRoot, filepath.Clean(rel)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func serviceURLConfigValues(idx *astpkg.ProjectIndex) map[string]string {
	out := map[string]string{}
	if idx == nil {
		return out
	}
	for _, cf := range idx.Configs {
		if cf == nil {
			continue
		}
		for _, entry := range cf.Entries {
			key := strings.TrimSpace(entry.Key)
			value := strings.TrimSpace(entry.Value)
			if key == "" || value == "" {
				continue
			}
			lowerKey := strings.ToLower(key)
			if strings.Contains(lowerKey, ".url") || strings.Contains(lowerKey, ".uri") || strings.Contains(lowerKey, ".base-url") {
				out[key] = value
			}
		}
	}
	return out
}

func javaStringConstants(src string) map[string]string {
	out := map[string]string{}
	re := regexp.MustCompile(`(?m)\b(?:public|private|protected)?\s*(?:static\s+)?(?:final\s+)?String\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out[m[1]] = m[2]
	}
	return out
}

func repositoryJavaStringConstants(idx *astpkg.ProjectIndex) map[string]string {
	out := map[string]string{}
	for _, fa := range sortedFiles(idx, "java") {
		src, ok := readIndexedSource(idx, fa.Path)
		if !ok {
			continue
		}
		for k, v := range javaStringConstants(src) {
			out[k] = v
		}
	}
	return out
}

func resolveJavaStringExpr(expr string, constants map[string]string) string {
	expr = strings.TrimSpace(expr)
	expr = strings.Trim(expr, "()")
	if len(expr) >= 2 {
		if expr[0] == '"' && expr[len(expr)-1] == '"' || expr[0] == '\'' && expr[len(expr)-1] == '\'' {
			return strings.Trim(expr, `"'`)
		}
	}
	return strings.TrimSpace(constants[expr])
}

func repositoryURLConstants(idx *astpkg.ProjectIndex) map[string]string {
	out := map[string]string{}
	for _, fa := range sortedFiles(idx, "javascript", "jsx", "typescript", "tsx") {
		src, ok := readIndexedSource(idx, fa.Path)
		if !ok {
			continue
		}
		for k, v := range jsStringConstants(src) {
			if strings.HasPrefix(strings.ToLower(v), "http://") || strings.HasPrefix(strings.ToLower(v), "https://") {
				out[k] = v
			}
		}
	}
	return out
}

func jsStringConstants(src string) map[string]string {
	out := map[string]string{}
	re := regexp.MustCompile(`(?m)\b(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out[m[1]] = m[2]
	}
	getURLRe := regexp.MustCompile(`(?m)\b(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*apiUrlsInstance\.getUrl\(\s*['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]\s*\)`)
	repoConstants := map[string]string{}
	for k, v := range out {
		repoConstants[k] = v
	}
	for _, m := range getURLRe.FindAllStringSubmatch(src, -1) {
		if v := repoConstants[m[2]]; v != "" {
			out[m[1]] = v
		}
	}
	return out
}

func applyJSURLGetters(src string, constants map[string]string) {
	getURLRe := regexp.MustCompile(`(?m)\b(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*apiUrlsInstance\.getUrl\(\s*['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]\s*\)`)
	for _, m := range getURLRe.FindAllStringSubmatch(src, -1) {
		if v := constants[m[2]]; v != "" {
			constants[m[1]] = v
		}
	}
}

func axiosInstances(src string, constants map[string]string) map[string]string {
	out := map[string]string{}
	re := regexp.MustCompile(`(?s)\b(?:export\s+)?(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)(?:\s*:[^=]+)?=\s*axios\.create\s*\(\s*\{(.*?)\}\s*\)`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		name := m[1]
		body := m[2]
		base := axiosBaseURL(body, constants)
		if base != "" {
			out[name] = base
		}
	}
	return out
}

func axiosBaseURL(body string, constants map[string]string) string {
	for _, pattern := range []string{
		`baseURL\s*:\s*` + "`" + `\$\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}` + "`",
		`baseURL\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\b`,
		`baseURL\s*:\s*['"]([^'"]+)['"]`,
		`baseURL\s*:\s*` + "`" + `([^` + "`" + `]+)` + "`",
	} {
		re := regexp.MustCompile(pattern)
		if m := re.FindStringSubmatch(body); len(m) == 2 {
			raw := strings.TrimSpace(m[1])
			if v := constants[raw]; v != "" {
				return v
			}
			if strings.HasPrefix(strings.ToLower(raw), "http://") || strings.HasPrefix(strings.ToLower(raw), "https://") {
				return raw
			}
		}
	}
	re := regexp.MustCompile(`baseURL\s*:\s*([^,\n]+)`)
	m := re.FindStringSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	raw := strings.TrimSpace(strings.TrimSuffix(m[1], ","))
	raw = strings.Trim(raw, `"'`+"`")
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "${") && strings.HasSuffix(raw, "}") {
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "${"), "}"))
	}
	if v := constants[raw]; v != "" {
		return v
	}
	if strings.HasPrefix(strings.ToLower(raw), "http://") || strings.HasPrefix(strings.ToLower(raw), "https://") {
		return raw
	}
	return ""
}

func serviceNameFromURLTemplate(raw string) string {
	raw = strings.TrimSpace(stripPlaceholderDefault(raw))
	if raw == "" {
		return ""
	}
	parseable := regexp.MustCompile(`\$\{[^}]+\}`).ReplaceAllString(raw, "placeholder")
	u, err := url.Parse(parseable)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range []string{
		".example.global",
		".example.biz",
		".aws-sdlc-example.com",
		".svc.cluster.local",
		".cluster.local",
	} {
		host = strings.TrimSuffix(host, suffix)
	}
	host = strings.TrimPrefix(host, "www.")
	host = regexp.MustCompile(`^[a-z0-9]+-placeholder-`).ReplaceAllString(host, "")
	host = regexp.MustCompile(`^cdp-\$\{[^}]+\}-`).ReplaceAllString(host, "cdp-")
	return normalizeConfiguredServiceRef(host)
}

func lineNumberAt(src string, offset int) int {
	if offset < 0 {
		return 1
	}
	if offset > len(src) {
		offset = len(src)
	}
	return strings.Count(src[:offset], "\n") + 1
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
