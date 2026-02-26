package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

type Objective struct {
	ID                string
	Kind              model.EntityKind
	Type              string
	Description       string
	DiscoveryPrompt   string
	DetailPrompt      string
	ConnectionContext string
}

func Default() []Objective {
	return []Objective{
		{
			ID:          "exposure.http_route",
			Kind:        model.KindExposure,
			Type:        "http_route",
			Description: "HTTP REST/API routes exposed by the service (non-webhook)",
			DiscoveryPrompt: "Find all externally reachable HTTP API routes (GET/POST/PUT/PATCH/DELETE, request mappings, controller endpoints). " +
				"Include route path, HTTP method, handler symbol, request inputs and validation entry points.",
			DetailPrompt: "For this route, extract handler flow in order, request input contract, authentication/authorization checks, validation, and all downstream dependency operations. " +
				"For DB operations include table names and read/write operation type.",
			ConnectionContext: "Prioritize ordered call-path mapping from HTTP route to downstream dependencies with conditions.",
		},
		{
			ID:          "exposure.webhook",
			Kind:        model.KindExposure,
			Type:        "webhook",
			Description: "HTTP webhook callback endpoints (subset of HTTP entrypoints)",
			DiscoveryPrompt: "Find webhook/callback endpoints (incoming third-party callbacks, signed webhook handlers, event webhook controllers). " +
				"Include path, method, signature verification inputs, and parsing flow.",
			DetailPrompt:      "For this webhook, extract signature/auth checks, payload schema, branching rules, idempotency/duplicate handling, and ordered downstream operations.",
			ConnectionContext: "Map webhook-to-dependency conditional paths with explicit guard expressions.",
		},
		{
			ID:          "exposure.rpc_endpoint",
			Kind:        model.KindExposure,
			Type:        "rpc_endpoint",
			Description: "RPC/gRPC entrypoints exposed by the service",
			DiscoveryPrompt: "Find externally reachable RPC entrypoints (gRPC service methods, protobuf RPC handlers, thrift endpoints). " +
				"Include service/method names, request message types, and handler symbols.",
			DetailPrompt:      "For this RPC endpoint, extract request contract, auth/validation, branching logic, and ordered downstream operations.",
			ConnectionContext: "Map RPC endpoint paths to dependencies with explicit branch conditions.",
		},
		{
			ID:          "exposure.queue_consumer",
			Kind:        model.KindExposure,
			Type:        "queue_consumer",
			Description: "Queue/topic consumers",
			DiscoveryPrompt: "Find message consumer entrypoints (SQS/Kafka/Rabbit/JMS/stream listeners, pollers). " +
				"Include queue/topic name bindings, payload shape, and consumer handler symbol.",
			DetailPrompt:      "For this consumer, extract payload contract, deserialization/validation flow, retry/error handling, and ordered downstream operations.",
			ConnectionContext: "Map queue-consumer paths to dependencies and include queue destination config guards.",
		},
		{
			ID:          "exposure.scheduled_job",
			Kind:        model.KindExposure,
			Type:        "scheduled_job",
			Description: "Scheduled jobs and startup runners",
			DiscoveryPrompt: "Find scheduled/background entrypoints (@Scheduled, cron triggers, profile-gated CommandLineRunner/ApplicationRunner jobs). " +
				"Include schedule expression/profile/property guards and entry method.",
			DetailPrompt:      "For this job, extract trigger conditions (profiles/properties), execution order, dataset selection logic, and ordered downstream operations.",
			ConnectionContext: "Map scheduled-job paths with schedule/profile/property guards.",
		},
		{
			ID:                "exposure.cli_command",
			Kind:              model.KindExposure,
			Type:              "cli_command",
			Description:       "CLI command entrypoints and script command handlers",
			DiscoveryPrompt:   "Find CLI command entrypoints (main command dispatch, script commands, management commands) that trigger business flows.",
			DetailPrompt:      "For this CLI entrypoint, extract arguments, command options, validation, and ordered downstream operations.",
			ConnectionContext: "Map command-triggered execution paths to dependencies.",
		},
		{
			ID:          "dependency.db_operation",
			Kind:        model.KindDependency,
			Type:        "db_operation",
			Description: "Database operations",
			DiscoveryPrompt: "Find database operations reachable from service code: repository/DAO calls, query builders, ORM operations, native SQL, migrations used at runtime paths. " +
				"Extract datasource, schema/table names, operation type (read/write/upsert/delete), query method name.",
			DetailPrompt: "For this DB dependency, provide exact table/entity names, operation semantics, transaction context, and input parameters. " +
				"Do not collapse unrelated DB calls into one item.",
			ConnectionContext: "Connection mapping must include db table and read/write operation per step.",
		},
		{
			ID:          "dependency.outbound_http",
			Kind:        model.KindDependency,
			Type:        "outbound_http",
			Description: "Outbound HTTP calls",
			DiscoveryPrompt: "Find outbound HTTP service calls (Feign/REST clients/external HTTP SDK APIs). " +
				"Extract client class, host/base URL config, method/path, and request inputs.",
			DetailPrompt:      "For this outbound dependency, extract exact endpoint path/method, request/response shape, timeout/retry behavior, and call conditions.",
			ConnectionContext: "Connection mapping must include outbound method/path and guard condition.",
		},
		{
			ID:          "dependency.outbound_rpc",
			Kind:        model.KindDependency,
			Type:        "outbound_rpc",
			Description: "Outbound RPC/gRPC calls",
			DiscoveryPrompt: "Find outbound RPC dependencies (gRPC stubs, protobuf RPC clients, thrift clients). " +
				"Extract target service, rpc method, request type, and callsite.",
			DetailPrompt:      "For this outbound RPC dependency, extract rpc service/method, request/response contracts, retry/timeout behavior, and call conditions.",
			ConnectionContext: "Connection mapping must include rpc target service/method and guard condition.",
		},
		{
			ID:          "dependency.queue_publish",
			Kind:        model.KindDependency,
			Type:        "queue_publish",
			Description: "Queue/topic publish operations",
			DiscoveryPrompt: "Find message publish dependencies (SQS/Kafka/SNS/Rabbit producers, event bus publishers). " +
				"Extract destination name/url/topic, payload shape, and publish callsite.",
			DetailPrompt:      "For this publish dependency, extract destination resolution path, payload fields, and publish semantics (sync/async/batch).",
			ConnectionContext: "Connection mapping must include destination and publish operation step.",
		},
		{
			ID:                "dependency.command_exec",
			Kind:              model.KindDependency,
			Type:              "command_exec",
			Description:       "External command/process execution",
			DiscoveryPrompt:   "Find shell/process execution dependencies (Runtime.exec, ProcessBuilder, os command wrappers, scripts).",
			DetailPrompt:      "For this command dependency, extract executed command pattern, arguments, environment inputs, and guard conditions.",
			ConnectionContext: "Connection mapping must include executed command and trigger condition.",
		},
	}
}
