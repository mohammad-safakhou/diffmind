package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

type Objective struct {
	ID                string
	Kind              model.EntityKind
	Type              string
	Description       string
	DiscoveryPrompt   string
	ConnectionContext string

	// Example is an optional, concise, schema-valid example item rendered
	// under GOOD_EXAMPLE in the discovery prompt. Empty → omitted.
	Example string
	// DetailKeys lists the details{} keys this objective's items should
	// populate (e.g. http_route → method,path). Rendered as
	// REQUIRED_DETAIL_KEYS in discovery and reexamination prompts. details{}
	// stays a free map (no schema change); this only nudges consistency,
	// which improves downstream semantic dedup. Empty → omitted.
	DetailKeys []string

	// NegativeExample is an optional item that is COMMONLY misclassified as
	// this objective but belongs to a neighbour (e.g. a webhook reported as an
	// http_route). Rendered as BAD_EXAMPLE in discovery. Populated only for the
	// confusable pairs (not all objectives) — empty → omitted.
	NegativeExample string
	// Boundary is an optional one-line scope-exclusion that keeps this
	// objective distinct from a confusable neighbour. Rendered as BOUNDARY in
	// discovery, right after the description. Empty → omitted.
	Boundary string
	// HighVariance marks the LLM-only objectives whose run-to-run recall wobbles
	// most (no strong deterministic floor to anchor them). It (a) renders an
	// extra exhaustiveness line in discovery and (b) gates the optional
	// verification pass, which only runs for HighVariance objectives so its cost
	// stays bounded.
	HighVariance bool
}

func Default() []Objective {
	objs := defaultObjectives()
	for i := range objs {
		if m, ok := objectiveMeta[objs[i].Type]; ok {
			objs[i].Example = m.example
			objs[i].DetailKeys = m.detailKeys
			objs[i].NegativeExample = m.bad
			objs[i].Boundary = m.boundary
			objs[i].HighVariance = m.highVariance
		}
	}
	return objs
}

// defaultObjectives returns the objectives in a fixed order. Order matters:
// discovery emits jobs positionally and "first error" attribution reads the
// slice index (final artifacts are ID-sorted, but emission is not). Each
// objective is defined in its own file (http_route.go, db_operation.go, …).
func defaultObjectives() []Objective {
	return []Objective{
		objHTTPRoute,
		objWebhook,
		objRPCEndpoint,
		objQueueConsumer,
		objScheduledJob,
		objCLICommand,
		objDBOperation,
		objOutboundHTTP,
		objOutboundRPC,
		objQueuePublish,
		objCommandExec,
		objCacheOperation,
		objStreamConsume,
		// connection_client is appended LAST: defaultObjectives order is
		// positional for failure attribution, so new objectives must not shift
		// existing indices.
		objConnectionClient,
	}
}
