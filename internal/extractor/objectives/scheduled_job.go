package objectives

import "github.com/mohammad-safakhou/diffmind/internal/extractor/model"

var objScheduledJob = Objective{
	ID:                "exposure.scheduled_job",
	Kind:              model.KindExposure,
	Type:              "scheduled_job",
	Description:       "Scheduled jobs, cron triggers, and startup runners",
	ConnectionContext: "Map scheduled-job paths with schedule/profile/property guards.",
}
