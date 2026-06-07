package framework

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

func init() { register(&springDetector{}) }

// springDetector implements ast.FrameworkDetector for Spring Framework (Java/Kotlin).
type springDetector struct{}

func (d *springDetector) Name() string { return "spring" }

func (d *springDetector) Detect(idx *ast.ProjectIndex) []ast.FrameworkBinding {
	var out []ast.FrameworkBinding
	for _, fa := range idx.Files {
		if fa.Language != "java" && fa.Language != "kotlin" {
			continue
		}
		classes := classesByName(fa)
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			cls := enclosingClassForSymbol(fa, sym, classes)
			for _, ann := range sym.Annotations {
				bindings := springAnnotationToBindings(sym, cls, ann)
				if len(bindings) > 0 {
					out = append(out, bindings...)
				}
			}
		}
	}
	return out
}

func springAnnotationToBindings(sym ast.SymbolDef, cls *ast.SymbolDef, ann ast.Annotation) []ast.FrameworkBinding {
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

	// Kafka listener.
	if name == "KafkaListener" {
		topics := extractArgValue(args, "topics")
		return []ast.FrameworkBinding{{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "kafka: " + topics,
			TriggerSource: "@KafkaListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}}
	}

	// RabbitMQ listener.
	if name == "RabbitListener" {
		queues := extractArgValue(args, "queues")
		return []ast.FrameworkBinding{{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "rabbitmq: " + queues,
			TriggerSource: "@RabbitListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}}
	}

	// SQS listener.
	if name == "SqsListener" {
		queue := extractFirstStringArg(args)
		return []ast.FrameworkBinding{{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "sqs: " + queue,
			TriggerSource: "@SqsListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}}
	}

	// JMS listener.
	if name == "JmsListener" {
		dest := extractArgValue(args, "destination")
		return []ast.FrameworkBinding{{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Direction:     "inbound",
			Symbol:        sym.Qualified,
			Trigger:       "jms: " + dest,
			TriggerSource: "@JmsListener(" + args + ")",
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
	paths := extractStringArgs(ann.Arguments)
	if len(paths) == 0 {
		paths = []string{""}
	}
	classPrefixes := []string{""}
	controller := false
	feign := false
	if cls != nil {
		controller = hasAnyAnnotation(*cls, "RestController", "Controller")
		feign = hasAnyAnnotation(*cls, "FeignClient")
		if p := classRequestMappingPrefixes(*cls); len(p) > 0 {
			classPrefixes = p
		}
	}
	kind := "http_handler"
	direction := "inbound"
	reason := "controller_mapping_literal"
	rejection := ""
	if feign {
		kind = "http_client"
		direction = "outbound"
		reason = "feign_client_mapping_literal"
	} else if !controller {
		rejection = "spring_mapping_without_controller_context"
	}
	out := make([]ast.FrameworkBinding, 0, len(classPrefixes)*len(paths))
	for _, prefix := range classPrefixes {
		for _, path := range paths {
			routePath := joinPath(prefix, path)
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

// extractFirstStringArg extracts the first string literal from an annotation
// argument text (e.g. `"/users/{id}"` → `/users/{id}`).
func extractFirstStringArg(args string) string {
	values := extractStringArgs(args)
	if len(values) > 0 {
		return values[0]
	}
	// Return as-is if no quotes.
	return strings.TrimSpace(strings.Trim(args, "{}"))
}

// extractArgValue extracts a named argument value from annotation args text.
// e.g. extractArgValue(`topics = "orders", groupId = "g1"`, "topics") → `"orders"`
func extractArgValue(args, key string) string {
	// Try "key = value" pattern.
	search := key + " = "
	if idx := strings.Index(args, search); idx >= 0 {
		rest := strings.TrimSpace(args[idx+len(search):])
		// Read until comma or end.
		if comma := strings.Index(rest, ","); comma >= 0 {
			rest = rest[:comma]
		}
		return strings.Trim(strings.TrimSpace(rest), `"'`)
	}
	// Fall back to first string arg.
	return extractFirstStringArg(args)
}

func extractStringArgs(args string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] != '"' && args[i] != '\'' {
			continue
		}
		quote := args[i]
		start := i + 1
		i++
		for i < len(args) {
			if args[i] == '\\' && i+1 < len(args) {
				i += 2
				continue
			}
			if args[i] == quote {
				out = append(out, args[start:i])
				break
			}
			i++
		}
	}
	return out
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
			return extractStringArgs(ann.Arguments)
		}
	}
	return nil
}
