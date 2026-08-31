package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objRPCEndpoint = Objective{
	ID:                "exposure.rpc_endpoint",
	Kind:              model.KindExposure,
	Type:              "rpc_endpoint",
	Description:       "RPC/gRPC entrypoints exposed by the service",
	ConnectionContext: "Map RPC endpoint paths to dependencies with explicit branch conditions.",
}
