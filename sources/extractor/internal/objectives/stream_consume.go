package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objStreamConsume = Objective{
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
}
