package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

type Objective struct {
	ID                string
	Kind              model.EntityKind
	Type              string
	Description       string
	DiscoveryPrompt   string
	DetailPrompt      string
	ConnectionContext string

	// Example is an optional, concise, schema-valid example item rendered
	// under GOOD_EXAMPLE in the discovery prompt. Empty → omitted.
	Example string
	// DetailKeys lists the details{} keys this objective's items should
	// populate (e.g. http_route → method,path). Rendered as
	// REQUIRED_DETAIL_KEYS in the discovery + detail prompts. details{}
	// stays a free map (no schema change); this only nudges consistency,
	// which improves downstream semantic dedup. Empty → omitted.
	DetailKeys []string
}

func Default() []Objective {
	objs := defaultObjectives()
	for i := range objs {
		if m, ok := objectiveMeta[objs[i].Type]; ok {
			objs[i].Example = m.example
			objs[i].DetailKeys = m.detailKeys
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
	}
}
