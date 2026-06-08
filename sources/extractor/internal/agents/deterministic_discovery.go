package agents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func (o *orchestrator) runDeterministicDiscovery(ctx context.Context, objs []objectives.Objective, mode string) []discoveryResult {
	started := time.Now()
	o.emit(events.Event{
		Kind:   events.KindStageStarted,
		Stage:  "deterministic_discovery",
		Status: events.StatusRunning,
		Payload: map[string]any{
			"mode": mode,
		},
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
			Payload: map[string]any{"mode": mode},
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
		e, ok := entityFromFrameworkBinding(obj, b)
		if !ok {
			continue
		}
		outMap[obj.ID] = append(outMap[obj.ID], e)
	}

	results := make([]discoveryResult, 0, len(outMap))
	total := 0
	for _, obj := range objs {
		items := outMap[obj.ID]
		if len(items) == 0 {
			continue
		}
		o.pathMapper().applyToEntities(items)
		sortLLMEntities(items)
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
		"mode":        mode,
		"items":       total,
		"objectives":  len(results),
		"duration_ms": time.Since(started).Milliseconds(),
	})
	util.Info("agents.deterministic_discovery", "deterministic discovery completed", map[string]any{
		"mode": mode, "items": total, "objectives": len(results),
	})
	return results
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
			case "outbound_http":
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

func entityFromFrameworkBinding(obj objectives.Objective, b astpkg.FrameworkBinding) (llmEntity, bool) {
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
	default:
		return llmEntity{}, false
	}
	return e, true
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
	sortLLMEntities(out)
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
	out.Tags = dedupeStrings(append(append([]string(nil), det.Tags...), llm.Tags...))
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

func discoverySemanticKey(obj objectives.Objective, e llmEntity) string {
	deriveDetailsFromName(obj.Type, &e)
	get := func(key string) string {
		if e.Details == nil {
			return ""
		}
		if v, ok := e.Details[key]; ok {
			return strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
		}
		return ""
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := get(k); v != "" {
				return v
			}
		}
		return ""
	}
	switch obj.Type {
	case "http_route", "webhook", "outbound_http":
		method, path := get("method"), normalizePathForKey(get("path"))
		if method != "" || path != "" {
			return strings.Join([]string{obj.ID, method, path}, "|")
		}
	case "queue_consumer", "queue_publish", "stream_consume":
		platform := get("platform")
		dest := first("queue", "topic", "destination", "stream")
		if platform != "" || dest != "" {
			return strings.Join([]string{obj.ID, platform, dest}, "|")
		}
	case "scheduled_job":
		schedule, handler := get("schedule"), get("handler")
		if schedule != "" || handler != "" {
			return strings.Join([]string{obj.ID, schedule, handler}, "|")
		}
	case "db_operation", "cache_operation":
		// High-level identity: a dependency is "<operation> on <resource>"
		// (e.g. read from orders), NOT one row per repository method. Keying
		// on (resource, operation) collapses the LLM's per-method jitter into
		// the architectural fact the extractor is after, which is also what
		// stabilises the run-to-run count.
		resource := first("table", "entity", "cache", "key", "collection", "index")
		op := get("operation")
		if resource != "" || op != "" {
			return strings.Join([]string{obj.ID, resource, op}, "|")
		}
	case "rpc_endpoint", "outbound_rpc":
		svc, meth := get("service"), get("method")
		if svc != "" || meth != "" {
			return strings.Join([]string{obj.ID, svc, meth}, "|")
		}
	case "cli_command", "command_exec":
		cmd := first("command", "invocation")
		handler := get("handler")
		if cmd != "" || handler != "" {
			return strings.Join([]string{obj.ID, cmd, handler}, "|")
		}
	}
	return shardEntityKey(e)
}

func normalizePathForKey(path string) string {
	path = strings.TrimSpace(strings.ToLower(path))
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	return path
}

func buildDiscoveryEvaluation(mode string, baseline, deterministic, candidate []discoveryResult) *discoveryEvaluationReport {
	report := &discoveryEvaluationReport{
		Mode: mode,
		Baseline: discoveryEvaluationSide{
			ItemsByObjective: countItemsByObjective(baseline),
			LLMItems:         countDiscoveryItems(baseline),
		},
		Candidate: discoveryEvaluationSide{
			ItemsByObjective:   countItemsByObjective(candidate),
			DeterministicItems: countDiscoveryItems(deterministic),
			LLMItems:           countDiscoveryItems(baseline),
		},
		Comparison: discoveryEvaluationCompare{
			MatchedByObjective:       map[string]int{},
			BaselineOnlyByObjective:  map[string]int{},
			CandidateOnlyByObjective: map[string]int{},
		},
	}
	report.Baseline.Items = countDiscoveryItems(baseline)
	report.Candidate.Items = countDiscoveryItems(candidate)
	objIDs := map[string]struct{}{}
	for _, batch := range [][]discoveryResult{baseline, deterministic, candidate} {
		for _, r := range batch {
			objIDs[r.Objective.ID] = struct{}{}
		}
	}
	for id := range objIDs {
		report.Objectives = append(report.Objectives, id)
	}
	sort.Strings(report.Objectives)

	baselineKeys := keysByObjective(baseline)
	candidateKeys := keysByObjective(candidate)
	for objID, bkeys := range baselineKeys {
		ckeys := candidateKeys[objID]
		for k := range bkeys {
			if _, ok := ckeys[k]; ok {
				report.Comparison.Matched++
				report.Comparison.MatchedByObjective[objID]++
			} else {
				report.Comparison.BaselineOnly++
				report.Comparison.BaselineOnlyByObjective[objID]++
			}
		}
	}
	for objID, ckeys := range candidateKeys {
		bkeys := baselineKeys[objID]
		for k := range ckeys {
			if _, ok := bkeys[k]; !ok {
				report.Comparison.CandidateOnly++
				report.Comparison.CandidateOnlyByObjective[objID]++
			}
		}
	}
	report.Comparison.DuplicatesMerged = countDiscoveryItems(baseline) + countDiscoveryItems(deterministic) - countDiscoveryItems(candidate)
	if report.Comparison.DuplicatesMerged < 0 {
		report.Comparison.DuplicatesMerged = 0
	}
	return report
}

func countItemsByObjective(in []discoveryResult) map[string]int {
	out := map[string]int{}
	for _, r := range in {
		out[r.Objective.ID] += len(r.Items)
	}
	return out
}

func countDiscoveryItems(in []discoveryResult) int {
	total := 0
	for _, r := range in {
		total += len(r.Items)
	}
	return total
}

func keysByObjective(in []discoveryResult) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	for _, r := range in {
		if out[r.Objective.ID] == nil {
			out[r.Objective.ID] = map[string]struct{}{}
		}
		for _, e := range r.Items {
			out[r.Objective.ID][discoverySemanticKey(r.Objective, e)] = struct{}{}
		}
	}
	return out
}

func deterministicModeEnabled(mode string) bool {
	return mode != string(config.DeterministicDiscoveryOff)
}

func deterministicModePromotes(mode string) bool {
	return mode == string(config.DeterministicDiscoveryActive)
}

func isCompleteDeterministicSeed(obj objectives.Objective, e *llmEntity) bool {
	if e == nil {
		return false
	}
	if obj.Kind != model.KindExposure {
		return false
	}
	switch obj.Type {
	case "http_route", "queue_consumer", "scheduled_job":
	default:
		return false
	}
	if !hasDeterministicEvidence(*e) {
		return false
	}
	if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Type) == "" {
		return false
	}
	if _, ok := canonicalObjectiveType(obj, e.Type); !ok {
		return false
	}
	if len(e.Locations) == 0 || strings.TrimSpace(e.Locations[0].File) == "" || e.Locations[0].StartLine <= 0 {
		return false
	}
	if e.Confidence < 1.0 {
		return false
	}
	deriveDetailsFromName(obj.Type, e)
	return missingRequiredDetails(obj.Type, e.Details) == ""
}

func hasDeterministicEvidence(e llmEntity) bool {
	for _, ev := range e.Evidence {
		if strings.HasPrefix(strings.TrimSpace(ev.Source), "deterministic_") {
			return true
		}
	}
	for _, tag := range e.Tags {
		if strings.TrimSpace(tag) == "deterministic" {
			return true
		}
	}
	return false
}

func (o *orchestrator) detailCheckpointForSeed(j detailJob) (detailCheckpointEntry, bool) {
	base, ur := toBase(o.repoPath, j.Objective, j.Seed, o.cfg.Quality.MinConfidence)
	if ur != nil {
		return detailCheckpointEntry{}, false
	}
	entry := detailCheckpointEntry{
		Key:         detailEntityKey(j.Objective.ID, j.Seed.Name),
		ObjectiveID: j.Objective.ID,
		SeedName:    j.Seed.Name,
	}
	if j.Objective.Kind == model.KindExposure {
		exp := model.Exposure{BaseEntity: base}
		entry.Exposure = &exp
	} else {
		dep := model.Dependency{BaseEntity: base}
		entry.Dependency = &dep
	}
	return entry, true
}

func (o *orchestrator) recordDeterministicDetailSkip(n int) {
	if n <= 0 || o.deterministicEvaluation == nil {
		return
	}
	o.deterministicEvaluation.Skipped.DetailDeterministicComplete += n
	o.persistStageState("discovery_evaluation.json", o.deterministicEvaluation)
}
