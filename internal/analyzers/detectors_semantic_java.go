package analyzers

import (
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
)

var (
	javaQuotedStringRe          = regexp.MustCompile(`"([^"]+)"`)
	javaNamedPathValueRe        = regexp.MustCompile(`(?i)(path|value)\s*=\s*"([^"]+)"`)
	javaRequestMethodRefRe      = regexp.MustCompile(`RequestMethod\.([A-Z]+)`)
	javaRequestMethodAllRefRe   = regexp.MustCompile(`RequestMethod\.([A-Z]+)`)
	javaRestTemplateCallRe      = regexp.MustCompile(`(?i)\.(getForObject|getForEntity|postForObject|postForEntity|put|delete|exchange)\(\s*"([^"]+)"`)
	javaFeignClientAnnRe        = regexp.MustCompile(`@FeignClient\((.*)\)`)
	javaFeignClientURLRe        = regexp.MustCompile(`(?i)\burl\s*=\s*"([^"]+)"`)
	javaFeignClientNameRe       = regexp.MustCompile(`(?i)\b(?:name|value)\s*=\s*"([^"]+)"`)
	javaFeignClientPathRe       = regexp.MustCompile(`(?i)\bpath\s*=\s*"([^"]+)"`)
	javaRestExchangeVerbRe      = regexp.MustCompile(`HttpMethod\.([A-Z]+)`)
	javaScheduledExprRe         = regexp.MustCompile(`(?i)(cron|fixedRate|fixedDelay)\s*=\s*("?[^,\)]+"?)`)
	javaQueueURLBuilderStringRe = regexp.MustCompile(`(?i)\bqueueUrl\(\s*"([^"]+)"\s*\)`)
	javaQueueURLBuilderVarRe    = regexp.MustCompile(`(?i)\bqueueUrl\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)`)
	javaRequestBuilderAssignRe  = regexp.MustCompile(`(?i)\b(?:[A-Za-z0-9_<>\[\], ?]+\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:SendMessageRequest|ReceiveMessageRequest)\.builder\(`)
	javaCallArgVarRe            = regexp.MustCompile(`\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:[,)]|$)`)
	javaReceiveMessageCallRe    = regexp.MustCompile(`(?i)\b(sqsClient|amazonSqs|amazonSQS|sqsAsyncClient|sqs)\s*\.\s*receiveMessage\(`)
	javaSendMessageCallRe       = regexp.MustCompile(`(?i)\b(sqsClient|amazonSqs|amazonSQS|sqsAsyncClient|sqs)\s*\.\s*sendMessage\(`)
	javaKafkaSendCallRe         = regexp.MustCompile(`(?i)\b(kafkaTemplate|kafkaProducer|kafkaOperations)\s*\.\s*send(Default)?\(`)
	javaRabbitPublishCallRe     = regexp.MustCompile(`(?i)\b(rabbitTemplate|amqpTemplate)\s*\.\s*(convertAndSend|send)\(`)
	javaValueFieldRe            = regexp.MustCompile(`^\s*(?:private|protected|public)?\s*(?:final\s+)?[A-Za-z0-9_<>\[\], ?]+\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:=.*)?;`)
	javaJdbcReadCallRe          = regexp.MustCompile(`(?i)\.(query|queryForObject|queryForList|queryForMap)\(`)
	javaJdbcWriteCallRe         = regexp.MustCompile(`(?i)\.(update|batchUpdate)\(`)
	javaJpaReadCallRe           = regexp.MustCompile(`(?i)\.(createQuery|createNamedQuery)\(`)
	javaJpaWriteCallRe          = regexp.MustCompile(`(?i)\.(persist|merge|remove)\(`)
	javaRuntimeExecRe           = regexp.MustCompile(`(?i)Runtime\.getRuntime\(\)\.exec\(`)
	javaProcessBuilderRe        = regexp.MustCompile(`(?i)new\s+ProcessBuilder\(`)
	javaCommandArgRe            = regexp.MustCompile(`(?i)(exec|ProcessBuilder)\(\s*"([^"]+)"`)
)

func detectJavaInboundEndpointsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJavaAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	walkJava(root, func(classNode *sitter.Node) {
		if classNode == nil {
			return
		}
		if classNode.Kind() != "class_declaration" && classNode.Kind() != "interface_declaration" {
			return
		}

		classModifiers := javaModifiersNode(classNode)
		classAnnotations := javaAnnotationTexts(classModifiers, content)
		isController := javaHasControllerAnnotation(classAnnotations)
		classBasePaths := []string{""}
		if isController {
			classBasePaths = javaControllerBasePaths(classAnnotations)
		}

		body := javaDeclarationBody(classNode)
		if body == nil {
			return
		}

		for i := uint(0); i < body.NamedChildCount(); i++ {
			methodNode := body.NamedChild(i)
			if methodNode == nil || methodNode.Kind() != "method_declaration" {
				continue
			}
			methodModifiers := javaModifiersNode(methodNode)
			methodAnnotations := javaAnnotationTexts(methodModifiers, content)
			if len(methodAnnotations) == 0 {
				continue
			}

			if isController {
				for _, ann := range methodAnnotations {
					methods, paths, matched := parseSpringMappingAnnotationDetailed(ann)
					if !matched {
						continue
					}
					expandedPaths := combineControllerAndMethodPaths(classBasePaths, paths)
					line, col := javaLineCol(methodNode)
					for _, m := range methods {
						for _, p := range expandedPaths {
							c.addFactWithEvidence("Endpoint", map[string]any{
								"direction": "inbound",
								"method":    m,
								"path":      p,
								"framework": "spring-semantic",
							}, file, line, col, javaLineSnippet(file, line), func() { c.report.Endpoints++ })
						}
					}
				}
			}

			for _, ann := range methodAnnotations {
				if !strings.Contains(ann, "@Scheduled") {
					continue
				}
				scheduleKind, scheduleExpr := parseJavaScheduledAnnotation(ann)
				line, col := javaLineCol(methodNode)
				c.addFactWithEvidence("Endpoint", map[string]any{
					"direction":     "inbound",
					"method":        "SCHEDULE",
					"path":          scheduleExpr,
					"framework":     "spring-scheduler-semantic",
					"schedule_kind": scheduleKind,
				}, file, line, col, javaLineSnippet(file, line), func() { c.report.Endpoints++ })
			}

			for _, ann := range methodAnnotations {
				target, queueKind, ok := parseJavaQueueListenerAnnotation(ann)
				if !ok {
					continue
				}
				if target == "" {
					target = queueKind + ":unknown"
				}
				line, col := javaLineCol(methodNode)
				c.addFactWithEvidence("ExternalCall", map[string]any{
					"protocol":        "queue",
					"method":          "CONSUME",
					"target":          target,
					"library":         "java-listener-semantic",
					"queue_operation": "consume",
					"queue_kind":      queueKind,
				}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
			}
		}
	})
	return true
}

func detectJavaOutboundCallsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJavaAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	walkJava(root, func(classNode *sitter.Node) {
		if classNode == nil || classNode.Kind() != "class_declaration" {
			return
		}
		classModifiers := javaModifiersNode(classNode)
		classAnnotations := javaAnnotationTexts(classModifiers, content)
		baseURL, basePath, targetService, isFeign := parseJavaFeignClient(classAnnotations)
		if !isFeign {
			return
		}

		body := javaDeclarationBody(classNode)
		if body == nil {
			return
		}
		for i := uint(0); i < body.NamedChildCount(); i++ {
			methodNode := body.NamedChild(i)
			if methodNode == nil || methodNode.Kind() != "method_declaration" {
				continue
			}
			methodModifiers := javaModifiersNode(methodNode)
			methodAnnotations := javaAnnotationTexts(methodModifiers, content)
			if len(methodAnnotations) == 0 {
				continue
			}
			for _, ann := range methodAnnotations {
				methods, paths, matched := parseSpringMappingAnnotationDetailed(ann)
				if !matched {
					continue
				}
				line, col := javaLineCol(methodNode)
				for _, m := range methods {
					for _, p := range paths {
						target := javaJoinFeignTarget(baseURL, basePath, p)
						attrs := map[string]any{
							"protocol": "http",
							"method":   m,
							"target":   target,
							"library":  "feign-semantic",
						}
						if strings.TrimSpace(targetService) != "" {
							attrs["target_service"] = targetService
						}
						if strings.TrimSpace(baseURL) != "" {
							attrs["base_url_ref"] = baseURL
						}
						if strings.TrimSpace(basePath) != "" {
							attrs["base_path"] = normalizePath(basePath)
						}
						c.addFactWithEvidence("ExternalCall", attrs, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
					}
				}
			}
		}
	})

	walkJava(root, func(n *sitter.Node) {
		if n == nil || n.Kind() != "method_invocation" {
			return
		}
		text := strings.TrimSpace(n.Utf8Text(content))
		m := javaRestTemplateCallRe.FindStringSubmatch(text)
		if len(m) >= 3 {
			callName := strings.ToLower(strings.TrimSpace(m[1]))
			target := strings.TrimSpace(m[2])
			if target == "" {
				target = "unknown-target"
			}
			if !javaLikelyHTTPClientInvocation(text, callName) {
				return
			}

			method := "UNKNOWN"
			switch callName {
			case "getforobject", "getforentity":
				method = "GET"
			case "postforobject", "postforentity":
				method = "POST"
			case "put":
				method = "PUT"
			case "delete":
				method = "DELETE"
			case "exchange":
				if mm := javaRestExchangeVerbRe.FindStringSubmatch(text); len(mm) >= 2 {
					method = strings.ToUpper(strings.TrimSpace(mm[1]))
				}
			}

			line, col := javaLineCol(n)
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol": "http",
				"method":   method,
				"target":   target,
				"library":  "resttemplate-semantic",
			}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}

	})
	detectJavaFeignFromText(c, file)
	return true
}

func javaLikelyHTTPClientInvocation(invocation string, callName string) bool {
	raw := strings.TrimSpace(invocation)
	if raw == "" {
		return false
	}
	lower := strings.ToLower(raw)
	token := "." + strings.ToLower(strings.TrimSpace(callName)) + "("
	receiver := lower
	if idx := strings.Index(lower, token); idx >= 0 {
		receiver = strings.TrimSpace(lower[:idx])
	}
	// Constrain generic verb method invocations (get/put/delete/exchange) to HTTP-client-like contexts
	// to avoid false positives such as Map.put("key", value).
	switch {
	case strings.Contains(receiver, "resttemplate"),
		strings.Contains(receiver, "webclient"),
		strings.Contains(receiver, "httpclient"),
		strings.Contains(receiver, "restoperations"),
		strings.Contains(receiver, "retrofit"),
		strings.Contains(receiver, "feign"):
		return true
	case strings.Contains(lower, "new resttemplate("),
		strings.Contains(lower, "resttemplatebuilder"),
		strings.Contains(lower, "webclient.builder"),
		strings.Contains(lower, "httprequest.newbuilder"):
		return true
	default:
		return false
	}
}

func detectJavaQueueAndDBCallsSemantic(c *collector, file sourceFile) bool {
	tree, content, ok := parseJavaAST(file)
	if !ok {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()

	valueBindings := javaValueFieldBindings(file.Lines)
	requestBindings := javaRequestQueueBindings(file.Lines, valueBindings)
	walkJava(root, func(n *sitter.Node) {
		if n == nil || n.Kind() != "method_invocation" {
			return
		}
		text := strings.TrimSpace(n.Utf8Text(content))
		line, col := javaLineCol(n)

		if javaSendMessageCallRe.MatchString(text) {
			target := javaQueueTargetFromInvocation(text, valueBindings, "")
			if target == "" {
				if arg := javaFirstCallArgIdentifier(text); arg != "" {
					target = strings.TrimSpace(requestBindings[arg])
				}
			}
			if target == "" {
				target = "sqs:unknown-queue"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "PUBLISH",
				"target":          target,
				"library":         "aws-sdk-sqs-java-semantic",
				"queue_operation": "publish",
				"queue_kind":      "sqs",
			}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}
		if javaReceiveMessageCallRe.MatchString(text) {
			target := javaQueueTargetFromInvocation(text, valueBindings, "")
			if target == "" {
				if arg := javaFirstCallArgIdentifier(text); arg != "" {
					target = strings.TrimSpace(requestBindings[arg])
				}
			}
			if target == "" {
				target = "sqs:unknown-queue"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "CONSUME",
				"target":          target,
				"library":         "aws-sdk-sqs-java-semantic",
				"queue_operation": "consume",
				"queue_kind":      "sqs",
			}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}
		if javaKafkaSendCallRe.MatchString(text) {
			target := javaFirstQuoted(text)
			if target == "" {
				target = "kafka:unknown-topic"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "PUBLISH",
				"target":          target,
				"library":         "kafka-template-semantic",
				"queue_operation": "publish",
				"queue_kind":      "kafka",
			}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}
		if javaRabbitPublishCallRe.MatchString(text) {
			target := javaFirstQuoted(text)
			if target == "" {
				target = "rabbitmq:unknown"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":        "queue",
				"method":          "PUBLISH",
				"target":          target,
				"library":         "rabbit-template-semantic",
				"queue_operation": "publish",
				"queue_kind":      "rabbitmq",
			}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}

		if javaJdbcReadCallRe.MatchString(text) || javaJpaReadCallRe.MatchString(text) {
			target := javaSQLTarget(text, "db")
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":     "db",
				"method":       "READ",
				"target":       target,
				"library":      "java-db-semantic",
				"db_operation": "read",
			}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}
		if javaJdbcWriteCallRe.MatchString(text) || javaJpaWriteCallRe.MatchString(text) {
			target := javaSQLTarget(text, "db")
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol":     "db",
				"method":       "WRITE",
				"target":       target,
				"library":      "java-db-semantic",
				"db_operation": "write",
			}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}

		if javaRuntimeExecRe.MatchString(text) || javaProcessBuilderRe.MatchString(text) {
			target := javaCommandTarget(text)
			if target == "" {
				target = "unknown-command"
			}
			c.addFactWithEvidence("ExternalCall", map[string]any{
				"protocol": "command",
				"method":   "EXEC",
				"target":   target,
				"library":  "java-process-semantic",
			}, file, line, col, javaLineSnippet(file, line), func() { c.report.ExternalCalls++ })
		}
	})
	return true
}

func parseJavaAST(file sourceFile) (*sitter.Tree, []byte, bool) {
	if strings.ToLower(strings.TrimSpace(file.Ext)) != ".java" {
		return nil, nil, false
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(java.Language())); err != nil {
		return nil, nil, false
	}
	content := []byte(file.Text)
	tree := parser.Parse(content, nil)
	if tree == nil || tree.RootNode() == nil {
		return nil, nil, false
	}
	return tree, content, true
}

func walkJava(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		walkJava(node.Child(i), fn)
	}
}

func parseSpringMappingAnnotation(text string) (method string, path string, matched bool) {
	methods, paths, ok := parseSpringMappingAnnotationDetailed(text)
	if !ok || len(methods) == 0 {
		return "", "", false
	}
	method = methods[0]
	if len(paths) > 0 {
		path = paths[0]
	}
	return method, path, true
}

func parseSpringMappingAnnotationDetailed(text string) (methods []string, paths []string, matched bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "@") {
		return nil, nil, false
	}

	name := strings.TrimPrefix(text, "@")
	if i := strings.Index(name, "("); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, false
	}

	switch name {
	case "GetMapping":
		methods = []string{"GET"}
	case "PostMapping":
		methods = []string{"POST"}
	case "PutMapping":
		methods = []string{"PUT"}
	case "PatchMapping":
		methods = []string{"PATCH"}
	case "DeleteMapping":
		methods = []string{"DELETE"}
	case "RequestMapping":
		methods = []string{"ANY"}
		allMethods := javaRequestMethodAllRefRe.FindAllStringSubmatch(text, -1)
		if len(allMethods) > 0 {
			methods = methods[:0]
			seen := map[string]struct{}{}
			for _, m := range allMethods {
				if len(m) < 2 {
					continue
				}
				v := strings.ToUpper(strings.TrimSpace(m[1]))
				if v == "" {
					continue
				}
				if _, ok := seen[v]; ok {
					continue
				}
				seen[v] = struct{}{}
				methods = append(methods, v)
			}
			if len(methods) == 0 {
				methods = []string{"ANY"}
			}
		}
	default:
		return nil, nil, false
	}

	paths = make([]string, 0, 2)
	if m := javaNamedPathValueRe.FindAllStringSubmatch(text, -1); len(m) > 0 {
		for _, item := range m {
			if len(item) < 3 {
				continue
			}
			v := normalizePath(strings.TrimSpace(item[2]))
			if v != "" {
				paths = append(paths, v)
			}
		}
	}
	if len(paths) == 0 {
		if all := javaQuotedStringRe.FindAllStringSubmatch(text, -1); len(all) > 0 {
			for _, item := range all {
				if len(item) < 2 {
					continue
				}
				v := normalizePath(strings.TrimSpace(item[1]))
				if v != "" {
					paths = append(paths, v)
				}
			}
		}
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	return methods, dedupeStrings(paths), true
}

func javaAnnotationTexts(modifiers *sitter.Node, content []byte) []string {
	if modifiers == nil {
		return nil
	}
	out := make([]string, 0, modifiers.NamedChildCount())
	for i := uint(0); i < modifiers.NamedChildCount(); i++ {
		ch := modifiers.NamedChild(i)
		if ch == nil {
			continue
		}
		if ch.Kind() == "annotation" || ch.Kind() == "marker_annotation" {
			out = append(out, strings.TrimSpace(ch.Utf8Text(content)))
		}
	}
	return out
}

func javaModifiersNode(node *sitter.Node) *sitter.Node {
	if node == nil {
		return nil
	}
	if m := node.ChildByFieldName("modifiers"); m != nil {
		return m
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		ch := node.NamedChild(i)
		if ch != nil && ch.Kind() == "modifiers" {
			return ch
		}
	}
	return nil
}

func javaDeclarationBody(node *sitter.Node) *sitter.Node {
	if node == nil {
		return nil
	}
	if b := node.ChildByFieldName("body"); b != nil {
		return b
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		ch := node.NamedChild(i)
		if ch == nil {
			continue
		}
		if ch.Kind() == "class_body" || ch.Kind() == "interface_body" {
			return ch
		}
	}
	return nil
}

func javaHasControllerAnnotation(annotations []string) bool {
	for _, a := range annotations {
		switch annotationName(a) {
		case "RestController", "Controller":
			return true
		}
	}
	return false
}

func javaControllerBasePaths(annotations []string) []string {
	out := []string{""}
	for _, a := range annotations {
		if annotationName(a) != "RequestMapping" {
			continue
		}
		_, paths, ok := parseSpringMappingAnnotationDetailed(a)
		if !ok {
			continue
		}
		out = paths
		break
	}
	return out
}

func parseJavaScheduledAnnotation(annotation string) (string, string) {
	scheduleKind := "unknown"
	scheduleExpr := "scheduled"
	matches := javaScheduledExprRe.FindAllStringSubmatch(annotation, -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(m[1]))
		expr := strings.Trim(strings.TrimSpace(m[2]), "\"")
		if kind != "" {
			scheduleKind = kind
		}
		if expr != "" {
			scheduleExpr = expr
		}
	}
	return scheduleKind, scheduleExpr
}

func parseJavaQueueListenerAnnotation(annotation string) (target string, queueKind string, ok bool) {
	switch annotationName(annotation) {
	case "SqsListener":
		queueKind = "sqs"
	case "KafkaListener":
		queueKind = "kafka"
	case "RabbitListener":
		queueKind = "rabbitmq"
	case "JmsListener":
		queueKind = "jms"
	default:
		return "", "", false
	}
	if m := javaQuotedStringRe.FindStringSubmatch(annotation); len(m) >= 2 {
		target = strings.TrimSpace(m[1])
	}
	return target, queueKind, true
}

func parseJavaFeignClient(annotations []string) (baseURL string, basePath string, targetService string, ok bool) {
	for _, ann := range annotations {
		if annotationName(ann) != "FeignClient" {
			continue
		}
		ok = true
		if m := javaFeignClientURLRe.FindStringSubmatch(ann); len(m) >= 2 {
			baseURL = strings.TrimSpace(m[1])
		}
		if m := javaFeignClientNameRe.FindStringSubmatch(ann); len(m) >= 2 {
			targetService = strings.TrimSpace(m[1])
		}
		if m := javaFeignClientPathRe.FindStringSubmatch(ann); len(m) >= 2 {
			basePath = strings.TrimSpace(m[1])
		}
	}
	return baseURL, basePath, targetService, ok
}

func detectJavaFeignFromText(c *collector, file sourceFile) {
	baseURL := ""
	basePath := ""
	targetService := ""
	inFeign := false
	for i := 0; i < len(file.Lines); i++ {
		line := strings.TrimSpace(file.Lines[i])
		if line == "" {
			continue
		}
		if strings.Contains(line, "@FeignClient(") {
			raw := line
			for !strings.Contains(raw, ")") && i+1 < len(file.Lines) {
				i++
				raw += " " + strings.TrimSpace(file.Lines[i])
			}
			m := javaFeignClientAnnRe.FindStringSubmatch(raw)
			ann := raw
			if len(m) >= 2 {
				ann = "@FeignClient(" + m[1] + ")"
			}
			baseURL, basePath, targetService, inFeign = parseJavaFeignClient([]string{ann})
			continue
		}
		if !inFeign {
			continue
		}
		if strings.HasPrefix(line, "@") {
			methods, paths, matched := parseSpringMappingAnnotationDetailed(line)
			if matched {
				for _, m := range methods {
					for _, p := range paths {
						target := javaJoinFeignTarget(baseURL, basePath, p)
						attrs := map[string]any{
							"protocol": "http",
							"method":   m,
							"target":   target,
							"library":  "feign-semantic",
						}
						if strings.TrimSpace(targetService) != "" {
							attrs["target_service"] = targetService
						}
						if strings.TrimSpace(baseURL) != "" {
							attrs["base_url_ref"] = baseURL
						}
						if strings.TrimSpace(basePath) != "" {
							attrs["base_path"] = normalizePath(basePath)
						}
						c.addFactWithEvidence("ExternalCall", attrs, file, i+1, 1, file.Lines[i], func() { c.report.ExternalCalls++ })
					}
				}
			}
		}
	}
}

func javaJoinFeignTarget(baseURL string, basePath string, methodPath string) string {
	methodPath = normalizePath(methodPath)
	if methodPath == "" {
		methodPath = "/"
	}
	basePath = normalizePath(basePath)
	if strings.TrimSpace(baseURL) == "" {
		if basePath == "" {
			return methodPath
		}
		return joinPaths(basePath, methodPath)
	}
	if basePath != "" {
		return strings.TrimRight(baseURL, "/") + joinPaths(basePath, methodPath)
	}
	return strings.TrimRight(baseURL, "/") + methodPath
}

func javaValueFieldBindings(lines []string) map[string]string {
	out := map[string]string{}
	pendingKey := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if m := reSpringValue.FindStringSubmatch(line); len(m) >= 2 {
			pendingKey = strings.TrimSpace(m[1])
			continue
		}
		if pendingKey == "" {
			continue
		}
		if m := javaValueFieldRe.FindStringSubmatch(line); len(m) >= 2 {
			field := strings.TrimSpace(m[1])
			if field != "" {
				out[field] = pendingKey
			}
			pendingKey = ""
			continue
		}
		// Avoid leaking pending key too far when annotation and field are not adjacent.
		if strings.Contains(trimmed, ";") || strings.HasPrefix(trimmed, "@") {
			pendingKey = ""
		}
	}
	return out
}

func javaQueueTargetFromInvocation(text string, valueBindings map[string]string, fallback string) string {
	if target, ok := javaQueueTargetCandidate(text, valueBindings); ok {
		return target
	}
	return fallback
}

func javaQueueTargetCandidate(text string, valueBindings map[string]string) (string, bool) {
	if m := javaQueueURLBuilderStringRe.FindStringSubmatch(text); len(m) >= 2 {
		v := strings.TrimSpace(m[1])
		if v != "" {
			return v, true
		}
	}
	if m := javaQueueURLBuilderVarRe.FindStringSubmatch(text); len(m) >= 2 {
		if key := strings.TrimSpace(valueBindings[strings.TrimSpace(m[1])]); key != "" {
			return "cfg:" + key, true
		}
	}
	return "", false
}

func javaRequestQueueBindings(lines []string, valueBindings map[string]string) map[string]string {
	out := map[string]string{}
	currentVar := ""
	currentTarget := ""
	for _, line := range lines {
		if m := javaRequestBuilderAssignRe.FindStringSubmatch(line); len(m) >= 2 {
			currentVar = strings.TrimSpace(m[1])
			currentTarget = ""
			if target, ok := javaQueueTargetCandidate(line, valueBindings); ok {
				currentTarget = target
			}
			if strings.Contains(line, ".build()") {
				if currentVar != "" && currentTarget != "" {
					out[currentVar] = currentTarget
				}
				currentVar = ""
				currentTarget = ""
			}
			continue
		}
		if currentVar == "" {
			continue
		}
		if target, ok := javaQueueTargetCandidate(line, valueBindings); ok {
			currentTarget = target
		}
		if strings.Contains(line, ".build()") {
			if currentTarget != "" {
				out[currentVar] = currentTarget
			}
			currentVar = ""
			currentTarget = ""
		}
	}
	return out
}

func javaFirstCallArgIdentifier(text string) string {
	if m := javaCallArgVarRe.FindStringSubmatch(text); len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func annotationName(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "@") {
		return ""
	}
	name := strings.TrimPrefix(text, "@")
	if idx := strings.Index(name, "("); idx >= 0 {
		name = name[:idx]
	}
	return strings.TrimSpace(name)
}

func combineControllerAndMethodPaths(classPaths []string, methodPaths []string) []string {
	if len(classPaths) == 0 {
		classPaths = []string{""}
	}
	if len(methodPaths) == 0 {
		methodPaths = []string{""}
	}
	out := make([]string, 0, len(classPaths)*len(methodPaths))
	for _, cp := range classPaths {
		for _, mp := range methodPaths {
			out = append(out, joinPaths(cp, mp))
		}
	}
	return dedupeStrings(out)
}

func joinPaths(a string, b string) string {
	a = normalizePath(a)
	b = normalizePath(b)
	switch {
	case a == "" && b == "":
		return "/"
	case a == "":
		return b
	case b == "":
		return a
	case a == "/":
		return b
	case b == "/":
		return a
	default:
		return normalizePath(strings.TrimRight(a, "/") + "/" + strings.TrimLeft(b, "/"))
	}
}

func normalizePath(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	if len(v) > 1 {
		v = strings.TrimRight(v, "/")
	}
	return v
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func javaLineCol(node *sitter.Node) (int, int) {
	if node == nil {
		return 1, 1
	}
	sp := node.StartPosition()
	line := int(sp.Row) + 1
	col := int(sp.Column) + 1
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	return line, col
}

func javaLineSnippet(file sourceFile, line int) string {
	if line < 1 || line > len(file.Lines) {
		return ""
	}
	return file.Lines[line-1]
}

func javaFirstQuoted(text string) string {
	m := javaQuotedStringRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func javaSQLTarget(text string, fallback string) string {
	for _, m := range javaQuotedStringRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		value := strings.TrimSpace(m[1])
		upper := strings.ToUpper(value)
		if strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "INSERT ") || strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ") {
			return value
		}
	}
	return fallback
}

func javaCommandTarget(text string) string {
	m := javaCommandArgRe.FindStringSubmatch(text)
	if len(m) >= 3 {
		return strings.TrimSpace(m[2])
	}
	return javaFirstQuoted(text)
}
