// Package protocol defines the Diffmind Service Context Protocol.
package protocol

import "time"

const SchemaServiceV1 = "diffmind.service.v1"

type Status string
type Confidence string
type Origin string
type Reachability string

const (
	StatusConfirmed   Status = "confirmed"
	StatusProposed    Status = "proposed"
	StatusRejected    Status = "rejected"
	StatusStale       Status = "stale"
	StatusDeprecated  Status = "deprecated"
	StatusUnresolved  Status = "unresolved"
	StatusConflicting Status = "conflicting"

	ConfidenceHigh    Confidence = "high"
	ConfidenceMedium  Confidence = "medium"
	ConfidenceLow     Confidence = "low"
	ConfidenceUnknown Confidence = "unknown"

	OriginManual        Origin = "manual"
	OriginDeterministic Origin = "deterministic"
	OriginLLM           Origin = "llm"
	OriginImported      Origin = "imported"
	OriginRuntime       Origin = "runtime"
	OriginExternal      Origin = "external"

	ReachabilityMust        Reachability = "must"
	ReachabilityConditional Reachability = "conditional"
	ReachabilityMay         Reachability = "may"
	ReachabilityUnknown     Reachability = "unknown"
)

type Document struct {
	Schema       string        `json:"schema" yaml:"schema"`
	Service      Service       `json:"service" yaml:"service"`
	Repository   Repository    `json:"repository,omitempty" yaml:"repository,omitempty"`
	Objects      Objects       `json:"objects" yaml:"objects"`
	Flows        []Flow        `json:"flows" yaml:"flows"`
	Observations []Observation `json:"observations" yaml:"observations"`
	Evidence     []Evidence    `json:"evidence" yaml:"evidence"`
	Review       *Review       `json:"review,omitempty" yaml:"review,omitempty"`
	Metadata     Metadata      `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type Service struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Team        string `json:"team,omitempty" yaml:"team,omitempty"`
	Domain      string `json:"domain,omitempty" yaml:"domain,omitempty"`
	Criticality string `json:"criticality,omitempty" yaml:"criticality,omitempty"`
}

type Repository struct {
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	URL      string `json:"url,omitempty" yaml:"url,omitempty"`
	Branch   string `json:"branch,omitempty" yaml:"branch,omitempty"`
	Commit   string `json:"commit,omitempty" yaml:"commit,omitempty"`
	Dirty    bool   `json:"dirty,omitempty" yaml:"dirty,omitempty"`
	Path     string `json:"path,omitempty" yaml:"path,omitempty"`
}

type Metadata struct {
	GeneratedBy string            `json:"generated_by,omitempty" yaml:"generated_by,omitempty"`
	GeneratedAt time.Time         `json:"generated_at,omitempty" yaml:"generated_at,omitempty"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

type Objects struct {
	HTTPEndpoints   []HTTPEndpoint   `json:"http_endpoints" yaml:"http_endpoints"`
	HTTPCalls       []HTTPCall       `json:"http_calls" yaml:"http_calls"`
	DBResources     []DBResource     `json:"db_resources" yaml:"db_resources"`
	DBQueries       []DBQuery        `json:"db_queries" yaml:"db_queries"`
	QueueConsumers  []QueueConsumer  `json:"queue_consumers" yaml:"queue_consumers"`
	QueuePublishers []QueuePublisher `json:"queue_publishers" yaml:"queue_publishers"`
	RPCEndpoints    []RPCObjective   `json:"rpc_endpoints" yaml:"rpc_endpoints"`
	RPCCalls        []RPCObjective   `json:"rpc_calls" yaml:"rpc_calls"`
	CLICommands     []CLICommand     `json:"cli_commands" yaml:"cli_commands"`
	Activations     []Activation     `json:"activations" yaml:"activations"`
	CacheOperations []CacheOperation `json:"cache_operations" yaml:"cache_operations"`
	ConfigReads     []ConfigRead     `json:"config_reads" yaml:"config_reads"`
	FeatureFlags    []FeatureFlag    `json:"feature_flags" yaml:"feature_flags"`
}

type ObjectiveBase struct {
	ID           string         `json:"id" yaml:"id"`
	Kind         string         `json:"kind" yaml:"kind"`
	Name         string         `json:"name" yaml:"name"`
	Status       Status         `json:"status,omitempty" yaml:"status,omitempty"`
	Confidence   Confidence     `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Origin       Origin         `json:"origin,omitempty" yaml:"origin,omitempty"`
	Observations []string       `json:"observations" yaml:"observations"`
	EvidenceRefs []string       `json:"evidence_refs" yaml:"evidence_refs"`
	Metadata     map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type Auth struct {
	Required bool     `json:"required" yaml:"required"`
	Schemes  []string `json:"schemes,omitempty" yaml:"schemes,omitempty"`
	Scopes   []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

type HTTPField struct {
	Name        string         `json:"name" yaml:"name"`
	Type        string         `json:"type,omitempty" yaml:"type,omitempty"`
	Required    bool           `json:"required,omitempty" yaml:"required,omitempty"`
	Sensitivity string         `json:"sensitivity,omitempty" yaml:"sensitivity,omitempty"`
	Source      map[string]any `json:"source,omitempty" yaml:"source,omitempty"`
	Validation  map[string]any `json:"validation,omitempty" yaml:"validation,omitempty"`
}

type BodySpec struct {
	ContentType string         `json:"content_type,omitempty" yaml:"content_type,omitempty"`
	Required    bool           `json:"required,omitempty" yaml:"required,omitempty"`
	Schema      map[string]any `json:"schema,omitempty" yaml:"schema,omitempty"`
}

type HTTPInputs struct {
	PathParams  []HTTPField    `json:"path_params,omitempty" yaml:"path_params,omitempty"`
	QueryParams []HTTPField    `json:"query_params,omitempty" yaml:"query_params,omitempty"`
	Headers     []HTTPField    `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body        *BodySpec      `json:"body,omitempty" yaml:"body,omitempty"`
	Flags       []HTTPField    `json:"flags,omitempty" yaml:"flags,omitempty"`
	Env         []HTTPField    `json:"env,omitempty" yaml:"env,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type HTTPResponse struct {
	Status int    `json:"status" yaml:"status"`
	Error  string `json:"error,omitempty" yaml:"error,omitempty"`
}

type HTTPEndpoint struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Method        string         `json:"method,omitempty" yaml:"method,omitempty"`
	Path          string         `json:"path,omitempty" yaml:"path,omitempty"`
	BasePath      string         `json:"base_path,omitempty" yaml:"base_path,omitempty"`
	Visibility    string         `json:"visibility,omitempty" yaml:"visibility,omitempty"`
	Auth          *Auth          `json:"auth,omitempty" yaml:"auth,omitempty"`
	Inputs        *HTTPInputs    `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Responses     []HTTPResponse `json:"responses,omitempty" yaml:"responses,omitempty"`
}

type TargetRef struct {
	Type       string `json:"type,omitempty" yaml:"type,omitempty"`
	Ref        string `json:"ref,omitempty" yaml:"ref,omitempty"`
	Unresolved bool   `json:"unresolved,omitempty" yaml:"unresolved,omitempty"`
}

type RetrySpec struct {
	Enabled     bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	MaxAttempts int  `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`
}

type HTTPCall struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Method        string         `json:"method,omitempty" yaml:"method,omitempty"`
	URLTemplate   string         `json:"url_template,omitempty" yaml:"url_template,omitempty"`
	Target        *TargetRef     `json:"target,omitempty" yaml:"target,omitempty"`
	Inputs        *HTTPInputs    `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Responses     []HTTPResponse `json:"responses,omitempty" yaml:"responses,omitempty"`
	TimeoutMS     int            `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	Retry         *RetrySpec     `json:"retry,omitempty" yaml:"retry,omitempty"`
}

type DBResource struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Engine        string         `json:"engine,omitempty" yaml:"engine,omitempty"`
	ResourceType  string         `json:"resource_type,omitempty" yaml:"resource_type,omitempty"`
	Database      string         `json:"database,omitempty" yaml:"database,omitempty"`
	SchemaName    string         `json:"schema,omitempty" yaml:"schema,omitempty"`
	Table         string         `json:"table,omitempty" yaml:"table,omitempty"`
	Ownership     string         `json:"ownership,omitempty" yaml:"ownership,omitempty"`
	Data          map[string]any `json:"data,omitempty" yaml:"data,omitempty"`
}

type DBQueryTarget struct {
	ResourceRef string   `json:"resource_ref,omitempty" yaml:"resource_ref,omitempty"`
	Database    string   `json:"database,omitempty" yaml:"database,omitempty"`
	SchemaName  string   `json:"schema,omitempty" yaml:"schema,omitempty"`
	Tables      []string `json:"tables,omitempty" yaml:"tables,omitempty"`
}

type QuerySpec struct {
	Language    string `json:"language,omitempty" yaml:"language,omitempty"`
	Template    string `json:"template,omitempty" yaml:"template,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`
	Redacted    bool   `json:"redacted,omitempty" yaml:"redacted,omitempty"`
}

type ORMSpec struct {
	Library string `json:"library,omitempty" yaml:"library,omitempty"`
	Model   string `json:"model,omitempty" yaml:"model,omitempty"`
	Method  string `json:"method,omitempty" yaml:"method,omitempty"`
}

type ColumnSpec struct {
	Reads  []string `json:"reads,omitempty" yaml:"reads,omitempty"`
	Writes []string `json:"writes,omitempty" yaml:"writes,omitempty"`
}

type DBQuery struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Engine        string         `json:"engine,omitempty" yaml:"engine,omitempty"`
	Operation     string         `json:"operation,omitempty" yaml:"operation,omitempty"`
	Access        string         `json:"access,omitempty" yaml:"access,omitempty"`
	Target        *DBQueryTarget `json:"target,omitempty" yaml:"target,omitempty"`
	Query         *QuerySpec     `json:"query,omitempty" yaml:"query,omitempty"`
	ORM           *ORMSpec       `json:"orm,omitempty" yaml:"orm,omitempty"`
	Columns       *ColumnSpec    `json:"columns,omitempty" yaml:"columns,omitempty"`
}

type QueuePublisher struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Platform      string         `json:"platform,omitempty" yaml:"platform,omitempty"`
	Topic         string         `json:"topic,omitempty" yaml:"topic,omitempty"`
	Queue         string         `json:"queue,omitempty" yaml:"queue,omitempty"`
	Message       map[string]any `json:"message,omitempty" yaml:"message,omitempty"`
}

type QueueConsumer struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Platform      string         `json:"platform,omitempty" yaml:"platform,omitempty"`
	Topic         string         `json:"topic,omitempty" yaml:"topic,omitempty"`
	Queue         string         `json:"queue,omitempty" yaml:"queue,omitempty"`
	ConsumerGroup string         `json:"consumer_group,omitempty" yaml:"consumer_group,omitempty"`
	Message       map[string]any `json:"message,omitempty" yaml:"message,omitempty"`
}

type RPCObjective struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Protocol      string     `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	Service       string     `json:"service,omitempty" yaml:"service,omitempty"`
	Method        string     `json:"method,omitempty" yaml:"method,omitempty"`
	Target        *TargetRef `json:"target,omitempty" yaml:"target,omitempty"`
}

type CLICommand struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Command       map[string]any `json:"command,omitempty" yaml:"command,omitempty"`
	Inputs        *HTTPInputs    `json:"inputs,omitempty" yaml:"inputs,omitempty"`
}

type Activation struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Schedule      string         `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	Timezone      string         `json:"timezone,omitempty" yaml:"timezone,omitempty"`
	Invokes       map[string]any `json:"invokes,omitempty" yaml:"invokes,omitempty"`
	Kubernetes    map[string]any `json:"kubernetes,omitempty" yaml:"kubernetes,omitempty"`
	Environment   map[string]any `json:"environment,omitempty" yaml:"environment,omitempty"`
}

type CacheOperation struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Platform      string         `json:"platform,omitempty" yaml:"platform,omitempty"`
	Operation     string         `json:"operation,omitempty" yaml:"operation,omitempty"`
	Target        map[string]any `json:"target,omitempty" yaml:"target,omitempty"`
	KeyPattern    string         `json:"key_pattern,omitempty" yaml:"key_pattern,omitempty"`
}

type ConfigRead struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Key           string `json:"key,omitempty" yaml:"key,omitempty"`
	Value         string `json:"value,omitempty" yaml:"value,omitempty"`
	Source        string `json:"source,omitempty" yaml:"source,omitempty"`
}

type FeatureFlag struct {
	ObjectiveBase `json:",inline" yaml:",inline"`
	Key           string `json:"key,omitempty" yaml:"key,omitempty"`
	Provider      string `json:"provider,omitempty" yaml:"provider,omitempty"`
}

type Location struct {
	File      string `json:"file,omitempty" yaml:"file,omitempty"`
	StartLine int    `json:"start_line,omitempty" yaml:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty" yaml:"end_line,omitempty"`
	Symbol    string `json:"symbol,omitempty" yaml:"symbol,omitempty"`
}

type Observation struct {
	ID          string     `json:"id" yaml:"id"`
	ObjectRef   string     `json:"object_ref,omitempty" yaml:"object_ref,omitempty"`
	Perspective string     `json:"perspective,omitempty" yaml:"perspective,omitempty"`
	Location    *Location  `json:"location,omitempty" yaml:"location,omitempty"`
	Detector    string     `json:"detector,omitempty" yaml:"detector,omitempty"`
	Confidence  Confidence `json:"confidence,omitempty" yaml:"confidence,omitempty"`
}

type Evidence struct {
	ID          string     `json:"id" yaml:"id"`
	Type        string     `json:"type,omitempty" yaml:"type,omitempty"`
	Source      string     `json:"source,omitempty" yaml:"source,omitempty"`
	Detector    string     `json:"detector,omitempty" yaml:"detector,omitempty"`
	File        string     `json:"file,omitempty" yaml:"file,omitempty"`
	StartLine   int        `json:"start_line,omitempty" yaml:"start_line,omitempty"`
	EndLine     int        `json:"end_line,omitempty" yaml:"end_line,omitempty"`
	Symbol      string     `json:"symbol,omitempty" yaml:"symbol,omitempty"`
	SnippetHash string     `json:"snippet_hash,omitempty" yaml:"snippet_hash,omitempty"`
	ObservedAt  time.Time  `json:"observed_at,omitempty" yaml:"observed_at,omitempty"`
	Confidence  Confidence `json:"confidence,omitempty" yaml:"confidence,omitempty"`
}

type Flow struct {
	ID               string           `json:"id" yaml:"id"`
	Kind             string           `json:"kind,omitempty" yaml:"kind,omitempty"`
	Entrypoint       string           `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
	From             string           `json:"from,omitempty" yaml:"from,omitempty"`
	To               string           `json:"to,omitempty" yaml:"to,omitempty"`
	Reachability     Reachability     `json:"reachability,omitempty" yaml:"reachability,omitempty"`
	Condition        *Condition       `json:"condition,omitempty" yaml:"condition,omitempty"`
	Nodes            []FlowNode       `json:"nodes,omitempty" yaml:"nodes,omitempty"`
	Edges            []FlowEdge       `json:"edges,omitempty" yaml:"edges,omitempty"`
	DataDependencies []DataDependency `json:"data_dependencies,omitempty" yaml:"data_dependencies,omitempty"`
	SideEffects      []SideEffect     `json:"side_effects,omitempty" yaml:"side_effects,omitempty"`
	Status           Status           `json:"status,omitempty" yaml:"status,omitempty"`
	Confidence       Confidence       `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Origin           Origin           `json:"origin,omitempty" yaml:"origin,omitempty"`
	EvidenceRefs     []string         `json:"evidence_refs,omitempty" yaml:"evidence_refs,omitempty"`
}

type FlowNode struct {
	ID     string `json:"id" yaml:"id"`
	Ref    string `json:"ref,omitempty" yaml:"ref,omitempty"`
	Symbol string `json:"symbol,omitempty" yaml:"symbol,omitempty"`
	Role   string `json:"role,omitempty" yaml:"role,omitempty"`
}

type FlowEdge struct {
	From         string       `json:"from" yaml:"from"`
	To           string       `json:"to" yaml:"to"`
	Reachability Reachability `json:"reachability,omitempty" yaml:"reachability,omitempty"`
	Condition    *Condition   `json:"condition,omitempty" yaml:"condition,omitempty"`
	Kind         string       `json:"kind,omitempty" yaml:"kind,omitempty"`
	Branch       string       `json:"branch,omitempty" yaml:"branch,omitempty"`
	Loop         *LoopSpec    `json:"loop,omitempty" yaml:"loop,omitempty"`
	Fanout       bool         `json:"fanout,omitempty" yaml:"fanout,omitempty"`
	Repeat       *RepeatSpec  `json:"repeat,omitempty" yaml:"repeat,omitempty"`
}

type LoopSpec struct {
	Kind        string     `json:"kind,omitempty" yaml:"kind,omitempty"`
	Expression  string     `json:"expression,omitempty" yaml:"expression,omitempty"`
	MaxInferred int        `json:"max_inferred,omitempty" yaml:"max_inferred,omitempty"`
	Location    *Location  `json:"location,omitempty" yaml:"location,omitempty"`
	Confidence  Confidence `json:"confidence,omitempty" yaml:"confidence,omitempty"`
}

type RepeatSpec struct {
	Min        int        `json:"min,omitempty" yaml:"min,omitempty"`
	Max        int        `json:"max,omitempty" yaml:"max,omitempty"`
	Expression string     `json:"expression,omitempty" yaml:"expression,omitempty"`
	Confidence Confidence `json:"confidence,omitempty" yaml:"confidence,omitempty"`
}

type Condition struct {
	Summary         string              `json:"summary,omitempty" yaml:"summary,omitempty"`
	Expression      string              `json:"expression,omitempty" yaml:"expression,omitempty"`
	SourceLocations []Location          `json:"source_locations,omitempty" yaml:"source_locations,omitempty"`
	DependsOn       map[string][]string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Confidence      Confidence          `json:"confidence,omitempty" yaml:"confidence,omitempty"`
}

type DataEndpoint struct {
	ObjectRef  string `json:"object_ref,omitempty" yaml:"object_ref,omitempty"`
	Expression string `json:"expression,omitempty" yaml:"expression,omitempty"`
}

type DataDependency struct {
	ID           string         `json:"id,omitempty" yaml:"id,omitempty"`
	From         DataEndpoint   `json:"from" yaml:"from"`
	To           DataEndpoint   `json:"to" yaml:"to"`
	Kind         string         `json:"kind,omitempty" yaml:"kind,omitempty"`
	Transforms   []string       `json:"transforms,omitempty" yaml:"transforms,omitempty"`
	Sanitization map[string]any `json:"sanitization,omitempty" yaml:"sanitization,omitempty"`
	Confidence   Confidence     `json:"confidence,omitempty" yaml:"confidence,omitempty"`
}

type SideEffect struct {
	ObjectRef   string `json:"object_ref,omitempty" yaml:"object_ref,omitempty"`
	Effect      string `json:"effect,omitempty" yaml:"effect,omitempty"`
	ResourceRef string `json:"resource_ref,omitempty" yaml:"resource_ref,omitempty"`
	TargetRef   string `json:"target_ref,omitempty" yaml:"target_ref,omitempty"`
}

type Review struct {
	Proposals []ReviewProposal `json:"proposals,omitempty" yaml:"proposals,omitempty"`
}

type ReviewProposal struct {
	ID                string           `json:"id" yaml:"id"`
	Kind              string           `json:"kind,omitempty" yaml:"kind,omitempty"`
	Question          string           `json:"question,omitempty" yaml:"question,omitempty"`
	Subject           map[string]any   `json:"subject,omitempty" yaml:"subject,omitempty"`
	Options           []map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
	RecommendedAction map[string]any   `json:"recommended_action,omitempty" yaml:"recommended_action,omitempty"`
	Status            string           `json:"status,omitempty" yaml:"status,omitempty"`
}
