package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objStreamConsume = Objective{
	ID:                "dependency.stream_consume",
	Kind:              model.KindDependency,
	Type:              "stream_consume",
	Description:       "Stream consumption dependencies (Kinesis, Kafka Streams, DynamoDB Streams)",
	ConnectionContext: "Connection mapping must include stream source and processing conditions.",
}
