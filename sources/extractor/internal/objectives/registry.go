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
			DiscoveryPrompt: `Find ALL externally reachable HTTP API routes exposed by this service.

FRAMEWORK-SPECIFIC PATTERNS TO CHECK:
- Spring Boot: @RestController, @Controller, @RequestMapping, @GetMapping, @PostMapping, @PutMapping, @PatchMapping, @DeleteMapping
- JAX-RS: @Path, @GET, @POST, @PUT, @DELETE
- Node.js: Express router.get/post/put/delete, Fastify routes, NestJS @Get/@Post/@Controller
- Python: Flask @app.route, Django urlpatterns, FastAPI @app.get/@app.post
- Go: http.HandleFunc, mux.HandleFunc, gin.GET/POST, echo.GET/POST

FOR EACH ROUTE EXTRACT:
- HTTP method and path pattern (e.g., GET /v1/content-ranker)
- Handler class/function name and package
- Request input parameters (path params, query params, request body type)
- Authentication/authorization annotations (e.g., @PreAuthorize, @Secured)
- Response type

ALSO CHECK:
- Actuator/health/management endpoints (Spring Boot /actuator/*, /app/health)
- Swagger/OpenAPI documentation endpoints
- Debug/admin endpoints
- Infrastructure configuration files (helm values, *values.yaml, config/*.yaml) for ingress definitions that reveal exposed routes

Do NOT include webhook callback endpoints (those are a separate objective).
Include route path, HTTP method, handler symbol, request inputs, and validation entry points.`,
			DetailPrompt: `For this HTTP route, extract the complete handler flow in execution order:
1. Authentication/authorization checks (Spring Security filters, JWT validation, @PreAuthorize)
2. Request validation (@Valid, custom validators, input sanitization)
3. Business logic flow (service calls in order)
4. ALL downstream dependency operations - for each one identify:
   - DB operations: exact table names, read vs write, repository method
   - Outbound HTTP calls: target service, method/path
   - Queue publishes: queue/topic name, message type
   - Cache operations: cache name, get/put/evict
5. Response mapping and error handling
6. For DB operations include table names and read/write operation type.`,
			ConnectionContext: "Prioritize ordered call-path mapping from HTTP route to downstream dependencies with conditions.",
		},
		{
			ID:          "exposure.webhook",
			Kind:        model.KindExposure,
			Type:        "webhook",
			Description: "HTTP webhook callback endpoints (incoming third-party callbacks)",
			DiscoveryPrompt: `Find webhook/callback endpoints - these are HTTP endpoints that receive callbacks from external systems.

PATTERNS TO CHECK:
- Endpoints with signature/HMAC verification (e.g., X-Hub-Signature, Stripe-Signature)
- Endpoints named *webhook*, *callback*, *notify*, *hook*
- Endpoints that receive events from third-party systems (payment processors, CRM, etc.)
- Spring Boot: @PostMapping with webhook/callback in path
- Node.js: routes handling POST with signature verification

FOR EACH WEBHOOK EXTRACT:
- Path and HTTP method
- Signature/auth verification mechanism
- Event type branching (different handlers for different event types)
- Payload parsing and validation
- Idempotency/duplicate handling

If no webhooks exist, return {"items": []}.`,
			DetailPrompt:      "For this webhook, extract signature/auth checks, payload schema, branching rules, idempotency/duplicate handling, and ordered downstream operations.",
			ConnectionContext: "Map webhook-to-dependency conditional paths with explicit guard expressions.",
		},
		{
			ID:          "exposure.rpc_endpoint",
			Kind:        model.KindExposure,
			Type:        "rpc_endpoint",
			Description: "RPC/gRPC entrypoints exposed by the service",
			DiscoveryPrompt: `Find externally reachable RPC entrypoints exposed by this service.

PATTERNS TO CHECK:
- gRPC: protobuf service definitions (.proto files), generated server stubs, @GrpcService
- Thrift: .thrift IDL files, generated server handlers
- SOAP: @WebService, WSDL-defined operations

FOR EACH RPC ENDPOINT EXTRACT:
- Service name and method name
- Request/response message types
- Handler implementation class/function

If no RPC endpoints exist, return {"items": []}.`,
			DetailPrompt:      "For this RPC endpoint, extract request contract, auth/validation, handler flow order, and downstream actions.",
			ConnectionContext: "Map RPC endpoint paths to dependencies with explicit branch conditions.",
		},
		{
			ID:          "exposure.queue_consumer",
			Kind:        model.KindExposure,
			Type:        "queue_consumer",
			Description: "Queue/topic consumers and message listeners (SQS, Kafka, RabbitMQ, Kinesis, JMS)",
			DiscoveryPrompt: `Find ALL message consumer entrypoints in this service - these are listeners/consumers that receive messages from queues or streams.

FRAMEWORK-SPECIFIC PATTERNS TO CHECK:
- AWS SQS: @SqsListener, @SqsMessageDrivenAnnotation, SQSListener, QueueMessageHandler, AmazonSQSAsync.receiveMessage, boto3 sqs.receive_message
- AWS Kinesis: KinesisConsumer, @KinesisListener, KCL (Kinesis Client Library) RecordProcessor, KinesisConsumersStarter
- Kafka: @KafkaListener, KafkaConsumer, ConsumerFactory
- RabbitMQ: @RabbitListener, @RabbitHandler, SimpleMessageListenerContainer
- JMS: @JmsListener, MessageListener
- Spring Cloud Stream: @StreamListener, @Input
- AWS Lambda SQS triggers: Lambda handler with SQSEvent parameter
- Python: boto3 sqs client polling, Lambda handler for SQS events

FOR EACH CONSUMER EXTRACT:
- Queue/topic/stream name (check application.yml/properties AND any *values.yaml / config/*.yaml for the actual queue name or ARN)
- Consumer handler class/function name
- Message/payload type being consumed
- Concurrency/batch settings
- Error handling (DLQ, retry policy)
- The environment variables or config properties that define the queue URL/name

IMPORTANT: Check infrastructure configuration files (helm values, *values.yaml, application.yml, application.properties) for queue name bindings and ARNs.`,
			DetailPrompt: `For this consumer, extract:
1. Queue/topic/stream name and how it's configured (env var, property, hardcoded)
2. Payload contract (message type, deserialization)
3. Message validation and filtering
4. Handler flow in execution order
5. ALL downstream dependency operations triggered by this consumer
6. Retry/error handling (DLQ, max retries, backoff)
7. Batch vs single message processing
8. Concurrency settings`,
			ConnectionContext: "Map queue-consumer paths to dependencies and include queue destination config guards.",
		},
		{
			ID:          "exposure.scheduled_job",
			Kind:        model.KindExposure,
			Type:        "scheduled_job",
			Description: "Scheduled jobs, cron triggers, and startup runners",
			DiscoveryPrompt: `Find ALL scheduled/background entrypoints in this service.

PATTERNS TO CHECK:
- Spring Boot: @Scheduled (fixedDelay, fixedRate, cron), @EnableScheduling, CommandLineRunner, ApplicationRunner
- Spring Boot: ShedLock (@SchedulerLock) for distributed locking
- Quartz: @DisallowConcurrentExecution, JobDetail, Trigger
- Node.js: node-cron, node-schedule, setInterval
- Python: APScheduler, Celery beat, crontab entries
- AWS: CloudWatch Events/EventBridge rules triggering Lambdas

FOR EACH JOB EXTRACT:
- Schedule expression (cron, fixedDelay, fixedRate with exact values)
- Profile/property guards (@Profile, @ConditionalOnProperty)
- Entry method and class
- What the job does (brief description)
- Distributed locking (ShedLock, database locks)

IMPORTANT: Check for @Profile annotations - some jobs only run in specific environments (e.g., @Profile("prod")).`,
			DetailPrompt: `For this job, extract:
1. Trigger conditions (schedule expression, profile/property guards)
2. Distributed locking configuration (ShedLock name, lockAtLeast, lockAtMost)
3. Execution flow in order
4. Dataset selection logic (what data does it query/process?)
5. ALL downstream dependency operations
6. Error handling and recovery`,
			ConnectionContext: "Map scheduled-job paths with schedule/profile/property guards.",
		},
		{
			ID:          "exposure.cli_command",
			Kind:        model.KindExposure,
			Type:        "cli_command",
			Description: "CLI command entrypoints, script command handlers, and Lambda function handlers",
			DiscoveryPrompt: `Find CLI command entrypoints, main method dispatch, management commands, and AWS Lambda function handlers that trigger business flows.

PATTERNS TO CHECK:
- Java: main() methods, Spring Shell @ShellComponent, Picocli @Command
- Python: console_scripts in setup.py/pyproject.toml, argparse, click @command, Lambda handler functions
- Node.js: bin scripts in package.json, commander/yargs commands
- Go: cobra commands, flag-based dispatch
- AWS Lambda: handler functions (def handler, exports.handler, @LambdaHandler)
- AWS SAM: template.yaml Handler definitions

FOR EACH ENTRYPOINT EXTRACT:
- Command name/path
- Arguments and options
- Handler function/class
- What it triggers

If this is a standard web service with no CLI commands or Lambda handlers, return {"items": []}.`,
			DetailPrompt:      "For this CLI entrypoint, extract arguments, command options, validation, and ordered downstream operations.",
			ConnectionContext: "Map command-triggered execution paths to dependencies.",
		},
		{
			ID:          "dependency.db_operation",
			Kind:        model.KindDependency,
			Type:        "db_operation",
			Description: "Database operations (SQL, NoSQL, Redis, DynamoDB, Elasticsearch)",
			DiscoveryPrompt: `Find ALL database operations reachable from this service's code.

FRAMEWORK-SPECIFIC PATTERNS TO CHECK:
- Spring Data JPA: @Repository, JpaRepository, CrudRepository, @Query, @Modifying, EntityManager
- Spring JDBC: JdbcTemplate, NamedParameterJdbcTemplate
- MyBatis: @Mapper, @Select, @Insert, @Update, @Delete, XML mapper files
- Hibernate: Session, SessionFactory, @Entity classes
- Redis: RedisTemplate, StringRedisTemplate, Jedis, JedisPool, RedisService, @Cacheable/@CacheEvict/@CachePut with Redis cache manager, Lettuce
- DynamoDB: DynamoDBMapper, DynamoDBTable, PynamoDB (Python), @DynamoDBTable
- Elasticsearch: ElasticsearchRepository, RestHighLevelClient, ElasticsearchOperations
- MongoDB: MongoRepository, MongoTemplate
- Raw SQL: DataSource, Connection, PreparedStatement
- Python: psycopg2, SQLAlchemy, boto3 dynamodb, redis-py
- Liquibase/Flyway migrations (mention but don't list as runtime ops)

FOR EACH DB OPERATION EXTRACT:
- Database type (PostgreSQL, MySQL, Redis, DynamoDB, Elasticsearch, MongoDB)
- Table/entity/key name
- Operation type (read/write/upsert/delete)
- Repository/DAO class and method name
- The datasource/connection config property name

IMPORTANT:
- Check application.yml/properties for datasource configuration to identify database type and connection details
- Check any *values.yaml / config/*.yaml for database connection environment variables
- DO NOT collapse multiple repository methods into one item - list each distinct operation
- Redis GET/SET/DEL operations count as db_operations
- In-memory caches (EhCache, Caffeine) with NO external backing store are NOT db_operations`,
			DetailPrompt: `For this DB operation, provide:
1. Exact table/entity/key names
2. Database type and connection source
3. Operation semantics (SELECT/INSERT/UPDATE/DELETE, or Redis GET/SET/DEL, or DynamoDB GetItem/PutItem)
4. Transaction context (@Transactional, isolation level)
5. Input parameters and query conditions
6. Repository/DAO class and method`,
			ConnectionContext: "Connection mapping must include db table and read/write operation per step.",
		},
		{
			ID:          "dependency.outbound_http",
			Kind:        model.KindDependency,
			Type:        "outbound_http",
			Description: "Outbound HTTP calls to other services or external APIs",
			DiscoveryPrompt: `Find ALL outbound HTTP service calls made by this service.

FRAMEWORK-SPECIFIC PATTERNS TO CHECK:
- Spring Feign: @FeignClient, @RequestLine
- Spring RestTemplate: RestTemplate.getForObject/postForObject/exchange
- Spring WebClient: WebClient.get()/post()/put()/delete()
- Retrofit: Retrofit interface methods with @GET/@POST/@PUT/@DELETE annotations, Retrofit.Builder
- OkHttp: OkHttpClient, Request.Builder
- Apache HttpClient: HttpGet, HttpPost, CloseableHttpClient
- Java 11+ HttpClient: HttpClient.newHttpClient(), HttpRequest.newBuilder()
- Node.js: axios, fetch, got, node-fetch, superagent
- Python: requests, httpx, urllib3, aiohttp, boto3 (for AWS API calls)

FOR EACH OUTBOUND CALL EXTRACT:
- Target service name or host (check application.yml/properties AND any *values.yaml / config/*.yaml for the actual URL)
- HTTP method and path
- Client class/interface name
- Resilience patterns (circuit breaker, retry, timeout - check @CircuitBreaker, @Retry, Resilience4j config)
- Request/response types

CRITICAL: Check infrastructure configuration files for the ACTUAL base URLs:
- application.yml/properties for service.*.url or *.baseUrl properties
- any *values.yaml / config/production/*.yaml for environment-specific URLs
- These URLs often reveal the target service name (e.g., http://gateway-service.lead2cash.svc.cluster.local/)

DO NOT miss Retrofit interfaces - they define HTTP calls via annotated Java interfaces.`,
			DetailPrompt: `For this outbound HTTP dependency, extract:
1. Target service/host and the config property that defines the URL
2. Exact endpoint path and HTTP method
3. Request body type and response type
4. Circuit breaker configuration (name, failure threshold, timeout)
5. Retry configuration (max attempts, backoff)
6. Timeout configuration
7. Error handling (fallback methods)`,
			ConnectionContext: "Connection mapping must include outbound method/path and guard condition.",
		},
		{
			ID:          "dependency.outbound_rpc",
			Kind:        model.KindDependency,
			Type:        "outbound_rpc",
			Description: "Outbound RPC/gRPC calls",
			DiscoveryPrompt: `Find outbound RPC dependencies (gRPC stubs/channels, protobuf RPC clients, thrift clients).
Extract target service, RPC method, request type, and callsite.
If no outbound RPC calls exist, return {"items": []}.`,
			DetailPrompt:      "For this outbound RPC dependency, extract rpc service/method, request/response contracts, retry/timeout behavior, and call conditions.",
			ConnectionContext: "Connection mapping must include rpc target service/method and guard condition.",
		},
		{
			ID:          "dependency.queue_publish",
			Kind:        model.KindDependency,
			Type:        "queue_publish",
			Description: "Queue/topic publish operations (SQS, SNS, Kafka, RabbitMQ)",
			DiscoveryPrompt: `Find ALL message publish operations in this service - anywhere the code sends/publishes messages to a queue, topic, or notification service.

FRAMEWORK-SPECIFIC PATTERNS TO CHECK:
- AWS SQS: SqsTemplate.send, AmazonSQS.sendMessage, SQSAsyncClient.sendMessage, QueueMessagingTemplate.convertAndSend, boto3 sqs.send_message
- AWS SNS: AmazonSNS.publish, SnsTemplate.send, SNSAsyncClient.publish, NotificationMessagingTemplate, boto3 sns.publish
- Kafka: KafkaTemplate.send, KafkaProducer.send
- RabbitMQ: RabbitTemplate.convertAndSend, AmqpTemplate.send
- Spring Cloud Stream: @Output, StreamBridge.send
- EventBridge: AmazonEventBridge.putEvents, EventBridgeClient.putEvents

FOR EACH PUBLISH OPERATION EXTRACT:
- Destination queue/topic/ARN name (check application.yml AND any *values.yaml / config/*.yaml for the actual queue/topic name)
- Message/payload type being published
- Publisher class/method
- Sync vs async publishing
- The config property or environment variable that defines the destination

IMPORTANT: Check infrastructure configuration files (helm values, *values.yaml, application.yml, application.properties) for queue URLs, topic ARNs, and queue names.`,
			DetailPrompt: `For this publish operation, extract:
1. Destination queue/topic/ARN and how it's configured
2. Message type and payload structure
3. Publishing method (sync/async/batch)
4. Serialization format
5. Message attributes/headers
6. Error handling on publish failure`,
			ConnectionContext: "Connection mapping must include destination and publish operation step.",
		},
		{
			ID:                "dependency.command_exec",
			Kind:              model.KindDependency,
			Type:              "command_exec",
			Description:       "External command/process execution",
			DiscoveryPrompt:   "Find shell/process execution dependencies (Runtime.exec, ProcessBuilder, os command wrappers, subprocess, scripts). If none exist, return {\"items\": []}.",
			DetailPrompt:      "For this command dependency, extract executed command pattern, arguments, environment inputs, and guard conditions.",
			ConnectionContext: "Connection mapping must include executed command and trigger condition.",
		},
		{
			ID:          "dependency.cache_operation",
			Kind:        model.KindDependency,
			Type:        "cache_operation",
			Description: "External cache operations (Redis, Memcached, distributed caches)",
			DiscoveryPrompt: `Find ALL external/distributed cache operations in this service.

PATTERNS TO CHECK:
- Redis: RedisTemplate, StringRedisTemplate, Jedis, JedisPool, Lettuce, RedisService
- Spring Cache with Redis backing: @Cacheable, @CacheEvict, @CachePut with Redis CacheManager
- Memcached: MemcachedClient
- Hazelcast: HazelcastInstance
- Python: redis-py, aioredis

FOR EACH CACHE OPERATION EXTRACT:
- Cache type (Redis, Memcached, etc.)
- Operation type (get/set/delete/expire)
- Key pattern/prefix
- Cache name or namespace
- TTL/expiration settings
- Service class that uses the cache

NOTE: In-memory-only caches (EhCache without external store, Caffeine, Guava Cache) are NOT external cache operations - do not include them.
If no external cache operations exist, return {"items": []}.`,
			DetailPrompt: `For this cache operation, provide:
1. Cache type (Redis, Memcached, etc.) and connection config
2. Key pattern and namespace
3. Operation types (read/write/evict)
4. TTL/expiration configuration
5. Serialization format
6. Cache-aside vs write-through pattern`,
			ConnectionContext: "Connection mapping must include cache key pattern and read/write operation per step.",
		},
		{
			ID:          "dependency.stream_consume",
			Kind:        model.KindDependency,
			Type:        "stream_consume",
			Description: "Stream consumption dependencies (Kinesis, Kafka Streams, DynamoDB Streams)",
			DiscoveryPrompt: `Find stream consumption dependencies - these are streaming data sources the service reads from.

PATTERNS TO CHECK:
- AWS Kinesis: KinesisClient, KCL (Kinesis Client Library), KinesisConsumersStarter, IRecordProcessor
- Kafka Streams: KafkaStreams, StreamsBuilder, KStream
- DynamoDB Streams: DynamoDBStreamsClient
- AWS Lambda with Kinesis/DynamoDB stream triggers

FOR EACH STREAM CONSUMER EXTRACT:
- Stream name/ARN
- Consumer application name
- Processing library (KCL, Kafka Streams, etc.)
- Checkpoint/offset management strategy

If no stream consumers exist, return {"items": []}.`,
			DetailPrompt: `For this stream consumer, extract:
1. Stream name/ARN and config
2. Consumer group/application name
3. Processing model (batch/single record)
4. Checkpoint strategy
5. Error handling and retry
6. Downstream operations triggered`,
			ConnectionContext: "Connection mapping must include stream source and processing conditions.",
		},
	}
}
