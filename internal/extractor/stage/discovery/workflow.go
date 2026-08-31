package discovery

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

var (
	javaStaticStringConstRe = regexp.MustCompile(`(?m)\b(?:private|public|protected)?\s*static\s+final\s+String\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"`)
	javaSuperTopicRe        = regexp.MustCompile(`super\s*\(\s*[^,]+,\s*([A-Za-z_][A-Za-z0-9_]*|"[^"]+")\s*\)`)
	javaAddVariableRe       = regexp.MustCompile(`\.addVariable\s*\(\s*([A-Za-z_][A-Za-z0-9_]*|"[^"]+")\s*,\s*([^)]+)\)`)
)

// DeterministicWorkflowOrchestration detects source-code workflow integration
// points. The first supported pattern is Camunda/Cibseven external task
// workers: the service subscribes to an external task topic and returns process
// variables to the workflow engine. This is not modeled as a direct HTTP call;
// it is an orchestration dependency.
func DeterministicWorkflowOrchestration(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil || idx.RepoRoot == "" {
		return nil
	}
	var out []candidate
	seen := map[string]struct{}{}
	projectConstants := projectJavaStringConstants(idx)
	for _, fa := range sortedFiles(idx, "java") {
		src, ok := readIndexedSource(idx, fa.Path)
		if !ok || !looksLikeCamundaExternalTaskWorker(src) {
			continue
		}
		constants := copyStringMap(projectConstants)
		for k, v := range javaStringConstants(src) {
			constants[k] = v
		}
		for k, v := range javaStaticStringConstants(src) {
			constants[k] = v
		}
		topic := camundaTopic(src, constants)
		if topic == "" {
			topic = lastIdentOf(fa.Path)
		}
		target, urlTemplate, configSource := camundaEngineTarget(idx)
		if target == "" {
			target = "camunda"
		}
		variables := camundaVariables(src, constants)
		line := lineNumberAt(src, strings.Index(src, "AbstractExternalTaskClient"))
		if line < 1 {
			line = 1
		}
		key := strings.ToLower(target + "|" + topic + "|" + fa.Path)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		name := "Camunda external task " + topic
		out = append(out, candidate{
			Type:       "workflow_orchestration",
			Name:       name,
			Summary:    "Source-derived Camunda external task workflow integration",
			Confidence: 1.0,
			Tags:       []string{"deterministic", "workflow", "camunda", "external-task"},
			Details: map[string]any{
				"orchestrator":       "camunda",
				"target_service":     target,
				"url_template":       urlTemplate,
				"config_source":      configSource,
				"topic":              topic,
				"variables":          variables,
				"callback_variables": callbackVariables(variables),
				"invocation_mode":    "external_task_worker",
				"framework":          "camunda_external_task",
			},
			Locations: []candidateLocation{{File: fa.Path, StartLine: line, EndLine: line}},
			Evidence: []candidateEvidence{{
				File:      fa.Path,
				StartLine: line,
				EndLine:   line,
				Snippet:   camundaEvidenceSnippet(src),
				Source:    "deterministic_source",
			}},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func looksLikeCamundaExternalTaskWorker(src string) bool {
	lower := strings.ToLower(src)
	return strings.Contains(lower, "abstractexternaltaskclient") ||
		strings.Contains(lower, "externaltaskresult") ||
		strings.Contains(lower, "externaltasktaskhandler")
}

func projectJavaStringConstants(idx *astpkg.ProjectIndex) map[string]string {
	out := map[string]string{}
	for _, fa := range sortedFiles(idx, "java") {
		src, ok := readIndexedSource(idx, fa.Path)
		if !ok {
			continue
		}
		for k, v := range javaStringConstants(src) {
			out[k] = v
		}
		for k, v := range javaStaticStringConstants(src) {
			out[k] = v
		}
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func javaStaticStringConstants(src string) map[string]string {
	out := map[string]string{}
	for _, m := range javaStaticStringConstRe.FindAllStringSubmatch(src, -1) {
		out[m[1]] = m[2]
	}
	return out
}

func camundaTopic(src string, constants map[string]string) string {
	if m := javaSuperTopicRe.FindStringSubmatch(src); len(m) == 2 {
		return resolveJavaStringExpr(m[1], constants)
	}
	return ""
}

func camundaVariables(src string, constants map[string]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range javaAddVariableRe.FindAllStringSubmatch(src, -1) {
		name := resolveJavaStringExpr(m[1], constants)
		if name == "" {
			name = strings.Trim(m[1], `"`)
		}
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func callbackVariables(vars []string) []string {
	var out []string
	for _, v := range vars {
		lower := strings.ToLower(v)
		if strings.Contains(lower, "url") || strings.Contains(lower, "callback") {
			out = append(out, v)
		}
	}
	return out
}

func camundaEngineTarget(idx *astpkg.ProjectIndex) (target, urlTemplate, source string) {
	var fallbackValue, fallbackSource string
	for _, path := range camundaSortedConfigPaths(idx) {
		cfg := idx.Configs[path]
		if cfg == nil {
			continue
		}
		for _, entry := range cfg.Entries {
			key := strings.ToLower(strings.TrimSpace(entry.Key))
			if key != "external-task.url" && key != "camunda.url" && key != "camunda.engine.url" {
				continue
			}
			value := strings.TrimSpace(entry.Value)
			if value == "" {
				continue
			}
			src := fmt.Sprintf("%s:%s", cfg.Path, entry.Key)
			if service := serviceNameFromURLTemplate(value); service != "" {
				return service, value, src
			}
			if fallbackValue == "" {
				fallbackValue = value
				fallbackSource = src
			}
		}
	}
	return "", fallbackValue, fallbackSource
}

func camundaSortedConfigPaths(idx *astpkg.ProjectIndex) []string {
	paths := make([]string, 0, len(idx.Configs))
	for path := range idx.Configs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func camundaEvidenceSnippet(src string) string {
	for _, marker := range []string{"extends AbstractExternalTaskClient", "ExternalTaskResult", "ExternalTaskTaskHandler"} {
		if idx := strings.Index(src, marker); idx >= 0 {
			start := idx - 120
			if start < 0 {
				start = 0
			}
			end := idx + len(marker) + 240
			if end > len(src) {
				end = len(src)
			}
			return strings.TrimSpace(src[start:end])
		}
	}
	return "Camunda external task worker"
}
