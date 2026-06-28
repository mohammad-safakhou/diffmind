package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objHTTPRoute = Objective{
	ID:                "exposure.http_route",
	Kind:              model.KindExposure,
	Type:              "http_route",
	Description:       "HTTP REST/API routes exposed by the service (non-webhook)",
	ConnectionContext: "Prioritize ordered call-path mapping from HTTP route to downstream dependencies with conditions.",
}
