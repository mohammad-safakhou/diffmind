package agents

import (
	"context"
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
//   - type-specific required detail fields missing (e.g. http_route without method/path)
func shouldReexamine(obj objectives.Objective, e llmEntity, minConfidence float64) (string, string, bool) {
	if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Type) == "" {
		return "missing_name_or_type", "Candidate is missing name or type fields.", true
	}
	if len(e.Locations) == 0 {
		return "no_source_location", "Candidate has no source_locations entry; confirm the file and line range.", true
	}
	if e.Confidence < minConfidence {
		return "low_confidence", "Candidate confidence is below the run threshold. Re-verify or reject.", true
	}
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
// LLM re-ask for each suspect seed, and returns (cleanSeeds, unresolved).
// Clean seeds from discovery pass through unchanged; confirmed seeds from
// re-ask replace the suspect originals; rejected seeds become Unresolved.
func (o *orchestrator) runReexamination(
	ctx context.Context,
	seeds []detailJob,
	rf *repoFacts,
	onResult func(),
) ([]detailJob, []model.UnresolvedItem) {
	if len(seeds) == 0 {
		return nil, nil
	}

	cleanJobs := make([]detailJob, 0, len(seeds))
	suspect := make([]reexamineTrigger, 0)
	for _, s := range seeds {
		reasonID, reason, needs := shouldReexamine(s.Objective, s.Seed, o.cfg.Quality.MinConfidence)
		if !needs {
			cleanJobs = append(cleanJobs, s)
			continue
		}
		suspect = append(suspect, reexamineTrigger{
			Seed:     s.Seed,
			Obj:      s.Objective,
			Reason:   reason,
			ReasonID: reasonID,
		})
	}

	if len(suspect) == 0 {
		util.Info("agents.reexamine", "no suspects to re-examine", map[string]any{"total": len(seeds)})
		return cleanJobs, nil
	}

	util.Info("agents.reexamine", "re-examination starting", map[string]any{
		"total": len(seeds), "clean": len(cleanJobs), "suspect": len(suspect),
	})

	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(suspect) {
		workers = len(suspect)
	}

	type resultEnv struct {
		Trigger reexamineTrigger
		Item    *llmEntity
		Err     error
	}
	jobs := make(chan reexamineTrigger)
	results := make(chan resultEnv)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for t := range jobs {
				item, err := o.runReexamineOne(ctx, t, rf)
				results <- resultEnv{Trigger: t, Item: item, Err: err}
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

	unresolved := make([]model.UnresolvedItem, 0)
	for r := range results {
		if onResult != nil {
			onResult()
		}
		if r.Err != nil {
			unresolved = append(unresolved, model.UnresolvedItem{
				Kind:       r.Trigger.Obj.Kind,
				Type:       r.Trigger.Obj.Type,
				Name:       r.Trigger.Seed.Name,
				ReasonCode: "reexamine_failure",
				Reason:     r.Err.Error(),
				Confidence: r.Trigger.Seed.Confidence,
			})
			continue
		}
		if r.Item == nil {
			// LLM confirmed the candidate is not real.
			unresolved = append(unresolved, model.UnresolvedItem{
				Kind:       r.Trigger.Obj.Kind,
				Type:       r.Trigger.Obj.Type,
				Name:       r.Trigger.Seed.Name,
				ReasonCode: "rejected_on_reexamination",
				Reason:     "Re-examination agent rejected candidate (trigger: " + r.Trigger.ReasonID + ")",
				Confidence: r.Trigger.Seed.Confidence,
				Evidence:   toEvidence(r.Trigger.Seed.Evidence),
			})
			continue
		}
		// Higher of the two confidences wins; keep the corrected item.
		if r.Item.Confidence < r.Trigger.Seed.Confidence {
			r.Item.Confidence = r.Trigger.Seed.Confidence
		}
		cleanJobs = append(cleanJobs, detailJob{Objective: r.Trigger.Obj, Seed: *r.Item})
	}

	util.Info("agents.reexamine", "re-examination completed", map[string]any{
		"clean_after": len(cleanJobs), "unresolved": len(unresolved),
	})
	return cleanJobs, unresolved
}

func (o *orchestrator) runReexamineOne(ctx context.Context, t reexamineTrigger, rf *repoFacts) (*llmEntity, error) {
	prompt := buildReexaminePrompt(t.Obj, t.Seed, t.ReasonID+": "+t.Reason, rf, o.subDir)
	schema := entityListSchema()
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
