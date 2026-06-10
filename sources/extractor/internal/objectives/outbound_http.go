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

FOR EACH OUTBOUND CALL EXTRACT:
- Target service name or host (check application.yml/properties AND any *values.yaml / config/*.yaml for the actual URL)
- HTTP method and path
- Client class/interface name
- Resilience patterns (circuit breaker, retry, timeout - check @CircuitBreaker, @Retry, Resilience4j config)
- Request/response types

CRITICAL: Check infrastructure configuration files for the ACTUAL base URLs:
- application.yml/properties for service.*.url or *.baseUrl properties
- any *values.yaml / config/production/*.yaml for environment-specific URLs
- These URLs often reveal the target service name (e.g., http://gateway-service.lead2cash.svc.cluster.local/)

DO NOT miss Retrofit interfaces - they define HTTP calls via annotated Java interfaces.

BOUNDARY (do not double-report): EXCLUDE AWS SDK calls that have their own
objective — SQS/SNS publishes are queue_publish, DynamoDB is db_operation,
Kinesis is stream_consume. (Object storage like S3 has no dedicated objective
yet, so DO report S3 here rather than dropping it.) A Feign/HTTP client is
outbound_http, NOT outbound_rpc — only gRPC/Thrift/protobuf stubs are
outbound_rpc.`,
	DetailPrompt: `For this outbound HTTP dependency, extract:
1. Target service/host and the config property that defines the URL
2. Exact endpoint path and HTTP method
3. Request body type and response type
4. Circuit breaker configuration (name, failure threshold, timeout)
5. Retry configuration (max attempts, backoff)
6. Timeout configuration
7. Error handling (fallback methods)`,
	ConnectionContext: "Connection mapping must include outbound method/path and guard condition.",
}
