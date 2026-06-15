package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objScheduledJob = Objective{
	ID:          "exposure.scheduled_job",
	Kind:        model.KindExposure,
	Type:        "scheduled_job",
	Description: "Scheduled jobs, cron triggers, and startup runners",
	DiscoveryPrompt: `Find ALL scheduled/background entrypoints in this service.

PATTERNS TO CHECK:
- Spring Boot: @Scheduled (fixedDelay, fixedRate, cron), @EnableScheduling, CommandLineRunner, ApplicationRunner
- Spring Boot: ShedLock (@SchedulerLock) for distributed locking
- Quartz: @DisallowConcurrentExecution, JobDetail, Trigger
- Node.js: node-cron, node-schedule, setInterval
- Python: APScheduler, Celery beat, crontab entries
- AWS: CloudWatch Events/EventBridge rules triggering Lambdas

FOR EACH JOB EXTRACT:
- Schedule expression (cron, fixedDelay, fixedRate with exact values)
- Profile/property guards (@Profile, @ConditionalOnProperty)
- Entry method and class
- What the job does (brief description)
- Distributed locking (ShedLock, database locks)

IMPORTANT: Check for @Profile annotations - some jobs only run in specific environments (e.g., @Profile("prod")).

BOUNDARY (this objective OWNS them): a CommandLineRunner/ApplicationRunner gated
by @Profile/@ConditionalOnProperty and triggered externally (e.g. a Kubernetes
CronJob launching the app with that profile) is a scheduled_job. Report it HERE,
not as a cli_command, and name it by its handler class.method so it has one
stable identity.`,
	ConnectionContext: "Map scheduled-job paths with schedule/profile/property guards.",
}
