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
		for _, sym := range fa.Symbols {
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
				continue
			}
			for _, ann := range sym.Annotations {
				binding := springAnnotationToBinding(sym, ann)
				if binding != nil {
					out = append(out, *binding)
				}
			}
		}
	}
	return out
}

func springAnnotationToBinding(sym ast.SymbolDef, ann ast.Annotation) *ast.FrameworkBinding {
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
		path := extractFirstStringArg(args)
		trigger := method + " " + path
		return &ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "http_handler",
			Symbol:        sym.Qualified,
			Trigger:       trigger,
			TriggerSource: "@" + name + "(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	// Scheduled jobs.
	if name == "Scheduled" {
		return &ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "scheduler",
			Symbol:        sym.Qualified,
			Trigger:       "scheduled: " + args,
			TriggerSource: "@Scheduled(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	// Event listeners.
	if name == "EventListener" {
		return &ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "event_listener",
			Symbol:        sym.Qualified,
			Trigger:       "event: " + args,
			TriggerSource: "@EventListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	// Kafka listener.
	if name == "KafkaListener" {
		topics := extractArgValue(args, "topics")
		return &ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Symbol:        sym.Qualified,
			Trigger:       "kafka: " + topics,
			TriggerSource: "@KafkaListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	// RabbitMQ listener.
	if name == "RabbitListener" {
		queues := extractArgValue(args, "queues")
		return &ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Symbol:        sym.Qualified,
			Trigger:       "rabbitmq: " + queues,
			TriggerSource: "@RabbitListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	// SQS listener.
	if name == "SqsListener" {
		queue := extractFirstStringArg(args)
		return &ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Symbol:        sym.Qualified,
			Trigger:       "sqs: " + queue,
			TriggerSource: "@SqsListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	// JMS listener.
	if name == "JmsListener" {
		dest := extractArgValue(args, "destination")
		return &ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "queue_consumer",
			Symbol:        sym.Qualified,
			Trigger:       "jms: " + dest,
			TriggerSource: "@JmsListener(" + args + ")",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	// Async dispatch.
	if name == "Async" {
		return &ast.FrameworkBinding{
			Framework:     "spring",
			Kind:          "async_dispatch",
			Symbol:        sym.Qualified,
			Trigger:       "async",
			TriggerSource: "@Async",
			File:          sym.File,
			Range:         sym.Range,
		}
	}

	return nil
}

// extractFirstStringArg extracts the first string literal from an annotation
// argument text (e.g. `"/users/{id}"` → `/users/{id}`).
func extractFirstStringArg(args string) string {
	args = strings.TrimSpace(args)
	// Remove named attribute prefix: "value = \"/foo\"" → "\"/foo\""
	if idx := strings.Index(args, "="); idx >= 0 {
		args = strings.TrimSpace(args[idx+1:])
	}
	// Trim braces for array literals.
	args = strings.Trim(args, "{}")
	// Extract first string literal.
	args = strings.TrimSpace(args)
	if strings.HasPrefix(args, `"`) {
		end := strings.Index(args[1:], `"`)
		if end >= 0 {
			return args[1 : end+1]
		}
	}
	if strings.HasPrefix(args, `'`) {
		end := strings.Index(args[1:], `'`)
		if end >= 0 {
			return args[1 : end+1]
		}
	}
	// Return as-is if no quotes.
	return args
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
