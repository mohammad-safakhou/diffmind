package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objQueuePublish = Objective{
	ID:                "dependency.queue_publish",
	Kind:              model.KindDependency,
	Type:              "queue_publish",
	Description:       "Queue/topic publish operations (SQS, SNS, Kafka, RabbitMQ)",
	ConnectionContext: "Connection mapping must include destination and publish operation step.",
}
