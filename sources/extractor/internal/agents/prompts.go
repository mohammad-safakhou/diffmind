package agents

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// readOnlyPreamble is prepended to every stage prompt. It tells the agent
// that the working directory is a disposable, isolated copy and that it
// MUST NOT modify it. We cannot enforce this from the client side, but
// OpenCode follows clear instructions and this dramatically reduces
// accidental edits between concurrent workers.
const readOnlyPreamble = `OPERATIONAL RULES (apply to the entire response):
- The working directory is an isolated, read-only analysis snapshot. Do NOT
  edit, create, rename, chmod, delete, or move any file. Do NOT run shell
  commands that mutate state (no installs, no formatters, no codegen).
- Your job is to READ source code and return a structured JSON answer.
- All file paths in your answer MUST be paths relative to the working
  directory (no leading slash, no temp-dir prefix). Use forward slashes.

`

// ---- Stage 0 ----

func buildRepoFactsPrompt(subDir string) string {
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: repo-facts\n")
	sb.WriteString(readOnlyPreamble)
	sb.WriteString("TASK: Produce a compact, evidence-backed snapshot of this repository so that\n")
	sb.WriteString("downstream extraction agents do not have to re-discover the tech stack.\n\n")
	if subDir != "" {
		sb.WriteString("IMPORTANT: This is a monorepo. ONLY analyze files under the '")
		sb.WriteString(subDir)
		sb.WriteString("/' subdirectory. All file paths in your answer must be relative to the repo root and start with '")
		sb.WriteString(subDir)
		sb.WriteString("/'.\n\n")
	}
	sb.WriteString(`STEPS:
1. Read build files that exist (pom.xml, build.gradle, package.json, go.mod, pyproject.toml, requirements.txt, setup.py, Cargo.toml).
2. Read application config (application.yml, application.properties, application-*.yml) if present.
3. Read deployment/infrastructure config (.example/config/values.yaml, .example/config/production/values.yaml, .example/config/stage/values.yaml, Chart.yaml, helm values, serverless.yml, template.yaml).
4. Identify declared languages, frameworks, main module layout, and service name.
5. List environment-specific URLs, queue names, DB config you observe for downstream agents.

RULES:
- Do NOT invent. Only list what you can confirm by reading a file.
- Return an empty array for any category that is not present.
- Keep every list short (max 25 items). Prefer the most representative entries.
- "probable_tech_hints" should capture cues like: "Spring Boot", "@SqsListener present", "Retrofit interface used", "DynamoDBMapper usage", "Redis via Jedis", "AWS Lambda handler".
- "deployment_hints" captures things like "ingress host X", "SQS queue example-foo-prod", "DB host X", "Feature flag X".

OUTPUT: Return a single JSON object matching the provided schema.`)
	return sb.String()
}

// ---- Shared REPO_FACTS injection ----

func repoFactsBlock(rf *repoFacts) string {
	if rf == nil {
		return ""
	}
	b, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return ""
	}
	return "REPO_FACTS:\n" + string(b) + "\n\n"
}

func monorepoScopeLine(subDir string) string {
	if subDir == "" {
		return ""
	}
	return fmt.Sprintf("IMPORTANT: This is a monorepo. ONLY analyze files under '%s/'. All source_locations MUST be relative to repo root and start with '%s/'.\n\n", subDir, subDir)
}

// ---- Stage 1: per-objective discovery ----

func buildDiscoveryPrompt(obj objectives.Objective, rf *repoFacts, subDir string) string {
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: objective-extractor\n")
	sb.WriteString(readOnlyPreamble)
	sb.WriteString("OBJECTIVE_ID: ")
	sb.WriteString(obj.ID)
	sb.WriteString("\n")
	sb.WriteString("OBJECTIVE_KIND: ")
	sb.WriteString(string(obj.Kind))
	sb.WriteString("\n")
	sb.WriteString("OBJECTIVE_TYPE: ")
	sb.WriteString(obj.Type)
	sb.WriteString("\n")
	sb.WriteString("DESCRIPTION: ")
	sb.WriteString(obj.Description)
	sb.WriteString("\n\n")
	sb.WriteString(monorepoScopeLine(subDir))
	sb.WriteString(repoFactsBlock(rf))
	sb.WriteString("DISCOVERY INSTRUCTIONS:\n")
	sb.WriteString(obj.DiscoveryPrompt)
	sb.WriteString("\n\n")
	sb.WriteString(`HARD RULES:
- Every item MUST have name, type, summary, confidence in [0,1], and at least one source_locations entry with file + start_line.
- Every file path MUST be relative to repo root.
- If nothing matches, return {"items": []}.
- Do NOT include items from unrelated categories. This agent is scoped to the objective above.
- Confidence reflects your certainty the item is real and of this objective's type.

OUTPUT: Return a single JSON object {"items": [...]} matching the provided schema.`)
	return sb.String()
}

// ---- Stage 2: re-examination ----

func buildReexaminePrompt(obj objectives.Objective, seed llmEntity, triggerReason string, rf *repoFacts, subDir string) string {
	seedJSON, _ := json.MarshalIndent(seed, "", "  ")
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: reexaminer\n")
	sb.WriteString(readOnlyPreamble)
	sb.WriteString("OBJECTIVE_ID: ")
	sb.WriteString(obj.ID)
	sb.WriteString("\n")
	sb.WriteString("OBJECTIVE_KIND: ")
	sb.WriteString(string(obj.Kind))
	sb.WriteString("\n")
	sb.WriteString("OBJECTIVE_TYPE: ")
	sb.WriteString(obj.Type)
	sb.WriteString("\n")
	sb.WriteString("TRIGGER_REASON: ")
	sb.WriteString(triggerReason)
	sb.WriteString("\n\n")
	sb.WriteString(monorepoScopeLine(subDir))
	sb.WriteString(repoFactsBlock(rf))
	sb.WriteString("CANDIDATE_ITEM:\n")
	sb.Write(seedJSON)
	sb.WriteString("\n\n")
	sb.WriteString(`TASK:
A prior extraction produced this candidate but it is suspect (low confidence, missing source location, or missing required detail fields).
Open the referenced files (or nearby files) and decide:
1. Is this candidate a REAL `)
	sb.WriteString(obj.Type)
	sb.WriteString(` in this repository?
2. If yes, return a corrected version of the item with:
   - accurate source_locations (file + start_line + end_line)
   - confidence honestly reflecting what you verified
   - populated details{} (e.g. method/path for http_route, table/operation for db_operation, queue/topic for queue_* items, etc.)
   - at least one evidence snippet
3. If the candidate is NOT real (hallucination or misclassification), return {"items": []}.

OUTPUT: Return {"items": [correctedItem]} on confirmation, or {"items": []} on rejection.`)
	return sb.String()
}

// ---- Stage 3: detail enrichment ----

func buildDetailPrompt(obj objectives.Objective, seed llmEntity, rf *repoFacts, subDir string) string {
	seedJSON, _ := json.MarshalIndent(seed, "", "  ")
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: detail-extractor\n")
	sb.WriteString(readOnlyPreamble)
	sb.WriteString("OBJECTIVE_ID: ")
	sb.WriteString(obj.ID)
	sb.WriteString("\n")
	sb.WriteString("OBJECTIVE_KIND: ")
	sb.WriteString(string(obj.Kind))
	sb.WriteString("\n")
	sb.WriteString("OBJECTIVE_TYPE: ")
	sb.WriteString(obj.Type)
	sb.WriteString("\n\n")
	sb.WriteString(monorepoScopeLine(subDir))
	sb.WriteString(repoFactsBlock(rf))
	sb.WriteString("SEED_ITEM:\n")
	sb.Write(seedJSON)
	sb.WriteString("\n\n")
	sb.WriteString("DETAIL INSTRUCTIONS:\n")
	sb.WriteString(obj.DetailPrompt)
	sb.WriteString("\n\n")
	sb.WriteString(`HARD RULES:
- Open the seed's source_locations and surrounding files. Produce a single, enriched item.
- Preserve the seed's type and name (correct if clearly wrong, but do not change the underlying entity).
- Return rich details{} (method/path, table/operation, queue, topic, target_url, schedule, batch, auth, transaction, etc.).
- Include key_actions in execution order.
- Append additional source_locations if the entity spans multiple files or ranges.
- Keep at least one evidence snippet with a real code excerpt.
- confidence reflects how well you could confirm the details from code.

OUTPUT: Return a single JSON object {"item": {...}} matching the provided schema. If the candidate cannot be confirmed at all, return {"item": null}.`)
	return sb.String()
}

// ---- Stage 4: connection mapping ----

type connectionCatalogItem struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Summary  string         `json:"summary"`
	Details  map[string]any `json:"details,omitempty"`
	Location string         `json:"location,omitempty"`
}

func buildConnectionPrompt(
	obj objectives.Objective,
	exposure connectionCatalogItem,
	catalog []connectionCatalogItem,
	batchIndex, batchCount int,
	rf *repoFacts,
	subDir string,
) string {
	expJSON, _ := json.MarshalIndent(exposure, "", "  ")
	catJSON, _ := json.MarshalIndent(catalog, "", "  ")
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: connection-extractor\n")
	sb.WriteString(readOnlyPreamble)
	sb.WriteString("OBJECTIVE_ID: ")
	sb.WriteString(obj.ID)
	sb.WriteString("\n")
	sb.WriteString("OBJECTIVE_KIND: exposure\n")
	sb.WriteString("OBJECTIVE_TYPE: ")
	sb.WriteString(obj.Type)
	sb.WriteString("\n")
	sb.WriteString("EXPOSURE_ID: ")
	sb.WriteString(exposure.ID)
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("BATCH: %d/%d\n\n", batchIndex, batchCount))
	sb.WriteString(monorepoScopeLine(subDir))
	sb.WriteString(repoFactsBlock(rf))
	sb.WriteString("EXPOSURE:\n")
	sb.Write(expJSON)
	sb.WriteString("\n\n")
	sb.WriteString("DEPENDENCY_CATALOG (closed set - you MUST pick to_dependency_id from these IDs):\n")
	sb.Write(catJSON)
	sb.WriteString("\n\n")
	sb.WriteString("CONNECTION CONTEXT:\n")
	sb.WriteString(obj.ConnectionContext)
	sb.WriteString("\n\n")
	sb.WriteString(`TASK:
Trace the execution paths from EXPOSURE's handler through the codebase and identify
which DEPENDENCY_CATALOG entries are actually invoked. For each invoked dependency,
produce ONE connection entry with ordered steps and the condition under which the
dependency is reached.

HARD RULES:
- from_exposure_id MUST equal the EXPOSURE_ID above.
- to_dependency_id MUST be one of the id values from DEPENDENCY_CATALOG. If the real
  dependency is not in the catalog (e.g. out of this batch), OMIT the connection.
- If the exposure does not call any dependency in this batch, return {"items": []}.
- Every connection MUST include a condition (kind, expression, explanation).
- Prefer ordered steps with file+line locations where the call actually happens.
- confidence in [0,1] reflects how strongly the call path is evidenced in source.

OUTPUT: Return a single JSON object {"items": [...]} matching the provided schema.`)
	return sb.String()
}
