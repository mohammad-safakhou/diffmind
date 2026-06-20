package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objOutboundHTTP = Objective{
	ID:          "dependency.outbound_http",
	Kind:        model.KindDependency,
	Type:        "outbound_http",
	Description: "Outbound HTTP calls to other services or external APIs",
	DiscoveryPrompt: `Find ALL outbound HTTP service calls made by this service.

FRAMEWORK-SPECIFIC PATTERNS TO CHECK:
- Spring Feign: @FeignClient, @RequestLine
- Spring RestTemplate: RestTemplate.getForObject/postForObject/exchange
- Spring WebClient: WebClient.get()/post()/put()/delete()
- Retrofit: Retrofit interface methods with @GET/@POST/@PUT/@DELETE annotations, Retrofit.Builder
- OkHttp: OkHttpClient, Request.Builder
- Apache HttpClient: HttpGet, HttpPost, CloseableHttpClient
- Java 11+ HttpClient: HttpClient.newHttpClient(), HttpRequest.newBuilder()
- Node.js: axios, fetch, got, node-fetch, superagent
- Python: requests, httpx, urllib3, aiohttp, boto3 (for AWS API calls)

FOR EACH OUTBOUND CALL EXTRACT (point at the call; do NOT resolve the host):
- details.method and details.path — the HTTP method and request path (the call identity).
- details.client — the HTTP client/bean/interface SYMBOL the call goes through
  (e.g. the @FeignClient interface, a named RestTemplate/WebClient bean, a
  Retrofit service). This is how the concrete base URL/host is attached
  deterministically.
- details.target_service — the logical peer name when it is obvious from the
  client (e.g. a @FeignClient name), but do NOT hunt config for the resolved URL.

DO NOT guess or resolve the base URL/host: the connection_client objective points
at the HTTP client and a deterministic pass resolves its configured base URL from
config. Naming the client symbol above is enough.

DO NOT miss Retrofit interfaces - they define HTTP calls via annotated Java interfaces.

BOUNDARY (do not double-report): EXCLUDE AWS SDK calls that have their own
objective — SQS/SNS publishes are queue_publish, DynamoDB is db_operation,
Kinesis is stream_consume. (Object storage like S3 has no dedicated objective
yet, so DO report S3 here rather than dropping it.) A Feign/HTTP client is
outbound_http, NOT outbound_rpc — only gRPC/Thrift/protobuf stubs are
outbound_rpc.`,
	ConnectionContext: "Connection mapping must include outbound method/path and guard condition.",
}
