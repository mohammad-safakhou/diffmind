package objectives

// objectiveDetailKeys defines the structured detail fields deterministic
// detectors should populate for each objective type.
var objectiveDetailKeys = map[string][]string{
	"http_route":        {"method", "path", "auth"},
	"webhook":           {"method", "path", "verification"},
	"rpc_endpoint":      {"service", "method", "proto"},
	"queue_consumer":    {"queue", "platform", "handler"},
	"scheduled_job":     {"schedule", "handler"},
	"cli_command":       {"command", "handler"},
	"db_operation":      {"table", "operation", "datasource", "client"},
	"outbound_http":     {"method", "path", "target_service", "client"},
	"outbound_rpc":      {"service", "method", "target_service"},
	"queue_publish":     {"queue", "platform", "message_type", "client"},
	"command_exec":      {"command", "invocation"},
	"cache_operation":   {"cache", "operation", "platform", "client"},
	"stream_consume":    {"stream", "platform", "consumer_group", "client"},
	"connection_client": {"kind", "config_anchor", "symbol", "framework"},
}
