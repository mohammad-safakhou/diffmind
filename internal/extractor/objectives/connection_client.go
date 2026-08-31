package objectives

import "github.com/mohammad-safakhou/diffmind/internal/extractor/model"

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
	// Discovery-only: clients are resolved deterministically, never enriched.
	ConnectionContext: "",
}
