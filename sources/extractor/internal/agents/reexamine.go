package agents

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// reexamineTrigger describes why a seed was flagged for re-examination.
type reexamineTrigger struct {
	Seed     llmEntity
	Obj      objectives.Objective
	Reason   string
	ReasonID string
}

// shouldReexamine decides whether a seed needs Stage 2 re-examination.
// Rules (all LLM-only, no filesystem checks):
//   - confidence below minConfidence
//   - missing name or type
//   - no source_locations entries
//   - type-specific required detail fields missing (e.g. http_route
//     without method/path) AND those fields cannot be derived from the
//     entity's name. We back-fill derived fields into the entity in
//     place so the rest of the pipeline sees structured details.
//
// IMPORTANT: this function MUTATES e.Details when it can derive missing
// fields from the name (e.g. parsing "GET /accounts/{id}" into
// {method, path}). The mutation is the cheap fix for the previous
// reexamination flood: most LLM responses already encode the required
// fields in the name, just not in the details object.
func shouldReexamine(obj objectives.Objective, e *llmEntity, minConfidence float64) (string, string, bool) {
	if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Type) == "" {
		return "missing_name_or_type", "Candidate is missing name or type fields.", true
	}
	if _, ok := canonicalObjectiveType(obj, e.Type); !ok {
		return "wrong_objective_type", "Candidate type does not match the objective type.", true
	}
	if len(e.Locations) == 0 {
		return "no_source_location", "Candidate has no source_locations entry; confirm the file and line range.", true
	}
	if e.Confidence < minConfidence {
		return "low_confidence", "Candidate confidence is below the run threshold. Re-verify or reject.", true
	}
	deriveDetailsFromName(obj.Type, e)
	if missing := missingRequiredDetails(obj.Type, e.Details); missing != "" {
		return "missing_required_details", "Candidate is missing required detail fields: " + missing, true
	}
	return "", "", false
}

// missingRequiredDetails returns a comma separated list of missing field keys
// for the given objective type. Empty result means all required fields are
// present.
func missingRequiredDetails(objType string, details map[string]any) string {
	required := map[string][]string{
		"http_route":      {"method", "path"},
		"webhook":         {"path"},
		"rpc_endpoint":    {"service", "method"},
		"queue_consumer":  {"queue"},
		"scheduled_job":   {"schedule"},
		"cli_command":     {"command"},
		"db_operation":    {"operation"},
		"outbound_http":   {"method"},
		"outbound_rpc":    {"method"},
		"queue_publish":   {"destination"},
		"cache_operation": {"operation"},
		"stream_consume":  {"stream"},
		"command_exec":    {"command"},
	}
	keys, ok := required[objType]
	if !ok {
		return ""
	}
	missing := []string{}
	for _, k := range keys {
		if !hasDetailKey(details, k) {
			missing = append(missing, k)
		}
	}
	return strings.Join(missing, ",")
}

// httpMethods enumerates the HTTP verbs we recognize when parsing
// route names like "GET /users/{id}".
var httpMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {},
	"HEAD": {}, "OPTIONS": {}, "TRACE": {}, "CONNECT": {},
}

// deriveDetailsFromName fills in entity.Details from name/summary when
// the detail field is implicit. This is purely a pre-processing step:
// the LLM frequently emits "GET /users/{id}" as the name and leaves
// details empty, so without derivation we'd send 100% of items into
// reexamination unnecessarily.
//
// Mutations are conservative: we only fill fields that are clearly
// implied by the name's syntax for that objective type.
func deriveDetailsFromName(objType string, e *llmEntity) {
	if e == nil {
		return
	}
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	name := strings.TrimSpace(e.Name)
	switch objType {
	case "http_route", "webhook", "outbound_http":
		// Pattern: "<METHOD> <path>" or "<METHOD>:<path>"
		method, path, ok := splitMethodPath(name)
		if ok {
			if !hasDetailKey(e.Details, "method") {
				e.Details["method"] = method
			}
			if !hasDetailKey(e.Details, "path") {
				e.Details["path"] = path
			}
		}
	case "rpc_endpoint", "outbound_rpc":
		// Pattern: "Service.Method" or "Service/Method" or "Service#Method"
		svc, meth, ok := splitServiceMethod(name)
		if ok {
			if !hasDetailKey(e.Details, "service") {
				e.Details["service"] = svc
			}
			if !hasDetailKey(e.Details, "method") {
				e.Details["method"] = meth
			}
		}
	case "queue_consumer", "queue_publish", "stream_consume":
		// Pattern: name often IS the queue/topic/stream identifier
		// (e.g. "catalogue-target-request-sqs"). Only fall back to it
		// when the name looks like a queue identifier, not free prose.
		if looksLikeIdentifier(name) {
			required := "queue"
			if objType == "queue_publish" {
				required = "destination"
			}
			if objType == "stream_consume" {
				required = "stream"
			}
			if !hasDetailKey(e.Details, required) {
				e.Details[required] = name
			}
		}
	case "db_operation", "cache_operation":
		// Many model responses encode the operation in the name
		// (e.g. "users_table_select", "orders.upsert", "redis SET key").
		op := guessOperation(name, e.Summary)
		if op != "" && !hasDetailKey(e.Details, "operation") {
			e.Details["operation"] = op
		}
	case "scheduled_job":
		if !hasDetailKey(e.Details, "schedule") {
			// If the summary mentions a cron-ish token, use that;
			// otherwise leave it empty (requires the model).
			if cron := extractCronLike(e.Summary); cron != "" {
				e.Details["schedule"] = cron
			}
		}
	case "cli_command", "command_exec":
		if !hasDetailKey(e.Details, "command") {
			if looksLikeCommand(name) {
				e.Details["command"] = name
			}
		}
	}
}

// splitMethodPath parses strings like "GET /v1/users" or "POST: /charge"
// into (method, path). Returns ok=false when the prefix isn't a
// recognised HTTP verb.
func splitMethodPath(name string) (method, path string, ok bool) {
	s := strings.TrimSpace(name)
	if s == "" {
		return "", "", false
	}
	// Allow "GET /x", "GET:/x", "GET /x  ", "GET /x (handler)", etc.
	for i, r := range s {
		if r == ' ' || r == ':' || r == '\t' {
			head := strings.ToUpper(s[:i])
			if _, isVerb := httpMethods[head]; isVerb {
				rest := strings.TrimLeft(s[i:], " :\t")
				// If the path itself has trailing prose ("/x (handler)"),
				// keep just the leading slash-path token.
				p := rest
				if idx := strings.IndexAny(rest, " \t("); idx >= 0 {
					p = rest[:idx]
				}
				if strings.HasPrefix(p, "/") {
					return head, p, true
				}
			}
			return "", "", false
		}
	}
	return "", "", false
}

// splitServiceMethod parses strings like "Foo.bar", "foo/bar",
// "FooService#bar", or "fooBar.baz" into (service, method).
func splitServiceMethod(name string) (svc, meth string, ok bool) {
	for _, sep := range []string{"#", ".", "/"} {
		if i := strings.LastIndex(name, sep); i > 0 && i < len(name)-1 {
			s := strings.TrimSpace(name[:i])
			m := strings.TrimSpace(name[i+1:])
			if s != "" && m != "" {
				return s, m, true
			}
		}
	}
	return "", "", false
}

// looksLikeIdentifier returns true for short names without spaces — the
// kind of thing that IS a queue/topic/stream id rather than free prose.
func looksLikeIdentifier(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 96 {
		return false
	}
	if strings.ContainsAny(s, " \t\n,;()") {
		return false
	}
	return true
}

// looksLikeCommand returns true for tokens that look like CLI commands
// or shell pipelines.
func looksLikeCommand(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	first := strings.IndexAny(s, " ")
	head := s
	if first > 0 {
		head = s[:first]
	}
	// Must look like an executable name: alnum, dashes, underscores,
	// dots, slashes — no parentheses or punctuation.
	for _, r := range head {
		if !(r == '-' || r == '_' || r == '.' || r == '/' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// guessOperation extracts a CRUD-like operation keyword from name/summary.
// Returns "" when nothing obvious is present.
func guessOperation(name, summary string) string {
	candidates := strings.ToLower(name + " " + summary)
	for _, op := range []string{
		"select", "insert", "update", "delete", "upsert",
		"read", "write", "scan", "query", "fetch", "get", "put",
		"connect", "create", "find",
	} {
		if strings.Contains(candidates, op) {
			return op
		}
	}
	return ""
}

// extractCronLike picks out a cron-style schedule expression from text
// (very loose match: 5–7 whitespace-separated tokens of the form
// digits/asterisks/commas/slashes/dashes).
func extractCronLike(s string) string {
	if s == "" {
		return ""
	}
	fields := strings.Fields(s)
	for i := 0; i+5 <= len(fields); i++ {
		end := i + 5
		if end < len(fields) && cronTokenLike(fields[end]) {
			end++
		}
		if end < len(fields) && cronTokenLike(fields[end]) {
			end++
		}
		ok := true
		for j := i; j < end; j++ {
			if !cronTokenLike(fields[j]) {
				ok = false
				break
			}
		}
		if ok && end-i >= 5 {
			return strings.Join(fields[i:end], " ")
		}
	}
	return ""
}

func cronTokenLike(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		switch {
		case r == '*' || r == ',' || r == '/' || r == '-' || r == '?':
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// hasDetailKey performs a tolerant existence check. We accept several common
// spellings so prompts that return e.g. "queue_name" instead of "queue" are
// not punished.
func hasDetailKey(details map[string]any, key string) bool {
	if details == nil {
		return false
	}
	aliases := map[string][]string{
		"method":      {"method", "http_method", "verb"},
		"path":        {"path", "route", "url_path", "endpoint_path"},
		"service":     {"service", "service_name", "rpc_service"},
		"queue":       {"queue", "queue_name", "topic", "stream"},
		"schedule":    {"schedule", "cron", "interval", "fixed_rate", "fixed_delay"},
		"command":     {"command", "command_name", "bin", "handler"},
		"operation":   {"operation", "op", "action", "verb"},
		"destination": {"destination", "queue", "topic", "queue_name", "topic_name", "arn"},
		"stream":      {"stream", "stream_name", "arn"},
	}
	candidates := aliases[key]
	if len(candidates) == 0 {
		candidates = []string{key}
	}
	for _, c := range candidates {
		if v, ok := details[c]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
				continue
			}
			return true
		}
	}
	return false
}

// runReexamination executes Stage 2 in parallel. It takes the raw discovery
// seeds, splits them into "clean" and "needs re-ask" groups, fires a targeted
// LLM re-ask for each suspect seed. Clean seeds from discovery pass
// through unchanged; confirmed seeds from re-ask replace the suspect
// originals; rejected seeds become Unresolved.
//
// Returns (cleanSeeds, unresolved, firstErr, firstFailedTrigger). On
// failure the orchestrator halts the run; the trigger gives it the
// objective + seed name needed for the failure report.
func (o *orchestrator) runReexamination(
	ctx context.Context,
	seeds []detailJob,
	rf *repoFacts,
	onResult func(),
) ([]detailJob, []model.UnresolvedItem, error, reexamineTrigger) {
	var noTrigger reexamineTrigger
	if len(seeds) == 0 {
		return nil, nil, nil, noTrigger
	}

	// Load per-item checkpoint so a retry can skip already-completed suspects.
	checkpoint := o.loadReexaminationCheckpoint(filepath.Join(o.runDir, stateDir))

	cleanJobs := make([]detailJob, 0, len(seeds))
	suspect := make([]reexamineTrigger, 0)
	unresolved := make([]model.UnresolvedItem, 0)

	for i := range seeds {
		// shouldReexamine MUTATES seeds[i].Seed.Details when it can
		// derive missing fields from the name/summary. Take a pointer so
		// the cleanJobs slice carries the enriched entity forward.
		reasonID, reason, needs := shouldReexamine(seeds[i].Objective, &seeds[i].Seed, o.cfg.Quality.MinConfidence)
		if !needs {
			cleanJobs = append(cleanJobs, seeds[i])
			continue
		}

		key := reexamKey(seeds[i].Objective.ID, seeds[i].Seed.Name)
		if entry, done := checkpoint[key]; done {
			// This suspect was already resolved in a prior run.
			// Restore the outcome without re-asking the model.
			switch entry.Outcome {
			case "confirmed":
				if entry.Seed != nil {
					cleanJobs = append(cleanJobs, detailJob{Objective: seeds[i].Objective, Seed: *entry.Seed})
				}
			case "rejected":
				if entry.Unresolved != nil {
					unresolved = append(unresolved, *entry.Unresolved)
				}
			}
			if onResult != nil {
				onResult()
			}
			continue
		}

		suspect = append(suspect, reexamineTrigger{
			Seed:     seeds[i].Seed,
			Obj:      seeds[i].Objective,
			Reason:   reason,
			ReasonID: reasonID,
		})
	}

	if len(suspect) == 0 {
		skipped := len(checkpoint)
		util.Info("agents.reexamine", "no suspects to re-examine", map[string]any{
			"total": len(seeds), "skipped_from_checkpoint": skipped,
		})
		return cleanJobs, unresolved, nil, noTrigger
	}

	util.Info("agents.reexamine", "re-examination starting", map[string]any{
		"total": len(seeds), "clean": len(cleanJobs), "suspect": len(suspect),
		"skipped_from_checkpoint": len(checkpoint),
	})

	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(suspect) {
		workers = len(suspect)
	}

	type resultEnv struct {
		Trigger       reexamineTrigger
		Item          *llmEntity
		Err           error
		PeerCancelled bool
	}
	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan reexamineTrigger)
	results := make(chan resultEnv)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for t := range jobs {
				if stageCtx.Err() != nil {
					// Peer-cancelled: another worker tripped
					// fail-fast before this job ran.
					results <- resultEnv{Trigger: t, Err: stageCtx.Err(), PeerCancelled: true}
					continue
				}
				item, err := o.runReexamineOne(stageCtx, t, rf)
				results <- resultEnv{Trigger: t, Item: item, Err: err}
				if err != nil {
					cancel()
				}
			}
		}(i + 1)
	}
	go func() {
		for _, t := range suspect {
			jobs <- t
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var firstErr error
	var firstTrigger reexamineTrigger
	for r := range results {
		if onResult != nil {
			onResult()
		}
		if r.Err != nil {
			u := model.UnresolvedItem{
				Kind:       r.Trigger.Obj.Kind,
				Type:       r.Trigger.Obj.Type,
				Name:       r.Trigger.Seed.Name,
				ReasonCode: "reexamine_failure",
				Reason:     r.Err.Error(),
				Confidence: r.Trigger.Seed.Confidence,
			}
			unresolved = append(unresolved, u)
			// Capture this as the root cause unless it's a
			// peer-cancelled sibling (which the worker flagged
			// explicitly). A per-call HTTP timeout wraps
			// context.DeadlineExceeded but happens while the parent
			// is still alive — those MUST surface, not get silently
			// filtered out.
			if firstErr == nil && !r.PeerCancelled {
				firstErr = r.Err
				firstTrigger = r.Trigger
			}
			// Do NOT checkpoint a hard error — the item must be
			// retried. PeerCancelled siblings also skip the
			// checkpoint because they were never actually attempted.
			continue
		}
		if r.Item == nil {
			// LLM confirmed the candidate is not real.
			u := model.UnresolvedItem{
				Kind:       r.Trigger.Obj.Kind,
				Type:       r.Trigger.Obj.Type,
				Name:       r.Trigger.Seed.Name,
				ReasonCode: "rejected_on_reexamination",
				Reason:     "Re-examination agent rejected candidate (trigger: " + r.Trigger.ReasonID + ")",
				Confidence: r.Trigger.Seed.Confidence,
				Evidence:   toEvidence(r.Trigger.Seed.Evidence),
			}
			unresolved = append(unresolved, u)
			o.appendReexamEntity(reexamCheckpointEntry{
				Key:        reexamKey(r.Trigger.Obj.ID, r.Trigger.Seed.Name),
				Outcome:    "rejected",
				Unresolved: &u,
			})
			continue
		}
		// Higher of the two confidences wins; keep the corrected item.
		if r.Item.Confidence < r.Trigger.Seed.Confidence {
			r.Item.Confidence = r.Trigger.Seed.Confidence
		}
		cleanJobs = append(cleanJobs, detailJob{Objective: r.Trigger.Obj, Seed: *r.Item})
		o.appendReexamEntity(reexamCheckpointEntry{
			Key:     reexamKey(r.Trigger.Obj.ID, r.Trigger.Seed.Name),
			Outcome: "confirmed",
			Seed:    r.Item,
		})
	}

	util.Info("agents.reexamine", "re-examination completed", map[string]any{
		"clean_after": len(cleanJobs), "unresolved": len(unresolved),
	})
	return cleanJobs, unresolved, firstErr, firstTrigger
}

func (o *orchestrator) runReexamineOne(ctx context.Context, t reexamineTrigger, rf *repoFacts) (*llmEntity, error) {
	prompt := buildReexaminePrompt(t.Obj, t.Seed, t.ReasonID+": "+t.Reason, rf, o.subDir)
	schema := entityListSchemaForObjective(t.Obj)
	jobID := "reexamine." + t.Obj.ID + "." + safeJobID(t.Seed.Name)
	started := time.Now()
	o.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "reexamination", JobID: jobID, Status: events.StatusRunning,
		Payload: map[string]any{"objective_id": t.Obj.ID, "name": t.Seed.Name, "trigger": t.ReasonID},
	})
	payload, err := o.promptAgent(ctx, jobID, prompt, schema)
	if err != nil {
		o.emit(events.Event{
			Kind: events.KindJobFailed, Stage: "reexamination", JobID: jobID, Status: events.StatusFailed,
			Message: err.Error(),
			Payload: map[string]any{"objective_id": t.Obj.ID, "name": t.Seed.Name, "duration_ms": time.Since(started).Milliseconds()},
		})
		return nil, err
	}
	items := parseEntities(payload["items"])
	kept := items[:0]
	for i := range items {
		if forceObjectiveType(t.Obj, &items[i]) {
			kept = append(kept, items[i])
		}
	}
	items = kept
	o.pathMapper().applyToEntities(items)
	resolution := "rejected"
	if len(items) > 0 {
		resolution = "rescued"
	}
	o.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "reexamination", JobID: jobID, Status: events.StatusSuccess,
		Payload: map[string]any{
			"objective_id": t.Obj.ID, "name": t.Seed.Name, "resolution": resolution, "duration_ms": time.Since(started).Milliseconds(),
		},
	})
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}
