package detectors

import "strings"

// IDsForFrameworkBinding maps the current AST framework binding shape to the
// public detector IDs accepted by diffmind-configuration.yaml.
func IDsForFrameworkBinding(framework, kind, trigger, reason string) []string {
	framework = strings.ToLower(strings.TrimSpace(framework))
	kind = strings.ToLower(strings.TrimSpace(kind))
	trigger = strings.ToLower(strings.TrimSpace(trigger))
	reason = strings.ToLower(strings.TrimSpace(reason))

	switch framework {
	case "net/http":
		if kind == "http_handler" {
			return []string{"golang.http.nethttp"}
		}
	case "echo":
		if kind == "http_handler" {
			return []string{"golang.http.echo"}
		}
	case "fiber":
		if kind == "http_handler" {
			return []string{"golang.http.fiber"}
		}
	case "gin":
		if kind == "http_handler" {
			return []string{"golang.http.gin"}
		}
	case "go-grpc", "grpc":
		if kind == "rpc_endpoint" || kind == "rpc_call" {
			return []string{"golang.rpc.grpc"}
		}
	case "openai":
		if kind == "http_client" || kind == "http_call" {
			return []string{"golang.ai.openai"}
		}
	case "go-wire", "wire":
		switch kind {
		case "http_client", "dependency_wiring":
			return []string{"golang.di.wire"}
		}
	case "flask":
		if kind == "http_handler" {
			return []string{"python.http.flask"}
		}
	case "fastapi":
		if kind == "http_handler" {
			return []string{"python.http.fastapi"}
		}
	case "django", "celery":
		switch kind {
		case "http_handler":
			return []string{"python.http.django"}
		case "event_listener", "scheduler":
			return []string{"python.http.django"}
		}
	case "express":
		if kind == "http_handler" {
			return []string{"javascript.http.express"}
		}
	case "nestjs":
		if kind == "http_handler" {
			return []string{"typescript.http.nestjs"}
		}
	case "rails":
		if kind == "http_handler" {
			return []string{"ruby.http.rails"}
		}
	case "laravel":
		if kind == "http_handler" {
			return []string{"php.http.laravel"}
		}
	case "aspnet":
		if kind == "http_handler" {
			return []string{"dotnet.http.aspnet"}
		}
	case "sam", "aws-sam":
		if kind == "activation" || kind == "queue_consumer" {
			return []string{"python.aws.sam"}
		}
	case "redis", "redis-py":
		if kind == "cache_operation" {
			return []string{"python.cache.redis", "golang.cache.redis"}
		}
	case "argparse":
		if kind == "cli_command" {
			return []string{"python.cli.argparse"}
		}
	case "retrofit":
		if kind == "http_client" || kind == "http_call" {
			return []string{"java.httpclient.retrofit"}
		}
	case "spring":
		switch kind {
		case "http_handler":
			return []string{"java.http.spring"}
		case "http_client", "http_call":
			if strings.Contains(reason, "retrofit") || strings.Contains(trigger, "retrofit") {
				return []string{"java.httpclient.retrofit"}
			}
			return []string{"java.httpclient.feign"}
		case "queue_consumer", "queue_publisher":
			if strings.Contains(trigger, "kafka:") || strings.Contains(reason, "kafka") {
				return []string{"java.queue.kafka"}
			}
			if strings.Contains(trigger, "sqs:") || strings.Contains(trigger, "sns:") || strings.Contains(reason, "sqs") || strings.Contains(reason, "sns") || strings.Contains(reason, "aws") {
				return []string{"java.queue.sqs"}
			}
			if strings.Contains(trigger, "rabbitmq:") || strings.Contains(trigger, "rabbit:") || strings.Contains(reason, "rabbit") || strings.Contains(reason, "amqp") {
				return []string{"java.queue.rabbitmq"}
			}
			if strings.Contains(trigger, "jms:") || strings.Contains(reason, "jms") {
				return []string{"java.queue.jms"}
			}
			return []string{"java.queue.kafka", "java.queue.sqs", "java.queue.rabbitmq", "java.queue.jms"}
		case "scheduler", "event_listener", "activation", "async_dispatch":
			return []string{"java.activation.spring"}
		case "cache_operation":
			return []string{"java.cache.spring"}
		}
	}

	return nil
}

// AllowFrameworkBinding applies detector disable rules to a framework binding.
// The enabled argument is intentionally ignored for backward compatibility:
// DiffMind discovers with all registered detectors by default, and
// diffmind-configuration.yaml may only suppress noisy detectors explicitly.
func AllowFrameworkBinding(ids, enabled, disabled []string) bool {
	_ = enabled
	if matchesAny(ids, disabled) {
		return false
	}
	return true
}

func matchesAny(ids, candidates []string) bool {
	if len(ids) == 0 || len(candidates) == 0 {
		return false
	}
	set := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			set[id] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if _, ok := set[strings.TrimSpace(candidate)]; ok {
			return true
		}
	}
	return false
}
