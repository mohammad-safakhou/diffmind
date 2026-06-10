package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objQueuePublish = Objective{
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
}
