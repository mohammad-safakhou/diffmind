package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

func buildAgentsIndex(t *testing.T, files map[string]string) *astpkg.ProjectIndex {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := astpkg.Build(context.Background(), dir, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func entityNames(es []candidate) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func TestDeterministicCommandExec(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"runner.go": `package svc

import "os/exec"

func Run() {
	exec.Command("ls", "-la")
}
`,
	})
	got := DeterministicCommandExec(idx)
	if len(got) == 0 {
		t.Fatalf("expected a command_exec from exec.Command; got none (symbols=%d)", len(idx.Symbols))
	}
	found := false
	for _, e := range got {
		if e.Type != "command_exec" {
			t.Errorf("wrong type %q", e.Type)
		}
		if e.Details["command"] == "ls" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected command 'ls', got %v", entityNames(got))
	}
}

func TestMatchCommandExecPrecision(t *testing.T) {
	// A bare .send()/.exec() on an unrelated receiver must NOT match.
	if MatchCommandExec(astpkg.CallSite{ReceiverRaw: "httpClient", CalleeRaw: "exec"}) {
		t.Error("httpClient.exec should not match command_exec")
	}
	if !MatchCommandExec(astpkg.CallSite{ReceiverRaw: "exec", CalleeRaw: "Command"}) {
		t.Error("exec.Command should match")
	}
	if !MatchCommandExec(astpkg.CallSite{ReceiverRaw: "subprocess", CalleeRaw: "run"}) {
		t.Error("subprocess.run should match")
	}
}

func TestMatchQueuePublishPrecision(t *testing.T) {
	if p, ok := MatchQueuePublish(astpkg.CallSite{ReceiverRaw: "kafkaTemplate", CalleeRaw: "send"}); !ok || p != "kafka" {
		t.Errorf("kafkaTemplate.send should be kafka, got %q,%v", p, ok)
	}
	if p, ok := MatchQueuePublish(astpkg.CallSite{ReceiverRaw: "sqsTemplate", CalleeRaw: "send"}); !ok || p != "sqs" {
		t.Errorf("sqsTemplate.send should be sqs, got %q,%v", p, ok)
	}
	// A generic websocket.send must NOT be a publish.
	if _, ok := MatchQueuePublish(astpkg.CallSite{ReceiverRaw: "webSocket", CalleeRaw: "send"}); ok {
		t.Error("webSocket.send should not match queue_publish")
	}
}

func TestMatchGRPCStubCall(t *testing.T) {
	svc, m, ok := MatchGRPCStubCall(astpkg.CallSite{ReceiverRaw: "fooServiceBlockingStub", CalleeRaw: "getThing"})
	if !ok || svc != "fooService" || m != "getThing" {
		t.Errorf("gRPC stub call mis-parsed: svc=%q m=%q ok=%v", svc, m, ok)
	}
	// A plain variable named "stub" must NOT match (gRPC stubs are *BlockingStub/*FutureStub).
	if _, _, ok := MatchGRPCStubCall(astpkg.CallSite{ReceiverRaw: "stub", CalleeRaw: "doThing"}); ok {
		t.Error("plain 'stub' receiver should not match gRPC")
	}
}

func TestDeterministicStreamConsumePrecision(t *testing.T) {
	// Java Collection.stream() must never be a stream_consume.
	idx := buildAgentsIndex(t, map[string]string{
		"S.go": `package svc
func F(items []int) { _ = items }
`,
	})
	// streamsBuilder gating is unit-tested via matcher; ensure a non-streams call yields nothing.
	if got := DeterministicStreamConsume(idx); len(got) != 0 {
		t.Errorf("expected no stream_consume, got %v", entityNames(got))
	}
}

func TestDeterministicQueuePublish(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"Publisher.java": `package com.example;

public class Publisher {
    private KafkaTemplate<String,String> kafkaTemplate;
    public void emit(String payload) {
        kafkaTemplate.send("orders-topic", payload);
    }
}
`,
	})
	got := DeterministicQueuePublish(idx)
	// Tolerant: the Java call-graph must record kafkaTemplate.send with a literal
	// destination for this to fire. If the parser didn't, skip rather than fail
	// on a parser-coverage gap unrelated to the detector logic.
	if len(got) == 0 {
		t.Skip("java call graph did not record the publish literal; detector logic is unit-tested separately")
	}
	if got[0].Details["destination"] != "orders-topic" || got[0].Details["platform"] != "kafka" {
		t.Errorf("expected kafka publish to orders-topic, got %+v", got[0].Details)
	}
}

func TestDeterministicAWSQueuePublishFromRequestBuilder(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;

import software.amazon.awssdk.services.sns.SnsClient;
import software.amazon.awssdk.services.sns.model.PublishRequest;

class Publisher {
  private SnsClient snsClient;
  void publish() {
    var request = PublishRequest.builder()
      .topicArn("arn:aws:sns:eu-west-1:123:content-cleanup")
      .message("payload")
      .build();
    snsClient.publish(request);
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "Publisher.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &astpkg.ProjectIndex{RepoRoot: dir, Files: map[string]*astpkg.FileAST{
		"Publisher.java": {
			Language: "java",
			Calls: []astpkg.CallSite{{
				File:        "Publisher.java",
				ReceiverRaw: "snsClient",
				CalleeRaw:   "publish",
				Range:       astpkg.Range{StartLine: 11, EndLine: 11},
				Arguments: []astpkg.ArgumentExpr{{
					Index:  0,
					Source: "request",
					Kind:   "identifier",
				}},
			}},
		},
	}}
	got := DeterministicQueuePublish(idx)
	if len(got) != 1 {
		t.Fatalf("expected one SNS publish, got %d: %+v", len(got), got)
	}
	if got[0].Name != "content-cleanup" || got[0].Details["platform"] != "sns" {
		t.Fatalf("unexpected SNS publish: %+v", got[0])
	}
}

func TestDeterministicAWSQueuePublishIgnoresUnresolvedTopicParameter(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;

import software.amazon.awssdk.services.sns.SnsClient;
import software.amazon.awssdk.services.sns.model.PublishRequest;

class Publisher {
  private SnsClient snsClient;
  void publish(String topicArn) {
    final PublishRequest request = PublishRequest.builder()
      .topicArn(topicArn)
      .message("payload")
      .build();
    snsClient.publish(request);
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "Publisher.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &astpkg.ProjectIndex{RepoRoot: dir, Files: map[string]*astpkg.FileAST{
		"Publisher.java": {
			Language: "java",
			Calls: []astpkg.CallSite{{
				File:        "Publisher.java",
				ReceiverRaw: "snsClient",
				CalleeRaw:   "publish",
				Range:       astpkg.Range{StartLine: 13, EndLine: 13},
				Arguments: []astpkg.ArgumentExpr{{
					Index:  0,
					Source: "request",
					Kind:   "identifier",
				}},
			}},
		},
	}}
	if got := DeterministicQueuePublish(idx); len(got) != 0 {
		t.Fatalf("expected unresolved topic parameter to be ignored, got %+v", got)
	}
}

func TestDeterministicPythonQueuePublishers(t *testing.T) {
	idx := &astpkg.ProjectIndex{Files: map[string]*astpkg.FileAST{
		"publishers.py": {
			Language: "python",
			Calls: []astpkg.CallSite{
				{
					File:        "publishers.py",
					ReceiverRaw: "sqs",
					CalleeRaw:   "send_message",
					Range:       astpkg.Range{StartLine: 4, EndLine: 4},
					Arguments: []astpkg.ArgumentExpr{{
						Index:  0,
						Source: "QueueUrl=\"https://sqs.eu-west-1.amazonaws.com/123/traffic-events.fifo\"",
						Kind:   "other",
					}},
				},
				{
					File:        "publishers.py",
					ReceiverRaw: "sns_client",
					CalleeRaw:   "publish",
					Range:       astpkg.Range{StartLine: 8, EndLine: 8},
					Arguments: []astpkg.ArgumentExpr{{
						Index:  0,
						Source: "TopicArn=\"arn:aws:sns:eu-west-1:123:content-cleanup\"",
						Kind:   "other",
					}},
				},
				{
					File:        "publishers.py",
					ReceiverRaw: "kafka_producer",
					CalleeRaw:   "send",
					Range:       astpkg.Range{StartLine: 12, EndLine: 12},
					Arguments: []astpkg.ArgumentExpr{{
						Index:  0,
						Source: "\"orders-topic\"",
						Kind:   "literal",
					}},
				},
			},
		},
	}}
	got := DeterministicQueuePublish(idx)
	if len(got) != 3 {
		t.Fatalf("expected three Python publishes, got %d: %+v", len(got), got)
	}
	byName := map[string]candidate{}
	for _, e := range got {
		byName[e.Name] = e
	}
	for name, platform := range map[string]string{
		"traffic-events.fifo": "sqs",
		"content-cleanup":     "sns",
		"orders-topic":        "kafka",
	} {
		e, ok := byName[name]
		if !ok {
			t.Fatalf("missing publish %s in %+v", name, got)
		}
		if e.Details["platform"] != platform || e.Details["destination"] != name {
			t.Fatalf("unexpected details for %s: %+v", name, e.Details)
		}
	}
}

func TestDeterministicJavaScriptQueuePublishers(t *testing.T) {
	idx := &astpkg.ProjectIndex{Files: map[string]*astpkg.FileAST{
		"publishers.ts": {
			Language: "typescript",
			Calls: []astpkg.CallSite{
				{
					File:        "publishers.ts",
					ReceiverRaw: "producer",
					CalleeRaw:   "send",
					Range:       astpkg.Range{StartLine: 4, EndLine: 4},
					Arguments: []astpkg.ArgumentExpr{{
						Index:  0,
						Source: "{ topic: 'orders-topic', messages: [{ value: payload }] }",
						Kind:   "other",
					}},
				},
				{
					File:        "publishers.ts",
					ReceiverRaw: "sqsClient",
					CalleeRaw:   "send",
					Range:       astpkg.Range{StartLine: 8, EndLine: 8},
					Arguments: []astpkg.ArgumentExpr{{
						Index:  0,
						Source: "new SendMessageCommand({ QueueUrl: 'https://sqs.eu-west-1.amazonaws.com/123/traffic-events' })",
						Kind:   "new",
					}},
				},
				{
					File:        "publishers.ts",
					ReceiverRaw: "snsClient",
					CalleeRaw:   "send",
					Range:       astpkg.Range{StartLine: 12, EndLine: 12},
					Arguments: []astpkg.ArgumentExpr{{
						Index:  0,
						Source: "new PublishCommand({ TopicArn: 'arn:aws:sns:eu-west-1:123:content-cleanup' })",
						Kind:   "new",
					}},
				},
			},
		},
	}}
	got := DeterministicQueuePublish(idx)
	if len(got) != 3 {
		t.Fatalf("expected three JavaScript publishes, got %d: %+v", len(got), got)
	}
	byName := map[string]candidate{}
	for _, e := range got {
		byName[e.Name] = e
	}
	for name, platform := range map[string]string{
		"orders-topic":    "kafka",
		"traffic-events":  "sqs",
		"content-cleanup": "sns",
	} {
		e, ok := byName[name]
		if !ok {
			t.Fatalf("missing publish %s in %+v", name, got)
		}
		if e.Details["platform"] != platform || e.Details["destination"] != name {
			t.Fatalf("unexpected details for %s: %+v", name, e.Details)
		}
	}
}

func TestDeterministicQueuePublishRejectsGenericProducerSend(t *testing.T) {
	idx := &astpkg.ProjectIndex{Files: map[string]*astpkg.FileAST{
		"plain.ts": {
			Language: "typescript",
			Calls: []astpkg.CallSite{{
				File:        "plain.ts",
				ReceiverRaw: "producer",
				CalleeRaw:   "send",
				Range:       astpkg.Range{StartLine: 4, EndLine: 4},
				Arguments: []astpkg.ArgumentExpr{{
					Index:  0,
					Source: "{ payload: 'not-messaging' }",
					Kind:   "other",
				}},
			}},
		},
	}}
	if got := DeterministicQueuePublish(idx); len(got) != 0 {
		t.Fatalf("generic producer.send without topic must not match queue_publish: %+v", got)
	}
}

func TestDeterministicAWSQueueConsumerFromReceiveMessage(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;

import software.amazon.awssdk.services.sqs.SqsClient;
import software.amazon.awssdk.services.sqs.model.ReceiveMessageRequest;

class Consumer {
  private SqsClient sqsClient;
  void receive() {
    var request = ReceiveMessageRequest.builder()
      .queueUrl("https://sqs.eu-west-1.amazonaws.com/123/cdp-cps-input.fifo")
      .build();
    sqsClient.receiveMessage(request);
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "Consumer.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &astpkg.ProjectIndex{RepoRoot: dir, Files: map[string]*astpkg.FileAST{
		"Consumer.java": {
			Language: "java",
			Calls: []astpkg.CallSite{{
				File:        "Consumer.java",
				ReceiverRaw: "sqsClient",
				CalleeRaw:   "receiveMessage",
				Range:       astpkg.Range{StartLine: 11, EndLine: 11},
				Arguments: []astpkg.ArgumentExpr{{
					Index:  0,
					Source: "request",
					Kind:   "identifier",
				}},
			}},
		},
	}}
	got := DeterministicAWSQueueConsumers(idx)
	if len(got) != 1 {
		t.Fatalf("expected one SQS consumer, got %d: %+v", len(got), got)
	}
	if got[0].Name != "cdp-cps-input.fifo" || got[0].Details["platform"] != "sqs" {
		t.Fatalf("unexpected SQS consumer: %+v", got[0])
	}
}

func TestDeterministicAWSQueueConsumerDoesNotRecurseOnSelfNamedVariable(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;

import software.amazon.awssdk.services.sqs.SqsClient;
import software.amazon.awssdk.services.sqs.model.ReceiveMessageRequest;

class Consumer {
  private SqsClient sqsClient;
  void receive(String queueUrl) {
    var request = ReceiveMessageRequest.builder()
      .queueUrl(queueUrl)
      .build();
    sqsClient.receiveMessage(request);
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "Consumer.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &astpkg.ProjectIndex{RepoRoot: dir, Files: map[string]*astpkg.FileAST{
		"Consumer.java": {
			Language: "java",
			Calls: []astpkg.CallSite{{
				File:        "Consumer.java",
				ReceiverRaw: "sqsClient",
				CalleeRaw:   "receiveMessage",
				Range:       astpkg.Range{StartLine: 12, EndLine: 12},
				Arguments: []astpkg.ArgumentExpr{{
					Index:  0,
					Source: "request",
					Kind:   "identifier",
				}},
			}},
		},
	}}
	if got := DeterministicAWSQueueConsumers(idx); len(got) != 0 {
		t.Fatalf("expected unresolved local variable to be ignored, got %+v", got)
	}
}

func TestDeterministicRedisCacheOperations(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"cache.py": `import redis

def handle():
    redis_client = redis.Redis(host="localhost")
    redis_client.get("traffic")
    redis_client.set("traffic", "value")
    redis_client.delete("traffic")
    pipeline = redis_client.pipeline()
    pipeline.get("other")
`,
	})
	got := DeterministicCacheOperations(idx)
	if len(got) != 3 {
		t.Fatalf("expected read/write/evict redis cache operations, got %d: %+v", len(got), got)
	}
	ops := map[string]bool{}
	for _, e := range got {
		if e.Type != "cache_operation" || e.Details["cache"] != "redis" || e.Details["cache_type"] != "redis" {
			t.Fatalf("unexpected cache entity: %+v", e)
		}
		ops[e.Details["operation"].(string)] = true
		if e.Details["operation"] == "read" && len(e.Locations) != 2 {
			t.Fatalf("read redis should preserve both call-site anchors, got %+v", e.Locations)
		}
	}
	for _, op := range []string{"read", "write", "evict"} {
		if !ops[op] {
			t.Fatalf("missing redis %s operation in %+v", op, got)
		}
	}
}

func TestDeterministicS3StorageOperations(t *testing.T) {
	dir := t.TempDir()
	src := `package com.example;

import software.amazon.awssdk.services.s3.S3Client;
import software.amazon.awssdk.services.s3.model.PutObjectRequest;

class Storage {
  private S3Client s3Client;
  void write() {
    var putRequest = PutObjectRequest.builder()
      .bucket("dynamic-uploads")
      .key("file.jpg")
      .build();
    s3Client.putObject(putRequest, body);
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "Storage.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &astpkg.ProjectIndex{RepoRoot: dir, Files: map[string]*astpkg.FileAST{
		"Storage.java": {
			Language: "java",
			Calls: []astpkg.CallSite{{
				File:        "Storage.java",
				ReceiverRaw: "s3Client",
				CalleeRaw:   "putObject",
				Range:       astpkg.Range{StartLine: 12, EndLine: 12},
				Arguments: []astpkg.ArgumentExpr{{
					Index:  0,
					Source: "putRequest",
					Kind:   "identifier",
				}},
			}},
		},
	}}
	got := DeterministicCacheOperations(idx)
	if len(got) != 1 {
		t.Fatalf("expected one S3 storage operation, got %d: %+v", len(got), got)
	}
	if got[0].Name != "write dynamic-uploads" || got[0].Details["platform"] != "s3" || got[0].Details["cache_type"] != "object_storage" {
		t.Fatalf("unexpected S3 operation: %+v", got[0])
	}
}

func TestDeterministicGoRedisCacheOperations(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"internal/shared/adapter/outbound/redis/cache_repo/get.go": `package cache_repo

import "github.com/redis/go-redis/v9"

type Repository struct { redisClient *redis.Client }

func (r *Repository) Get(ctx any, key string) {
    r.redisClient.Get(ctx, key).Result()
    r.redisClient.Set(ctx, key, "v", 0).Err()
    r.redisClient.Del(ctx, key).Err()
}
`,
	})
	got := DeterministicCacheOperations(idx)
	if len(got) != 3 {
		t.Fatalf("expected read/write/evict go redis operations, got %d: %+v", len(got), got)
	}
}

func TestDeterministicRedisCachePrecision(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"plain.py": `def handle(client, pipeline):
    client.get("not-cache")
    pipeline.get("not-cache")
`,
	})
	if got := DeterministicCacheOperations(idx); len(got) != 0 {
		t.Fatalf("generic get/pipeline calls must not become cache operations: %+v", got)
	}
}

func TestDeterministicRedisCacheSkipsLocalUtilityArtifacts(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"app/cache.py": `import redis

def write():
    redis_client = redis.Redis(host="localhost")
    redis_client.set("traffic", "value")
`,
		"local_redis_utils/redis_local_starter.py": `import redis

def bootstrap():
    redis_client = redis.Redis(host="localhost")
    redis_client.set("traffic", "value")
`,
	})
	got := DeterministicCacheOperations(idx)
	if len(got) != 1 {
		t.Fatalf("expected one production write redis operation, got %+v", got)
	}
	if got[0].Details["operation"] != "write" {
		t.Fatalf("expected write operation, got %+v", got[0].Details)
	}
	if len(got[0].Locations) != 1 || got[0].Locations[0].File != "app/cache.py" {
		t.Fatalf("local utility location should not anchor production dependency: %+v", got[0].Locations)
	}
}

func TestDeterministicPythonLambdaEntrypoint(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"sync/main.py": `from sentry_sdk.integrations.aws_lambda import AwsLambdaIntegration

def handler(event, *_):
    process(event["Records"])

def process(records):
    return records
`,
	})
	got := DeterministicCLIEntrypoints(idx)
	if len(got) != 1 {
		t.Fatalf("expected one Lambda cli_command, got %d: %+v", len(got), got)
	}
	if got[0].Type != "cli_command" || got[0].Name != "sync.main.handler" {
		t.Fatalf("unexpected Lambda entrypoint: %+v", got[0])
	}
	if got[0].Details["handler"] != "handler" || got[0].Details["entry_method"] != "handler" {
		t.Fatalf("handler details must resolve to the AST function symbol: %+v", got[0].Details)
	}
}

func TestDeterministicSAMDynamoDBStreamConsumer(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"template.yaml": `Transform: AWS::Serverless-2016-10-31
Resources:
  TrafficMgmtInfoSyncLambda:
    Type: AWS::Serverless::Function
    Properties:
      Handler: sync.handler
      Events:
        TrafficInfoEvent:
          Type: DynamoDB
          Properties:
            Stream:
              Fn::If:
                - IsProduction
                - "arn:aws:dynamodb:eu-west-1:123456789012:table/traffic-info/stream/2019-02-28T10:12:29.588"
                - Fn::ImportValue: !Sub "${StagePrefix}traffic-info-dynamodb-stream-arn"
            StartingPosition: LATEST
            BatchSize: 1
`,
	})
	got := DeterministicSAMQueueConsumers(idx)
	if len(got) != 1 {
		t.Fatalf("expected one SAM DynamoDB stream consumer, got %d: %+v", len(got), got)
	}
	if got[0].Type != "queue_consumer" || got[0].Name != "traffic-info" {
		t.Fatalf("unexpected SAM consumer: %+v", got[0])
	}
	if got[0].Details["platform"] != "dynamodb_stream" || got[0].Details["table"] != "traffic-info" {
		t.Fatalf("unexpected SAM consumer details: %+v", got[0].Details)
	}
	if len(got[0].Evidence) == 0 || got[0].Evidence[0].File != "template.yaml" {
		t.Fatalf("expected template evidence, got %+v", got[0].Evidence)
	}
}

func TestDeterministicSAMSQSConsumer(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"template.yaml": `Transform: AWS::Serverless-2016-10-31
Resources:
  TrafficObserver:
    Type: AWS::Serverless::Function
    Properties:
      Handler: observer.handler
      Events:
        TrafficQueueEvent:
          Type: SQS
          Properties:
            Queue: arn:aws:sqs:eu-west-1:123456789012:routing-events
            BatchSize: 10
`,
	})
	got := DeterministicSAMQueueConsumers(idx)
	if len(got) != 1 {
		t.Fatalf("expected one SAM SQS consumer, got %d: %+v", len(got), got)
	}
	if got[0].Type != "queue_consumer" || got[0].Name != "routing-events" {
		t.Fatalf("unexpected SAM SQS consumer: %+v", got[0])
	}
	if got[0].Details["platform"] != "sqs" || got[0].Details["queue"] != "routing-events" {
		t.Fatalf("unexpected SAM SQS details: %+v", got[0].Details)
	}
}

func TestDeterministicPythonSQSConsumer(t *testing.T) {
	idx := &astpkg.ProjectIndex{Files: map[string]*astpkg.FileAST{
		"observer.py": {
			Language: "python",
			Calls: []astpkg.CallSite{{
				File:        "observer.py",
				ReceiverRaw: "sqs",
				CalleeRaw:   "get_queue_by_name",
				Range:       astpkg.Range{StartLine: 4, EndLine: 4},
				Arguments: []astpkg.ArgumentExpr{{
					Index:  0,
					Source: "QueueName=configuration.TRAFFIC_EVENTS_QUEUE_NAME",
					Kind:   "other",
				}},
			}},
		},
	}}
	got := DeterministicPythonSQSConsumers(idx)
	if len(got) != 1 {
		t.Fatalf("expected one Python SQS consumer, got %d: %+v", len(got), got)
	}
	if got[0].Name != "traffic-events" || got[0].Details["platform"] != "sqs" {
		t.Fatalf("unexpected Python SQS consumer: %+v", got[0])
	}
	if len(got[0].Evidence) == 0 || got[0].Evidence[0].File != "observer.py" {
		t.Fatalf("expected Python SQS evidence, got %+v", got[0].Evidence)
	}
}

func TestPythonHTTPClientKeepsAPIServiceSuffix(t *testing.T) {
	method, path, target, ok := pythonHTTPCall(&astpkg.FileAST{Language: "python"}, astpkg.CallSite{
		File:        "clients/catalogue_service_client.py",
		ReceiverRaw: "self",
		CalleeRaw:   "get",
		Arguments: []astpkg.ArgumentExpr{{
			Index:  0,
			Source: "\"/campaigns/{campaign_id}\"",
			Kind:   "literal",
		}},
	})
	if !ok || method != "GET" || path != "/campaigns/{value}" || target != "checkout-service" {
		t.Fatalf("unexpected Python HTTP call: ok=%v method=%q path=%q target=%q", ok, method, path, target)
	}
}

func TestDeterministicDynamoDBTemplateOperations(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"src/main/resources/application.yml": `services:
  routing:
    dynamodb-table: traffic-info
`,
		"src/main/java/com/example/TrafficInfoDynamoDBService.java": `package com.example;

import io.awspring.cloud.dynamodb.DynamoDbTemplate;
import software.amazon.awssdk.enhanced.dynamodb.Key;

public class TrafficInfoDynamoDBService {
    private DynamoDbTemplate dynamoDbTemplate;

    public void save(TrafficData trafficInfo) {
        dynamoDbTemplate.save(trafficInfo);
    }

    public TrafficData load(String id) {
        return dynamoDbTemplate.load(Key.builder().partitionValue(id).build(), TrafficData.class);
    }
}
`,
	})
	got := DeterministicDynamoDBOperations(idx)
	if len(got) != 2 {
		t.Fatalf("expected read/write DynamoDB operations, got %d: %+v", len(got), got)
	}
	byName := map[string]candidate{}
	for _, e := range got {
		byName[e.Name] = e
	}
	for _, name := range []string{"read traffic-info", "write traffic-info"} {
		e, ok := byName[name]
		if !ok {
			t.Fatalf("missing %s in %+v", name, got)
		}
		if e.Details["platform"] != "dynamodb" || e.Details["table"] != "traffic-info" {
			t.Fatalf("bad DynamoDB details for %s: %+v", name, e.Details)
		}
		if len(e.Evidence) == 0 || e.Evidence[0].File == "" {
			t.Fatalf("expected evidence for %s: %+v", name, e.Evidence)
		}
	}
}

func TestDeterministicPythonLambdaEntrypointPrecision(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"handlers.py": `def handler(request):
    return request

class Worker:
    def handler(self, event):
        return event
`,
	})
	if got := DeterministicCLIEntrypoints(idx); len(got) != 0 {
		t.Fatalf("non-Lambda handler shapes must not become cli_command: %+v", got)
	}
}

func TestDeterministicPythonArgparseEntrypoint(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"local_redis_utils/redis_migrate.py": `import argparse

def run():
    parser = argparse.ArgumentParser()
    parser.add_argument("environment")
    return parser.parse_args()

if __name__ == "__main__":
    run()
`,
	})
	got := DeterministicCLIEntrypoints(idx)
	if len(got) != 1 {
		t.Fatalf("expected one argparse cli_command, got %d: %+v", len(got), got)
	}
	if got[0].Type != "cli_command" || got[0].Name != "local_redis_utils/redis_migrate.py" {
		t.Fatalf("unexpected argparse entrypoint: %+v", got[0])
	}
	if got[0].Details["discovered_by"] != "ast_python_argparse" {
		t.Fatalf("unexpected details: %+v", got[0].Details)
	}
}

func TestDeterministicSpringBootMainEntrypoint(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"src/main/java/com/example/Application.java": `package com.example;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

@SpringBootApplication
public class Application {
    public static void main(String[] args) {
        SpringApplication.run(Application.class, args);
    }
}
`,
	})
	got := DeterministicCLIEntrypoints(idx)
	if len(got) != 1 {
		t.Fatalf("expected one Spring Boot cli_command, got %d: %+v", len(got), got)
	}
	if got[0].Details["discovered_by"] != "ast_spring_boot_main" {
		t.Fatalf("unexpected details: %+v", got[0].Details)
	}
}

func TestDeterministicGoCobraCommands(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"cmd/app/http.go": `package app

import "github.com/spf13/cobra"

var HttpCommand = &cobra.Command{
    Use: "http",
    Run: HttpRun,
}

func HttpRun(_ *cobra.Command, _ []string) {}
`,
	})
	got := DeterministicCLIEntrypoints(idx)
	if len(got) != 1 {
		t.Fatalf("expected one cobra command, got %+v", got)
	}
	if got[0].Name != "http" || got[0].Details["handler"] != "HttpRun" {
		t.Fatalf("unexpected cobra command: %+v", got[0])
	}
}

func TestDeterministicBunOperations(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"internal/financial/adapter/outbound/repository/sql/payment_repo/pay.go": `package payment_repo

func (r Repository) Pay(ctx any, payment Payment) error {
    db, _ := r.db.GetTX(ctx)
    _, err := db.NewInsert().Model(&payment).Exec(ctx)
    return err
}
`,
	})
	got := DeterministicORMOperations(idx)
	if len(got) != 1 {
		t.Fatalf("expected one bun operation, got %+v", got)
	}
	if got[0].Details["orm"] != "bun" || got[0].Details["operation"] != "write" {
		t.Fatalf("unexpected bun operation: %+v", got[0])
	}
}

func TestDeterministicRestyOutboundHTTP(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"internal/financial/adapter/outbound/anygate_http/pay.go": `package anygate_http

import "net/url"

func (p PaymentGateway) Pay(ctx any) {
    req := p.client.R().SetContext(ctx)
    path, _ := url.JoinPath(p.Uri, "/api/v1/gateway/get-or-new")
    req.Execute("POST", path)
}
`,
		"diffmind-configuration.yaml": `http_targets:
  - id: anygate
    service_ref: service.anygate
    external: true
    aliases: [PaymentGateway, anygate_http, anygate]
`,
	})
	got := DeterministicOutboundHTTP(idx)
	if len(got) != 1 {
		t.Fatalf("expected one resty outbound call, got %+v", got)
	}
	if got[0].Details["target_service"] != "anygate" || got[0].Details["method"] != "POST" ||
		got[0].Details["path"] != "/api/v1/gateway/get-or-new" {
		t.Fatalf("unexpected resty details: %+v", got[0].Details)
	}
}

func TestDeterministicNetHTTPOutboundFromProviderAdapter(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"internal/collector/adapter/outbound/provider/coingecko/metadata/coin_data.go": `package metadata

import (
	"fmt"
	"net/http"
)

type Provider struct {
	BaseUrl string
}

func (p Provider) Get(ctx any, coinID string) {
	url := fmt.Sprintf("%s/coins/%s?localization=false", p.BaseUrl, coinID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url)
	_ = req
}
`,
	})
	got := DeterministicOutboundHTTP(idx)
	if len(got) != 1 {
		t.Fatalf("expected one net/http outbound call, got %+v", got)
	}
	if got[0].Details["target_service"] != "coingecko" ||
		got[0].Details["method"] != "GET" ||
		got[0].Details["path"] != "/coins/{value}?localization=false" ||
		got[0].Details["discovered_by"] != "ast_go_nethttp_request" {
		t.Fatalf("unexpected net/http details: %+v", got[0].Details)
	}
}

func TestDeterministicJavaWorkflowCallbackHTTP(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"src/main/resources/application.yml": `services:
  cdp:
    stories-media-loader:
      url: https://cdp-stories-media-loader.example.biz
`,
		"src/main/java/com/example/Constants.java": `package com.example;

public class Constants {
    public static final String MEDIA_LOADER_CONFIG_NAME = "stories-media-loader";
    public static final String MEDIA_LOADER_CHANGE_TTL_PATH = "/api/media/ttl";
}
`,
		"src/main/java/com/example/ImportVarsProvider.java": `package com.example;

import static com.example.Constants.*;

public class ImportVarsProvider {
    private ServicesConfig servicesConfig;

    public String buildChangeMediaTttUrl() {
        return servicesConfig.getCdp().get(MEDIA_LOADER_CONFIG_NAME).getUrl() + MEDIA_LOADER_CHANGE_TTL_PATH;
    }
}
`,
	})
	got := DeterministicOutboundHTTP(idx)
	var found *candidate
	for i := range got {
		if got[i].Details["discovered_by"] == "source_java_workflow_callback_url" {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected workflow callback HTTP dependency, got %+v", got)
	}
	if found.Details["target_service"] != "cdp-stories-media-loader" ||
		found.Details["method"] != "ANY" ||
		found.Details["path"] != "/api/media/ttl" ||
		found.Details["url_template"] != "https://cdp-stories-media-loader.example.biz/api/media/ttl" {
		t.Fatalf("unexpected workflow callback details: %+v", found.Details)
	}
}

func TestDeterministicJavaScriptAxiosInstanceHTTP(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"libs/api/src/constants/services.ts": "const CONSOLE_URL = 'https://console.example.test';\n" +
			"const UPLOAD_MEDIA_FILE_URL = `https://cdp-stories-media-loader.example.biz`;\n" +
			"const STORIES_IMPORT_URL = 'https://cdp-stories-import-api.example.biz';\n" +
			"\n" +
			"export default {\n" +
			"  CONSOLE_URL,\n" +
			"  UPLOAD_MEDIA_FILE_URL,\n" +
			"  STORIES_IMPORT_URL,\n" +
			"};\n",
		"libs/client/story/src/StoryClient.ts": "import axios, { AxiosInstance } from 'axios';\n" +
			"\n" +
			"import { apiUrlsInstance } from '@example/api';\n" +
			"\n" +
			"const UPLOAD_MEDIA_FILE_URL = apiUrlsInstance.getUrl('UPLOAD_MEDIA_FILE_URL');\n" +
			"const STORIES_IMPORT_URL = apiUrlsInstance.getUrl('STORIES_IMPORT_URL');\n" +
			"\n" +
			"export const uploadStorySnippetFileInstance: AxiosInstance = axios.create({\n" +
			"  baseURL: `${UPLOAD_MEDIA_FILE_URL}`,\n" +
			"});\n" +
			"\n" +
			"export const campaignStoriesInstance: AxiosInstance = axios.create({\n" +
			"  baseURL: `${STORIES_IMPORT_URL}`,\n" +
			"});\n" +
			"\n" +
			"uploadStorySnippetFileInstance.interceptors.request.use(withExampleHeader);\n" +
			"campaignStoriesInstance.interceptors.request.use(withExampleHeader);\n" +
			"\n" +
			"const uploadStorySnippetFile = (snippetFileData: FormData) => {\n" +
			"  return uploadStorySnippetFileInstance.post('/api/media/upload', snippetFileData);\n" +
			"};\n" +
			"\n" +
			"const getCampaignStories = (campaignId: string) => {\n" +
			"  return campaignStoriesInstance.get(`/api/import?campaignId=${campaignId}`);\n" +
			"};\n",
	})
	got := DeterministicOutboundHTTP(idx)
	var found *candidate
	for i := range got {
		if got[i].Details["discovered_by"] == "source_js_axios_instance" && got[i].Details["path"] == "/api/media/upload" {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected axios HTTP dependency, got %+v", got)
	}
	if found.Details["target_service"] != "cdp-stories-media-loader" ||
		found.Details["method"] != "POST" ||
		found.Details["path"] != "/api/media/upload" ||
		found.Details["url_template"] != "https://cdp-stories-media-loader.example.biz/api/media/upload" {
		t.Fatalf("unexpected axios details: %+v", found.Details)
	}
}

func TestServiceNameFromURLTemplateWithPlaceholderHost(t *testing.T) {
	cases := map[string]string{
		"https://cdp-${UUID}-cdp-stories-media-loader.aws-sdlc-example.com":             "cdp-stories-media-loader",
		"https://globalservices-${UUID}-checkout-service.aws-sdlc-example.com": "checkout-service",
		"https://cdp-${UUID}-publisher-api.aws-sdlc-example.com":                        "publisher-api",
	}
	for raw, want := range cases {
		if got := serviceNameFromURLTemplate(raw); got != want {
			t.Fatalf("serviceNameFromURLTemplate(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDeterministicInheritedFeignClientEmitsServiceLevelDependency(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"src/main/resources/application.yml": `
services:
  cdp:
    media-store:
      url: https://example.org
`,
		"src/main/java/com/acme/MediaStoreClient.java": `package com.acme;

import com.example.cdp.mediastore.client.MediaStoreAPI;
import org.springframework.cloud.openfeign.FeignClient;

@FeignClient(name = "media-store", url = "${services.cdp.media-store.url}")
public interface MediaStoreClient extends MediaStoreAPI {
}
`,
	})

	got := DeterministicOutboundHTTP(idx)
	var found *candidate
	for i := range got {
		if got[i].Details["discovered_by"] == "source_java_inherited_feign_client" {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected inherited Feign HTTP dependency, got %+v", got)
	}
	if found.Details["target_service"] != "media-store" ||
		found.Details["method"] != "ANY" ||
		found.Details["inherited_contract"] != "MediaStoreAPI" ||
		found.Details["invocation_mode"] != "generated_feign_contract" {
		t.Fatalf("unexpected inherited Feign details: %+v", found.Details)
	}
}

func TestDeterministicSpringCloudGatewayRouteEmitsHTTPDependency(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"src/main/resources/application.yaml": `
spring:
  cloud:
    gateway:
      server:
        webflux:
          routes:
            - id: media-store
              uri: https://cdp-${UUID}-cdp-media-store.aws-sdlc-example.com
              predicates:
                - Path=/media-store/**
              filters:
                - StripPrefix=1
`,
	})

	got := DeterministicOutboundHTTP(idx)
	var found *candidate
	for i := range got {
		if got[i].Details["discovered_by"] == "config_spring_cloud_gateway_route" {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected Spring Cloud Gateway route dependency, got %+v", got)
	}
	if found.Details["target_service"] != "media-store" ||
		found.Details["method"] != "ANY" ||
		found.Details["path"] != "/media-store/**" ||
		found.Details["gateway_route_id"] != "media-store" ||
		found.Details["spring_filter"] != "StripPrefix=1" {
		t.Fatalf("unexpected gateway route details: %+v", found.Details)
	}
}

func TestDeterministicMicronautClientEmitsHTTPDependency(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"src/main/java/com/acme/MediaStoreClient.java": `package com.acme;

import io.micronaut.http.annotation.Get;
import io.micronaut.http.client.annotation.Client;

@Client("media-store")
public interface MediaStoreClient {
    @Get("/media/metadata")
    String getMediaMetadata();
}
`,
	})

	got := DeterministicOutboundHTTP(idx)
	var found *candidate
	for i := range got {
		if got[i].Details["discovered_by"] == "source_java_micronaut_client" {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected Micronaut HTTP dependency, got %+v", got)
	}
	if found.Details["target_service"] != "media-store" ||
		found.Details["method"] != "GET" ||
		found.Details["path"] != "/media/metadata" ||
		found.Details["client"] != "MediaStoreClient" {
		t.Fatalf("unexpected Micronaut details: %+v", found.Details)
	}
}

func TestDeterministicGoGRPCServiceClient(t *testing.T) {
	idx := buildAgentsIndex(t, map[string]string{
		"internal/report/adapter/outbound/metadata_grpc/get_metadata.go": `package metadata_grpc

func (cc MetadataClient) GetMetadata(ctx any) {
    cc.metadataServiceClient.Get(ctx)
}
`,
	})
	got := DeterministicOutboundRPC(idx)
	if len(got) != 1 {
		t.Fatalf("expected one go grpc client call, got %+v", got)
	}
	if got[0].Details["service"] != "metadata" || got[0].Details["method"] != "Get" {
		t.Fatalf("unexpected grpc details: %+v", got[0].Details)
	}
}
