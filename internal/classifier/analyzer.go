package classifier

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const toolVersion = "dev"

func BuildReport(sourceRoot string, files []ScannedFile, stats RepoStats) ScanReport {
	caps := detectCapabilities(files)
	profile := classifyProfile(stats, caps)

	return ScanReport{
		GeneratedAt:   time.Now().UTC(),
		SourceRoot:    sourceRoot,
		Profile:       profile,
		Capabilities:  caps,
		Stats:         stats,
		ToolVersion:   toolVersion,
		AnalyzerID:    "repo-classifier-v1",
		AnalyzerStage: "M2",
	}
}

func detectCapabilities(files []ScannedFile) RepoCapabilities {
	caps := RepoCapabilities{}
	langEvidence := make(map[string][]string)

	buildSig := map[string]string{
		"go.mod":           "go-mod",
		"package.json":     "npm",
		"pom.xml":          "maven",
		"build.gradle":     "gradle",
		"build.gradle.kts": "gradle",
		"cargo.toml":       "cargo",
		"pyproject.toml":   "python-poetry-or-pep517",
		"requirements.txt": "python-pip",
	}
	ciSig := map[string]string{
		".gitlab-ci.yml": "gitlab-ci",
		"jenkinsfile":    "jenkins",
	}

	buildEvidence := map[string][]string{}
	ciEvidence := map[string][]string{}
	iacEvidence := map[string][]string{}
	apiEvidence := map[string][]string{}
	migEvidence := map[string][]string{}
	containerEvidence := map[string][]string{}

	for _, f := range files {
		lower := strings.ToLower(f.Path)
		base := strings.ToLower(filepath.Base(lower))

		if lang := languageFromExt(f.Ext); lang != "" {
			langEvidence[lang] = appendLimited(langEvidence[lang], f.Path, 8)
		}

		if tool, ok := buildSig[base]; ok {
			buildEvidence[tool] = appendLimited(buildEvidence[tool], f.Path, 8)
		}
		if ci, ok := ciSig[base]; ok {
			ciEvidence[ci] = appendLimited(ciEvidence[ci], f.Path, 8)
		}
		if strings.HasPrefix(lower, ".github/workflows/") && (strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")) {
			ciEvidence["github-actions"] = appendLimited(ciEvidence["github-actions"], f.Path, 8)
		}

		if strings.HasSuffix(lower, ".tf") {
			iacEvidence["terraform"] = appendLimited(iacEvidence["terraform"], f.Path, 8)
		}
		if strings.Contains(lower, "helm/") && base == "chart.yaml" {
			iacEvidence["helm"] = appendLimited(iacEvidence["helm"], f.Path, 8)
		}
		if base == "kustomization.yaml" || base == "kustomization.yml" {
			iacEvidence["kustomize"] = appendLimited(iacEvidence["kustomize"], f.Path, 8)
		}
		if strings.HasPrefix(lower, "k8s/") && (strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")) {
			iacEvidence["kubernetes-manifests"] = appendLimited(iacEvidence["kubernetes-manifests"], f.Path, 8)
		}

		if strings.Contains(base, "openapi") && (strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".json")) {
			apiEvidence["openapi"] = appendLimited(apiEvidence["openapi"], f.Path, 8)
		}
		if strings.HasSuffix(lower, ".proto") {
			apiEvidence["protobuf"] = appendLimited(apiEvidence["protobuf"], f.Path, 8)
		}
		if strings.Contains(base, "asyncapi") {
			apiEvidence["asyncapi"] = appendLimited(apiEvidence["asyncapi"], f.Path, 8)
		}

		if strings.Contains(lower, "/migrations/") && strings.HasSuffix(lower, ".sql") {
			migEvidence["sql-migrations"] = appendLimited(migEvidence["sql-migrations"], f.Path, 8)
		}
		if strings.HasPrefix(lower, "migrations/") && strings.HasSuffix(lower, ".sql") {
			migEvidence["sql-migrations"] = appendLimited(migEvidence["sql-migrations"], f.Path, 8)
		}

		if base == "dockerfile" {
			containerEvidence["dockerfile"] = appendLimited(containerEvidence["dockerfile"], f.Path, 8)
		}
		if base == "docker-compose.yml" || base == "docker-compose.yaml" || base == "compose.yml" || base == "compose.yaml" {
			containerEvidence["docker-compose"] = appendLimited(containerEvidence["docker-compose"], f.Path, 8)
		}
	}

	caps.Languages = buildLanguageCounts(langEvidence)
	caps.BuildTools = mapToCapabilities(buildEvidence, 0.95)
	caps.CI = mapToCapabilities(ciEvidence, 0.95)
	caps.IaC = mapToCapabilities(iacEvidence, 0.9)
	caps.APISpecs = mapToCapabilities(apiEvidence, 0.9)
	caps.Migrations = mapToCapabilities(migEvidence, 0.9)
	caps.Containers = mapToCapabilities(containerEvidence, 0.95)
	return caps
}

func classifyProfile(stats RepoStats, caps RepoCapabilities) RepoProfile {
	scores := map[string]float64{}
	evidence := map[string][]string{}

	total := float64(max(stats.TotalFiles, 1))
	iacFiles := float64(capabilityEvidenceCount(caps.IaC))
	ciFiles := float64(capabilityEvidenceCount(caps.CI))
	sourceFiles := float64(stats.SourceFiles)
	containerSignals := float64(capabilityEvidenceCount(caps.Containers))
	buildSignals := float64(capabilityEvidenceCount(caps.BuildTools))

	if iacFiles/total > 0.20 {
		scores["infra-repo"] += 0.45
		evidence["infra-repo"] = append(evidence["infra-repo"], firstEvidence(caps.IaC)...)
	}
	if iacFiles > 0 && sourceFiles < 5 {
		scores["infra-repo"] += 0.35
	}
	if hasEntrypointHints(caps, stats) {
		scores["service-repo"] += 0.45
		evidence["service-repo"] = append(evidence["service-repo"], entrypointEvidence(caps)...)
	}
	if containerSignals > 0 && sourceFiles > 0 {
		scores["service-repo"] += 0.20
		evidence["service-repo"] = append(evidence["service-repo"], firstEvidence(caps.Containers)...)
	}
	if buildSignals > 0 && sourceFiles > 0 && !hasEntrypointHints(caps, stats) {
		scores["library-repo"] += 0.55
		evidence["library-repo"] = append(evidence["library-repo"], firstEvidence(caps.BuildTools)...)
	}
	if ciFiles > 0 && sourceFiles == 0 {
		scores["ci-only-repo"] += 0.9
		evidence["ci-only-repo"] = append(evidence["ci-only-repo"], firstEvidence(caps.CI)...)
	}
	if buildSignals >= 2 {
		scores["monorepo"] += 0.65
		evidence["monorepo"] = append(evidence["monorepo"], firstEvidence(caps.BuildTools)...)
	}

	labels := make([]LabelScore, 0, len(scores))
	for label, score := range scores {
		if score < 0.2 {
			continue
		}
		labels = append(labels, LabelScore{
			Label:      label,
			Confidence: clamp(score, 0.05, 0.99),
			Evidence:   uniqueSorted(evidence[label]),
		})
	}
	if len(labels) == 0 {
		labels = append(labels, LabelScore{Label: "unclassified", Confidence: 0.5, Evidence: []string{}})
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].Confidence > labels[j].Confidence })
	return RepoProfile{Labels: labels}
}

func hasEntrypointHints(caps RepoCapabilities, stats RepoStats) bool {
	if stats.SourceFiles == 0 {
		return false
	}
	for _, c := range caps.BuildTools {
		if c.Name == "go-mod" || c.Name == "npm" || c.Name == "maven" || c.Name == "gradle" {
			return true
		}
	}
	return false
}

func entrypointEvidence(caps RepoCapabilities) []string {
	return firstEvidence(caps.BuildTools)
}

func firstEvidence(caps []Capability) []string {
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		if len(c.Evidence) > 0 {
			out = append(out, c.Evidence[0])
		}
	}
	return out
}

func capabilityEvidenceCount(caps []Capability) int {
	total := 0
	for _, c := range caps {
		total += len(c.Evidence)
	}
	return total
}

func mapToCapabilities(in map[string][]string, confidence float64) []Capability {
	keys := make([]string, 0, len(in))
	for name := range in {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	out := make([]Capability, 0, len(keys))
	for _, name := range keys {
		evidence := uniqueSorted(in[name])
		if len(evidence) == 0 {
			continue
		}
		out = append(out, Capability{Name: name, Evidence: evidence, Confidence: confidence})
	}
	return out
}

func buildLanguageCounts(in map[string][]string) []LanguageCount {
	keys := make([]string, 0, len(in))
	for lang := range in {
		keys = append(keys, lang)
	}
	sort.Strings(keys)

	out := make([]LanguageCount, 0, len(keys))
	for _, lang := range keys {
		evidence := uniqueSorted(in[lang])
		out = append(out, LanguageCount{Language: lang, Count: len(in[lang]), Evidence: evidence})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func languageFromExt(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".js", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp":
		return "cpp"
	case ".kt":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".scala":
		return "scala"
	case ".sh":
		return "shell"
	default:
		return ""
	}
}

func appendLimited(values []string, value string, max int) []string {
	if len(values) >= max {
		return values
	}
	return append(values, value)
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func clamp(v float64, min float64, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
