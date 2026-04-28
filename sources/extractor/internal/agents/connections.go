package agents

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// runConnectionsBatch executes Stage 4. For each exposure we issue one LLM
// call per batch of dependencies (batch size = cfg.Runtime.MaxCatalogItems).
// Exposures are processed in parallel, batches for a given exposure are
// processed serially so we can cheaply enforce the closed-set constraint on
// to_dependency_id.
func (o *orchestrator) runConnectionsBatch(
	ctx context.Context,
	exposures []model.Exposure,
	dependencies []model.Dependency,
	exposureObjectives map[string]objectives.Objective,
	rf *repoFacts,
	onResult func(),
) ([]model.Connection, []model.UnresolvedItem) {
	if len(exposures) == 0 || len(dependencies) == 0 {
		return nil, nil
	}

	catalog := buildDependencyCatalog(dependencies)
	depByID := map[string]model.Dependency{}
	for _, d := range dependencies {
		depByID[d.ID] = d
	}
	expByID := map[string]model.Exposure{}
	for _, e := range exposures {
		expByID[e.ID] = e
	}

	workers := o.cfg.Runtime.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > len(exposures) {
		workers = len(exposures)
	}

	jobCh := make(chan model.Exposure)
	resCh := make(chan connectionResult)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for exp := range jobCh {
				util.Trace("agents.connections", "worker mapping exposure", map[string]any{"worker": workerID, "exposure": exp.ID})
				obj := exposureObjectives[exp.Type]
				items, err := o.runConnectionsForExposure(ctx, exp, obj, catalog, rf)
				resCh <- connectionResult{ExposureID: exp.ID, Items: items, Err: err}
			}
		}(i + 1)
	}
	go func() {
		for _, e := range exposures {
			jobCh <- e
		}
		close(jobCh)
		wg.Wait()
		close(resCh)
	}()

	rawLinks := make([]llmConnection, 0)
	unresolved := make([]model.UnresolvedItem, 0)
	for r := range resCh {
		if onResult != nil {
			onResult()
		}
		if r.Err != nil {
			unresolved = append(unresolved, model.UnresolvedItem{
				Kind: model.KindDependency, Type: "connection", Name: r.ExposureID,
				ReasonCode: "agent_failure", Reason: r.Err.Error(),
			})
			continue
		}
		rawLinks = append(rawLinks, r.Items...)
	}

	conns, convUnresolved := assembleConnections(expByID, depByID, rawLinks, o.cfg.Quality.MinConfidence)
	unresolved = append(unresolved, convUnresolved...)
	util.Info("agents.connections", "connection extraction completed", map[string]any{
		"connections": len(conns), "llm_links": len(rawLinks), "unresolved": len(unresolved),
	})
	return conns, unresolved
}

func (o *orchestrator) runConnectionsForExposure(
	ctx context.Context,
	exposure model.Exposure,
	obj objectives.Objective,
	catalog []connectionCatalogItem,
	rf *repoFacts,
) ([]llmConnection, error) {
	batchSize := o.cfg.Runtime.MaxCatalogItems
	if batchSize <= 0 {
		batchSize = 80
	}
	expItem := exposureToCatalogItem(exposure)
	batchCount := (len(catalog) + batchSize - 1) / batchSize
	if batchCount == 0 {
		batchCount = 1
	}

	exposureJobID := "connections." + exposure.ID
	expStarted := time.Now()
	o.emit(events.Event{
		Kind: events.KindJobStarted, Stage: "connections", JobID: exposureJobID, Status: events.StatusRunning,
		Payload: map[string]any{
			"exposure_id":   exposure.ID,
			"exposure_name": exposure.Name,
			"exposure_type": exposure.Type,
			"batches":       batchCount,
		},
	})

	schema := connectionListSchema()
	out := make([]llmConnection, 0)
	for batchNo, start := 1, 0; start < len(catalog); batchNo, start = batchNo+1, start+batchSize {
		end := start + batchSize
		if end > len(catalog) {
			end = len(catalog)
		}
		batch := catalog[start:end]
		prompt := buildConnectionPrompt(obj, expItem, batch, batchNo, batchCount, rf, o.subDir)
		batchJobID := fmt.Sprintf("%s.batch.%d", exposureJobID, batchNo)
		batchStarted := time.Now()
		o.emit(events.Event{
			Kind: events.KindJobStarted, Stage: "connections", JobID: batchJobID, ParentID: exposureJobID,
			Status:  events.StatusRunning,
			Payload: map[string]any{"batch": batchNo, "batches": batchCount, "batch_size": len(batch)},
		})
		payload, err := o.promptAgent(ctx, batchJobID, prompt, schema)
		if err != nil {
			o.emit(events.Event{
				Kind: events.KindJobFailed, Stage: "connections", JobID: batchJobID, ParentID: exposureJobID,
				Status:  events.StatusFailed,
				Message: err.Error(),
				Payload: map[string]any{"batch": batchNo, "duration_ms": time.Since(batchStarted).Milliseconds()},
			})
			o.emit(events.Event{
				Kind: events.KindJobFailed, Stage: "connections", JobID: exposureJobID, Status: events.StatusFailed,
				Message: err.Error(),
				Payload: map[string]any{"exposure_id": exposure.ID, "duration_ms": time.Since(expStarted).Milliseconds()},
			})
			return nil, err
		}
		items := parseConnections(payload["items"])
		o.pathMapper().applyToConnections(items)
		// Enforce closed-set invariants locally regardless of the model's output.
		allowedIDs := make(map[string]struct{}, len(batch))
		for _, c := range batch {
			allowedIDs[c.ID] = struct{}{}
		}
		for i := range items {
			if strings.TrimSpace(items[i].FromExposureID) == "" {
				items[i].FromExposureID = exposure.ID
			}
		}
		matched := 0
		for _, it := range items {
			// Reject items that reference a dependency outside the batch; the
			// assembler will also catch orphans but dropping early saves
			// downstream work and keeps the log signal clean.
			if _, ok := allowedIDs[strings.TrimSpace(it.ToDependencyID)]; !ok {
				continue
			}
			out = append(out, it)
			matched++
		}
		o.emit(events.Event{
			Kind: events.KindJobCompleted, Stage: "connections", JobID: batchJobID, ParentID: exposureJobID,
			Status:  events.StatusSuccess,
			Payload: map[string]any{"batch": batchNo, "matched": matched, "duration_ms": time.Since(batchStarted).Milliseconds()},
		})
	}
	o.emit(events.Event{
		Kind: events.KindJobCompleted, Stage: "connections", JobID: exposureJobID, Status: events.StatusSuccess,
		Payload: map[string]any{
			"exposure_id": exposure.ID,
			"connections": len(out),
			"duration_ms": time.Since(expStarted).Milliseconds(),
		},
	})
	return out, nil
}

// assembleConnections converts raw LLM connection links into model.Connection
// values. It enforces: from must match a known exposure, to must match a
// known dependency (closed-set), confidence gate, deterministic IDs.
func assembleConnections(
	expByID map[string]model.Exposure,
	depByID map[string]model.Dependency,
	rawLinks []llmConnection,
	minConfidence float64,
) ([]model.Connection, []model.UnresolvedItem) {
	conns := make([]model.Connection, 0, len(rawLinks))
	unresolved := make([]model.UnresolvedItem, 0)
	seen := map[string]struct{}{}

	for _, c := range rawLinks {
		fromID := strings.TrimSpace(c.FromExposureID)
		toID := strings.TrimSpace(c.ToDependencyID)
		exp, okE := expByID[fromID]
		dep, okD := depByID[toID]
		if !okE || !okD {
			unresolved = append(unresolved, model.UnresolvedItem{
				Kind: model.KindDependency, Type: "connection",
				Name:       fromID + " -> " + toID,
				ReasonCode: "unmatched_reference",
				Reason:     fmt.Sprintf("could not resolve from=%q (found=%v) to=%q (found=%v)", fromID, okE, toID, okD),
				Confidence: c.Confidence,
			})
			continue
		}
		if c.Confidence < minConfidence {
			continue
		}
		cond := fillCondition(c.Condition, c.Summary)
		pathSig := strings.TrimSpace(c.PathSignature)
		if pathSig == "" {
			pathSig = util.StableID(exp.ID, dep.ID, cond.Expression)
		}
		connID := util.StableID(exp.ID, dep.ID, pathSig)
		if _, dup := seen[connID]; dup {
			continue
		}
		seen[connID] = struct{}{}

		locations := toLocations(c.Locations)
		if len(locations) == 0 {
			locations = exp.Locations
		}
		if len(locations) == 0 {
			locations = dep.Locations
		}
		evidence := toEvidence(c.Evidence)
		if len(evidence) == 0 && len(locations) > 0 {
			evidence = append(evidence, model.Evidence{
				Location: locations[0],
				Snippet:  defaultStr(c.Summary, "Connection"),
				Source:   "opencode",
			})
		}

		conns = append(conns, model.Connection{
			ID:             connID,
			FromExposureID: exp.ID,
			ToDependencyID: dep.ID,
			Condition:      cond,
			PathSignature:  pathSig,
			Summary:        defaultStr(c.Summary, "Connection"),
			Locations:      locations,
			Evidence:       evidence,
			Confidence:     c.Confidence,
			FromType:       exp.Type,
			ToType:         dep.Type,
			Paths:          toConnectionPaths(c.Paths),
		})
	}
	return conns, unresolved
}

func buildDependencyCatalog(deps []model.Dependency) []connectionCatalogItem {
	out := make([]connectionCatalogItem, 0, len(deps))
	for _, d := range deps {
		loc := ""
		if len(d.Locations) > 0 {
			loc = fmt.Sprintf("%s:%d", d.Locations[0].File, d.Locations[0].StartLine)
		}
		out = append(out, connectionCatalogItem{
			ID:       d.ID,
			Type:     d.Type,
			Name:     d.Name,
			Summary:  d.Summary,
			Details:  d.Details,
			Location: loc,
		})
	}
	return out
}

func exposureToCatalogItem(e model.Exposure) connectionCatalogItem {
	loc := ""
	if len(e.Locations) > 0 {
		loc = fmt.Sprintf("%s:%d", e.Locations[0].File, e.Locations[0].StartLine)
	}
	return connectionCatalogItem{
		ID:       e.ID,
		Type:     e.Type,
		Name:     e.Name,
		Summary:  e.Summary,
		Details:  e.Details,
		Location: loc,
	}
}
