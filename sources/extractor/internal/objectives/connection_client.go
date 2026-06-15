package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

// objConnectionClient surfaces the shared connection BACKBONES a service uses —
// the datasource / ORM-repository base, HTTP client bean, and messaging/cache
// clients — together with the config property that wires each one. It is not a
// graph node (KindClient): a deterministic pass resolves each client to a
// concrete instance from config and fans that identity to every operation that
// uses the client, replacing per-operation instance guessing. Discovery here is
// deliberately small and high-level: list the clients, not their call sites.
var objConnectionClient = Objective{
	ID:          "client.connection_client",
	Kind:        model.KindClient,
	Type:        "connection_client",
	Description: "Shared connection backbones: datasource/ORM-repository base, HTTP client bean, and messaging (SQS/SNS/Kafka/Kinesis) or cache (Redis) clients",
	DiscoveryPrompt: `Identify the SHARED CONNECTION CLIENTS this service uses to talk to external
systems — NOT individual operations, the reusable backbone each operation goes
through.

WHAT COUNTS AS A CLIENT/BACKBONE:
- Database: the configured DataSource / EntityManager / ORM base (Spring Data
  repositories share one DataSource; GORM *gorm.DB; Sequelize/Prisma client;
  Django default DB; ActiveRecord connection).
- HTTP: a shared HTTP client/bean (Feign @FeignClient, a configured RestTemplate/
  WebClient bean, an Axios instance, a generated API client).
- Messaging: the SQS/SNS/Kafka/Kinesis client, template, or producer/consumer
  factory used to publish/consume.
- Cache: the Redis/Memcached client or cache manager.

FOR EACH CLIENT REPORT (in details):
- kind: one of db | http | queue | cache | stream
- the bean/variable/field NAME as the item "name" (e.g. "orderRepository",
  "sqsClient", "billingClient", "redisTemplate")
- symbol: its declared type / bean class when visible (e.g. "OrderRepository",
  "software.amazon.awssdk.services.sqs.SqsClient")
- framework: the library/framework (spring-data, feign, aws-sdk, gorm,
  sequelize, jedis, lettuce, ...)
- config_anchor: the EXACT config property key that configures it, when you can
  find it (e.g. "spring.datasource.url", "services.billing.url",
  "app.sqs.order-events.url"). This is what lets us resolve the concrete
  instance — include it whenever a config file references the client.

RULES:
- Report each distinct client ONCE. Two repositories on the same DataSource are
  ONE db client (the DataSource), unless the repo wires a SECOND datasource.
- Do NOT list per-operation rows; another objective already enumerates the
  operations. Here we only want the backbones they share.
- Only report config_anchor you can confirm by reading a config/build file.`,
	// Discovery-only: clients are resolved deterministically, never enriched.
	DetailPrompt:      "",
	ConnectionContext: "",
}
