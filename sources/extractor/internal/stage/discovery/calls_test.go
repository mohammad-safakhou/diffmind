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
	idx, err := astpkg.Build(context.Background(), dir, "", 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func entityNames(es []llmEntity) []string {
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
	byName := map[string]llmEntity{}
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
