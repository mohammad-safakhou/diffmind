package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objOutboundRPC = Objective{
	ID:          "dependency.outbound_rpc",
	Kind:        model.KindDependency,
	Type:        "outbound_rpc",
	Description: "Outbound RPC/gRPC calls",
	DiscoveryPrompt: `Find outbound RPC dependencies (gRPC stubs/channels, protobuf RPC clients, thrift clients).
Extract target service, RPC method, request type, and callsite.
If no outbound RPC calls exist, return {"items": []}.`,
	DetailPrompt:      "For this outbound RPC dependency, extract rpc service/method, request/response contracts, retry/timeout behavior, and call conditions.",
	ConnectionContext: "Connection mapping must include rpc target service/method and guard condition.",
}
