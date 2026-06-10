package core

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
- Do NOT delegate to subagents or use a task tool; inspect directly with
  read, glob, grep, and other direct read-only tools.
- All file paths in your answer MUST be paths relative to the working
  directory (no leading slash, no temp-dir prefix). Use forward slashes.

`

// ---- Stage 0 ----

func BuildRepoFactsPrompt(subDir string) string {
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
3. Read deployment/infrastructure config (helm values, Chart.yaml, serverless.yml, template.yaml, any *values.yaml or config/*.yaml in the repo).
4. Identify declared languages, frameworks, main module layout, and service name.
5. List environment-specific URLs, queue names, DB config you observe for downstream agents.

RULES:
- Do NOT invent. Only list what you can confirm by reading a file.
- Return an empty array for any category that is not present.
- Keep every list short (max 25 items). Prefer the most representative entries.
- "probable_tech_hints" should capture cues like: "Spring Boot", "@SqsListener present", "Retrofit interface used", "DynamoDBMapper usage", "Redis via Jedis", "AWS Lambda handler".
- "deployment_hints" captures things like "ingress host X", "SQS queue my-service-prod", "DB host X", "Feature flag X".

OUTPUT: Return a single JSON object matching the provided schema.`)
	return sb.String()
}

// ---- Shared REPO_FACTS injection ----

func RepoFactsBlock(rf *RepoFacts) string {
	if rf == nil {
		return ""
	}
	b, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return ""
	}
	return "REPO_FACTS:\n" + string(b) + "\n\n"
}

func MonorepoScopeLine(subDir string) string {
	if subDir == "" {
		return ""
	}
	return fmt.Sprintf("IMPORTANT: This is a monorepo. ONLY analyze files under '%s/'. All source_locations MUST be relative to repo root and start with '%s/'.\n\n", subDir, subDir)
}

// ---- Shared AST_HINTS injection (grounding) ----

// astHintsBlock renders the deterministic AST candidates for an objective as
// an ADVISORY prompt section. Returns "" when there is nothing to show, so a
// nil/empty index (or DiscoveryASTHints=false, which yields empty hints)
// produces byte-identical prompts to the pre-grounding behaviour.
//
// The header is emphatic that the list is NOT a whitelist: the static index
// misses reflection / dynamic registration / custom frameworks, so the model
// MUST still search the code for anything not listed.
func AstHintsBlock(h ObjectiveHints) string {
	if h.Empty() {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("AST_HINTS (deterministic static-analysis candidates — these are HINTS, NOT a whitelist;\n")
	sb.WriteString("the index can miss reflection, dynamic registration, and custom frameworks, so you MUST\n")
	sb.WriteString("still search the code for anything not listed here, and you MUST drop any listed item you\n")
	sb.WriteString("cannot confirm in source):\n")
	if len(h.Symbols) > 0 {
		sb.WriteString("  CANDIDATE_SYMBOLS (file:line  annotations):\n")
		for _, s := range h.Symbols {
			sb.WriteString("    ")
			sb.WriteString(s.File)
			sb.WriteString(":")
			sb.WriteString(Itoa(int(s.Line)))
			sb.WriteString("  ")
			sb.WriteString(s.Qualified)
			if len(s.Annotations) > 0 {
				sb.WriteString("  [")
				sb.WriteString(strings.Join(s.Annotations, ","))
				sb.WriteString("]")
			}
			sb.WriteString("\n")
		}
	}
	if len(h.Bindings) > 0 {
		sb.WriteString("  FRAMEWORK_BINDINGS (kind  symbol  trigger  file:line):\n")
		for _, b := range h.Bindings {
			sb.WriteString("    ")
			sb.WriteString(b.Kind)
			sb.WriteString("  ")
			sb.WriteString(b.Symbol)
			sb.WriteString("  ")
			sb.WriteString(b.Trigger)
			sb.WriteString("  ")
			sb.WriteString(b.File)
			sb.WriteString(":")
			sb.WriteString(Itoa(int(b.Line)))
			sb.WriteString("\n")
		}
	}
	if len(h.Configs) > 0 {
		sb.WriteString("  CONFIG_ENTRIES (file  key=value):\n")
		for _, c := range h.Configs {
			sb.WriteString("    ")
			sb.WriteString(c.File)
			sb.WriteString("  ")
			sb.WriteString(c.Key)
			sb.WriteString("=")
			sb.WriteString(c.Value)
			sb.WriteString("\n")
		}
	}
	if h.Truncated {
		sb.WriteString("  [note: candidate list truncated; more exist — search beyond this list]\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// discoveryScopeBlock renders the shard SCOPE directive when discovery for an
// objective has been split into directory-scoped sub-tasks (Phase B). It tells
// the model to restrict its search to the listed directories so shards don't
// re-scan the whole repo. Empty scope (single whole-repo call) → "".
func DiscoveryScopeBlock(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("SCOPE: This is one shard of a larger discovery. The static index found this\n")
	sb.WriteString("objective's candidate declarations concentrated in these directories:\n")
	for _, d := range dirs {
		sb.WriteString("  - ")
		sb.WriteString(d)
		sb.WriteString("/\n")
	}
	sb.WriteString("FOCUS your search here and report ONLY items whose declaration lives under\n")
	sb.WriteString("these directories — another shard reports the rest. You MAY freely open and\n")
	sb.WriteString("read code ANYWHERE in the repo (shared base classes, configuration, imported\n")
	sb.WriteString("helpers) to understand those items; restrict only what you REPORT, never what\n")
	sb.WriteString("you READ.\n\n")
	return sb.String()
}

// exampleBlock renders the objective's few-shot example, if any.
func ExampleBlock(obj objectives.Objective) string {
	if strings.TrimSpace(obj.Example) == "" {
		return ""
	}
	return "GOOD_EXAMPLE (one well-formed item; match this shape, not its specific values):\n" + obj.Example + "\n\n"
}

// detailKeysLine renders the required-detail-keys hint, if any.
func DetailKeysLine(obj objectives.Objective) string {
	if len(obj.DetailKeys) == 0 {
		return ""
	}
	return "REQUIRED_DETAIL_KEYS: populate details{} with at least these keys when determinable from code: " + strings.Join(obj.DetailKeys, ", ") + "\n\n"
}

// ---- Language scoping of discovery prompts ----
//
// Objective discovery prompts carry FRAMEWORK-SPECIFIC PATTERNS lists that
// enumerate cues across many languages (Spring, Flask, Express, gin, ...).
// On a single-language repo most of those lines are noise: they waste prompt
// tokens and — worse for a smaller model — prime it to hunt for constructs
// that cannot exist here, inviting misclassification. repo_facts already
// tells us the languages in play, so we drop bullet lines that are explicitly
// labelled with a language the repo does not use.
//
// Only lines whose leading label ("- <Label>: ...") names a PROGRAMMING
// LANGUAGE are eligible for removal. Framework-labelled lines (Spring, Redis,
// AWS SQS, ...) and prose are always kept — a framework can be cross-cutting
// and we never want to over-trim. When the language set is unknown (nil/empty)
// nothing is filtered, so behaviour is identical to the pre-scoping prompt.

// promptLabelLanguages maps a discovery-prompt bullet label (the token before
// the first colon, lower-cased) to the canonical language(s) that satisfy it.
// A label absent from this map is treated as framework/agnostic and kept.
var promptLabelLanguages = map[string][]string{
	"python":  {"python"},
	"node.js": {"javascript", "typescript"},
	"nodejs":  {"javascript", "typescript"},
	"node":    {"javascript", "typescript"},
	"go":      {"go"},
	"golang":  {"go"},
	"java":    {"java", "kotlin"},
}

// detectedLanguageSet canonicalises the repo's languages (from both the LLM
// repo_facts and the deterministic marker-file facts) into a lower-cased set.
// Returns nil when nothing is known, signalling callers to skip filtering.
func DetectedLanguageSet(rf *RepoFacts) map[string]bool {
	if rf == nil {
		return nil
	}
	set := map[string]bool{}
	add := func(s string) {
		c := CanonicalLanguage(s)
		if c != "" {
			set[c] = true
		}
	}
	for _, l := range rf.Languages {
		add(l)
	}
	for _, f := range rf.LanguageFacts {
		add(f.Language)
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// canonicalLanguage normalises a free-form language name to a stable key.
// Non-language entries (XML, YAML, JSON, ...) map to "" and are ignored, so
// they never accidentally widen or narrow the detected set.
func CanonicalLanguage(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "java":
		return "java"
	case "kotlin", "kt":
		return "kotlin"
	case "python", "py":
		return "python"
	case "javascript", "js", "node", "node.js", "nodejs":
		return "javascript"
	case "typescript", "ts":
		return "typescript"
	case "go", "golang":
		return "go"
	case "ruby":
		return "ruby"
	case "c#", "csharp", ".net", "dotnet":
		return "csharp"
	case "php":
		return "php"
	case "rust":
		return "rust"
	default:
		return ""
	}
}

// scopeFrameworkPatterns drops language-labelled bullet lines for languages the
// repo does not use. langs==nil (unknown) returns the prompt unchanged.
func ScopeFrameworkPatterns(prompt string, langs map[string]bool) string {
	if len(langs) == 0 {
		return prompt
	}
	lines := strings.Split(prompt, "\n")
	out := lines[:0]
	for _, line := range lines {
		if want, ok := BulletLanguages(line); ok {
			keep := false
			for _, l := range want {
				if langs[l] {
					keep = true
					break
				}
			}
			if !keep {
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// bulletLanguages inspects a line of the form "- <Label>: ..." and, when Label
// names a programming language, returns the canonical languages that satisfy
// it. ok=false means the line is not a language-labelled bullet and must be
// kept verbatim.
func BulletLanguages(line string) (langs []string, ok bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "- ") {
		return nil, false
	}
	t = strings.TrimSpace(t[2:])
	colon := strings.Index(t, ":")
	if colon <= 0 {
		return nil, false
	}
	label := strings.ToLower(strings.TrimSpace(t[:colon]))
	got, found := promptLabelLanguages[label]
	if !found {
		return nil, false
	}
	return got, true
}

// ---- Stage 1: per-objective discovery ----

func BuildDiscoveryPrompt(obj objectives.Objective, rf *RepoFacts, subDir string, hints ObjectiveHints, scopeDirs []string, confirmed []LLMEntity) string {
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
	sb.WriteString(MonorepoScopeLine(subDir))
	sb.WriteString(DiscoveryScopeBlock(scopeDirs))
	sb.WriteString(RepoFactsBlock(rf))
	sb.WriteString(AstHintsBlock(hints))
	sb.WriteString(ConfirmedDiscoveryBlock(confirmed))
	sb.WriteString("DISCOVERY INSTRUCTIONS:\n")
	sb.WriteString(ScopeFrameworkPatterns(obj.DiscoveryPrompt, DetectedLanguageSet(rf)))
	sb.WriteString("\n\n")
	sb.WriteString(ExampleBlock(obj))
	sb.WriteString(DetailKeysLine(obj))
	sb.WriteString(`HARD RULES:
- Every item MUST have name, type, summary, confidence in [0,1], and at least one source_locations entry with file + start_line. start_line MUST be the exact declaration line; do not approximate.
- Every file path MUST be relative to repo root.
- If nothing matches, return {"items": []}.
- Do NOT include items from unrelated categories. This agent is scoped to the objective above.
- Confidence reflects your certainty the item is real and of this objective's type.

OUTPUT: Return a single JSON object {"items": [...]} matching the provided schema.`)
	return sb.String()
}

func ConfirmedDiscoveryBlock(items []LLMEntity) string {
	if len(items) == 0 {
		return ""
	}
	const max = 80
	var sb strings.Builder
	sb.WriteString("KNOWN_CONFIRMED_ITEMS:\n")
	limit := len(items)
	if limit > max {
		limit = max
	}
	for i := 0; i < limit; i++ {
		it := items[i]
		loc := ""
		if len(it.Locations) > 0 {
			loc = fmt.Sprintf(" at %s:%d", it.Locations[0].File, it.Locations[0].StartLine)
		}
		sb.WriteString("- ")
		sb.WriteString(strings.TrimSpace(it.Name))
		sb.WriteString(loc)
		sb.WriteString("\n")
	}
	if len(items) > limit {
		sb.WriteString("- ... ")
		sb.WriteString(Itoa(len(items) - limit))
		sb.WriteString(" more confirmed items omitted\n")
	}
	sb.WriteString("\nDo not rediscover known confirmed items above. Search for additional custom, dynamic, or non-standard items not covered by the confirmed list.\n\n")
	return sb.String()
}

// ---- Stage 2: re-examination ----

func BuildReexaminePrompt(obj objectives.Objective, seed LLMEntity, triggerReason string, rf *RepoFacts, subDir string, hints ObjectiveHints) string {
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
	sb.WriteString(MonorepoScopeLine(subDir))
	sb.WriteString(RepoFactsBlock(rf))
	sb.WriteString(AstHintsBlock(hints))
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

func BuildDetailPrompt(obj objectives.Objective, seed LLMEntity, rf *RepoFacts, subDir string, hints ObjectiveHints) string {
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
	sb.WriteString(MonorepoScopeLine(subDir))
	sb.WriteString(RepoFactsBlock(rf))
	sb.WriteString(AstHintsBlock(hints))
	sb.WriteString(DetailKeysLine(obj))
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

// buildDetailBatchPrompt produces the multi-entity variant of the
// detail prompt: instead of asking the model to enrich ONE seed,
// it asks for several at once. The seeds in a batch are
// objective-homogenous (same kind+type) and selected by file/name
// affinity (see grouping.go), so the model can read each shared
// source file once and answer N entities from it.
//
// The prompt also lists the unique source files referenced by the
// batch's seeds upfront, so the model has a hint about which files
// to open with its first tool call. We do NOT pre-read them
// ourselves — that would make the prompt huge and unbounded; the
// agent decides when each file is worth opening.
//
// Failure-tolerance: the model is told to return ONE item per input
// seed in the same order, EVEN IF it can't fully enrich one
// (return the seed unchanged with details_complete:false). This is
// what lets the parser distinguish "model dropped this entity" from
// "model deliberately marked it incomplete" — the orchestrator
// treats the former as an error (re-batch later) and the latter as
// a normal "not enough info" outcome.
func BuildDetailBatchPrompt(obj objectives.Objective, seeds []LLMEntity, rf *RepoFacts, subDir string, hints ObjectiveHints) string {
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: detail-extractor (BATCH)\n")
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
	sb.WriteString(MonorepoScopeLine(subDir))
	sb.WriteString(RepoFactsBlock(rf))
	sb.WriteString(AstHintsBlock(hints))
	sb.WriteString(DetailKeysLine(obj))

	// Collect the unique source files this batch points at. The
	// affinity grouper picks seeds that share files, so this list
	// is short (typically 1-5 paths). Tell the model these are the
	// files most worth opening first.
	fileSet := map[string]struct{}{}
	var fileList []string
	for _, s := range seeds {
		for _, loc := range s.Locations {
			if loc.File == "" {
				continue
			}
			if _, seen := fileSet[loc.File]; !seen {
				fileSet[loc.File] = struct{}{}
				fileList = append(fileList, loc.File)
			}
		}
	}
	if len(fileList) > 0 {
		sb.WriteString("RELATED_FILES (open these once; many seeds reference them):\n")
		for _, f := range fileList {
			sb.WriteString("  - ")
			sb.WriteString(f)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("SEEDS (")
	sb.WriteString(Itoa(len(seeds)))
	sb.WriteString("):\n")
	seedsJSON, _ := json.MarshalIndent(seeds, "", "  ")
	sb.Write(seedsJSON)
	sb.WriteString("\n\n")

	sb.WriteString("DETAIL INSTRUCTIONS (apply to EACH seed):\n")
	sb.WriteString(obj.DetailPrompt)
	sb.WriteString("\n\n")

	sb.WriteString(`HARD RULES:
- Open each seed's source_locations + the RELATED_FILES list. Where multiple seeds reference the same file, use ONE read to answer all of them.
- For EVERY input seed produce EXACTLY one output item, in the same order.
- Preserve the seed's type and name (correct if clearly wrong, but do not change the underlying entity).
- Return rich details{} per item (method/path, table/operation, queue, topic, target_url, schedule, batch, auth, transaction, etc.).
- Include key_actions in execution order.
- Keep at least one evidence snippet per item with a real code excerpt.
- confidence reflects how well you could confirm the details from code.
- If you genuinely cannot enrich a particular seed (file unreadable, ambiguous context, etc.), STILL return it in the output array — copy the seed as-is, set confidence to the seed's original value, and add "details_complete": false in details. NEVER drop a seed from the output.

OUTPUT: a single JSON object {"items": [<one entry per input seed, same order>]} matching the provided schema. Length of items MUST equal the number of input seeds.`)
	return sb.String()
}

// itoa is a tiny helper so prompts.go does not need to pull in
// strconv just to render a count.
func Itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---- Stage 4: connection mapping ----
//
// The connections stage is now deterministic and SCIP-driven. No LLM
// prompt is built or sent. The old buildConnectionPrompt /
// connectionCatalogItem types have been removed. See
// internal/agents/connections.go for the new pipeline and
// internal/scip/ for the underlying call-graph walker.
