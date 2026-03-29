// Package interview implements the AI-driven DevOps interview that
// generates extraction blueprints by asking operators about their
// infrastructure patterns.
package interview

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/blueprints"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Interviewer runs the interactive DevOps interview.
type Interviewer struct {
	client    *opencode.Client
	log       *util.Logger
	repoPaths []string // all repo paths to scan
	outputDir string   // where to write generated blueprints
	reader    io.Reader
	writer    io.Writer
}

// NewInterviewer creates a new interviewer.
func NewInterviewer(client *opencode.Client, log *util.Logger, repoPaths []string, outputDir string) *Interviewer {
	return &Interviewer{
		client:    client,
		log:       log,
		repoPaths: repoPaths,
		outputDir: outputDir,
		reader:    os.Stdin,
		writer:    os.Stdout,
	}
}

// Run starts the interactive interview.
func (iv *Interviewer) Run() ([]*blueprints.Blueprint, error) {
	// Step 1: Scan repos for common patterns.
	patterns := iv.scanPatterns()

	fmt.Fprintln(iv.writer, "\n=== DiffMind Infrastructure Interview ===")
	fmt.Fprintf(iv.writer, "I found %d repositories to analyze.\n\n", len(iv.repoPaths))

	if len(patterns) > 0 {
		fmt.Fprintln(iv.writer, "I noticed the following patterns across your repos:")
		for _, p := range patterns {
			fmt.Fprintf(iv.writer, "  - %s (found in %d repos)\n", p.Description, p.Count)
		}
		fmt.Fprintln(iv.writer)
	}

	// Step 2: Use LLM to generate questions based on discovered patterns.
	if iv.client == nil {
		return iv.generateBlueprintsWithoutLLM(patterns)
	}

	session, err := iv.client.CreateSession(".")
	if err != nil {
		iv.log.Warn("could not create session for interview, using pattern-based generation", "error", err.Error())
		return iv.generateBlueprintsWithoutLLM(patterns)
	}
	defer iv.client.DeleteSession(session.ID)

	return iv.runInteractiveInterview(session.ID, patterns)
}

// pattern represents a discovered infrastructure pattern.
type pattern struct {
	Path        string
	Description string
	Count       int
	Examples    []string // repos where this was found
}

func (iv *Interviewer) scanPatterns() []pattern {
	// Common infrastructure patterns to look for.
	checks := []struct {
		glob string
		desc string
	}{
		{".example/*/values.yaml", "Example Helm values"},
		{"helm/*/values.yaml", "Helm chart values"},
		{"chart/values.yaml", "Helm chart values"},
		{"deploy/*/values.yaml", "Deployment values"},
		{"k8s/*.yaml", "Kubernetes manifests"},
		{"kubernetes/*.yaml", "Kubernetes manifests"},
		{"docker-compose*.yml", "Docker Compose files"},
		{"docker-compose*.yaml", "Docker Compose files"},
		{"Dockerfile", "Dockerfiles"},
		{"terraform/*.tf", "Terraform configs"},
		{"modules/*/*.tf", "Terraform modules"},
		{".github/workflows/*.yml", "GitHub Actions workflows"},
		{"Jenkinsfile", "Jenkins pipelines"},
		{"serverless.yml", "Serverless Framework"},
		{"serverless.yaml", "Serverless Framework"},
		{"pom.xml", "Maven projects"},
		{"build.gradle", "Gradle projects"},
		{"package.json", "Node.js projects"},
		{"go.mod", "Go modules"},
		{"application.yml", "Spring Boot config"},
		{"application.yaml", "Spring Boot config"},
	}

	var patterns []pattern
	for _, check := range checks {
		var count int
		var examples []string
		for _, repoPath := range iv.repoPaths {
			matches, err := blueprints.ResolveGlob(repoPath, check.glob)
			if err != nil || len(matches) == 0 {
				continue
			}
			count++
			if len(examples) < 3 {
				examples = append(examples, filepath.Base(repoPath))
			}
		}
		if count > 0 {
			patterns = append(patterns, pattern{
				Path:        check.glob,
				Description: check.desc,
				Count:       count,
				Examples:    examples,
			})
		}
	}
	return patterns
}

func (iv *Interviewer) runInteractiveInterview(sessionID string, patterns []pattern) ([]*blueprints.Blueprint, error) {
	scanner := bufio.NewScanner(iv.reader)

	// Build context for the LLM.
	var sb strings.Builder
	sb.WriteString("You are helping generate extraction blueprints for DiffMind, a cross-service dependency mapper.\n\n")
	sb.WriteString("The following infrastructure patterns were found across the service repos:\n")
	for _, p := range patterns {
		sb.WriteString(fmt.Sprintf("- %s (%s) found in %d repos (e.g. %s)\n", p.Path, p.Description, p.Count, strings.Join(p.Examples, ", ")))
	}
	sb.WriteString("\nAsk the DevOps engineer 3-5 targeted questions to understand:\n")
	sb.WriteString("1. How services are identified in their network (DNS, IAM roles, service names)\n")
	sb.WriteString("2. Where routing/proxy configs live (nginx, Istio, API gateway)\n")
	sb.WriteString("3. How queues/databases are named and which services own them\n")
	sb.WriteString("4. Any naming conventions for infra resources\n")
	sb.WriteString("\nReturn questions as a JSON array of strings. Return ONLY the JSON array.")

	questionsRaw, err := iv.client.Prompt(sessionID, sb.String())
	if err != nil {
		iv.log.Error("failed to generate interview questions", "error", err.Error())
		return iv.generateBlueprintsWithoutLLM(patterns)
	}

	var questions []string
	if err := json.Unmarshal([]byte(questionsRaw), &questions); err != nil {
		// Try to extract from text.
		questions = extractQuestions(questionsRaw)
	}

	// Ask questions and collect answers.
	var answers []string
	for i, q := range questions {
		fmt.Fprintf(iv.writer, "\nQ%d: %s\n> ", i+1, q)
		if scanner.Scan() {
			answers = append(answers, scanner.Text())
		}
	}

	// Generate blueprints from answers.
	return iv.generateBlueprintsFromAnswers(sessionID, patterns, questions, answers)
}

func (iv *Interviewer) generateBlueprintsFromAnswers(sessionID string, patterns []pattern, questions, answers []string) ([]*blueprints.Blueprint, error) {
	var sb strings.Builder
	sb.WriteString("Based on the DevOps engineer's answers, generate extraction blueprints as JSON.\n\n")
	sb.WriteString("Patterns found:\n")
	for _, p := range patterns {
		sb.WriteString(fmt.Sprintf("- %s (%s)\n", p.Path, p.Description))
	}
	sb.WriteString("\nQ&A:\n")
	for i := range questions {
		sb.WriteString(fmt.Sprintf("Q: %s\n", questions[i]))
		if i < len(answers) {
			sb.WriteString(fmt.Sprintf("A: %s\n\n", answers[i]))
		}
	}
	sb.WriteString(`
Generate a JSON array of blueprint objects. Each blueprint has:
{
  "name": "string",
  "description": "string",
  "version": "v1",
  "applies_to": {"kind": "service_repo|infra_repo|any", "match": {"has_path": "path"}},
  "extractions": [{"name": "string", "description": "string", "source": {"glob": "pattern"}, "strategy": "field_path|regex|llm", "prompt_hint": "optional", "extract": [{"field": "dotted.path", "pattern": "regex", "maps_to": "service_name|dns_aliases|iam_role|database_connection|queue_identifiers|http_paths"}]}]
}

Return ONLY valid JSON array of blueprints.`)

	raw, err := iv.client.Prompt(sessionID, sb.String())
	if err != nil {
		return nil, fmt.Errorf("generate blueprints from answers: %w", err)
	}

	var bpList []*blueprints.Blueprint
	if err := json.Unmarshal([]byte(raw), &bpList); err != nil {
		// Try extracting JSON from response.
		raw = strings.TrimSpace(raw)
		if idx := strings.Index(raw, "["); idx >= 0 {
			if end := strings.LastIndex(raw, "]"); end > idx {
				if err := json.Unmarshal([]byte(raw[idx:end+1]), &bpList); err != nil {
					return nil, fmt.Errorf("parse generated blueprints: %w", err)
				}
			}
		}
		if bpList == nil {
			return nil, fmt.Errorf("could not parse blueprints from LLM response")
		}
	}

	// Save blueprints to disk.
	if err := os.MkdirAll(iv.outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create blueprint dir: %w", err)
	}
	for _, bp := range bpList {
		data, _ := json.MarshalIndent(bp, "", "  ")
		fname := filepath.Join(iv.outputDir, bp.Name+".json")
		if err := os.WriteFile(fname, data, 0o644); err != nil {
			iv.log.Warn("failed to save blueprint", "name", bp.Name, "error", err.Error())
		} else {
			fmt.Fprintf(iv.writer, "  Saved blueprint: %s\n", fname)
		}
	}

	return bpList, nil
}

func (iv *Interviewer) generateBlueprintsWithoutLLM(patterns []pattern) ([]*blueprints.Blueprint, error) {
	fmt.Fprintln(iv.writer, "\nNo LLM available. Generating blueprints from detected patterns only.")

	var bpList []*blueprints.Blueprint
	for _, p := range patterns {
		bp := patternToBlueprint(p)
		if bp != nil {
			bpList = append(bpList, bp)
		}
	}

	if err := os.MkdirAll(iv.outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create blueprint dir: %w", err)
	}
	for _, bp := range bpList {
		data, _ := json.MarshalIndent(bp, "", "  ")
		fname := filepath.Join(iv.outputDir, bp.Name+".json")
		if err := os.WriteFile(fname, data, 0o644); err != nil {
			iv.log.Warn("failed to save blueprint", "name", bp.Name, "error", err.Error())
		} else {
			fmt.Fprintf(iv.writer, "  Saved blueprint: %s\n", fname)
		}
	}

	return bpList, nil
}

func patternToBlueprint(p pattern) *blueprints.Blueprint {
	switch {
	case strings.Contains(p.Path, "values.yaml"):
		return &blueprints.Blueprint{
			Name:        "helm-values-identity",
			Description: fmt.Sprintf("Extract service identity from %s", p.Description),
			Version:     "v1",
			AppliesTo: blueprints.AppliesTo{
				Kind:  "service_repo",
				Match: blueprints.MatchConfig{HasPath: filepath.Dir(filepath.Dir(p.Path))},
			},
			Extractions: []blueprints.Extraction{
				{
					Name:        "helm_identity",
					Description: "Extract identity from Helm values",
					Source:      blueprints.ExtractionSource{Glob: p.Path},
					Strategy:    "llm",
					PromptHint:  "Extract the service name, DNS aliases (ingress hosts), IAM role, database connections, and queue identifiers from these Helm values files.",
					Extract: []blueprints.ExtractField{
						{MapsTo: "service_name"},
						{MapsTo: "dns_aliases"},
						{MapsTo: "iam_role"},
						{MapsTo: "database_connection"},
						{MapsTo: "queue_identifiers"},
					},
				},
			},
		}
	case strings.Contains(p.Path, "docker-compose"):
		return &blueprints.Blueprint{
			Name:        "docker-compose-identity",
			Description: "Extract service identity from Docker Compose",
			Version:     "v1",
			AppliesTo: blueprints.AppliesTo{
				Kind:  "any",
				Match: blueprints.MatchConfig{HasFile: "docker-compose.yml"},
			},
			Extractions: []blueprints.Extraction{
				{
					Name:        "compose_services",
					Description: "Extract service definitions from Docker Compose",
					Source:      blueprints.ExtractionSource{Glob: "docker-compose*.y*ml"},
					Strategy:    "llm",
					PromptHint:  "Extract service names, ports, environment variables with URLs, and network aliases from this Docker Compose file.",
					Extract: []blueprints.ExtractField{
						{MapsTo: "service_name"},
						{MapsTo: "dns_aliases"},
					},
				},
			},
		}
	case strings.Contains(p.Path, ".tf"):
		return &blueprints.Blueprint{
			Name:        "terraform-resources",
			Description: "Extract infrastructure resource mappings from Terraform",
			Version:     "v1",
			AppliesTo: blueprints.AppliesTo{
				Kind:  "infra_repo",
				Match: blueprints.MatchConfig{HasPath: filepath.Dir(p.Path)},
			},
			Extractions: []blueprints.Extraction{
				{
					Name:        "tf_resources",
					Description: "Extract resource ownership from Terraform",
					Source:      blueprints.ExtractionSource{Glob: p.Path},
					Strategy:    "llm",
					PromptHint:  "Extract queue names, database identifiers, and their owning service names from these Terraform files. Look for tags, naming conventions, and module references.",
					Extract: []blueprints.ExtractField{
						{MapsTo: "queue_ownership"},
						{MapsTo: "database_connection"},
					},
				},
			},
		}
	}
	return nil
}

func extractQuestions(text string) []string {
	var questions []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove numbering.
		for _, prefix := range []string{"1.", "2.", "3.", "4.", "5.", "6.", "7.", "8.", "9.", "- ", "* "} {
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
				break
			}
		}
		if strings.Contains(line, "?") {
			questions = append(questions, line)
		}
	}
	return questions
}
