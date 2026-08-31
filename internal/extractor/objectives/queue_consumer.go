package objectives

import "github.com/mohammad-safakhou/diffmind/internal/extractor/model"

var objQueueConsumer = Objective{
	ID:                "exposure.queue_consumer",
	Kind:              model.KindExposure,
	Type:              "queue_consumer",
	Description:       "Queue/topic consumers and message listeners (SQS, Kafka, RabbitMQ, Kinesis, JMS)",
	ConnectionContext: "Map queue-consumer paths to dependencies and include queue destination config guards.",
}
