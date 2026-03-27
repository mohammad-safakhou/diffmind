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

type rolePromptMetrics struct {
	Calls       int
	PromptBytes int
	SchemaBytes int
}

type promptMetrics struct {
	Role map[string]rolePromptMetrics

	ConnectionBatches      int
	ConnectionCatalogItems int
	ExposurePayloadBytes   int
	CatalogPayloadBytes    int
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

	sessionMu       sync.Mutex
	sharedSessionID string

	metricsMu sync.Mutex
	metrics   promptMetrics
}

func Run(ctx context.Context, cfg config.Config, repoPath string, oc openCodeAPI) (Result, error) {
	if oc == nil || !oc.Enabled() {
		return Result{}, fmt.Errorf("opencode is required for extraction")
	}
	if cfg.Runtime.Workers <= 0 {
		cfg.Runtime.Workers = 8
	}
	if cfg.Runtime.MaxCatalogItems <= 0 {
		cfg.Runtime.MaxCatalogItems = 80
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
		metrics: promptMetrics{
			Role: map[string]rolePromptMetrics{},
		},
	}
	defer o.closeSharedSession()
	progress := newProgressReporter()
	defer progress.Close()

	allObjectives := objectives.Default()
	util.Info("agents.orchestrator", "deterministic objective pipeline starting", map[string]any{
		"repo": repoPath, "workers": cfg.Runtime.Workers, "objectives": len(allObjectives),
		"max_catalog_items": cfg.Runtime.MaxCatalogItems, "reuse_opencode_session": cfg.Runtime.ReuseOpenCodeSession,
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
			o.unresolved = append(o.unresolved, model.UnresolvedItem{
				Kind:       d.Objective.Kind,
				Type:       d.Objective.Type,
				Name:       d.Objective.Description,
				ReasonCode: "detail_not_confirmed",
				Reason:     "Detail extraction did not return a source-backed entity for a discovered candidate.",
				Confidence: 0,
			})
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
	o.logPromptMetrics()
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
	prompt := buildDiscoveryPrompt(obj)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{"type": "array", "items": entitySchema()},
		},
		"required": []string{"items"},
	}
	payload, err := o.promptAgent(ctx, "discover."+obj.ID, prompt, schema)
	if err != nil {
		return nil, nil, err
	}
	items := parseEntities(payload["items"])
	util.Info("agents.discovery", "objective discovery completed", map[string]any{"objective": obj.ID, "items": len(items), "unresolved": 0})
	return items, nil, nil
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
	prompt := buildDetailPrompt(obj, seed)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item": entitySchema(),
		},
	}
	payload, err := o.promptAgent(ctx, "detail."+obj.ID, prompt, schema)
	if err != nil {
		return nil, nil, err
	}
	items := parseEntities(asList(payload["item"]))
	if len(items) == 0 {
		return nil, nil, nil
	}
	return &items[0], nil, nil
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
	batchSize := o.cfg.Runtime.MaxCatalogItems
	if batchSize <= 0 {
		batchSize = 80
	}
	if len(dependencies) == 0 {
		return nil, nil, nil
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{"type": "array", "items": connectionSchema()},
		},
		"required": []string{"items"},
	}
	out := make([]llmConnection, 0)
	for batchNo, start := 1, 0; start < len(dependencies); batchNo, start = batchNo+1, start+batchSize {
		end := start + batchSize
		if end > len(dependencies) {
			end = len(dependencies)
		}
		prompt, exposureBytes, catalogBytes, catalogItems := buildConnectionPrompt(exposure, dependencies[start:end])
		o.recordConnectionPayloadMetrics(exposureBytes, catalogBytes, catalogItems)

		role := fmt.Sprintf("connections.%s.batch.%d", exposure.ID, batchNo)
		payload, err := o.promptAgent(ctx, role, prompt, schema)
		if err != nil {
			return nil, nil, err
		}
		items := parseConnections(payload["items"])
		for i := range items {
			if strings.TrimSpace(items[i].FromExposureID) == "" {
				items[i].FromExposureID = exposure.ID
			}
		}
		out = append(out, items...)
	}
	return out, nil, nil
}

func (o *orchestrator) promptAgent(ctx context.Context, role, prompt string, schema map[string]any) (map[string]any, error) {
	schemaBytes := len(mustJSON(schema))
	o.recordPromptMetrics(role, len(prompt), schemaBytes)

	var (
		sessionID string
		err       error
	)
	if o.cfg.Runtime.ReuseOpenCodeSession {
		sessionID, err = o.getOrCreateSharedSession(ctx, role)
		if err != nil {
			return nil, err
		}
	} else {
		sessionID, err = o.oc.CreateSession(ctx, o.repoPath)
		if err != nil {
			return nil, fmt.Errorf("%s create session: %w", role, err)
		}
		util.Debug("agents.agent", "session created", map[string]any{"role": role, "session_id": sessionID})
	}

	payload, err := o.oc.PromptStructured(ctx, sessionID, o.repoPath, prompt, schema)
	if err != nil {
		return nil, fmt.Errorf("%s prompt: %w", role, err)
	}
	util.Trace("agents.agent", "prompt completed", map[string]any{"role": role, "session_id": sessionID})
	if !o.cfg.Runtime.ReuseOpenCodeSession {
		o.maybeScheduleSessionDelete(role, sessionID)
	}
	return payload, nil
}

func (o *orchestrator) getOrCreateSharedSession(ctx context.Context, role string) (string, error) {
	o.sessionMu.Lock()
	defer o.sessionMu.Unlock()
	if strings.TrimSpace(o.sharedSessionID) != "" {
		return o.sharedSessionID, nil
	}
	sessionID, err := o.oc.CreateSession(ctx, o.repoPath)
	if err != nil {
		return "", fmt.Errorf("%s create shared session: %w", role, err)
	}
	o.sharedSessionID = sessionID
	util.Debug("agents.agent", "shared session created", map[string]any{"role": role, "session_id": sessionID})
	return sessionID, nil
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

func (o *orchestrator) closeSharedSession() {
	if !o.cfg.Runtime.ReuseOpenCodeSession || !o.cfg.Runtime.CleanupOpenCodeSessions {
		return
	}
	o.sessionMu.Lock()
	sessionID := strings.TrimSpace(o.sharedSessionID)
	o.sharedSessionID = ""
	o.sessionMu.Unlock()
	if sessionID == "" {
		return
	}
	if err := o.oc.DeleteSession(context.Background(), sessionID, o.repoPath); err != nil {
		util.Warn("agents.agent", "shared session delete failed", map[string]any{"session_id": sessionID, "error": err})
		return
	}
	util.Trace("agents.agent", "shared session deleted", map[string]any{"session_id": sessionID})
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

func roleBucket(role string) string {
	switch {
	case strings.HasPrefix(role, "discover."):
		return "discover"
	case strings.HasPrefix(role, "detail."):
		return "detail"
	case strings.HasPrefix(role, "connections."):
		return "connections"
	default:
		return "other"
	}
}

func (o *orchestrator) recordPromptMetrics(role string, promptBytes, schemaBytes int) {
	o.metricsMu.Lock()
	defer o.metricsMu.Unlock()
	bucket := roleBucket(role)
	current := o.metrics.Role[bucket]
	current.Calls++
	current.PromptBytes += promptBytes
	current.SchemaBytes += schemaBytes
	o.metrics.Role[bucket] = current
}

func (o *orchestrator) recordConnectionPayloadMetrics(exposureBytes, catalogBytes, catalogItems int) {
	o.metricsMu.Lock()
	defer o.metricsMu.Unlock()
	o.metrics.ConnectionBatches++
	o.metrics.ConnectionCatalogItems += catalogItems
	o.metrics.ExposurePayloadBytes += exposureBytes
	o.metrics.CatalogPayloadBytes += catalogBytes
}

func (o *orchestrator) logPromptMetrics() {
	o.metricsMu.Lock()
	defer o.metricsMu.Unlock()

	roleMetrics := map[string]any{}
	for role, m := range o.metrics.Role {
		roleMetrics[role] = map[string]any{
			"calls":        m.Calls,
			"prompt_bytes": m.PromptBytes,
			"schema_bytes": m.SchemaBytes,
		}
	}
	util.Info("agents.metrics", "opencode prompt metrics", map[string]any{
		"roles":                    roleMetrics,
		"connection_batches":       o.metrics.ConnectionBatches,
		"connection_catalog_items": o.metrics.ConnectionCatalogItems,
		"exposure_payload_bytes":   o.metrics.ExposurePayloadBytes,
		"catalog_payload_bytes":    o.metrics.CatalogPayloadBytes,
		"session_strategy": map[bool]string{
			true:  "shared",
			false: "per_prompt",
		}[o.cfg.Runtime.ReuseOpenCodeSession],
	})
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

func buildDiscoveryPrompt(obj objectives.Objective) string {
	return fmt.Sprintf(`AGENT ROLE: objective-extractor
OBJECTIVE_ID: %s
OBJECTIVE_KIND: %s
OBJECTIVE_TYPE: %s
OBJECTIVE_DESCRIPTION: %s

MISSION:
%s

ANALYSIS PROTOCOL:
1) Explore recursively until you can confirm all reachable candidates for this objective type.
2) Use only source-backed evidence from this repository.
3) Track each entity at callsite-level granularity, not broad conceptual grouping.
4) Capture deep implementation facts in details{} (table names, operation semantics, route/path/method, queue/topic names, retry policies, command patterns, etc.).

STRICT ACCURACY RULES:
- Never invent files, lines, handlers, routes, tables, queues, endpoints, commands, or conditions.
- If you are uncertain, omit the entity.
- If nothing is found, return exactly {"items": []}.
- Confidence MUST be in [0,1] and reflect evidence strength.
- Do not duplicate the same callsite entity.

OUTPUT CONTRACT:
- Return only JSON matching schema.
- Every entity must include source_locations with valid file + line numbers.
- Include evidence snippets when available.
`, obj.ID, obj.Kind, obj.Type, obj.Description, obj.DiscoveryPrompt)
}

func buildDetailPrompt(obj objectives.Objective, seed llmEntity) string {
	return fmt.Sprintf(`AGENT ROLE: detail-extractor
OBJECTIVE_ID: %s
OBJECTIVE_KIND: %s
OBJECTIVE_TYPE: %s

MISSION:
%s

CANDIDATE_ENTITY:
%s

ANALYSIS PROTOCOL:
1) Validate candidate identity strictly against repository evidence.
2) Keep exact callsite identity; do not merge unrelated callsites.
3) Expand details{} deeply with concrete technical fields.
4) Include ordered flow summary, inputs, key actions, and evidence snippets.

DEPTH CHECKLIST FOR THIS OBJECTIVE TYPE:
%s

STRICT ACCURACY RULES:
- Never fill missing facts with guesses.
- If candidate is not confirmed, return {"item": null}.
- If candidate is confirmed, include at least one source location and one evidence snippet when available.
- Confidence MUST be in [0,1].

OUTPUT CONTRACT:
- Return only JSON matching schema.
`, obj.ID, obj.Kind, obj.Type, obj.DetailPrompt, mustJSON(seed), detailChecklist(obj.Type))
}

func buildConnectionPrompt(exposure model.Exposure, dependencies []model.Dependency) (string, int, int, int) {
	exposureJSON := mustJSON(compactExposureContext(exposure))
	catalogJSON := mustJSON(compactDependencyCatalog(dependencies))
	prompt := fmt.Sprintf(`AGENT ROLE: connection-extractor
EXPOSURE_ID: %s
EXPOSURE_TYPE: %s

MISSION:
Map source-backed connections from this single exposure to known dependencies.

ANALYSIS PROTOCOL:
1) Use exposure evidence + dependency catalog only.
2) Produce connection links only when code path is evidenced.
3) For each link provide condition + ordered paths with step-level operations.
4) Capture branch-specific behavior (guards, feature flags, input checks, error paths) when evidenced.

STRICT ACCURACY RULES:
- Never invent connection links.
- If no source-backed connection exists, return {"items": []}.
- Every step must reflect an evidenced action.
- For DB-related links include table/entity and read/write semantics in steps/summary.

EXPOSURE:
%s

DEPENDENCY_CATALOG:
%s
`, exposure.ID, exposure.Type, exposureJSON, catalogJSON)
	return prompt, len(exposureJSON), len(catalogJSON), len(dependencies)
}

func detailChecklist(entityType string) string {
	switch entityType {
	case "http_route":
		return "- route/method, auth checks, validation chain, handler flow order, downstream calls with conditions."
	case "webhook":
		return "- callback path/method, signature verification, event type branching, idempotency strategy, downstream actions."
	case "rpc_endpoint":
		return "- rpc service/method, request message contract, auth/validation, handler flow order, downstream actions."
	case "queue_consumer":
		return "- queue/topic binding, payload contract, retry/dead-letter behavior, handler flow order, downstream actions."
	case "scheduled_job":
		return "- schedule trigger, profile/property guards, dataset selection, execution order, downstream actions."
	case "cli_command":
		return "- command flags/args, dispatch path, validation, execution order, downstream actions."
	case "db_operation":
		return "- datasource/schema/table/entity, operation type (read/write/upsert/delete), transaction context, query method/callsite."
	case "outbound_http":
		return "- target service, method/path, request/response contract, retry/timeout behavior, call conditions."
	case "outbound_rpc":
		return "- target rpc service/method, request/response contracts, retry/timeout behavior, and call conditions."
	case "queue_publish":
		return "- destination topic/queue, payload fields, publish mode (sync/async/batch), publish conditions."
	case "command_exec":
		return "- command pattern, arguments, env/context, trigger conditions, error handling."
	default:
		return "- concrete callsite identity, ordered flow, deep details, and evidence-backed confidence."
	}
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

func compactDependencyCatalog(in []model.Dependency) []map[string]any {
	sort.Slice(in, func(i, j int) bool { return in[i].ID < in[j].ID })
	out := make([]map[string]any, 0, len(in))
	for _, d := range in {
		loc := pickLocation(d.Locations)
		out = append(out, map[string]any{
			"id":         d.ID,
			"type":       d.Type,
			"name":       d.Name,
			"summary":    d.Summary,
			"hints":      compactDetailsHint(d.Details),
			"file":       loc.File,
			"start_line": loc.StartLine,
		})
	}
	return out
}

func compactExposureContext(e model.Exposure) map[string]any {
	loc := pickLocation(e.Locations)
	return map[string]any{
		"id":         e.ID,
		"type":       e.Type,
		"name":       e.Name,
		"summary":    e.Summary,
		"hints":      compactDetailsHint(e.Details),
		"file":       loc.File,
		"start_line": loc.StartLine,
	}
}

func compactDetailsHint(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	keep := map[string]struct{}{
		"method": {}, "path": {}, "route": {}, "table": {}, "operation": {}, "query": {},
		"queue": {}, "topic": {}, "destination": {}, "rpc_service": {}, "rpc_method": {}, "command": {},
	}
	out := map[string]any{}
	for k, v := range in {
		if _, ok := keep[strings.ToLower(strings.TrimSpace(k))]; !ok {
			continue
		}
		switch t := v.(type) {
		case string:
			out[k] = strings.TrimSpace(t)
		case bool, float64, int, int64:
			out[k] = t
		}
		if len(out) >= 6 {
			break
		}
	}
	if len(out) == 0 {
		return nil
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
