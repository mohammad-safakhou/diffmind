package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type openCodeAPI interface {
	Enabled() bool
	CreateSession(ctx context.Context, directory string) (string, error)
	DeleteSession(ctx context.Context, sessionID, directory string) error
	PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error)
}

type Result struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
	Unresolved   []model.UnresolvedItem
	Warnings     []string
}

type llmLocation struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type llmEvidence struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Snippet   string `json:"snippet"`
	Source    string `json:"source"`
}

type llmInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type llmEntity struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Summary    string         `json:"summary"`
	Inputs     []llmInput     `json:"inputs"`
	Actions    []string       `json:"key_actions"`
	Confidence float64        `json:"confidence"`
	Tags       []string       `json:"tags"`
	Details    map[string]any `json:"details"`
	Locations  []llmLocation  `json:"source_locations"`
	Evidence   []llmEvidence  `json:"evidence"`
}

type llmConnection struct {
	FromExposureID string          `json:"from_exposure_id"`
	ToDependencyID string          `json:"to_dependency_id"`
	Summary        string          `json:"summary"`
	Confidence     float64         `json:"confidence"`
	PathSignature  string          `json:"path_signature"`
	Condition      model.Condition `json:"condition"`
	Paths          []llmPath       `json:"paths"`
	Locations      []llmLocation   `json:"source_locations"`
	Evidence       []llmEvidence   `json:"evidence"`
}

type llmPath struct {
	ID        string          `json:"id"`
	Summary   string          `json:"summary"`
	Condition model.Condition `json:"condition"`
	Steps     []llmPathStep   `json:"steps"`
}

type llmPathStep struct {
	Order     int             `json:"order"`
	Action    string          `json:"action"`
	Operation string          `json:"operation"`
	From      string          `json:"from"`
	To        string          `json:"to"`
	Condition model.Condition `json:"condition"`
	Location  llmLocation     `json:"location"`
	Evidence  []llmEvidence   `json:"evidence"`
}

type extractionResult struct {
	Objective  objectives.Objective
	Items      []llmEntity
	Unresolved []model.UnresolvedItem
	Err        error
}

type detailResult struct {
	Objective  objectives.Objective
	Item       *llmEntity
	Unresolved []model.UnresolvedItem
	Err        error
}

type connectionResult struct {
	ExposureID string
	Items      []llmConnection
	Unresolved []model.UnresolvedItem
	Err        error
}

type orchestrator struct {
	cfg      config.Config
	repoPath string
	oc       openCodeAPI

	exposures    map[string]model.Exposure
	dependencies map[string]model.Dependency
	connections  map[string]model.Connection
	unresolved   []model.UnresolvedItem
	warnings     []string
}

func Run(ctx context.Context, cfg config.Config, repoPath string, oc openCodeAPI) (Result, error) {
	if oc == nil || !oc.Enabled() {
		return Result{}, fmt.Errorf("opencode is required for extraction")
	}
	if cfg.Runtime.Workers <= 0 {
		cfg.Runtime.Workers = 8
	}
	if cfg.Runtime.MaxEntitiesPerObjective <= 0 {
		cfg.Runtime.MaxEntitiesPerObjective = 25
	}
	if cfg.Runtime.MaxCatalogItems <= 0 {
		cfg.Runtime.MaxCatalogItems = 200
	}

	o := &orchestrator{
		cfg:          cfg,
		repoPath:     repoPath,
		oc:           oc,
		exposures:    map[string]model.Exposure{},
		dependencies: map[string]model.Dependency{},
		connections:  map[string]model.Connection{},
		unresolved:   make([]model.UnresolvedItem, 0),
		warnings:     make([]string, 0),
	}
	progress := newProgressReporter()
	defer progress.Close()

	allObjectives := objectives.Default()
	util.Info("agents.orchestrator", "deterministic objective pipeline starting", map[string]any{
		"repo": repoPath, "workers": cfg.Runtime.Workers, "objectives": len(allObjectives),
		"max_entities_per_objective": cfg.Runtime.MaxEntitiesPerObjective, "max_catalog_items": cfg.Runtime.MaxCatalogItems,
	})
	progress.StartPhase(
		"discovery",
		len(allObjectives),
		0,
		30,
		"Scanning repository for requested exposure/dependency objective types.",
	)

	discovery := o.runObjectiveExtractionBatch(ctx, allObjectives, progress.Advance)
	progress.CompletePhase()
	detailJobs := make([]struct {
		Obj  objectives.Objective
		Seed llmEntity
	}, 0)
	for _, d := range discovery {
		if d.Err != nil {
			o.warnings = append(o.warnings, "Objective extraction failed for "+d.Objective.ID+": "+d.Err.Error())
			o.unresolved = append(o.unresolved, model.UnresolvedItem{Kind: d.Objective.Kind, Type: d.Objective.Type, Name: d.Objective.Description, ReasonCode: "agent_failure", Reason: d.Err.Error()})
			continue
		}
		o.unresolved = append(o.unresolved, d.Unresolved...)
		for _, item := range d.Items {
			detailJobs = append(detailJobs, struct {
				Obj  objectives.Objective
				Seed llmEntity
			}{Obj: d.Objective, Seed: item})
		}
	}

	progress.StartPhase(
		"detail_enrichment",
		len(detailJobs),
		30,
		60,
		"Enriching each discovered entity with evidence-backed details and IO contracts.",
	)
	details := o.runDetailBatch(ctx, detailJobs, progress.Advance)
	progress.CompletePhase()
	for _, d := range details {
		if d.Err != nil {
			o.warnings = append(o.warnings, "Detail extraction failed for "+d.Objective.ID+": "+d.Err.Error())
			o.unresolved = append(o.unresolved, model.UnresolvedItem{Kind: d.Objective.Kind, Type: d.Objective.Type, Name: d.Objective.Description, ReasonCode: "agent_failure", Reason: d.Err.Error()})
			continue
		}
		o.unresolved = append(o.unresolved, d.Unresolved...)
		if d.Item == nil {
			continue
		}
		base, unresolved := toBase(o.repoPath, d.Objective.Kind, *d.Item, o.cfg.Quality.MinConfidence)
		if unresolved != nil {
			o.unresolved = append(o.unresolved, *unresolved)
			continue
		}
		if d.Objective.Kind == model.KindExposure {
			o.upsertExposure(model.Exposure{BaseEntity: base})
		} else {
			o.upsertDependency(model.Dependency{BaseEntity: base})
		}
	}

	progress.StartPhase(
		"connection_mapping",
		len(o.exposures),
		60,
		85,
		"Mapping conditional exposure-to-dependency paths with ordered steps.",
	)
	connections, unresolvedConn := o.runConnectionsBatch(ctx, progress.Advance)
	progress.CompletePhase()
	o.unresolved = append(o.unresolved, unresolvedConn...)
	for _, c := range connections {
		o.upsertConnection(c)
	}

	exposures := mapValuesExposure(o.exposures)
	dependencies := mapValuesDependency(o.dependencies)
	conns := mapValuesConnection(o.connections)
	sort.Slice(exposures, func(i, j int) bool { return exposures[i].ID < exposures[j].ID })
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })

	out := Result{
		Exposures:    exposures,
		Dependencies: dependencies,
		Connections:  conns,
		Unresolved:   dedupeUnresolved(o.unresolved),
		Warnings:     dedupeStrings(o.warnings),
	}
	progress.StartPhase(
		"finalizing",
		1,
		85,
		90,
		"Preparing final artifact payloads and unresolved diagnostics.",
	)
	progress.Advance()
	progress.CompletePhase()
	util.Info("agents.orchestrator", "deterministic objective pipeline completed", map[string]any{
		"exposures": len(out.Exposures), "dependencies": len(out.Dependencies),
		"connections": len(out.Connections), "unresolved": len(out.Unresolved), "warnings": len(out.Warnings),
	})
	return out, nil
}

func (o *orchestrator) runObjectiveExtractionBatch(ctx context.Context, objs []objectives.Objective, onResult func()) []extractionResult {
	jobs := make(chan objectives.Objective)
	results := make(chan extractionResult)
	workers := minInt(o.cfg.Runtime.Workers, maxInt(1, len(objs)))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for obj := range jobs {
				util.Debug("agents.discovery", "worker picked objective", map[string]any{"worker": workerID, "objective": obj.ID})
				items, unresolved, err := o.runObjectiveExtractor(ctx, obj)
				results <- extractionResult{Objective: obj, Items: items, Unresolved: unresolved, Err: err}
			}
		}(i + 1)
	}

	go func() {
		for _, obj := range objs {
			jobs <- obj
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	out := make([]extractionResult, 0, len(objs))
	for r := range results {
		out = append(out, r)
		if onResult != nil {
			onResult()
		}
	}
	return out
}

func (o *orchestrator) runObjectiveExtractor(ctx context.Context, obj objectives.Objective) ([]llmEntity, []model.UnresolvedItem, error) {
	prompt := fmt.Sprintf(`AGENT ROLE: objective-extractor
OBJECTIVE_ID: %s
OBJECTIVE_KIND: %s
OBJECTIVE_TYPE: %s
OBJECTIVE_DESCRIPTION: %s

TASK:
%s

STRICT OUTPUT REQUIREMENTS:
1) Return only source-backed entities with exact file+line evidence.
2) Include detailed inputs and key_actions.
3) Include details object with deep technical fields (tables, operations, endpoint paths, queue destinations, etc.) when applicable.
4) Confidence range [0,1].
5) Do not emit duplicates of the same callsite.
6) If no entities are found for this objective, return an empty items array.
`, obj.ID, obj.Kind, obj.Type, obj.Description, obj.DiscoveryPrompt)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items":      map[string]any{"type": "array", "items": entitySchema()},
			"unresolved": unresolvedSchema(),
		},
		"required": []string{"items"},
	}
	payload, err := o.promptAgent(ctx, "discover."+obj.ID, prompt, schema)
	if err != nil {
		return nil, nil, err
	}
	items := parseEntities(payload["items"])
	items = clampEntities(items, o.cfg.Runtime.MaxEntitiesPerObjective)
	unresolved := parseUnresolved(payload["unresolved"], obj.Kind)
	util.Info("agents.discovery", "objective discovery completed", map[string]any{"objective": obj.ID, "items": len(items), "unresolved": len(unresolved)})
	return items, unresolved, nil
}

func (o *orchestrator) runDetailBatch(ctx context.Context, jobs []struct {
	Obj  objectives.Objective
	Seed llmEntity
}, onResult func()) []detailResult {
	if len(jobs) == 0 {
		return nil
	}
	jobCh := make(chan struct {
		Obj  objectives.Objective
		Seed llmEntity
	})
	resultCh := make(chan detailResult)
	workers := minInt(o.cfg.Runtime.Workers, maxInt(1, len(jobs)))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobCh {
				util.Trace("agents.detail", "worker picked entity", map[string]any{"worker": workerID, "objective": job.Obj.ID, "entity_name": job.Seed.Name, "entity_type": job.Seed.Type})
				item, unresolved, err := o.runDetailExtractor(ctx, job.Obj, job.Seed)
				resultCh <- detailResult{Objective: job.Obj, Item: item, Unresolved: unresolved, Err: err}
			}
		}(i + 1)
	}

	go func() {
		for _, job := range jobs {
			jobCh <- job
		}
		close(jobCh)
		wg.Wait()
		close(resultCh)
	}()

	out := make([]detailResult, 0, len(jobs))
	for r := range resultCh {
		out = append(out, r)
		if onResult != nil {
			onResult()
		}
	}
	return out
}

func (o *orchestrator) runDetailExtractor(ctx context.Context, obj objectives.Objective, seed llmEntity) (*llmEntity, []model.UnresolvedItem, error) {
	prompt := fmt.Sprintf(`AGENT ROLE: detail-extractor
OBJECTIVE_ID: %s
OBJECTIVE_KIND: %s
OBJECTIVE_TYPE: %s

TASK:
%s

CANDIDATE:
%s

STRICT OUTPUT REQUIREMENTS:
1) Validate candidate against source evidence; reject via unresolved if not valid.
2) Keep identity tied to exact callsite (avoid broad conceptual grouping).
3) Populate details object deeply.
4) Include at least one evidence item with snippet.
`, obj.ID, obj.Kind, obj.Type, obj.DetailPrompt, mustJSON(seed))

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item":       entitySchema(),
			"unresolved": unresolvedSchema(),
		},
	}
	payload, err := o.promptAgent(ctx, "detail."+obj.ID, prompt, schema)
	if err != nil {
		return nil, nil, err
	}
	unresolved := parseUnresolved(payload["unresolved"], obj.Kind)
	items := parseEntities(asList(payload["item"]))
	if len(items) == 0 {
		return nil, unresolved, nil
	}
	return &items[0], unresolved, nil
}

func (o *orchestrator) runConnectionsBatch(ctx context.Context, onResult func()) ([]model.Connection, []model.UnresolvedItem) {
	exposures := mapValuesExposure(o.exposures)
	dependencies := mapValuesDependency(o.dependencies)
	if len(exposures) == 0 || len(dependencies) == 0 {
		return nil, nil
	}

	jobCh := make(chan model.Exposure)
	resultCh := make(chan connectionResult)
	workers := minInt(o.cfg.Runtime.Workers, maxInt(1, len(exposures)))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for exp := range jobCh {
				util.Trace("agents.connections", "worker mapping exposure", map[string]any{"worker": workerID, "exposure": exp.ID})
				items, unresolved, err := o.runConnectionExtractor(ctx, exp, dependencies)
				resultCh <- connectionResult{ExposureID: exp.ID, Items: items, Unresolved: unresolved, Err: err}
			}
		}(i + 1)
	}

	go func() {
		for _, exp := range exposures {
			jobCh <- exp
		}
		close(jobCh)
		wg.Wait()
		close(resultCh)
	}()

	links := make([]llmConnection, 0)
	unresolved := make([]model.UnresolvedItem, 0)
	for r := range resultCh {
		if onResult != nil {
			onResult()
		}
		if r.Err != nil {
			o.warnings = append(o.warnings, "Connection extraction failed for "+r.ExposureID+": "+r.Err.Error())
			unresolved = append(unresolved, model.UnresolvedItem{Kind: model.KindDependency, Type: "connection", Name: r.ExposureID, ReasonCode: "agent_failure", Reason: r.Err.Error()})
			continue
		}
		links = append(links, r.Items...)
		unresolved = append(unresolved, r.Unresolved...)
	}
	converted, unresolvedConv := toConnections(exposures, dependencies, links, o.cfg.Quality.MinConfidence)
	unresolved = append(unresolved, unresolvedConv...)
	util.Info("agents.connections", "connection extraction completed", map[string]any{"connections": len(converted), "llm_links": len(links), "unresolved": len(unresolved)})
	return converted, unresolved
}

func (o *orchestrator) runConnectionExtractor(ctx context.Context, exposure model.Exposure, dependencies []model.Dependency) ([]llmConnection, []model.UnresolvedItem, error) {
	prompt := fmt.Sprintf(`AGENT ROLE: connection-extractor
EXPOSURE_ID: %s
EXPOSURE_TYPE: %s

TASK:
Map exposure -> dependency connections for this exposure only.
Return ordered execution paths and step-level operations.

Output guidance:
- Include condition expression for each connection.
- Include paths[] where each path has ordered steps with operation semantics (read/write/call/publish/etc).
- For DB calls include table/entity name and operation in summary/steps.
- Emit only source-backed links.

EXPOSURE:
%s

DEPENDENCY_CATALOG:
%s`, exposure.ID, exposure.Type, mustJSON(exposure), mustJSON(compactDependencyCatalog(dependencies, o.cfg.Runtime.MaxCatalogItems)))

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items":      map[string]any{"type": "array", "items": connectionSchema()},
			"unresolved": unresolvedSchema(),
		},
		"required": []string{"items"},
	}
	payload, err := o.promptAgent(ctx, "connections."+exposure.ID, prompt, schema)
	if err != nil {
		return nil, nil, err
	}
	items := parseConnections(payload["items"])
	for i := range items {
		if strings.TrimSpace(items[i].FromExposureID) == "" {
			items[i].FromExposureID = exposure.ID
		}
	}
	return items, parseUnresolved(payload["unresolved"], model.KindDependency), nil
}

func (o *orchestrator) promptAgent(ctx context.Context, role, prompt string, schema map[string]any) (map[string]any, error) {
	sessionID, err := o.oc.CreateSession(ctx, o.repoPath)
	if err != nil {
		return nil, fmt.Errorf("%s create session: %w", role, err)
	}
	util.Debug("agents.agent", "session created", map[string]any{"role": role, "session_id": sessionID})

	payload, err := o.oc.PromptStructured(ctx, sessionID, o.repoPath, prompt, schema)
	if err != nil {
		return nil, fmt.Errorf("%s prompt: %w", role, err)
	}
	util.Trace("agents.agent", "prompt completed", map[string]any{"role": role, "session_id": sessionID})
	o.maybeScheduleSessionDelete(role, sessionID)
	return payload, nil
}

func (o *orchestrator) maybeScheduleSessionDelete(role, sessionID string) {
	if !o.cfg.Runtime.CleanupOpenCodeSessions || strings.TrimSpace(sessionID) == "" {
		return
	}
	delay := o.cfg.Runtime.OpenCodeDeleteDelaySec
	if delay <= 0 {
		delay = 5
	}
	go func() {
		time.Sleep(time.Duration(delay) * time.Second)
		if err := o.oc.DeleteSession(context.Background(), sessionID, o.repoPath); err != nil {
			util.Warn("agents.agent", "session delete failed", map[string]any{"role": role, "session_id": sessionID, "error": err})
			return
		}
		util.Trace("agents.agent", "session deleted", map[string]any{"role": role, "session_id": sessionID, "delete_delay_sec": delay})
	}()
}

func (o *orchestrator) upsertExposure(exp model.Exposure) {
	if existing, ok := o.exposures[exp.ID]; ok {
		if exp.Confidence <= existing.Confidence {
			return
		}
	}
	o.exposures[exp.ID] = exp
}

func (o *orchestrator) upsertDependency(dep model.Dependency) {
	if existing, ok := o.dependencies[dep.ID]; ok {
		if dep.Confidence <= existing.Confidence {
			return
		}
	}
	o.dependencies[dep.ID] = dep
}

func (o *orchestrator) upsertConnection(conn model.Connection) {
	if existing, ok := o.connections[conn.ID]; ok {
		if conn.Confidence <= existing.Confidence {
			return
		}
	}
	o.connections[conn.ID] = conn
}

func clampEntities(items []llmEntity, max int) []llmEntity {
	if max <= 0 || len(items) <= max {
		return items
	}
	return items[:max]
}

func parseEntities(v any) []llmEntity {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []llmEntity
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

func parseConnections(v any) []llmConnection {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []llmConnection
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

func parseUnresolved(v any, kind model.EntityKind) []model.UnresolvedItem {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []model.UnresolvedItem
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	for i := range out {
		if out[i].Kind == "" {
			out[i].Kind = kind
		}
		if out[i].ReasonCode == "" {
			out[i].ReasonCode = "unresolved"
		}
	}
	return out
}

func toConnections(exposures []model.Exposure, dependencies []model.Dependency, links []llmConnection, minConfidence float64) ([]model.Connection, []model.UnresolvedItem) {
	expSet := map[string]model.Exposure{}
	for _, e := range exposures {
		expSet[e.ID] = e
	}
	depSet := map[string]model.Dependency{}
	for _, d := range dependencies {
		depSet[d.ID] = d
	}
	out := make([]model.Connection, 0, len(links))
	unresolved := make([]model.UnresolvedItem, 0)
	for _, link := range links {
		exp, okE := expSet[link.FromExposureID]
		dep, okD := depSet[link.ToDependencyID]
		if !okE || !okD {
			unresolved = append(unresolved, model.UnresolvedItem{Kind: model.KindDependency, Type: "connection", Name: link.FromExposureID + "->" + link.ToDependencyID, ReasonCode: "unknown_entity_reference", Reason: "Connection references unknown exposure/dependency", Confidence: link.Confidence})
			continue
		}
		if link.Confidence < minConfidence {
			unresolved = append(unresolved, model.UnresolvedItem{Kind: model.KindDependency, Type: "connection", Name: exp.Name + "->" + dep.Name, ReasonCode: "low_confidence", Reason: "Connection below confidence threshold", Confidence: link.Confidence})
			continue
		}
		locations := toLocations(link.Locations)
		if len(locations) == 0 {
			locations = append(locations, pickLocation(exp.Locations))
			locations = append(locations, pickLocation(dep.Locations))
		}
		evidence := toEvidence(link.Evidence)
		if len(evidence) == 0 && len(locations) > 0 {
			evidence = append(evidence, model.Evidence{Location: locations[0], Snippet: "Connection extracted by OpenCode", Source: "opencode"})
		}
		cond := link.Condition
		if strings.TrimSpace(cond.Kind) == "" {
			cond.Kind = "predicate"
		}
		if strings.TrimSpace(cond.Expression) == "" {
			cond.Expression = "true"
		}
		if strings.TrimSpace(cond.Explanation) == "" {
			cond.Explanation = "Condition extracted from source-backed path"
		}
		pathSig := link.PathSignature
		if strings.TrimSpace(pathSig) == "" {
			pathSig = util.StableID(link.FromExposureID, link.ToDependencyID, cond.Expression)
		}
		id := util.StableID(link.FromExposureID, link.ToDependencyID, cond.Expression, pathSig)
		paths := toConnectionPaths(link.Paths)
		out = append(out, model.Connection{
			ID:             id,
			FromExposureID: link.FromExposureID,
			ToDependencyID: link.ToDependencyID,
			Condition:      cond,
			PathSignature:  pathSig,
			Summary:        defaultSummary(link.Summary, "Direct conditional connection extracted by OpenCode."),
			Locations:      locations,
			Evidence:       evidence,
			Confidence:     link.Confidence,
			FromType:       exp.Type,
			ToType:         dep.Type,
			Paths:          paths,
		})
	}
	return out, unresolved
}

func toBase(repoPath string, kind model.EntityKind, e llmEntity, minConfidence float64) (model.BaseEntity, *model.UnresolvedItem) {
	name := strings.TrimSpace(e.Name)
	typ := strings.TrimSpace(e.Type)
	if name == "" || typ == "" {
		u := model.UnresolvedItem{Kind: kind, Type: typ, Name: name, ReasonCode: "invalid_entity", Reason: "Missing required name/type", Confidence: e.Confidence}
		return model.BaseEntity{}, &u
	}
	if e.Confidence < minConfidence {
		u := model.UnresolvedItem{Kind: kind, Type: typ, Name: name, ReasonCode: "low_confidence", Reason: "Entity below confidence threshold", Confidence: e.Confidence, Evidence: toEvidence(e.Evidence)}
		return model.BaseEntity{}, &u
	}
	locations := toLocations(e.Locations)
	if len(locations) == 0 {
		u := model.UnresolvedItem{Kind: kind, Type: typ, Name: name, ReasonCode: "missing_source_locations", Reason: "Source-backed evidence required", Confidence: e.Confidence, Evidence: toEvidence(e.Evidence)}
		return model.BaseEntity{}, &u
	}
	evidence := toEvidence(e.Evidence)
	if len(evidence) == 0 {
		evidence = append(evidence, model.Evidence{Location: locations[0], Snippet: "Entity extracted by OpenCode", Source: "opencode"})
	}
	inputs := make([]model.InputSpec, 0, len(e.Inputs))
	for _, in := range e.Inputs {
		if strings.TrimSpace(in.Name) == "" {
			continue
		}
		inputs = append(inputs, model.InputSpec{Name: in.Name, Type: in.Type, Required: in.Required, Description: in.Description})
	}
	id := util.StableID(string(kind), typ, name, locations[0].File, fmt.Sprintf("%d:%d", locations[0].StartLine, locations[0].EndLine))
	return model.BaseEntity{
		ID:           id,
		Type:         typ,
		Name:         name,
		Service:      repoPath,
		Inputs:       inputs,
		Summary:      defaultSummary(e.Summary, "Extracted by OpenCode"),
		KeyActions:   e.Actions,
		Locations:    locations,
		Evidence:     evidence,
		Confidence:   e.Confidence,
		Tags:         e.Tags,
		Details:      e.Details,
		PluginSource: "opencode",
	}, nil
}

func toLocations(in []llmLocation) []model.Location {
	out := make([]model.Location, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v.File) == "" || v.StartLine <= 0 {
			continue
		}
		end := v.EndLine
		if end < v.StartLine {
			end = v.StartLine
		}
		out = append(out, model.Location{File: v.File, StartLine: v.StartLine, EndLine: end})
	}
	return out
}

func toEvidence(in []llmEvidence) []model.Evidence {
	out := make([]model.Evidence, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v.File) == "" || v.StartLine <= 0 {
			continue
		}
		end := v.EndLine
		if end < v.StartLine {
			end = v.StartLine
		}
		source := v.Source
		if strings.TrimSpace(source) == "" {
			source = "opencode"
		}
		out = append(out, model.Evidence{Location: model.Location{File: v.File, StartLine: v.StartLine, EndLine: end}, Snippet: v.Snippet, Source: source})
	}
	return out
}

func toConnectionPaths(in []llmPath) []model.ConnectionPath {
	out := make([]model.ConnectionPath, 0, len(in))
	for _, path := range in {
		steps := make([]model.ConnectionPathStep, 0, len(path.Steps))
		for _, s := range path.Steps {
			steps = append(steps, model.ConnectionPathStep{
				Order:     s.Order,
				Action:    s.Action,
				Operation: s.Operation,
				From:      s.From,
				To:        s.To,
				Condition: s.Condition,
				Location:  model.Location{File: s.Location.File, StartLine: s.Location.StartLine, EndLine: maxInt(s.Location.EndLine, s.Location.StartLine)},
				Evidence:  toEvidence(s.Evidence),
			})
		}
		out = append(out, model.ConnectionPath{ID: path.ID, Summary: path.Summary, Condition: path.Condition, Steps: steps})
	}
	return out
}

func pickLocation(in []model.Location) model.Location {
	if len(in) == 0 {
		return model.Location{}
	}
	return in[0]
}

func defaultSummary(in, fallback string) string {
	if strings.TrimSpace(in) == "" {
		return fallback
	}
	return in
}

func entitySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":        map[string]any{"type": "string"},
			"name":        map[string]any{"type": "string"},
			"summary":     map[string]any{"type": "string"},
			"key_actions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"confidence":  map[string]any{"type": "number"},
			"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"details":     map[string]any{"type": "object", "additionalProperties": true},
			"inputs": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string"},
						"type":        map[string]any{"type": "string"},
						"required":    map[string]any{"type": "boolean"},
						"description": map[string]any{"type": "string"},
					},
				},
			},
			"source_locations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file":       map[string]any{"type": "string"},
						"start_line": map[string]any{"type": "number"},
						"end_line":   map[string]any{"type": "number"},
					},
					"required": []string{"file", "start_line"},
				},
			},
			"evidence": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file":       map[string]any{"type": "string"},
						"start_line": map[string]any{"type": "number"},
						"end_line":   map[string]any{"type": "number"},
						"snippet":    map[string]any{"type": "string"},
						"source":     map[string]any{"type": "string"},
					},
					"required": []string{"file", "start_line"},
				},
			},
		},
		"required": []string{"type", "name", "summary", "confidence", "source_locations"},
	}
}

func unresolvedSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":        map[string]any{"type": "string"},
				"type":        map[string]any{"type": "string"},
				"name":        map[string]any{"type": "string"},
				"reason_code": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string"},
				"confidence":  map[string]any{"type": "number"},
			},
		},
	}
}

func connectionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from_exposure_id": map[string]any{"type": "string"},
			"to_dependency_id": map[string]any{"type": "string"},
			"summary":          map[string]any{"type": "string"},
			"confidence":       map[string]any{"type": "number"},
			"path_signature":   map[string]any{"type": "string"},
			"condition": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind":        map[string]any{"type": "string"},
					"expression":  map[string]any{"type": "string"},
					"variables":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"operator":    map[string]any{"type": "string"},
					"value":       map[string]any{"type": "string"},
					"negated":     map[string]any{"type": "boolean"},
					"explanation": map[string]any{"type": "string"},
				},
				"required": []string{"expression", "explanation"},
			},
			"paths": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":      map[string]any{"type": "string"},
						"summary": map[string]any{"type": "string"},
						"condition": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"kind":        map[string]any{"type": "string"},
								"expression":  map[string]any{"type": "string"},
								"explanation": map[string]any{"type": "string"},
							},
						},
						"steps": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"order":     map[string]any{"type": "number"},
									"action":    map[string]any{"type": "string"},
									"operation": map[string]any{"type": "string"},
									"from":      map[string]any{"type": "string"},
									"to":        map[string]any{"type": "string"},
									"condition": map[string]any{"type": "object"},
									"location": map[string]any{
										"type":       "object",
										"properties": map[string]any{"file": map[string]any{"type": "string"}, "start_line": map[string]any{"type": "number"}, "end_line": map[string]any{"type": "number"}},
									},
									"evidence": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
								},
							},
						},
					},
				},
			},
			"source_locations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file":       map[string]any{"type": "string"},
						"start_line": map[string]any{"type": "number"},
						"end_line":   map[string]any{"type": "number"},
					},
					"required": []string{"file", "start_line"},
				},
			},
			"evidence": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file":       map[string]any{"type": "string"},
						"start_line": map[string]any{"type": "number"},
						"end_line":   map[string]any{"type": "number"},
						"snippet":    map[string]any{"type": "string"},
						"source":     map[string]any{"type": "string"},
					},
					"required": []string{"file", "start_line"},
				},
			},
		},
		"required": []string{"from_exposure_id", "to_dependency_id", "summary", "confidence", "condition"},
	}
}

func compactDependencyCatalog(in []model.Dependency, max int) []map[string]any {
	sort.Slice(in, func(i, j int) bool { return in[i].ID < in[j].ID })
	if len(in) > max {
		in = in[:max]
	}
	out := make([]map[string]any, 0, len(in))
	for _, d := range in {
		loc := pickLocation(d.Locations)
		out = append(out, map[string]any{"id": d.ID, "type": d.Type, "name": d.Name, "summary": d.Summary, "details": d.Details, "file": loc.File, "start_line": loc.StartLine})
	}
	return out
}

func mapValuesExposure(m map[string]model.Exposure) []model.Exposure {
	out := make([]model.Exposure, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func mapValuesDependency(m map[string]model.Dependency) []model.Dependency {
	out := make([]model.Dependency, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func mapValuesConnection(m map[string]model.Connection) []model.Connection {
	out := make([]model.Connection, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func dedupeUnresolved(in []model.UnresolvedItem) []model.UnresolvedItem {
	seen := map[string]struct{}{}
	out := make([]model.UnresolvedItem, 0, len(in))
	for _, u := range in {
		key := strings.TrimSpace(string(u.Kind) + "|" + u.Type + "|" + u.Name + "|" + u.ReasonCode + "|" + u.Reason)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, u)
	}
	return out
}

func asList(v any) any {
	if v == nil {
		return []any{}
	}
	if _, ok := v.([]any); ok {
		return v
	}
	return []any{v}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
