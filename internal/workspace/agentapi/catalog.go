// Code and descriptions form the public agent management catalog.
package agentapi

import "encoding/json"

var Operations = []Operation{
	{Name: "list_project_records", Method: "GET", Path: "/api/projects", Description: "List visible projects with configuration. Reuse an existing matching project before creation.", Destructive: false},
	{Name: "create_project", Method: "POST", Path: "/api/projects", Description: "Create a project. name required; search_roots and instruction optional. Creation is not idempotent: list projects after an uncertain response.", Destructive: false, BodyExample: example("{\"name\":\"Platform\",\"search_roots\":[],\"instruction\":\"\"}")},
	{Name: "get_project", Method: "GET", Path: "/api/projects/{pid}", Description: "Read a project.", Destructive: false},
	{Name: "update_project", Method: "PATCH", Path: "/api/projects/{pid}", Description: "Update only supplied name, search_roots, instruction fields.", Destructive: false, BodyExample: example("{\"instruction\":\"Service naming conventions\"}")},
	{Name: "delete_project", Method: "DELETE", Path: "/api/projects/{pid}", Description: "Delete project metadata and saved artifacts. Back up first. Does not delete external source directories.", Destructive: true},
	{Name: "list_repositories", Method: "GET", Path: "/api/projects/{pid}/repos", Description: "List registered repositories and IDs.", Destructive: false},
	{Name: "add_repository", Method: "POST", Path: "/api/projects/{pid}/repos", Description: "Register an explicit local path or Git URL. Fields: name, path, kind (service_repo/infra_repo), source_type (local/git), git_url, default_branch, team, pack_ids, instruction.", Destructive: false, BodyExample: example("{\"name\":\"gateway\",\"path\":\"/absolute/path/to/gateway\",\"kind\":\"service_repo\",\"source_type\":\"local\",\"team\":\"default\"}")},
	{Name: "get_repository", Method: "GET", Path: "/api/projects/{pid}/repos/{rid}", Description: "Read repository configuration.", Destructive: false},
	{Name: "update_repository", Method: "PATCH", Path: "/api/projects/{pid}/repos/{rid}", Description: "Update supplied fields supported by add_repository; preserve unrelated values.", Destructive: false, BodyExample: example("{\"team\":\"platform\"}")},
	{Name: "delete_repository", Method: "DELETE", Path: "/api/projects/{pid}/repos/{rid}", Description: "Remove registration, not source repository contents.", Destructive: true},
	{Name: "import_repositories", Method: "POST", Path: "/api/projects/{pid}/repo-imports", Description: "Preview/import local or GitHub repositories. provider local requires root; github requires org, optional api_base. Supports include/exclude regex, limit, recursive/max_depth, clone, clone_transport (auto/https/ssh), default_branch, include_forks/include_archived, team, concurrency. Use dry_run=true first; no credential arguments (credentials come from server environment). For full import+analysis prefer start_ingestion after preview.", Destructive: false, BodyExample: example("{\"provider\":\"local\",\"root\":\"/absolute/path/to/repos\",\"dry_run\":true}")},
	{Name: "repository_suggestions", Method: "GET", Path: "/api/projects/{pid}/repo-suggestions", Description: "Discover candidates under project search_roots. Requires host-discovery authority.", Destructive: false},
	{Name: "sync_repository", Method: "POST", Path: "/api/projects/{pid}/repos/{rid}/sync", Description: "Sync a managed Git checkout. Local imports are not pulled. Inspect live status afterward.", Destructive: false, BodyExample: example("{}")},
	{Name: "analyze_repository", Method: "POST", Path: "/api/projects/{pid}/repos/{rid}/diffmind-runs", Description: "Start analysis of one repository; options are DiffMindRunOptions. Prefer start_ingestion for complete graph workflow.", Destructive: false, BodyExample: example("{\"options\":{}}")},
	{Name: "analyze_repositories", Method: "POST", Path: "/api/projects/{pid}/diffmind-runs/batch", Description: "Start a repository analysis batch. Prefer start_ingestion for analysis plus graph.", Destructive: false, BodyExample: example("{}")},
	{Name: "get_repository_configuration", Method: "GET", Path: "/api/projects/{pid}/repos/{rid}/diffmind-configuration-yaml", Description: "Read effective editable YAML configuration.", Destructive: false},
	{Name: "set_repository_configuration", Method: "PUT", Path: "/api/projects/{pid}/repos/{rid}/diffmind-configuration-yaml", Description: "Validate/write repository analysis configuration; body is YAML text under the body field.", Destructive: false, BodyExample: example("{\"body\":\"\"}")},
	{Name: "get_workspace", Method: "GET", Path: "/api/projects/{pid}/workspace", Description: "Workspace graph/configuration overview; may be large, prefer focused operations.", Destructive: false},
	{Name: "get_live_status", Method: "GET", Path: "/api/projects/{pid}/live-status", Description: "Current sync and analysis status.", Destructive: false},
	{Name: "start_ingestion", Method: "POST", Path: "/api/projects/{pid}/ingestion", Description: "Durable full workflow. Empty body incrementally syncs/analyzes/builds existing repos. import uses import_repositories fields (dry_run must be false). Options: concurrency 0..16, force boolean, options. Returns 202 before completion: poll get_ingestion. Never infer completion from acceptance.", Destructive: false, BodyExample: example("{\"import\":{\"provider\":\"local\",\"root\":\"/absolute/path/to/repos\"},\"concurrency\":4}")},
	{Name: "get_ingestion", Method: "GET", Path: "/api/projects/{pid}/ingestion", Description: "Poll latest ingestion. completed means done; partial/failed/cancelled require inspection. Includes phase, counts, errors, graph_run_id and repository progress.", Destructive: false},
	{Name: "cancel_ingestion", Method: "POST", Path: "/api/projects/{pid}/ingestion/cancel", Description: "Request cancellation; poll until work drains.", Destructive: false, BodyExample: example("{}")},
	{Name: "resume_ingestion", Method: "POST", Path: "/api/projects/{pid}/ingestion/resume", Description: "Resume failed/interrupted/cancelled direct ingestion. Queued refreshes must use retry_job.", Destructive: false, BodyExample: example("{}")},
	{Name: "list_jobs", Method: "GET", Path: "/api/v1/jobs", Description: "List durable jobs. Query: project, status, offset, limit.", Destructive: false},
	{Name: "enqueue_refresh", Method: "POST", Path: "/api/v1/projects/{pid}/refresh-jobs", Description: "Queue a refresh respecting concurrency and project/global quotas.", Destructive: false, BodyExample: example("{}")},
	{Name: "cancel_job", Method: "POST", Path: "/api/v1/jobs/{jid}/cancel", Description: "Cancel a queued/running refresh; inspect job status afterward.", Destructive: false, BodyExample: example("{}")},
	{Name: "retry_job", Method: "POST", Path: "/api/v1/jobs/{jid}/retry", Description: "Retry a failed/cancelled refresh while retaining attempt history.", Destructive: false, BodyExample: example("{}")},
	{Name: "ingestion_history", Method: "GET", Path: "/api/v1/projects/{pid}/ingestion-history", Description: "Read saved ingestion attempts. Query: offset, limit.", Destructive: false},
	{Name: "get_session", Method: "GET", Path: "/api/v1/session", Description: "Authenticated identity, role and project-access mode.", Destructive: false},
	{Name: "get_capabilities", Method: "GET", Path: "/api/v1/projects/{pid}/capabilities", Description: "Effective caller permissions for this project.", Destructive: false},
	{Name: "get_access", Method: "GET", Path: "/api/v1/projects/{pid}/access", Description: "Read project membership revision and grants. Administrator only.", Destructive: false},
	{Name: "set_access", Method: "PUT", Path: "/api/v1/projects/{pid}/access", Description: "Replace memberships using the current revision; read first, preserve other members. Administrator only.", Destructive: false, BodyExample: example("{\"revision\":0,\"members\":{\"developer@example.test\":\"viewer\"}}")},
	{Name: "get_limits", Method: "GET", Path: "/api/v1/projects/{pid}/limits", Description: "Read quota policy, revision and effective usage.", Destructive: false},
	{Name: "set_limits", Method: "PUT", Path: "/api/v1/projects/{pid}/limits", Description: "Administrator sets pending-job and repository worker caps with current revision. 0 inherits global budgets.", Destructive: false, BodyExample: example("{\"revision\":0,\"max_pending_jobs\":10,\"repository_workers\":2}")},
	{Name: "list_tokens", Method: "GET", Path: "/api/v1/projects/{pid}/tokens", Description: "Administrator lists project token metadata (never secrets).", Destructive: false},
	{Name: "issue_token", Method: "POST", Path: "/api/v1/projects/{pid}/tokens", Description: "Administrator issues scoped viewer/editor token. Secret returned ONCE: keep out of chat/logs. Not idempotent; do not blindly retry. Lifetime 60..31536000 seconds; scoped mode required.", Destructive: false, BodyExample: example("{\"name\":\"Work agent\",\"role\":\"viewer\",\"expires_in_seconds\":2592000}")},
	{Name: "revoke_token", Method: "POST", Path: "/api/v1/projects/{pid}/tokens/{tid}/revoke", Description: "Administrator permanently revokes this token.", Destructive: true, BodyExample: example("{}")},
	{Name: "refresh_status", Method: "GET", Path: "/api/v1/refresh/status", Description: "Fleet schedule and refresh progress.", Destructive: false},
	{Name: "refresh_all", Method: "POST", Path: "/api/v1/refresh", Description: "Administrator queues fleet refresh.", Destructive: false, BodyExample: example("{}")},
	{Name: "list_project_packs", Method: "GET", Path: "/api/projects/{pid}/packs", Description: "List project-scoped declarative packs.", Destructive: false},
	{Name: "get_project_pack", Method: "GET", Path: "/api/projects/{pid}/packs/{pack_id}", Description: "Read pack JSON before editing. pack_id is the storage key returned by create/list, which can differ from the manifest id.", Destructive: false},
	{Name: "create_project_pack", Method: "POST", Path: "/api/projects/{pid}/packs", Description: "Create a validated project pack. Body is the complete KnowledgePack JSON manifest, not a wrapper. Include synthetic tests. Use local agent_command pack tools for scaffolding/fixtures/installing global packs.", Destructive: false, BodyExample: example("{\"api_version\":\"diffmind.dev/v1alpha1\",\"kind\":\"KnowledgePack\",\"id\":\"example.conventions\",\"name\":\"Example conventions\",\"version\":\"1.0.0\",\"license\":\"Apache-2.0\",\"compatibility\":\">=0.1.0\",\"applies_to\":{\"kind\":\"service_repo\"},\"extractions\":[{\"name\":\"service identity\",\"source\":{\"glob\":\"service-meta.json\"},\"strategy\":\"field_path\",\"extract\":[{\"field\":\"service.name\",\"maps_to\":\"service_name\"}]}]}")},
	{Name: "update_project_pack", Method: "PUT", Path: "/api/projects/{pid}/packs/{pack_id}", Description: "Validate/replace complete pack JSON. Preserve the existing manifest id; use the create/list storage key as pack_id. Read existing pack first; update graph afterward.", Destructive: false},
	{Name: "delete_project_pack", Method: "DELETE", Path: "/api/projects/{pid}/packs/{pack_id}", Description: "Remove a project pack; historical graphs are unchanged.", Destructive: true},
	{Name: "list_analysis_runs", Method: "GET", Path: "/api/diffmind-runs", Description: "Discover analysis artifacts. Optional query repo_path.", Destructive: false},
	{Name: "list_run_records", Method: "GET", Path: "/api/projects/{pid}/runs", Description: "List graph runs including incomplete/failed runs.", Destructive: false},
	{Name: "create_graph_run", Method: "POST", Path: "/api/projects/{pid}/runs", Description: "Build from explicit existing analysis references. Prefer start_ingestion for automatic selection.", Destructive: false, BodyExample: example("{\"repos\":[{\"repo_id\":\"REPOSITORY_ID\",\"diffmind_run_id\":\"ANALYSIS_ID\"}],\"options\":{}}")},
	{Name: "get_run", Method: "GET", Path: "/api/projects/{pid}/runs/{rid}", Description: "Read graph run and active state.", Destructive: false},
	{Name: "cancel_run", Method: "POST", Path: "/api/projects/{pid}/runs/{rid}/cancel", Description: "Cancel graph build.", Destructive: false, BodyExample: example("{}")},
	{Name: "delete_run", Method: "DELETE", Path: "/api/projects/{pid}/runs/{rid}", Description: "Delete saved graph run/artifacts; irreversible without backup.", Destructive: true},
	{Name: "list_pull_requests", Method: "GET", Path: "/api/projects/{pid}/pull-requests", Description: "List provider pull requests, using server credentials.", Destructive: false},
	{Name: "pull_request_impact", Method: "GET", Path: "/api/projects/{pid}/pull-requests/{repo_id}/{number}/impact", Description: "Inspect pull request impact.", Destructive: false},
}

func example(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic(err)
	}
	return v
}
