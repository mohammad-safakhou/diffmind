package objectives

import "github.com/mohammad-safakhou/diffmind/internal/extractor/model"

var objOutboundRPC = Objective{
	ID:                "dependency.outbound_rpc",
	Kind:              model.KindDependency,
	Type:              "outbound_rpc",
	Description:       "Outbound RPC/gRPC calls",
	ConnectionContext: "Connection mapping must include rpc target service/method and guard condition.",
}
