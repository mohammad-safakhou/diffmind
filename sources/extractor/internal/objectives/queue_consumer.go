package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objQueueConsumer = Objective{
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
	ConnectionContext: "Map queue-consumer paths to dependencies and include queue destination config guards.",
}
