package objectives

// objectiveMetaEntry carries the few-shot example and required detail keys for
// one objective type. Kept separate from the prompt literals so the large
// DiscoveryPrompt/DetailPrompt blocks stay readable.
type objectiveMetaEntry struct {
	example    string
	detailKeys []string

	// bad is an optional NegativeExample (BAD_EXAMPLE) — an item commonly
	// MISCLASSIFIED as this type but owned by a neighbour. Populated only for
	// confusable pairs (http_route↔webhook, db↔cache, queue_publish↔outbound_rpc).
	bad string
	// boundary is an optional one-line scope exclusion (BOUNDARY) that names the
	// neighbour this type is confused with and which way each case goes.
	boundary string
	// highVariance marks the LLM-only objectives (no strong deterministic floor)
	// whose run-to-run recall wobbles most. Drives the exhaustiveness line and
	// gates the verification pass. See objectives.Objective.HighVariance.
	highVariance bool
}

// objectiveMeta supplies a concise, schema-valid GOOD_EXAMPLE and the
// REQUIRED_DETAIL_KEYS for each objective type. Examples are intentionally
// minimal — they show the shape, not exhaustive fields. Co-located so all 13
// few-shot shapes stay comparable at a glance.
var objectiveMeta = map[string]objectiveMetaEntry{
	"http_route": {
		example:    `{"type":"http_route","name":"POST /v1/orders","summary":"Create an order","confidence":0.95,"details":{"method":"POST","path":"/v1/orders","auth":"@PreAuthorize(\"hasRole('USER')\")"},"source_locations":[{"file":"src/api/OrderController.java","start_line":34,"end_line":48}]}`,
		detailKeys: []string{"method", "path", "auth"},
		bad:        `{"type":"http_route","name":"POST /webhooks/stripe","summary":"Inbound Stripe event callback"} → WRONG: an endpoint invoked by an external provider's event callback is a WEBHOOK; report it under the webhook objective, not here.`,
		boundary:   "An endpoint invoked by an external provider's EVENT CALLBACK (Stripe, GitHub, Twilio, SNS HTTP subscription, ...) is a webhook, not an http_route. Ordinary client-facing REST/API routes belong here.",
	},
	"webhook": {
		example:      `{"type":"webhook","name":"POST /webhooks/stripe","summary":"Stripe payment webhook","confidence":0.9,"details":{"method":"POST","path":"/webhooks/stripe","verification":"Stripe-Signature HMAC"},"source_locations":[{"file":"src/api/StripeWebhook.java","start_line":20,"end_line":40}]}`,
		detailKeys:   []string{"method", "path", "verification"},
		bad:          `{"type":"webhook","name":"GET /v1/orders/{id}","summary":"Fetch an order for a client"} → WRONG: a normal client-facing REST route is an http_route, not a webhook. Only inbound provider event-callbacks belong here.`,
		boundary:     "Only endpoints that RECEIVE callbacks from an external provider/integration belong here. A route your own clients call is an http_route.",
		highVariance: true,
	},
	"rpc_endpoint": {
		example:      `{"type":"rpc_endpoint","name":"OrderService/GetOrder","summary":"gRPC unary RPC","confidence":0.9,"details":{"service":"OrderService","method":"GetOrder","proto":"order.proto"},"source_locations":[{"file":"src/grpc/OrderServiceImpl.java","start_line":15,"end_line":30}]}`,
		detailKeys:   []string{"service", "method", "proto"},
		highVariance: true,
	},
	"queue_consumer": {
		example:    `{"type":"queue_consumer","name":"order-events-consumer","summary":"Consumes order events from SQS","confidence":0.92,"details":{"queue":"order-events","platform":"sqs","handler":"OrderListener.onMessage"},"source_locations":[{"file":"src/messaging/OrderListener.java","start_line":18,"end_line":35}]}`,
		detailKeys: []string{"queue", "platform", "handler"},
	},
	"scheduled_job": {
		example:    `{"type":"scheduled_job","name":"ReportJob.run","summary":"Nightly report generation","confidence":0.9,"details":{"schedule":"0 0 2 * * *","handler":"ReportJob.run"},"source_locations":[{"file":"src/jobs/ReportJob.java","start_line":20,"end_line":40}]}`,
		detailKeys: []string{"schedule", "handler"},
	},
	"cli_command": {
		example:      `{"type":"cli_command","name":"migrate","summary":"DB migration entrypoint","confidence":0.85,"details":{"command":"migrate","handler":"main.runMigrate"},"source_locations":[{"file":"cmd/migrate/main.go","start_line":12,"end_line":30}]}`,
		detailKeys:   []string{"command", "handler"},
		highVariance: true,
	},
	"db_operation": {
		example:    `{"type":"db_operation","name":"OrderRepository.save","summary":"Insert/update an order row","confidence":0.93,"details":{"table":"orders","operation":"upsert","datasource":"primary","client":"orderRepository"},"source_locations":[{"file":"src/db/OrderRepository.java","start_line":12,"end_line":18}]}`,
		detailKeys: []string{"table", "operation", "datasource", "client"},
		bad:        `{"type":"db_operation","name":"orders cache get","summary":"Redis GET with a 60s TTL"} → WRONG: a TTL'd, cache-aside Redis access is a cache_operation. Report durable, source-of-truth stores here.`,
		boundary:   "Redis/Memcached used as a TTL'd CACHE (cache-aside, @Cacheable) is a cache_operation, not a db_operation. Redis used as a durable, source-of-truth datastore belongs here.",
	},
	"outbound_http": {
		example:    `{"type":"outbound_http","name":"GET billing/charge","summary":"Calls billing service","confidence":0.9,"details":{"method":"GET","path":"/charge","target_service":"billing-service"},"source_locations":[{"file":"src/clients/BillingClient.java","start_line":22,"end_line":30}]}`,
		detailKeys: []string{"method", "path", "target_service", "client"},
	},
	"outbound_rpc": {
		example:      `{"type":"outbound_rpc","name":"PricingService/Quote","summary":"gRPC call to pricing","confidence":0.88,"details":{"service":"PricingService","method":"Quote","target_service":"pricing-service"},"source_locations":[{"file":"src/clients/PricingClient.java","start_line":18,"end_line":28}]}`,
		detailKeys:   []string{"service", "method", "target_service"},
		bad:          `{"type":"outbound_rpc","name":"publish order-events","summary":"Sends an event to SNS"} → WRONG: fire-and-forget message/event production to a broker is queue_publish, not outbound_rpc. Only synchronous request/response RPC belongs here.`,
		boundary:     "A SYNCHRONOUS request/response call to another service (gRPC, Thrift, JSON-RPC) belongs here. Asynchronous message/event production to a broker is queue_publish.",
		highVariance: true,
	},
	"queue_publish": {
		example:      `{"type":"queue_publish","name":"publish order-events","summary":"Publishes order created event","confidence":0.9,"details":{"queue":"order-events","platform":"sns","message_type":"OrderCreated"},"source_locations":[{"file":"src/messaging/OrderPublisher.java","start_line":25,"end_line":33}]}`,
		detailKeys:   []string{"queue", "platform", "message_type", "client"},
		bad:          `{"type":"queue_publish","name":"PricingService/Quote","summary":"Calls pricing and awaits the quote"} → WRONG: a synchronous request/response call is outbound_rpc/outbound_http, not queue_publish. Only fire-and-forget production to a broker belongs here.`,
		boundary:     "Fire-and-forget production of a message/event to a broker (SNS/SQS/Kafka/RabbitMQ) belongs here. A call where you await a response is outbound_rpc or outbound_http.",
		highVariance: true,
	},
	"command_exec": {
		example:      `{"type":"command_exec","name":"exec ffmpeg","summary":"Shells out to ffmpeg","confidence":0.8,"details":{"command":"ffmpeg","invocation":"Runtime.exec"},"source_locations":[{"file":"src/media/Transcoder.java","start_line":40,"end_line":46}]}`,
		detailKeys:   []string{"command", "invocation"},
		highVariance: true,
	},
	"cache_operation": {
		example:      `{"type":"cache_operation","name":"orders cache get","summary":"Reads order from Redis","confidence":0.85,"details":{"cache":"orders","operation":"get","platform":"redis"},"source_locations":[{"file":"src/cache/OrderCache.java","start_line":15,"end_line":22}]}`,
		detailKeys:   []string{"cache", "operation", "platform", "client"},
		bad:          `{"type":"cache_operation","name":"orders read","summary":"Reads the durable orders table from Postgres"} → WRONG: a durable, source-of-truth datastore is a db_operation. Only TTL'd cache-aside access belongs here.`,
		boundary:     "TTL'd, cache-aside access (incl. Redis @Cacheable) belongs here. A durable, source-of-truth datastore (incl. Redis with no TTL) is a db_operation.",
		highVariance: true,
	},
	"stream_consume": {
		example:      `{"type":"stream_consume","name":"clickstream-consumer","summary":"Consumes Kinesis clickstream","confidence":0.85,"details":{"stream":"clickstream","platform":"kinesis","consumer_group":"analytics","client":"kinesisClient"},"source_locations":[{"file":"src/streams/ClickConsumer.java","start_line":18,"end_line":40}]}`,
		detailKeys:   []string{"stream", "platform", "consumer_group", "client"},
		highVariance: true,
	},
	"connection_client": {
		example:    `{"type":"connection_client","name":"orderRepository","summary":"Spring Data JPA repository over the primary datasource","confidence":0.9,"details":{"kind":"db","symbol":"OrderRepository","framework":"spring-data","config_anchor":"spring.datasource.url"},"source_locations":[{"file":"src/db/OrderRepository.java","start_line":8,"end_line":12}]}`,
		detailKeys: []string{"kind", "config_anchor", "symbol", "framework"},
	},
}
