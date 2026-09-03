package discovery

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/repositorycall"
)

type (
	candidate         = extraction.Candidate
	candidateLocation = extraction.Location
	candidateEvidence = extraction.Evidence
	discoveryResult   = extraction.DiscoveryResult
)

type DeterministicRunner struct{}

type DeterministicInput struct {
	Index      *astpkg.ProjectIndex
	Objectives []objectives.Objective
}

type DeterministicOutput struct {
	Results []extraction.DiscoveryResult
	Report  DeterministicFrameworkReport
	Items   int
}

func (DeterministicRunner) Run(input DeterministicInput) DeterministicOutput {
	if input.Index == nil {
		return DeterministicOutput{}
	}
	report := DeterministicFrameworkReport{
		Accepted:      append([]astpkg.FrameworkBinding{}, input.Index.Frameworks...),
		Rejected:      append([]astpkg.FrameworkBinding{}, input.Index.RejectedFrameworks...),
		RouteManifest: RouteHandlerManifest(input.Index.Frameworks),
	}
	byObjective := supportedDeterministicObjectives(input.Objectives)
	outMap := map[string][]extraction.Candidate{}
	for _, b := range input.Index.Frameworks {
		obj, ok := objectiveForBinding(byObjective, b)
		if !ok {
			continue
		}
		e, ok := EntityFromFrameworkBinding(input.Index, obj, b)
		if !ok {
			continue
		}
		outMap[obj.ID] = append(outMap[obj.ID], e)
	}
	if obj, ok := byObjective["queue_consumer"]; ok {
		for _, e := range DeterministicSAMQueueConsumers(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
		for _, e := range DeterministicAWSQueueConsumers(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
		for _, e := range DeterministicPythonSQSConsumers(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}

	// Call-graph-derived dependencies (not framework-binding based). These
	// reuse the connections stage's proven repository-call predicates. Only
	// emitted when the objective is in scope for this run.
	if dbObj, ok := objectiveByTypeIn(input.Objectives, "db_operation"); ok {
		for _, e := range DeterministicDBOperations(input.Index) {
			outMap[dbObj.ID] = append(outMap[dbObj.ID], e)
		}
		for _, e := range DeterministicDynamoDBOperations(input.Index) {
			outMap[dbObj.ID] = append(outMap[dbObj.ID], e)
		}
		// Raw-SQL leg (F6): language-agnostic, covers stacks the repository
		// deriver can't see. Same (table, operation) granularity, so the
		// per-objective merge dedups overlap with the JVM deriver.
		for _, e := range DeterministicSQLOperations(input.Index) {
			outMap[dbObj.ID] = append(outMap[dbObj.ID], e)
		}
		// ORM leg (F6): GORM/Django ORM/Sequelize/Prisma/ActiveRecord calls
		// whose model is statically resolvable from the call itself.
		for _, e := range DeterministicORMOperations(input.Index) {
			outMap[dbObj.ID] = append(outMap[dbObj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(input.Objectives, "command_exec"); ok {
		for _, e := range DeterministicCommandExec(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(input.Objectives, "queue_publish"); ok {
		for _, e := range DeterministicQueuePublish(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(input.Objectives, "outbound_rpc"); ok {
		for _, e := range DeterministicOutboundRPC(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(input.Objectives, "workflow_orchestration"); ok {
		for _, e := range DeterministicWorkflowOrchestration(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(input.Objectives, "outbound_http"); ok {
		for _, e := range DeterministicOutboundHTTP(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(input.Objectives, "stream_consume"); ok {
		for _, e := range DeterministicStreamConsume(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(input.Objectives, "cache_operation"); ok {
		for _, e := range DeterministicCacheOperations(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(input.Objectives, "cli_command"); ok {
		for _, e := range DeterministicCLIEntrypoints(input.Index) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}

	results := make([]extraction.DiscoveryResult, 0, len(outMap))
	total := 0
	for _, obj := range input.Objectives {
		items := outMap[obj.ID]
		if len(items) == 0 {
			continue
		}
		extraction.SortCandidates(items)
		total += len(items)
		results = append(results, extraction.DiscoveryResult{Objective: obj, Items: items})
	}
	return DeterministicOutput{Results: results, Report: report, Items: total}
}

// DeterministicDBOperations derives database operations directly from the AST
// call graph. It reuses the SAME repository-call predicates the connections
// stage already trusts (isRepositoryOperationSymbol, tableEntityFromRepository,
// inferDBOperationKind) so precision matches the connection resolver's
// interpretation of repository calls.
//
// Granularity is HIGH-LEVEL: one entity per (table, operation-kind) — e.g.
// "read orders", "write orders" — not one per repository method. That matches
// the extractor's purpose and the (resource, operation) dedup key, and it is
// what stabilises db_operation output. Each entity carries deterministic
// evidence/tags so the rest of the pipeline treats it as a confirmed seed.
//
// SCOPE / KNOWN LIMITATIONS (intentional, documented — see docs/PLATFORM.md):
//   - This repository-call leg is strongest on Spring Data / JPA / MyBatis.
//     Separate raw-SQL and ORM legs cover selected non-JVM patterns, but coverage
//     remains conservative and uneven.
//   - Table names are derived only when a resource can be resolved precisely;
//     generic handles and sequence names are rejected rather than guessed.
//
// InferConfigDBPlatform returns the single concrete relational DB platform
// referenced by the repo's config (jdbc URL / driver class), or "" when none or
// more than one distinct platform is found. Conservative on purpose: an
// ambiguous guess would be worse than leaving the platform generic (P7).
func InferConfigDBPlatform(idx *astpkg.ProjectIndex) string {
	if idx == nil {
		return ""
	}
	found := map[string]struct{}{}
	for _, cf := range idx.Configs {
		for _, e := range cf.Entries {
			if p := dbPlatformFromConfigValue(e.Value); p != "" {
				found[p] = struct{}{}
			}
		}
	}
	if len(found) == 1 {
		for p := range found {
			return p
		}
	}
	return ""
}

func dbPlatformFromConfigValue(v string) string {
	v = strings.ToLower(v)
	switch {
	case strings.Contains(v, "postgresql") || strings.Contains(v, "postgres") || strings.Contains(v, "org.postgresql"):
		return "postgres"
	case strings.Contains(v, "mariadb"):
		return "mariadb"
	case strings.Contains(v, "mysql"):
		return "mysql"
	case strings.Contains(v, "sqlserver") || strings.Contains(v, "jtds"):
		return "sqlserver"
	case strings.Contains(v, "oracle"):
		return "oracle"
	case strings.Contains(v, "mongodb"):
		return "mongodb"
	}
	return ""
}

// StampInferredDBPlatform fills a concrete platform onto db_operation deps that
// only have a generic/empty one, using the repo's single configured datasource
// platform. This lets db ops that know the table but not the engine share a
// stable identity with platform-qualified facts. Never overwrites an
// already-specific platform.
func StampInferredDBPlatform(idx *astpkg.ProjectIndex, deps []model.Dependency) {
	plat := InferConfigDBPlatform(idx)
	if plat == "" {
		return
	}
	generic := map[string]bool{"": true, "database": true, "jdbc": true, "sql": true, "rdbms": true, "unknown": true}
	for i := range deps {
		d := &deps[i]
		if d.Type != "db_operation" {
			continue
		}
		if !generic[strings.ToLower(strings.TrimSpace(d.Platform))] {
			continue // already specific
		}
		d.Platform = plat
		if d.Details == nil {
			d.Details = map[string]any{}
		}
		if _, ok := d.Details["database_type"]; !ok {
			d.Details["database_type"] = plat
		}
	}
}

func DeterministicDBOperations(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil || len(idx.CallGraph) == 0 {
		return nil
	}
	type agg struct {
		table, opKind string
		loc           candidateLocation
		owner         string
		hits          int
	}
	seen := map[string]*agg{}
	var order []string

	consider := func(target string, cs astpkg.CallSite) {
		if !repositorycall.IsOperationSymbol(target) {
			return
		}
		owner, _, ok := repositorycall.SplitOwnerMethod(repositorycall.NormalizeOperationName(target))
		if !ok || owner == "" {
			return
		}
		entity, table := repositorycall.TableEntity(owner)
		if table == "" {
			table = entity
		}
		if table == "" {
			return
		}
		// Precision guard: never emit a generic-handle / sequence table as a
		// deterministic db_operation (see isJunkTableName). A wrong fact poisons
		// downstream; a typed detector can recover the real table later when it
		// has enough evidence.
		if repositorycall.IsJunkTable(table) {
			return
		}
		opKind := repositorycall.InferOperationKind(idx, target)
		key := strings.ToLower(table + "|" + opKind)
		a, ok := seen[key]
		if !ok {
			a = &agg{
				table:  table,
				opKind: opKind,
				owner:  owner,
				loc:    candidateLocation{File: cs.File, StartLine: int(cs.Range.StartLine) + 1, EndLine: int(cs.Range.EndLine) + 1},
			}
			seen[key] = a
			order = append(order, key)
		}
		a.hits++
	}

	for _, sites := range idx.CallGraph {
		for _, cs := range sites {
			if len(cs.CalleeResolved) > 0 {
				for _, t := range cs.CalleeResolved {
					consider(t, cs)
				}
				continue
			}
			// Fall back to the syntactic receiver.callee (catches the common
			// `orderRepository.findById(...)` form even when resolution failed).
			if r := strings.TrimSpace(cs.ReceiverRaw); r != "" && strings.TrimSpace(cs.CalleeRaw) != "" {
				consider(r+"."+strings.TrimSpace(cs.CalleeRaw), cs)
			}
		}
	}

	out := make([]candidate, 0, len(order))
	for _, key := range order {
		a := seen[key]
		loc := a.loc
		if loc.File == "" {
			continue
		}
		name := a.opKind + " " + a.table
		out = append(out, candidate{
			Type:       "db_operation",
			Name:       name,
			Summary:    fmt.Sprintf("AST-derived %s on %s (via %s)", a.opKind, a.table, a.owner),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "framework:spring-data"},
			Details: map[string]any{
				"table":         a.table,
				"operation":     a.opKind,
				"repository":    a.owner,
				"discovered_by": "ast_repository_call",
			},
			Locations: []candidateLocation{loc},
			Evidence: []candidateEvidence{{
				File:      loc.File,
				StartLine: loc.StartLine,
				EndLine:   loc.EndLine,
				Snippet:   fmt.Sprintf("repository %s call resolved to %s table", a.owner, a.table),
				Source:    "deterministic_ast_repository",
			}},
		})
	}
	return out
}

// objectiveByTypeIn finds the in-scope objective with the given type.
func objectiveByTypeIn(objs []objectives.Objective, typ string) (objectives.Objective, bool) {
	for _, o := range objs {
		if o.Type == typ {
			return o, true
		}
	}
	return objectives.Objective{}, false
}

func supportedDeterministicObjectives(objs []objectives.Objective) map[string]objectives.Objective {
	out := map[string]objectives.Objective{}
	for _, obj := range objs {
		switch obj.Kind {
		case model.KindExposure:
			switch obj.Type {
			case "http_route", "rpc_endpoint", "queue_consumer", "scheduled_job", "cli_command":
				out[obj.Type] = obj
			}
		case model.KindDependency:
			switch obj.Type {
			case "outbound_http", "queue_publish", "cache_operation", "workflow_orchestration":
				out[obj.Type] = obj
			}
		}
	}
	return out
}

func objectiveForBinding(objs map[string]objectives.Objective, b astpkg.FrameworkBinding) (objectives.Objective, bool) {
	switch strings.TrimSpace(b.Kind) {
	case "http_handler":
		obj, ok := objs["http_route"]
		return obj, ok
	case "http_client":
		obj, ok := objs["outbound_http"]
		return obj, ok
	case "cache_operation":
		obj, ok := objs["cache_operation"]
		return obj, ok
	case "queue_consumer":
		obj, ok := objs["queue_consumer"]
		return obj, ok
	case "queue_publisher":
		obj, ok := objs["queue_publish"]
		return obj, ok
	case "scheduler":
		obj, ok := objs["scheduled_job"]
		return obj, ok
	case "rpc_endpoint":
		obj, ok := objs["rpc_endpoint"]
		return obj, ok
	default:
		return objectives.Objective{}, false
	}
}

func EntityFromFrameworkBinding(idx *astpkg.ProjectIndex, obj objectives.Objective, b astpkg.FrameworkBinding) (candidate, bool) {
	file := strings.TrimSpace(b.File)
	if file == "" {
		return candidate{}, false
	}
	start := int(b.Range.StartLine) + 1
	end := int(b.Range.EndLine) + 1
	if end < start {
		end = start
	}
	loc := candidateLocation{File: file, StartLine: start, EndLine: end}
	ev := candidateEvidence{
		File:      file,
		StartLine: start,
		EndLine:   end,
		Snippet:   strings.TrimSpace(b.TriggerSource),
		Source:    "deterministic_framework",
	}
	tags := []string{"deterministic", "framework:" + strings.TrimSpace(b.Framework)}
	handler := strings.TrimSpace(b.Symbol)
	trigger := strings.TrimSpace(b.Trigger)

	e := candidate{
		Type:       obj.Type,
		Confidence: 1.0,
		Tags:       tags,
		Details: map[string]any{
			"framework": b.Framework,
			"handler":   handler,
			"direction": b.Direction,
			"reason":    b.ConfidenceReason,
		},
		Locations: []candidateLocation{loc},
		Evidence:  []candidateEvidence{ev},
	}

	switch obj.Type {
	case "http_route":
		method, path := parseHTTPTrigger(trigger)
		if method == "" || path == "" {
			return candidate{}, false
		}
		e.Name = strings.TrimSpace(method + " " + path)
		e.Summary = fmt.Sprintf("%s HTTP route detected from framework binding", displayFramework(b.Framework))
		e.Details["method"] = method
		e.Details["path"] = path
	case "outbound_http":
		if b.Framework == "net/http" {
			parts := strings.SplitN(trigger, " ", 2)
			if len(parts) != 2 {
				return candidate{}, false
			}
			u, err := url.Parse(parts[1])
			if err != nil || u.Hostname() == "" {
				return candidate{}, false
			}
			path := u.Path
			if path == "" {
				path = "/"
			}
			e.Name = parts[0] + " " + parts[1]
			e.Summary = "net/http outbound call to a literal destination"
			e.Details["method"], e.Details["path"] = parts[0], path
			e.Details["host"], e.Details["target_service"] = u.Hostname(), u.Hostname()
			e.Details["url_template"], e.Details["base_url"] = parts[1], u.Scheme+"://"+u.Host
			return e, true
		}
		method, path := parseHTTPTrigger(trigger)
		if method == "" || path == "" {
			return candidate{}, false
		}
		e.Name = strings.TrimSpace(method + " " + path)
		e.Summary = fmt.Sprintf("%s outbound HTTP client detected from framework binding", displayFramework(b.Framework))
		e.Details["method"] = method
		e.Details["path"] = path
		if strings.EqualFold(b.Framework, "openai") || strings.EqualFold(b.Framework, "go-wire") {
			e.Name = "openai " + e.Name
			e.Summary = "OpenAI outbound API dependency detected from deterministic Go wiring/SDK usage"
			e.Details["target_service"] = "openai"
			e.Details["target_type"] = "external"
			e.Details["url_template"] = "https://api.openai.com" + path
			e.Details["base_url"] = "https://api.openai.com"
			e.Details["host"] = "api.openai.com"
			e.Details["provider"] = "openai"
			e.Details["sdk"] = "github.com/sashabaranov/go-openai"
			e.Details["operation"] = "chat_completion"
			e.Details["operation_kind"] = "create"
			if model := modelFromOpenAIReason(b.ConfidenceReason); model != "" {
				e.Details["model"] = model
			}
			if strings.EqualFold(b.Framework, "go-wire") {
				e.Details["wiring_provider"] = providerFromWireReason(b.ConfidenceReason)
			}
			return e, true
		}
		if target := configuredHTTPTargetForOperation(idx, handler, path); target.serviceRef != "" {
			applyConfiguredHTTPTargetDetails(e.Details, target)
		} else if strings.EqualFold(b.Framework, "retrofit") {
			if target := retrofitTargetFromHandler(idx, handler); target.logicalName != "" {
				e.Details["instance"] = target.logicalName
				e.Details["target_service"] = target.logicalName
				e.Details["url_template"] = target.urlTemplate
				e.Details["base_url"] = target.urlTemplate
				e.Details["config_source"] = target.configKey
			}
		}
	case "rpc_endpoint":
		protocol, service, method := parseRPCTrigger(trigger)
		if protocol == "" || service == "" {
			return candidate{}, false
		}
		e.Name = service + "/" + method
		e.Summary = fmt.Sprintf("%s RPC endpoint detected from framework binding", displayFramework(b.Framework))
		e.Details["protocol"] = protocol
		e.Details["service"] = service
		e.Details["method"] = method
	case "queue_consumer":
		platform, queue := parseQueueTrigger(trigger)
		// Resolve ${...} property placeholders to the real queue name using the
		// already-parsed config index, so the entity is named after the queue
		// (e.g. catalogue-target-response-sqs) rather than the raw placeholder.
		res := ResolveResourceNameDetailed(idx, queue)
		queue = normalizeQueueOrTopicDestination(res.Name, platform)
		if queue == "" {
			return candidate{}, false
		}
		e.Name = queue
		e.Summary = fmt.Sprintf("%s queue consumer detected from framework binding", displayFramework(b.Framework))
		e.Details["platform"] = platform
		e.Details["queue"] = queue
		addResolutionDetails(e.Details, res)
	case "queue_publish":
		platform, dest := parseQueueTrigger(trigger)
		res := ResolveResourceNameDetailed(idx, dest)
		dest = normalizeQueueOrTopicDestination(res.Name, platform)
		if dest == "" {
			return candidate{}, false
		}
		e.Name = dest
		e.Summary = fmt.Sprintf("%s queue publisher detected from framework binding", displayFramework(b.Framework))
		e.Details["platform"] = platform
		if platform == "kafka" || platform == "sns" || strings.Contains(platform, "stream") {
			e.Details["topic"] = dest
		} else {
			e.Details["queue"] = dest
		}
		addResolutionDetails(e.Details, res)
	case "scheduled_job":
		schedule := parseScheduleTrigger(trigger)
		if schedule == "" {
			return candidate{}, false
		}
		e.Name = schedule
		if handler != "" {
			e.Name = handler
		}
		e.Summary = fmt.Sprintf("%s scheduled job detected from framework binding", displayFramework(b.Framework))
		e.Details["schedule"] = schedule
	case "cache_operation":
		op, cache := parseCacheTrigger(trigger)
		cache = ResolveResourceName(idx, cache)
		if cache == "" {
			cache = lastIdentOf(handler)
		}
		if cache == "" || op == "" {
			return candidate{}, false
		}
		e.Name = op + " " + cache
		e.Summary = fmt.Sprintf("%s cache operation detected from framework binding", displayFramework(b.Framework))
		e.Details["operation"] = op
		e.Details["cache"] = cache
	default:
		return candidate{}, false
	}
	return e, true
}

func modelFromOpenAIReason(reason string) string {
	for _, part := range strings.Fields(reason) {
		if strings.HasPrefix(part, "model=") {
			return strings.TrimSpace(strings.TrimPrefix(part, "model="))
		}
	}
	return ""
}

func providerFromWireReason(reason string) string {
	for _, part := range strings.Fields(reason) {
		if strings.HasPrefix(part, "provider=") {
			return strings.TrimSpace(strings.TrimPrefix(part, "provider="))
		}
	}
	return ""
}

func applyConfiguredHTTPTargetDetails(details map[string]any, target configuredHTTPTarget) {
	service := normalizeConfiguredServiceRef(target.serviceRef)
	if service == "" {
		return
	}
	details["instance"] = service
	details["target_service"] = service
	if target.urlTemplate != "" {
		details["url_template"] = target.urlTemplate
		details["base_url"] = target.urlTemplate
	}
	if target.baseURL != "" {
		details["base_url"] = target.baseURL
	}
	if target.host != "" {
		details["host"] = target.host
	}
	if target.configKey != "" {
		details["config_source"] = target.configKey
	}
	if target.external {
		details["target_type"] = "external"
	}
}

// parseCacheTrigger splits a "cache: <op> <name>" trigger.
func parseCacheTrigger(trigger string) (op, cache string) {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trigger), "cache:"))
	f := strings.Fields(t)
	if len(f) == 0 {
		return "", ""
	}
	op = f[0]
	if len(f) > 1 {
		cache = strings.Join(f[1:], " ")
	}
	return op, cache
}

func parseHTTPTrigger(trigger string) (method, path string) {
	parts := strings.Fields(strings.TrimSpace(trigger))
	if len(parts) < 2 {
		return "", ""
	}
	method = strings.ToUpper(parts[0])
	if _, ok := extraction.HTTPMethods[method]; !ok && method != "ANY" {
		return "", ""
	}
	path = strings.TrimSpace(parts[1])
	if path == "" {
		return "", ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return method, path
}

func parseQueueTrigger(trigger string) (platform, queue string) {
	platform = "queue"
	queue = strings.TrimSpace(trigger)
	if i := strings.Index(queue, ":"); i >= 0 {
		platform = strings.TrimSpace(strings.ToLower(queue[:i]))
		queue = strings.TrimSpace(queue[i+1:])
	}
	queue = strings.Trim(queue, `"'`)
	if platform == "" {
		platform = "queue"
	}
	return platform, queue
}

func parseRPCTrigger(trigger string) (protocol, service, method string) {
	parts := strings.Fields(strings.TrimSpace(trigger))
	if len(parts) == 0 {
		return "", "", ""
	}
	protocol = strings.ToLower(strings.Trim(parts[0], `"'`))
	if protocol == "" {
		protocol = "rpc"
	}
	if len(parts) > 1 {
		service = parts[1]
	}
	service = strings.Trim(service, `"'`)
	method = "*"
	if len(parts) > 2 {
		method = strings.Join(parts[2:], " ")
	}
	method = strings.Trim(method, `"'`)
	if method == "" {
		method = "*"
	}
	return protocol, service, method
}

func parseScheduleTrigger(trigger string) string {
	s := strings.TrimSpace(trigger)
	if i := strings.Index(s, ":"); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	return strings.Trim(s, `"'`)
}

func displayFramework(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Framework"
	}
	return s
}

type retrofitTarget struct {
	logicalName string
	urlTemplate string
	configKey   string
}

func retrofitTargetFromHandler(idx *astpkg.ProjectIndex, handler string) retrofitTarget {
	if idx == nil {
		return retrofitTarget{}
	}
	className := handler
	if i := strings.LastIndex(className, "."); i >= 0 {
		className = className[:i]
	}
	className = lastIdentOf(className)
	tokens := camelTokens(strings.TrimSuffix(className, "Api"))
	if len(tokens) == 0 {
		return retrofitTarget{}
	}
	var bestKey, bestValue string
	bestScore := 0
	for _, path := range sortedConfigPaths(idx) {
		for _, entry := range idx.Configs[path].Entries {
			key := strings.ToLower(strings.TrimSpace(entry.Key))
			if !strings.HasSuffix(key, "baseurl") && !strings.HasSuffix(key, "base-url") && !strings.HasSuffix(key, ".url") {
				continue
			}
			score := tokenMatchScore(key, tokens)
			if score > bestScore {
				bestScore = score
				bestKey = entry.Key
				bestValue = strings.TrimSpace(entry.Value)
			}
		}
	}
	if bestScore == 0 || bestKey == "" {
		return retrofitTarget{}
	}
	value, ok := ConfigValue(idx, bestKey)
	if ok {
		bestValue = value
	}
	return retrofitTarget{logicalName: KeySegmentName(bestKey), urlTemplate: bestValue, configKey: bestKey}
}

func camelTokens(s string) []string {
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		words = append(words, strings.ToLower(b.String()))
		b.Reset()
	}
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			flush()
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func tokenMatchScore(text string, tokens []string) int {
	score := 0
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" || token == "api" || token == "client" {
			continue
		}
		if strings.Contains(text, token) {
			score++
		}
	}
	return score
}

func MergeDiscoveryResults(baseline, deterministic []discoveryResult) []discoveryResult {
	out := make([]discoveryResult, 0, len(baseline)+len(deterministic))
	index := map[string]int{}
	for _, r := range baseline {
		out = append(out, discoveryResult{Objective: r.Objective, Items: append([]candidate(nil), r.Items...), Err: r.Err, PeerCancelled: r.PeerCancelled})
		index[r.Objective.ID] = len(out) - 1
	}
	for _, d := range deterministic {
		pos, ok := index[d.Objective.ID]
		if !ok {
			out = append(out, discoveryResult{Objective: d.Objective, Items: append([]candidate(nil), d.Items...)})
			index[d.Objective.ID] = len(out) - 1
			continue
		}
		out[pos].Items = mergeEntitiesForObjective(d.Objective, out[pos].Items, d.Items)
	}
	return out
}

func DeterministicByObjective(results []discoveryResult) map[string][]candidate {
	out := map[string][]candidate{}
	for _, r := range results {
		if len(r.Items) == 0 {
			continue
		}
		out[r.Objective.ID] = append(out[r.Objective.ID], r.Items...)
	}
	return out
}

type DeterministicFrameworkReport struct {
	Accepted      []astpkg.FrameworkBinding `json:"accepted"`
	Rejected      []astpkg.FrameworkBinding `json:"rejected"`
	RouteManifest []RouteManifestEntry      `json:"route_manifest"`
}

type RouteManifestEntry struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler"`
	File    string `json:"file"`
	Line    int    `json:"line"`
}

func RouteHandlerManifest(bindings []astpkg.FrameworkBinding) []RouteManifestEntry {
	out := make([]RouteManifestEntry, 0)
	for _, b := range bindings {
		if strings.TrimSpace(b.Kind) != "http_handler" {
			continue
		}
		method, path := parseHTTPTrigger(b.Trigger)
		if method == "" || path == "" {
			continue
		}
		out = append(out, RouteManifestEntry{
			Method:  method,
			Path:    path,
			Handler: strings.TrimSpace(b.Symbol),
			File:    strings.TrimSpace(b.File),
			Line:    int(b.Range.StartLine) + 1,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		if out[i].Method != out[j].Method {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func mergeEntitiesForObjective(obj objectives.Objective, baseline, deterministic []candidate) []candidate {
	out := make([]candidate, 0, len(baseline)+len(deterministic))
	index := map[string]int{}
	for _, e := range baseline {
		k := extraction.DiscoverySemanticKey(obj, e)
		index[k] = len(out)
		out = append(out, e)
	}
	for _, e := range deterministic {
		k := extraction.DiscoverySemanticKey(obj, e)
		if pos, ok := index[k]; ok {
			out[pos] = mergeDeterministicDuplicate(out[pos], e)
			continue
		}
		index[k] = len(out)
		out = append(out, e)
	}
	extraction.SortCandidates(out)
	return out
}

func mergeDeterministicDuplicate(base, det candidate) candidate {
	out := det
	if strings.TrimSpace(out.Summary) == "" {
		out.Summary = base.Summary
	}
	if len(out.Actions) == 0 {
		out.Actions = base.Actions
	}
	if len(out.Inputs) == 0 {
		out.Inputs = base.Inputs
	}
	out.Tags = extraction.DedupeStrings(append(append([]string(nil), det.Tags...), base.Tags...))
	out.Locations = UnionLocations(det.Locations, base.Locations)
	out.Evidence = append(append([]candidateEvidence(nil), det.Evidence...), base.Evidence...)
	if out.Details == nil {
		out.Details = map[string]any{}
	}
	for k, v := range base.Details {
		if _, ok := out.Details[k]; !ok {
			out.Details[k] = v
		}
	}
	return out
}

func UnionLocations(a, b []candidateLocation) []candidateLocation {
	seen := map[string]struct{}{}
	out := make([]candidateLocation, 0, len(a)+len(b))
	for _, loc := range append(append([]candidateLocation(nil), a...), b...) {
		key := fmt.Sprintf("%s:%d:%d", loc.File, loc.StartLine, loc.EndLine)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, loc)
	}
	return out
}
