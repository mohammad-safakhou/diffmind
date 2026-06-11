package pipeline

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	connectionstage "github.com/mohammad-safakhou/diffmind/internal/stage/connections"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func (o *orchestrator) runDeterministicDiscovery(ctx context.Context, objs []objectives.Objective) []discoveryResult {
	started := time.Now()
	o.emit(events.Event{
		Kind:   events.KindStageStarted,
		Stage:  "deterministic_discovery",
		Status: events.StatusRunning,
	})
	if err := ctx.Err(); err != nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "deterministic_discovery", Status: events.StatusFailed,
			Message: err.Error(),
		})
		return nil
	}
	if o.astIndex == nil {
		o.emit(events.Event{
			Kind: events.KindStageCompleted, Stage: "deterministic_discovery", Status: events.StatusSkipped,
			Message: "AST index unavailable",
		})
		return nil
	}
	o.persistStageState("deterministic_frameworks.json", deterministicFrameworkReport{
		Accepted:      append([]astpkg.FrameworkBinding{}, o.astIndex.Frameworks...),
		Rejected:      append([]astpkg.FrameworkBinding{}, o.astIndex.RejectedFrameworks...),
		RouteManifest: routeHandlerManifest(o.astIndex.Frameworks),
	})

	byObjective := supportedDeterministicObjectives(objs)
	outMap := map[string][]llmEntity{}
	for _, b := range o.astIndex.Frameworks {
		obj, ok := objectiveForBinding(byObjective, b)
		if !ok {
			continue
		}
		e, ok := entityFromFrameworkBinding(o.astIndex, obj, b)
		if !ok {
			continue
		}
		outMap[obj.ID] = append(outMap[obj.ID], e)
	}

	// Call-graph-derived dependencies (not framework-binding based). These
	// reuse the connections stage's proven repository-call predicates. Only
	// emitted when the objective is in scope for this run.
	if dbObj, ok := objectiveByTypeIn(objs, "db_operation"); ok {
		for _, e := range deterministicDBOperations(o.astIndex) {
			outMap[dbObj.ID] = append(outMap[dbObj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(objs, "command_exec"); ok {
		for _, e := range deterministicCommandExec(o.astIndex) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(objs, "queue_publish"); ok {
		for _, e := range deterministicQueuePublish(o.astIndex) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(objs, "outbound_rpc"); ok {
		for _, e := range deterministicOutboundRPC(o.astIndex) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}
	if obj, ok := objectiveByTypeIn(objs, "stream_consume"); ok {
		for _, e := range deterministicStreamConsume(o.astIndex) {
			outMap[obj.ID] = append(outMap[obj.ID], e)
		}
	}

	results := make([]discoveryResult, 0, len(outMap))
	total := 0
	for _, obj := range objs {
		items := outMap[obj.ID]
		if len(items) == 0 {
			continue
		}
		o.PathMapper().ApplyToEntities(items)
		SortLLMEntities(items)
		total += len(items)
		jobID := "deterministic." + obj.ID
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "deterministic_discovery", JobID: jobID, Status: events.StatusSuccess,
			Payload: map[string]any{
				"objective_id": obj.ID,
				"kind":         string(obj.Kind),
				"type":         obj.Type,
				"items":        len(items),
			},
		})
		results = append(results, discoveryResult{Objective: obj, Items: items})
	}
	o.persistStageState("deterministic_discovery.json", results)
	o.emitStageCompleted("deterministic_discovery", events.StatusSuccess, map[string]any{
		"items":       total,
		"objectives":  len(results),
		"duration_ms": time.Since(started).Milliseconds(),
	})
	util.Info("agents.deterministic_discovery", "deterministic discovery completed", map[string]any{
		"items": total, "objectives": len(results),
	})
	return results
}

// deterministicDBOperations derives database operations directly from the AST
// call graph, independent of the LLM. It reuses the SAME repository-call
// predicates the connections stage already trusts (isRepositoryOperationSymbol,
// tableEntityFromRepository, inferDBOperationKind) so precision matches the
// post-detail AST augmentation that has been in production.
//
// Granularity is HIGH-LEVEL: one entity per (table, operation-kind) — e.g.
// "read orders", "write orders" — not one per repository method. That matches
// the extractor's purpose and the (resource, operation) dedup key, and it is
// what lets the deterministic floor stabilise db_operation, the worst LLM
// offender. Each entity carries deterministic evidence/tags so the rest of the
// pipeline treats it as a confirmed seed.
//
// SCOPE / KNOWN LIMITATIONS (intentional, documented — see docs/PLATFORM.md):
//   - JVM-ONLY. The repository-call predicates recognise Spring Data / JPA /
//     MyBatis conventions (*Repository, *Dao, EntityManager). On non-JVM stacks
//     (Django ORM, ActiveRecord, GORM, Sequelize, ...) this yields ZERO rows —
//     SAFE (db_operation falls back to LLM-only discovery) but NOT yet stable
//     there. Extending deterministic db coverage to other ORMs is a milestone.
//   - Table names are derived from the repository/entity symbol name; they can
//     be slightly off (e.g. "entity_manager" from a raw EntityManager call, or
//     a "*_id_seq" sequence). These are low-signal precision nits, not
//     duplicates; reconcile collapses by (resource, operation) regardless.
//
// inferConfigDBPlatform returns the single concrete relational DB platform
// referenced by the repo's config (jdbc URL / driver class), or "" when none or
// more than one distinct platform is found. Conservative on purpose: an
// ambiguous guess would be worse than leaving the platform generic (P7).
func inferConfigDBPlatform(idx *astpkg.ProjectIndex) string {
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

// stampInferredDBPlatform fills a concrete platform onto db_operation deps that
// only have a generic/empty one, using the repo's single configured datasource
// platform. This lets deterministic db ops (which know the table but not the
// engine) share identity with the LLM's platform-qualified ones — fixing the
// floor↔LLM coverage artifact and keeping the eval/dedup identity consistent
// (P7). Never overwrites an already-specific platform.
func stampInferredDBPlatform(idx *astpkg.ProjectIndex, deps []model.Dependency) {
	plat := inferConfigDBPlatform(idx)
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

func deterministicDBOperations(idx *astpkg.ProjectIndex) []llmEntity {
	if idx == nil || len(idx.CallGraph) == 0 {
		return nil
	}
	type agg struct {
		table, opKind string
		loc           llmLocation
		owner         string
		hits          int
	}
	seen := map[string]*agg{}
	var order []string

	consider := func(target string, cs astpkg.CallSite) {
		if !connectionstage.IsRepositoryOperationSymbol(target) {
			return
		}
		owner, _, ok := connectionstage.SplitOwnerMethod(connectionstage.NormalizeRepositoryOperationName(target))
		if !ok || owner == "" {
			return
		}
		entity, table := connectionstage.TableEntityFromRepository(owner)
		if table == "" {
			table = entity
		}
		if table == "" {
			return
		}
		// Precision guard: never emit a generic-handle / sequence table as a
		// deterministic db_operation (see isJunkTableName). A wrong fact poisons
		// downstream; the LLM recovers the real table from argument types.
		if connectionstage.IsJunkTableName(table) {
			return
		}
		opKind := connectionstage.InferDBOperationKind(idx, target)
		key := strings.ToLower(table + "|" + opKind)
		a, ok := seen[key]
		if !ok {
			a = &agg{
				table:  table,
				opKind: opKind,
				owner:  owner,
				loc:    llmLocation{File: cs.File, StartLine: int(cs.Range.StartLine) + 1, EndLine: int(cs.Range.EndLine) + 1},
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

	out := make([]llmEntity, 0, len(order))
	for _, key := range order {
		a := seen[key]
		loc := a.loc
		if loc.File == "" {
			continue
		}
		name := a.opKind + " " + a.table
		out = append(out, llmEntity{
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
			Locations: []llmLocation{loc},
			Evidence: []llmEvidence{{
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
			case "http_route", "queue_consumer", "scheduled_job":
				out[obj.Type] = obj
			}
		case model.KindDependency:
			switch obj.Type {
			case "outbound_http", "cache_operation":
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
	case "scheduler":
		obj, ok := objs["scheduled_job"]
		return obj, ok
	default:
		return objectives.Objective{}, false
	}
}

func entityFromFrameworkBinding(idx *astpkg.ProjectIndex, obj objectives.Objective, b astpkg.FrameworkBinding) (llmEntity, bool) {
	file := strings.TrimSpace(b.File)
	if file == "" {
		return llmEntity{}, false
	}
	start := int(b.Range.StartLine) + 1
	end := int(b.Range.EndLine) + 1
	if end < start {
		end = start
	}
	loc := llmLocation{File: file, StartLine: start, EndLine: end}
	ev := llmEvidence{
		File:      file,
		StartLine: start,
		EndLine:   end,
		Snippet:   strings.TrimSpace(b.TriggerSource),
		Source:    "deterministic_framework",
	}
	tags := []string{"deterministic", "framework:" + strings.TrimSpace(b.Framework)}
	handler := strings.TrimSpace(b.Symbol)
	trigger := strings.TrimSpace(b.Trigger)

	e := llmEntity{
		Type:       obj.Type,
		Confidence: 1.0,
		Tags:       tags,
		Details: map[string]any{
			"framework": b.Framework,
			"handler":   handler,
			"direction": b.Direction,
			"reason":    b.ConfidenceReason,
		},
		Locations: []llmLocation{loc},
		Evidence:  []llmEvidence{ev},
	}

	switch obj.Type {
	case "http_route":
		method, path := parseHTTPTrigger(trigger)
		if method == "" || path == "" {
			return llmEntity{}, false
		}
		e.Name = strings.TrimSpace(method + " " + path)
		e.Summary = fmt.Sprintf("%s HTTP route detected from framework binding", displayFramework(b.Framework))
		e.Details["method"] = method
		e.Details["path"] = path
	case "outbound_http":
		method, path := parseHTTPTrigger(trigger)
		if method == "" || path == "" {
			return llmEntity{}, false
		}
		e.Name = strings.TrimSpace(method + " " + path)
		e.Summary = fmt.Sprintf("%s outbound HTTP client detected from framework binding", displayFramework(b.Framework))
		e.Details["method"] = method
		e.Details["path"] = path
	case "queue_consumer":
		platform, queue := parseQueueTrigger(trigger)
		// Resolve ${...} property placeholders to the real queue name using the
		// already-parsed config index, so the entity is named after the queue
		// (e.g. catalogue-target-response-sqs) rather than the raw placeholder.
		queue = ResolveResourceName(idx, queue)
		if queue == "" {
			return llmEntity{}, false
		}
		e.Name = queue
		e.Summary = fmt.Sprintf("%s queue consumer detected from framework binding", displayFramework(b.Framework))
		e.Details["platform"] = platform
		e.Details["queue"] = queue
	case "scheduled_job":
		schedule := parseScheduleTrigger(trigger)
		if schedule == "" {
			return llmEntity{}, false
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
			return llmEntity{}, false
		}
		e.Name = op + " " + cache
		e.Summary = fmt.Sprintf("%s cache operation detected from framework binding", displayFramework(b.Framework))
		e.Details["operation"] = op
		e.Details["cache"] = cache
	default:
		return llmEntity{}, false
	}
	return e, true
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
	if _, ok := httpMethods[method]; !ok && method != "ANY" {
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

func mergeDiscoveryResults(baseline, deterministic []discoveryResult) []discoveryResult {
	out := make([]discoveryResult, 0, len(baseline)+len(deterministic))
	index := map[string]int{}
	for _, r := range baseline {
		out = append(out, discoveryResult{Objective: r.Objective, Items: append([]llmEntity(nil), r.Items...), Err: r.Err, PeerCancelled: r.PeerCancelled})
		index[r.Objective.ID] = len(out) - 1
	}
	for _, d := range deterministic {
		pos, ok := index[d.Objective.ID]
		if !ok {
			out = append(out, discoveryResult{Objective: d.Objective, Items: append([]llmEntity(nil), d.Items...)})
			index[d.Objective.ID] = len(out) - 1
			continue
		}
		out[pos].Items = mergeEntitiesForObjective(d.Objective, out[pos].Items, d.Items)
	}
	return out
}

func deterministicByObjective(results []discoveryResult) map[string][]llmEntity {
	out := map[string][]llmEntity{}
	for _, r := range results {
		if len(r.Items) == 0 {
			continue
		}
		out[r.Objective.ID] = append(out[r.Objective.ID], r.Items...)
	}
	return out
}

type deterministicFrameworkReport struct {
	Accepted      []astpkg.FrameworkBinding `json:"accepted"`
	Rejected      []astpkg.FrameworkBinding `json:"rejected"`
	RouteManifest []routeManifestEntry      `json:"route_manifest"`
}

type routeManifestEntry struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler"`
	File    string `json:"file"`
	Line    int    `json:"line"`
}

func routeHandlerManifest(bindings []astpkg.FrameworkBinding) []routeManifestEntry {
	out := make([]routeManifestEntry, 0)
	for _, b := range bindings {
		if strings.TrimSpace(b.Kind) != "http_handler" {
			continue
		}
		method, path := parseHTTPTrigger(b.Trigger)
		if method == "" || path == "" {
			continue
		}
		out = append(out, routeManifestEntry{
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

func mergeEntitiesForObjective(obj objectives.Objective, baseline, deterministic []llmEntity) []llmEntity {
	out := make([]llmEntity, 0, len(baseline)+len(deterministic))
	index := map[string]int{}
	for _, e := range baseline {
		k := discoverySemanticKey(obj, e)
		index[k] = len(out)
		out = append(out, e)
	}
	for _, e := range deterministic {
		k := discoverySemanticKey(obj, e)
		if pos, ok := index[k]; ok {
			out[pos] = mergeDeterministicDuplicate(out[pos], e)
			continue
		}
		index[k] = len(out)
		out = append(out, e)
	}
	SortLLMEntities(out)
	return out
}

func mergeDeterministicDuplicate(llm, det llmEntity) llmEntity {
	out := det
	if strings.TrimSpace(out.Summary) == "" {
		out.Summary = llm.Summary
	}
	if len(out.Actions) == 0 {
		out.Actions = llm.Actions
	}
	if len(out.Inputs) == 0 {
		out.Inputs = llm.Inputs
	}
	out.Tags = DedupeStrings(append(append([]string(nil), det.Tags...), llm.Tags...))
	out.Locations = unionLocations(det.Locations, llm.Locations)
	out.Evidence = append(append([]llmEvidence(nil), det.Evidence...), llm.Evidence...)
	if out.Details == nil {
		out.Details = map[string]any{}
	}
	for k, v := range llm.Details {
		if _, ok := out.Details[k]; !ok {
			out.Details[k] = v
		}
	}
	return out
}
