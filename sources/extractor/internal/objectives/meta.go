package objectives

// objectiveMetaEntry carries the few-shot example and required detail keys for
// one objective type. Kept separate from the prompt literals so the large
// DiscoveryPrompt/DetailPrompt blocks stay readable.
type objectiveMetaEntry struct {
	example    string
	detailKeys []string
}

// objectiveMeta supplies a concise, schema-valid GOOD_EXAMPLE and the
// REQUIRED_DETAIL_KEYS for each objective type. Examples are intentionally
// minimal — they show the shape, not exhaustive fields. Co-located so all 13
// few-shot shapes stay comparable at a glance.
var objectiveMeta = map[string]objectiveMetaEntry{
	"http_route": {
		example:    `{"type":"http_route","name":"POST /v1/orders","summary":"Create an order","confidence":0.95,"details":{"method":"POST","path":"/v1/orders","auth":"@PreAuthorize(\"hasRole('USER')\")"},"source_locations":[{"file":"src/api/OrderController.java","start_line":34,"end_line":48}]}`,
		detailKeys: []string{"method", "path", "auth"},
	},
	"webhook": {
		example:    `{"type":"webhook","name":"POST /webhooks/stripe","summary":"Stripe payment webhook","confidence":0.9,"details":{"method":"POST","path":"/webhooks/stripe","verification":"Stripe-Signature HMAC"},"source_locations":[{"file":"src/api/StripeWebhook.java","start_line":20,"end_line":40}]}`,
		detailKeys: []string{"method", "path", "verification"},
	},
	"rpc_endpoint": {
		example:    `{"type":"rpc_endpoint","name":"OrderService/GetOrder","summary":"gRPC unary RPC","confidence":0.9,"details":{"service":"OrderService","method":"GetOrder","proto":"order.proto"},"source_locations":[{"file":"src/grpc/OrderServiceImpl.java","start_line":15,"end_line":30}]}`,
		detailKeys: []string{"service", "method", "proto"},
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
		example:    `{"type":"cli_command","name":"migrate","summary":"DB migration entrypoint","confidence":0.85,"details":{"command":"migrate","handler":"main.runMigrate"},"source_locations":[{"file":"cmd/migrate/main.go","start_line":12,"end_line":30}]}`,
		detailKeys: []string{"command", "handler"},
	},
	"db_operation": {
		example:    `{"type":"db_operation","name":"OrderRepository.save","summary":"Insert/update an order row","confidence":0.93,"details":{"table":"orders","operation":"upsert","datasource":"primary"},"source_locations":[{"file":"src/db/OrderRepository.java","start_line":12,"end_line":18}]}`,
		detailKeys: []string{"table", "operation", "datasource"},
	},
	"outbound_http": {
		example:    `{"type":"outbound_http","name":"GET billing/charge","summary":"Calls billing service","confidence":0.9,"details":{"method":"GET","path":"/charge","target_service":"billing-service"},"source_locations":[{"file":"src/clients/BillingClient.java","start_line":22,"end_line":30}]}`,
		detailKeys: []string{"method", "path", "target_service"},
	},
	"outbound_rpc": {
		example:    `{"type":"outbound_rpc","name":"PricingService/Quote","summary":"gRPC call to pricing","confidence":0.88,"details":{"service":"PricingService","method":"Quote","target_service":"pricing-service"},"source_locations":[{"file":"src/clients/PricingClient.java","start_line":18,"end_line":28}]}`,
		detailKeys: []string{"service", "method", "target_service"},
	},
	"queue_publish": {
		example:    `{"type":"queue_publish","name":"publish order-events","summary":"Publishes order created event","confidence":0.9,"details":{"queue":"order-events","platform":"sns","message_type":"OrderCreated"},"source_locations":[{"file":"src/messaging/OrderPublisher.java","start_line":25,"end_line":33}]}`,
		detailKeys: []string{"queue", "platform", "message_type"},
	},
	"command_exec": {
		example:    `{"type":"command_exec","name":"exec ffmpeg","summary":"Shells out to ffmpeg","confidence":0.8,"details":{"command":"ffmpeg","invocation":"Runtime.exec"},"source_locations":[{"file":"src/media/Transcoder.java","start_line":40,"end_line":46}]}`,
		detailKeys: []string{"command", "invocation"},
	},
	"cache_operation": {
		example:    `{"type":"cache_operation","name":"orders cache get","summary":"Reads order from Redis","confidence":0.85,"details":{"cache":"orders","operation":"get","platform":"redis"},"source_locations":[{"file":"src/cache/OrderCache.java","start_line":15,"end_line":22}]}`,
		detailKeys: []string{"cache", "operation", "platform"},
	},
	"stream_consume": {
		example:    `{"type":"stream_consume","name":"clickstream-consumer","summary":"Consumes Kinesis clickstream","confidence":0.85,"details":{"stream":"clickstream","platform":"kinesis","consumer_group":"analytics"},"source_locations":[{"file":"src/streams/ClickConsumer.java","start_line":18,"end_line":40}]}`,
		detailKeys: []string{"stream", "platform", "consumer_group"},
	},
}
