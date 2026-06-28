package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

type Objective struct {
	ID                string
	Kind              model.EntityKind
	Type              string
	Description       string
	ConnectionContext string
	DetailKeys        []string
}

func Default() []Objective {
	objs := defaultObjectives()
	for i := range objs {
		if keys, ok := objectiveDetailKeys[objs[i].Type]; ok {
			objs[i].DetailKeys = append([]string(nil), keys...)
		}
	}
	return objs
}

// defaultObjectives returns the objectives in a fixed order. Order matters:
// deterministic discovery emits jobs positionally and "first error" attribution
// reads the slice index. Each objective is defined in its own file.
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
