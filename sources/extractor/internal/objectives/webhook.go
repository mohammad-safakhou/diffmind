package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objWebhook = Objective{
	ID:                "exposure.webhook",
	Kind:              model.KindExposure,
	Type:              "webhook",
	Description:       "HTTP webhook callback endpoints (incoming third-party callbacks)",
	ConnectionContext: "Map webhook-to-dependency conditional paths with explicit guard expressions.",
}
