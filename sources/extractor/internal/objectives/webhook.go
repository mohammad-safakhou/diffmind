package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objWebhook = Objective{
	ID:          "exposure.webhook",
	Kind:        model.KindExposure,
	Type:        "webhook",
	Description: "HTTP webhook callback endpoints (incoming third-party callbacks)",
	DiscoveryPrompt: `Find webhook/callback endpoints - these are HTTP endpoints that receive callbacks from external systems.

PATTERNS TO CHECK:
- Endpoints with signature/HMAC verification (e.g., X-Hub-Signature, Stripe-Signature)
- Endpoints named *webhook*, *callback*, *notify*, *hook*
- Endpoints that receive events from third-party systems (payment processors, CRM, etc.)
- Spring Boot: @PostMapping with webhook/callback in path
- Node.js: routes handling POST with signature verification

FOR EACH WEBHOOK EXTRACT:
- Path and HTTP method
- Signature/auth verification mechanism
- Event type branching (different handlers for different event types)
- Payload parsing and validation
- Idempotency/duplicate handling

If no webhooks exist, return {"items": []}.`,
	DetailPrompt:      "For this webhook, extract signature/auth checks, payload schema, branching rules, idempotency/duplicate handling, and ordered downstream operations.",
	ConnectionContext: "Map webhook-to-dependency conditional paths with explicit guard expressions.",
}
