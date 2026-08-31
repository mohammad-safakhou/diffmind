package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objOutboundHTTP = Objective{
	ID:                "dependency.outbound_http",
	Kind:              model.KindDependency,
	Type:              "outbound_http",
	Description:       "Outbound HTTP calls to other services or external APIs",
	ConnectionContext: "Connection mapping must include outbound method/path and guard condition.",
}
