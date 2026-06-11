// Package repofacts implements the first extraction stage.
package repofacts

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/langdetect"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type PromptFunc func(ctx context.Context, role, prompt string, schema map[string]any) (map[string]any, error)

type Runner struct {
	Prompt PromptFunc
}

type Input struct {
	SubDir     string
	SessionDir string
}

type Output struct {
	Facts *extraction.RepoFacts
}

func (r Runner) Run(ctx context.Context, input Input) (Output, error) {
	payload, err := r.Prompt(ctx, "repo_facts", BuildPrompt(input.SubDir), Schema())
	if err != nil {
		util.Warn("agents.repo_facts", "repo facts extraction failed", map[string]any{"error": err})
		return Output{}, err
	}
	facts := Parse(payload)
	if facts == nil {
		return Output{}, nil
	}

	detected, detectErr := langdetect.Inspect(ctx, input.SessionDir)
	if detectErr != nil {
		util.Warn("agents.repo_facts", "marker-file language detection failed", map[string]any{"error": detectErr})
	} else {
		for _, fact := range detected {
			facts.LanguageFacts = append(facts.LanguageFacts, extraction.LanguageFact{
				Language:         string(fact.Language),
				Version:          fact.Version,
				BuildTool:        fact.BuildTool,
				BuildToolVersion: fact.BuildToolVersion,
				Sources:          fact.Sources,
			})
		}
	}
	util.Info("agents.repo_facts", "repo facts gathered", map[string]any{
		"languages":      len(facts.Languages),
		"frameworks":     len(facts.Frameworks),
		"build_files":    len(facts.BuildFiles),
		"config_files":   len(facts.ConfigFiles),
		"language_facts": len(facts.LanguageFacts),
	})
	return Output{Facts: facts}, nil
}

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

func BuildPrompt(subDir string) string {
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

func Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"service_name":        map[string]any{"type": "string"},
			"languages":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"frameworks":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"build_files":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"config_files":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"monorepo_subdir":     map[string]any{"type": "string"},
			"probable_tech_hints": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"deployment_hints":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"extra_observations":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"module_map": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string"},
						"purpose": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

func Parse(value map[string]any) *extraction.RepoFacts {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var facts extraction.RepoFacts
	if err := json.Unmarshal(data, &facts); err != nil {
		return nil
	}
	return &facts
}
