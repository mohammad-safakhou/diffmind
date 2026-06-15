package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objRPCEndpoint = Objective{
	ID:          "exposure.rpc_endpoint",
	Kind:        model.KindExposure,
	Type:        "rpc_endpoint",
	Description: "RPC/gRPC entrypoints exposed by the service",
	DiscoveryPrompt: `Find externally reachable RPC entrypoints exposed by this service.

PATTERNS TO CHECK:
- gRPC: protobuf service definitions (.proto files), generated server stubs, @GrpcService
- Thrift: .thrift IDL files, generated server handlers
- SOAP: @WebService, WSDL-defined operations

FOR EACH RPC ENDPOINT EXTRACT:
- Service name and method name
- Request/response message types
- Handler implementation class/function

If no RPC endpoints exist, return {"items": []}.`,
	ConnectionContext: "Map RPC endpoint paths to dependencies with explicit branch conditions.",
}
