package producer

import (
	"regexp"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

type PlatformSpec struct {
	Framework string
	Platform  string
	Types     []string
	Methods   []string
}

func Detect(idx *ast.ProjectIndex, specs []PlatformSpec) []ast.FrameworkBinding {
	if idx == nil {
		return nil
	}
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa == nil || (fa.Language != "java" && fa.Language != "kotlin") {
			continue
		}
		for _, call := range fa.Calls {
			for _, spec := range specs {
				dest, ok := destinationForCall(idx, fa, call, spec)
				if !ok {
					continue
				}
				out = append(out, ast.FrameworkBinding{
					Framework:        firstNonEmpty(spec.Framework, "spring"),
					Kind:             "queue_publisher",
					Direction:        "outbound",
					Symbol:           call.Caller,
					Trigger:          spec.Platform + ": " + dest,
					TriggerSource:    callSnippet(call),
					File:             call.File,
					Range:            call.Range,
					ConfidenceReason: "receiver_type=" + receiverType(idx, fa, call),
				})
				break
			}
		}
	}
	return out
}

func SpringCloudStreamBindings(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	if idx == nil {
		return nil
	}
	var out []ast.FrameworkBinding
	for _, cf := range idx.Configs {
		if cf == nil {
			continue
		}
		for _, e := range cf.Entries {
			key := strings.ToLower(strings.TrimSpace(e.Key))
			if !strings.HasPrefix(key, "spring.cloud.stream.bindings.") || !strings.HasSuffix(key, "-out-0.destination") {
				continue
			}
			dest := strings.TrimSpace(e.Value)
			if dest == "" || dynamicDestination(dest) {
				continue
			}
			out = append(out, ast.FrameworkBinding{
				Framework:        "spring",
				Kind:             "queue_publisher",
				Direction:        "outbound",
				Trigger:          "kafka: " + dest,
				TriggerSource:    e.Key + "=" + e.Value,
				File:             cf.Path,
				Range:            ast.Range{StartLine: configLine(e.Line), EndLine: configLine(e.Line)},
				ConfidenceReason: "spring_cloud_stream_output_binding",
			})
		}
	}
	return out
}

func destinationForCall(idx *ast.ProjectIndex, fa *ast.FileAST, call ast.CallSite, spec PlatformSpec) (string, bool) {
	method := methodName(call)
	if !containsFold(spec.Methods, method) {
		return "", false
	}
	if !receiverMatches(idx, fa, call, spec.Types) {
		return "", false
	}
	typ := receiverType(idx, fa, call)
	dest := destinationFromArgs(idx, call.Arguments, method, spec.Platform, typ)
	if dest == "" {
		dest = destinationFromConfig(idx, spec.Platform)
	}
	if dest == "" || dynamicDestination(dest) {
		return "", false
	}
	return dest, true
}

func receiverMatches(idx *ast.ProjectIndex, fa *ast.FileAST, call ast.CallSite, types []string) bool {
	typ := receiverType(idx, fa, call)
	if typ != "" && containsType(types, typ) {
		return true
	}
	recv := strings.ToLower(strings.TrimSpace(call.ReceiverRaw))
	for _, typ := range types {
		if strings.Contains(recv, strings.ToLower(simpleType(typ))) {
			return true
		}
	}
	return false
}

func receiverType(idx *ast.ProjectIndex, fa *ast.FileAST, call ast.CallSite) string {
	recv := strings.TrimSpace(call.ReceiverRaw)
	if recv == "" {
		return ""
	}
	if idx != nil {
		if typ := idx.LocalTypes[call.Caller+"."+recv]; typ != "" {
			return typ
		}
		if cls := callerClass(call.Caller); cls != "" {
			if typ := idx.FieldTypes[cls+"."+recv]; typ != "" {
				return typ
			}
		}
	}
	if fa != nil {
		if typ := fa.LocalTypes[call.Caller+"."+recv]; typ != "" {
			return typ
		}
		if cls := callerClass(call.Caller); cls != "" {
			if typ := fa.FieldTypes[cls+"."+recv]; typ != "" {
				return typ
			}
		}
	}
	return ""
}

func destinationFromArgs(idx *ast.ProjectIndex, args []ast.ArgumentExpr, method, platform, receiverType string) string {
	switch platform {
	case "kafka":
		if strings.EqualFold(method, "sendDefault") {
			return ""
		}
		if len(args) > 0 {
			dest := literalOrIdentifier(args[0].Source)
			if strings.EqualFold(simpleType(receiverType), "StreamBridge") {
				if resolved := streamBridgeDestination(idx, dest); resolved != "" {
					return resolved
				}
			}
			return dest
		}
	case "sqs":
		if v := builderValue(args, "queue", "queueName", "queueUrl"); v != "" {
			return v
		}
		if len(args) > 0 {
			return literalOrIdentifier(args[0].Source)
		}
	case "sns":
		if v := builderValue(args, "topicArn", "topic", "targetArn"); v != "" {
			return v
		}
		if len(args) > 0 {
			return literalOrIdentifier(args[0].Source)
		}
	case "rabbitmq":
		if len(args) >= 2 {
			exchange := literalOrIdentifier(args[0].Source)
			routingKey := literalOrIdentifier(args[1].Source)
			if exchange != "" && routingKey != "" {
				return exchange + "." + routingKey
			}
		}
		if len(args) > 0 {
			return literalOrIdentifier(args[0].Source)
		}
	case "jms":
		if len(args) > 0 {
			return literalOrIdentifier(args[0].Source)
		}
	}
	return ""
}

func streamBridgeDestination(idx *ast.ProjectIndex, binding string) string {
	if idx == nil || binding == "" {
		return ""
	}
	candidates := []string{
		"spring.cloud.stream.bindings." + binding + ".destination",
		"spring.cloud.stream.bindings." + binding + "-out-0.destination",
	}
	for _, key := range candidates {
		if v := configValue(idx, key); v != "" {
			return v
		}
	}
	return ""
}

func destinationFromConfig(idx *ast.ProjectIndex, platform string) string {
	if idx == nil {
		return ""
	}
	keys := map[string][]string{
		"kafka": {
			"spring.kafka.template.default-topic",
		},
		"sqs": {
			"spring.cloud.aws.sqs.queue-name",
			"cloud.aws.sqs.queue-name",
		},
	}
	for _, key := range keys[platform] {
		if v := configValue(idx, key); v != "" {
			return v
		}
	}
	if platform == "kafka" {
		for _, cf := range idx.Configs {
			if cf == nil {
				continue
			}
			for _, e := range cf.Entries {
				k := strings.ToLower(strings.TrimSpace(e.Key))
				if strings.HasPrefix(k, "spring.cloud.stream.bindings.") && strings.HasSuffix(k, "-out-0.destination") && strings.TrimSpace(e.Value) != "" {
					return strings.TrimSpace(e.Value)
				}
			}
		}
	}
	return ""
}

func configValue(idx *ast.ProjectIndex, key string) string {
	for _, cf := range idx.Configs {
		if cf == nil {
			continue
		}
		for _, e := range cf.Entries {
			if strings.EqualFold(strings.TrimSpace(e.Key), key) {
				return strings.TrimSpace(e.Value)
			}
		}
	}
	return ""
}

func builderValue(args []ast.ArgumentExpr, names ...string) string {
	for _, arg := range args {
		src := arg.Source
		for _, name := range names {
			re := regexp.MustCompile(`(?s)\.` + regexp.QuoteMeta(name) + `\s*\(\s*(` + stringOrIdentPattern + `)`)
			if m := re.FindStringSubmatch(src); len(m) > 1 {
				return literalOrIdentifier(m[1])
			}
		}
	}
	return ""
}

const stringOrIdentPattern = `"([^"\\]|\\.)*"|'([^'\\]|\\.)*'|[A-Za-z_][A-Za-z0-9_$.]*`

func literalOrIdentifier(expr string) string {
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
	if !isConstant(expr) {
		return ""
	}
	return strings.ToLower(strings.ReplaceAll(strings.TrimSuffix(expr, "_TOPIC"), "_", "-"))
}

func isConstant(expr string) bool {
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

func dynamicDestination(dest string) bool {
	return strings.ContainsAny(dest, "+")
}

func methodName(call ast.CallSite) string {
	callee := strings.TrimSpace(call.CalleeRaw)
	if i := strings.LastIndex(callee, "."); i >= 0 {
		return callee[i+1:]
	}
	return callee
}

func callerClass(caller string) string {
	if i := strings.LastIndex(caller, "."); i > 0 {
		return caller[:i]
	}
	return ""
}

func simpleType(typ string) string {
	typ = strings.TrimSpace(typ)
	if i := strings.Index(typ, "<"); i >= 0 {
		typ = typ[:i]
	}
	if i := strings.LastIndex(typ, "."); i >= 0 {
		typ = typ[i+1:]
	}
	return typ
}

func containsType(types []string, typ string) bool {
	simple := strings.ToLower(simpleType(typ))
	for _, candidate := range types {
		if simple == strings.ToLower(simpleType(candidate)) {
			return true
		}
	}
	return false
}

func containsFold(items []string, item string) bool {
	for _, candidate := range items {
		if strings.EqualFold(candidate, item) {
			return true
		}
	}
	return false
}

func callSnippet(call ast.CallSite) string {
	callee := strings.TrimSpace(call.CalleeRaw)
	if callee == "" {
		callee = methodName(call)
	}
	var args []string
	for _, arg := range call.Arguments {
		args = append(args, strings.TrimSpace(arg.Source))
	}
	return callee + "(" + strings.Join(args, ", ") + ")"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func configLine(line int) uint32 {
	if line <= 0 {
		return 0
	}
	return uint32(line - 1)
}
