package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type openCodeAPI interface {
	Enabled() bool
	CreateSession(ctx context.Context, directory string) (string, error)
	DeleteSession(ctx context.Context, sessionID, directory string) error
	PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error)
	PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error)
}

type Result struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
	Unresolved   []model.UnresolvedItem
	Warnings     []string
}

// ---- LLM response types ----

type llmEntity struct {
	ID         string         `json:"id"`
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

type llmInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
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

type llmConnection struct {
	FromID     string          `json:"from_id"`
	ToID       string          `json:"to_id"`
	Summary    string          `json:"summary"`
	Confidence float64         `json:"confidence"`
	Condition  model.Condition `json:"condition"`
}

type llmFullResponse struct {
	Exposures    []llmEntity     `json:"exposures"`
	Dependencies []llmEntity     `json:"dependencies"`
	Connections  []llmConnection `json:"connections"`
}

// ---- Pipeline ----

func Run(ctx context.Context, cfg config.Config, repoPath string, oc openCodeAPI) (Result, error) {
	if oc == nil || !oc.Enabled() {
		return Result{}, fmt.Errorf("opencode is required")
	}

	// Detect monorepo: if repoPath is a subdirectory of a git repo, scope the prompt
	sessionDir, subDir := detectMonorepo(repoPath)

	util.Info("agents.orchestrator", "extraction starting", map[string]any{
		"repo": repoPath, "session_dir": sessionDir, "sub_dir": subDir,
	})

	sessionID, err := oc.CreateSession(ctx, sessionDir)
	if err != nil {
		return Result{}, fmt.Errorf("create session: %w", err)
	}
	util.Info("agents.orchestrator", "session created", map[string]any{"session_id": sessionID})

	prompt := buildFullExtractionPrompt()
	if subDir != "" {
		prompt = fmt.Sprintf("IMPORTANT: This is a monorepo. ONLY analyze files under the '%s/' subdirectory. Ignore all other directories at the repo root. All file paths in your response must be relative to the repo root and start with '%s/'.\n\n", subDir, subDir) + prompt
	}

	util.Info("agents.orchestrator", "sending extraction prompt", map[string]any{"prompt_len": len(prompt), "sub_dir": subDir})

	payload, err := oc.PromptText(ctx, sessionID, sessionDir, prompt)
	if err != nil {
		return Result{}, fmt.Errorf("extraction prompt: %w", err)
	}

	// Parse JSON from text response
	jsonPayload := extractJSONFromText(payload)
	if jsonPayload == nil {
		return Result{}, fmt.Errorf("no JSON found in LLM response")
	}

	util.Info("agents.orchestrator", "extraction response received", nil)

	resp := parseFullResponse(jsonPayload)
	return assembleResult(repoPath, cfg.Quality.MinConfidence, resp)
}

func assembleResult(repoPath string, minConfidence float64, resp llmFullResponse) (Result, error) {
	exposures := make([]model.Exposure, 0)
	dependencies := make([]model.Dependency, 0)
	connections := make([]model.Connection, 0)
	unresolved := make([]model.UnresolvedItem, 0)

	// Convert exposures
	expByLocalID := map[string]model.Exposure{}
	for _, e := range resp.Exposures {
		base, u := toBase(repoPath, model.KindExposure, e, minConfidence)
		if u != nil {
			unresolved = append(unresolved, *u)
			continue
		}
		exposures = append(exposures, model.Exposure{BaseEntity: base})
		// Store by LLM-assigned local ID for connection matching
		localID := strings.TrimSpace(e.ID)
		if localID != "" {
			expByLocalID[localID] = model.Exposure{BaseEntity: base}
		}
		// Also store by name for fallback
		expByLocalID[normKey(e.Name)] = model.Exposure{BaseEntity: base}
	}

	// Convert dependencies
	depByLocalID := map[string]model.Dependency{}
	for _, d := range resp.Dependencies {
		base, u := toBase(repoPath, model.KindDependency, d, minConfidence)
		if u != nil {
			unresolved = append(unresolved, *u)
			continue
		}
		dependencies = append(dependencies, model.Dependency{BaseEntity: base})
		localID := strings.TrimSpace(d.ID)
		if localID != "" {
			depByLocalID[localID] = model.Dependency{BaseEntity: base}
		}
		depByLocalID[normKey(d.Name)] = model.Dependency{BaseEntity: base}
	}

	// Match connections
	for _, c := range resp.Connections {
		exp, okE := resolveExposure(c.FromID, expByLocalID)
		dep, okD := resolveDependency(c.ToID, depByLocalID)

		if !okE || !okD {
			unresolved = append(unresolved, model.UnresolvedItem{
				Kind: model.KindDependency, Type: "connection",
				Name: c.FromID + " -> " + c.ToID, ReasonCode: "unmatched_reference",
				Reason: fmt.Sprintf("Could not resolve from=%q or to=%q", c.FromID, c.ToID), Confidence: c.Confidence,
			})
			continue
		}
		if c.Confidence < minConfidence {
			continue
		}

		cond := c.Condition
		if strings.TrimSpace(cond.Kind) == "" {
			cond.Kind = "predicate"
		}
		if strings.TrimSpace(cond.Expression) == "" {
			cond.Expression = "true"
		}
		if strings.TrimSpace(cond.Explanation) == "" {
			cond.Explanation = c.Summary
		}

		pathSig := util.StableID(exp.ID, dep.ID, cond.Expression)
		connID := util.StableID(exp.ID, dep.ID, pathSig)

		locs := exp.Locations
		if len(locs) == 0 {
			locs = dep.Locations
		}
		evidence := make([]model.Evidence, 0)
		if len(locs) > 0 {
			evidence = append(evidence, model.Evidence{Location: locs[0], Snippet: c.Summary, Source: "opencode"})
		}

		connections = append(connections, model.Connection{
			ID: connID, FromExposureID: exp.ID, ToDependencyID: dep.ID,
			Condition: cond, PathSignature: pathSig, Summary: defaultStr(c.Summary, "Connection"),
			Locations: locs, Evidence: evidence, Confidence: c.Confidence,
			FromType: exp.Type, ToType: dep.Type,
		})
	}

	sort.Slice(exposures, func(i, j int) bool { return exposures[i].ID < exposures[j].ID })
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })

	out := Result{
		Exposures: exposures, Dependencies: dependencies,
		Connections: connections, Unresolved: dedupeUnresolved(unresolved), Warnings: []string{},
	}

	util.Info("agents.orchestrator", "extraction completed", map[string]any{
		"exposures": len(out.Exposures), "dependencies": len(out.Dependencies),
		"connections": len(out.Connections), "unresolved": len(out.Unresolved),
	})
	return out, nil
}

// ---- Fuzzy Entity Resolution ----

func resolveExposure(ref string, byID map[string]model.Exposure) (model.Exposure, bool) {
	if ref == "" {
		return model.Exposure{}, false
	}
	// Exact match on ID or normalized name
	if e, ok := byID[strings.TrimSpace(ref)]; ok {
		return e, true
	}
	if e, ok := byID[normKey(ref)]; ok {
		return e, true
	}
	// Fuzzy: find best substring match
	refNorm := normKey(ref)
	for key, e := range byID {
		if strings.Contains(key, refNorm) || strings.Contains(refNorm, key) {
			return e, true
		}
	}
	// Token overlap match
	refTokens := tokenize(ref)
	bestScore := 0
	var bestExp model.Exposure
	found := false
	for key, e := range byID {
		keyTokens := tokenize(key)
		score := tokenOverlap(refTokens, keyTokens)
		if score > bestScore && score >= 2 {
			bestScore = score
			bestExp = e
			found = true
		}
	}
	return bestExp, found
}

func resolveDependency(ref string, byID map[string]model.Dependency) (model.Dependency, bool) {
	if ref == "" {
		return model.Dependency{}, false
	}
	if d, ok := byID[strings.TrimSpace(ref)]; ok {
		return d, true
	}
	if d, ok := byID[normKey(ref)]; ok {
		return d, true
	}
	refNorm := normKey(ref)
	for key, d := range byID {
		if strings.Contains(key, refNorm) || strings.Contains(refNorm, key) {
			return d, true
		}
	}
	refTokens := tokenize(ref)
	bestScore := 0
	var bestDep model.Dependency
	found := false
	for key, d := range byID {
		keyTokens := tokenize(key)
		score := tokenOverlap(refTokens, keyTokens)
		if score > bestScore && score >= 2 {
			bestScore = score
			bestDep = d
			found = true
		}
	}
	return bestDep, found
}

func normKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '/' || r == ':' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return ' '
	}, s)
	parts := strings.Fields(s)
	return strings.Join(parts, " ")
}

func tokenize(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.Fields(normKey(s)) {
		if len(w) >= 2 {
			out[w] = struct{}{}
		}
	}
	return out
}

func tokenOverlap(a, b map[string]struct{}) int {
	count := 0
	for k := range a {
		if _, ok := b[k]; ok {
			count++
		}
	}
	return count
}

// extractJSONFromText finds the main JSON object in a text response from the LLM.
// It handles preamble text, markdown code fences, and nested JSON.
func extractJSONFromText(text string) map[string]any {
	// Strategy 1: Look for ```json ... ``` blocks
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(text[start:start+end])), &payload); err == nil {
				return payload
			}
		}
	}

	// Strategy 2: Look for ``` ... ``` blocks
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + 3
		if nl := strings.Index(text[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		if end := strings.Index(text[start:], "```"); end >= 0 {
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(text[start:start+end])), &payload); err == nil {
				return payload
			}
		}
	}

	// Strategy 3: Find the LARGEST valid JSON object by trying from each { position
	// The main response JSON is typically the largest one
	var bestPayload map[string]any
	bestLen := 0

	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		// Try to find matching closing brace
		depth := 0
		inString := false
		escaped := false
		for j := i; j < len(text); j++ {
			ch := text[j]
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && inString {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					candidate := text[i : j+1]
					if len(candidate) > bestLen {
						var payload map[string]any
						if err := json.Unmarshal([]byte(candidate), &payload); err == nil {
							// Verify it has our expected keys
							if _, hasExp := payload["exposures"]; hasExp {
								return payload // Found the main response
							}
							if len(candidate) > bestLen {
								bestPayload = payload
								bestLen = len(candidate)
							}
						}
					}
					break
				}
			}
		}
	}

	return bestPayload
}

// detectMonorepo checks if repoPath is a subdirectory inside a larger git repo.
// Returns (sessionDir, subDir) where sessionDir is the git root and subDir is the
// relative path. If repoPath IS the git root, returns (repoPath, "").
func detectMonorepo(repoPath string) (string, string) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return repoPath, ""
	}
	// Check if repoPath itself has .git
	if _, err := os.Stat(filepath.Join(absPath, ".git")); err == nil {
		return repoPath, ""
	}
	// Walk up to find .git
	dir := absPath
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
			// parent is the git root, dir is inside it
			rel, err := filepath.Rel(parent, absPath)
			if err == nil {
				return parent, rel
			}
		}
		dir = parent
	}
	return repoPath, ""
}

// ---- Prompt ----

func buildFullExtractionPrompt() string {
	return `You are an architecture extraction agent. Analyze this codebase and extract a COMPLETE architecture map.

STEPS:
1. Read build file (pom.xml, package.json, go.mod, pyproject.toml) to understand tech stack.
2. Read application config (application.yml/properties) for services, datasources, queues.
3. Read .example/config/ (values.yaml, production/values.yaml, stage/values.yaml) for deployment: ingress, env vars, service URLs, queue names, DB connections.
4. Explore source code for ALL items below.

EXPOSURES (things this service exposes):
- http_route: HTTP endpoints (Spring @RestController/@*Mapping, Express routes, Flask, FastAPI)
- queue_consumer: Message listeners (Spring @SqsListener, @KafkaListener, @RabbitListener, Lambda SQS handler, KCL/Kinesis, boto3 polling)
- scheduled_job: Cron/scheduled jobs (Spring @Scheduled, @SchedulerLock, cron expressions)
- webhook: Incoming webhook/callback endpoints
- cli_command: CLI entrypoints, Lambda handlers, console_scripts

DEPENDENCIES (external systems this service calls):
- db_operation: Database ops (JPA @Repository, Redis/Jedis/RedisTemplate, DynamoDB/DynamoDbTemplate, Elasticsearch, psycopg2, PynamoDB). NOT in-memory caches.
- outbound_http: HTTP calls to other services (Feign @FeignClient, Retrofit @GET/@POST, RestTemplate, WebClient, OkHttp, requests/httpx). Check config for actual target URLs.
- queue_publish: Publish to queues/topics (SQS sendMessage, SNS publish, KafkaTemplate, RabbitTemplate)
- cache_operation: External cache ops (Redis, Memcached). NOT EhCache/Caffeine in-memory only.
- stream_consume: Stream consumers (Kinesis KCL, Kafka Streams, DynamoDB Streams)

IMPORTANT RULES:
- Assign each entity a short unique "id" (e.g., "exp1", "exp2", "dep1", "dep2"). Use these IDs in connections.
- Connections: use the entity "id" field for from_id and to_id. NOT the name.
- Every entity MUST have source_locations with real file paths (relative to repo root) and line numbers.
- Include evidence snippets from actual source code.
- Populate details{} with: method, path, table, operation, queue, topic, target_url, target_service, database_type, schedule, etc.
- Check .example/config/production/values.yaml for actual service URLs, queue names, DB config.
- If a category has nothing, return empty array [].
- Do NOT invent anything not in source code.
- Confidence in [0,1].

OUTPUT FORMAT:
Return ONLY a single JSON object (no markdown, no explanation before/after) with this structure:
` + "`" + `json
{
  "exposures": [
    {"id": "exp1", "type": "http_route", "name": "GET /api/v1/foo", "summary": "...", "confidence": 0.95,
     "details": {"method": "GET", "path": "/api/v1/foo", "controller": "FooController"},
     "source_locations": [{"file": "src/foo.go", "start_line": 10, "end_line": 15}],
     "evidence": [{"file": "src/foo.go", "start_line": 10, "snippet": "func GetFoo()..."}]}
  ],
  "dependencies": [...],
  "connections": [
    {"from_id": "exp1", "to_id": "dep1", "summary": "GetFoo reads from database", "confidence": 0.9,
     "condition": {"kind": "predicate", "expression": "true", "explanation": "always"}}
  ]
}
` + "`" + `
Return the JSON object directly. Do NOT wrap in markdown code fences.`
}

// ---- Schema ----

func fullExtractionSchema() map[string]any {
	entitySchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Short unique local ID (e.g. exp1, dep3)"},
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
						"name": map[string]any{"type": "string"}, "type": map[string]any{"type": "string"},
						"required": map[string]any{"type": "boolean"}, "description": map[string]any{"type": "string"},
					},
				},
			},
			"source_locations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file": map[string]any{"type": "string"}, "start_line": map[string]any{"type": "number"}, "end_line": map[string]any{"type": "number"},
					},
					"required": []string{"file", "start_line"},
				},
			},
			"evidence": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file": map[string]any{"type": "string"}, "start_line": map[string]any{"type": "number"},
						"end_line": map[string]any{"type": "number"}, "snippet": map[string]any{"type": "string"},
					},
					"required": []string{"file", "start_line"},
				},
			},
		},
		"required": []string{"id", "type", "name", "summary", "confidence", "source_locations"},
	}

	connectionSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from_id":    map[string]any{"type": "string", "description": "ID of the exposure entity"},
			"to_id":      map[string]any{"type": "string", "description": "ID of the dependency entity"},
			"summary":    map[string]any{"type": "string"},
			"confidence": map[string]any{"type": "number"},
			"condition": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{"type": "string"}, "expression": map[string]any{"type": "string"}, "explanation": map[string]any{"type": "string"},
				},
			},
		},
		"required": []string{"from_id", "to_id", "summary", "confidence"},
	}

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"exposures":    map[string]any{"type": "array", "items": entitySchema},
			"dependencies": map[string]any{"type": "array", "items": entitySchema},
			"connections":  map[string]any{"type": "array", "items": connectionSchema},
		},
		"required": []string{"exposures", "dependencies", "connections"},
	}
}

// ---- Response Parsing ----

func parseFullResponse(payload map[string]any) llmFullResponse {
	var resp llmFullResponse
	if payload == nil {
		return resp
	}
	resp.Exposures = parseEntities(payload["exposures"])
	resp.Dependencies = parseEntities(payload["dependencies"])
	resp.Connections = parseConnItems(payload["connections"])
	return resp
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
	_ = json.Unmarshal(b, &out)
	return out
}

func parseConnItems(v any) []llmConnection {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []llmConnection
	_ = json.Unmarshal(b, &out)
	return out
}

// ---- Conversion ----

func toBase(repoPath string, kind model.EntityKind, e llmEntity, minConfidence float64) (model.BaseEntity, *model.UnresolvedItem) {
	name := strings.TrimSpace(e.Name)
	typ := strings.TrimSpace(e.Type)
	if name == "" || typ == "" {
		return model.BaseEntity{}, &model.UnresolvedItem{Kind: kind, Type: typ, Name: name, ReasonCode: "invalid_entity", Reason: "Missing name/type"}
	}
	typ = strings.ToLower(strings.ReplaceAll(typ, " ", "_"))
	if e.Confidence < minConfidence {
		return model.BaseEntity{}, &model.UnresolvedItem{Kind: kind, Type: typ, Name: name, ReasonCode: "low_confidence",
			Reason: fmt.Sprintf("Confidence %.2f below threshold %.2f", e.Confidence, minConfidence), Confidence: e.Confidence, Evidence: toEvidence(e.Evidence)}
	}
	locations := toLocations(e.Locations)
	if len(locations) == 0 {
		return model.BaseEntity{}, &model.UnresolvedItem{Kind: kind, Type: typ, Name: name, ReasonCode: "no_source_location",
			Reason: "No source location provided", Confidence: e.Confidence, Evidence: toEvidence(e.Evidence)}
	}
	evidence := toEvidence(e.Evidence)
	if len(evidence) == 0 {
		evidence = append(evidence, model.Evidence{Location: locations[0], Snippet: e.Summary, Source: "opencode"})
	}
	inputs := make([]model.InputSpec, 0, len(e.Inputs))
	for _, in := range e.Inputs {
		if strings.TrimSpace(in.Name) != "" {
			inputs = append(inputs, model.InputSpec{Name: in.Name, Type: in.Type, Required: in.Required, Description: in.Description})
		}
	}
	id := util.StableID(string(kind), typ, name, locations[0].File, fmt.Sprintf("%d:%d", locations[0].StartLine, locations[0].EndLine))
	return model.BaseEntity{
		ID: id, Type: typ, Name: name, Service: repoPath, Inputs: inputs,
		Summary: defaultStr(e.Summary, "Extracted by OpenCode"), KeyActions: e.Actions,
		Locations: locations, Evidence: evidence, Confidence: e.Confidence,
		Tags: e.Tags, Details: e.Details, PluginSource: "opencode",
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

func defaultStr(in, fallback string) string {
	if strings.TrimSpace(in) == "" {
		return fallback
	}
	return in
}

func dedupeUnresolved(in []model.UnresolvedItem) []model.UnresolvedItem {
	seen := map[string]struct{}{}
	out := make([]model.UnresolvedItem, 0)
	for _, u := range in {
		key := string(u.Kind) + "|" + u.Type + "|" + u.Name + "|" + u.ReasonCode
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, u)
	}
	return out
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
