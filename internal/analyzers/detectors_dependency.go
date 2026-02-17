package analyzers

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	reReqTxt      = regexp.MustCompile(`^\s*([A-Za-z0-9._\-]+)\s*([=<>!~]{1,2})\s*([A-Za-z0-9*._\-]+)\s*$`)
	reReqTxtLoose = regexp.MustCompile(`^\s*([A-Za-z0-9._\-]+)\s*$`)
	reGoReqLine   = regexp.MustCompile(`^\s*([A-Za-z0-9._\-/]+)\s+([A-Za-z0-9.+\-]+)\s*$`)
)

func detectDependenciesAndOwnership(c *collector, file sourceFile) {
	base := strings.ToLower(filepath.Base(file.Path))
	switch base {
	case "package.json":
		detectNPMDependencies(c, file)
	case "go.mod":
		detectGoModDependencies(c, file)
	case "requirements.txt":
		detectRequirementsDependencies(c, file)
	case "pom.xml":
		detectMavenDependencies(c, file)
	case "codeowners":
		detectOwnershipRules(c, file)
	}
}

func detectNPMDependencies(c *collector, file sourceFile) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(file.Text), &doc); err != nil {
		return
	}
	recordMap := func(scope string, m map[string]any) {
		for name, raw := range m {
			version := strings.TrimSpace(strings.Trim(strings.TrimSpace(toString(raw)), `"'`))
			if version == "" {
				continue
			}
			line := findLineForKey(file.Lines, name)
			if line < 1 {
				line = 1
			}
			recordDependency(c, file, line, 1, lineSnippet(file, line), "npm", name, version, scope, false)
		}
	}
	if m, ok := toStringAnyMap(doc["dependencies"]); ok {
		recordMap("runtime", m)
	}
	if m, ok := toStringAnyMap(doc["devDependencies"]); ok {
		recordMap("dev", m)
	}
}

func detectGoModDependencies(c *collector, file sourceFile) {
	inRequireBlock := false
	for i, line := range file.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if trimmed == "require (" {
			inRequireBlock = true
			continue
		}
		if inRequireBlock && trimmed == ")" {
			inRequireBlock = false
			continue
		}
		if strings.HasPrefix(trimmed, "module ") {
			continue
		}
		if strings.HasPrefix(trimmed, "require ") && !inRequireBlock {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "require "))
		}
		if m := reGoReqLine.FindStringSubmatch(trimmed); len(m) >= 3 {
			name := strings.TrimSpace(m[1])
			version := strings.TrimSpace(m[2])
			internal := strings.HasPrefix(name, "internal/") || strings.Contains(name, "/internal/")
			recordDependency(c, file, i+1, 1, line, "go", name, version, "runtime", internal)
		}
	}
}

func detectRequirementsDependencies(c *collector, file sourceFile) {
	for i, line := range file.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := reReqTxt.FindStringSubmatch(trimmed); len(m) >= 4 {
			name := strings.TrimSpace(m[1])
			version := strings.TrimSpace(m[2] + m[3])
			recordDependency(c, file, i+1, 1, line, "python-pip", name, version, "runtime", false)
			continue
		}
		if m := reReqTxtLoose.FindStringSubmatch(trimmed); len(m) >= 2 {
			name := strings.TrimSpace(m[1])
			recordDependency(c, file, i+1, 1, line, "python-pip", name, "unbounded", "runtime", false)
		}
	}
}

func detectMavenDependencies(c *collector, file sourceFile) {
	type dep struct {
		GroupID    string
		ArtifactID string
		Version    string
		Scope      string
	}
	inDep := false
	cur := dep{}
	startLine := 1
	for i, line := range file.Lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "<dependency>") {
			inDep = true
			cur = dep{}
			startLine = i + 1
			continue
		}
		if strings.Contains(trimmed, "</dependency>") {
			if inDep && cur.ArtifactID != "" {
				name := cur.ArtifactID
				if cur.GroupID != "" {
					name = cur.GroupID + ":" + cur.ArtifactID
				}
				version := strings.TrimSpace(cur.Version)
				if version == "" {
					version = "unbounded"
				}
				scope := strings.TrimSpace(cur.Scope)
				if scope == "" {
					scope = "runtime"
				}
				recordDependency(c, file, startLine, 1, lineSnippet(file, startLine), "maven", name, version, scope, false)
			}
			inDep = false
			continue
		}
		if !inDep {
			continue
		}
		if val := extractXMLTagValue(trimmed, "groupId"); val != "" {
			cur.GroupID = val
		}
		if val := extractXMLTagValue(trimmed, "artifactId"); val != "" {
			cur.ArtifactID = val
		}
		if val := extractXMLTagValue(trimmed, "version"); val != "" {
			cur.Version = val
		}
		if val := extractXMLTagValue(trimmed, "scope"); val != "" {
			cur.Scope = val
		}
	}
}

func detectOwnershipRules(c *collector, file sourceFile) {
	for i, line := range file.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Fields(trimmed)
		if len(parts) < 2 {
			continue
		}
		pattern := strings.TrimSpace(parts[0])
		owners := parts[1:]
		for _, owner := range owners {
			owner = strings.TrimSpace(owner)
			if owner == "" {
				continue
			}
			c.addFactWithEvidence("OwnershipRule", map[string]any{
				"pattern":     pattern,
				"owner":       owner,
				"source_file": file.Path,
			}, file, i+1, 1, line, func() { c.report.OwnershipRules++ })
		}
	}
}

func recordDependency(c *collector, file sourceFile, line int, col int, snippet string, ecosystem string, name string, version string, scope string, internal bool) {
	attrs := map[string]any{
		"ecosystem":   ecosystem,
		"name":        name,
		"version":     version,
		"scope":       scope,
		"internal":    internal,
		"source_file": file.Path,
	}
	c.addFactWithEvidence("Dependency", attrs, file, line, col, snippet, func() { c.report.Dependencies++ })

	if isDependencyDriftRisk(version) {
		c.addFactWithEvidence("DependencyRisk", map[string]any{
			"ecosystem":   ecosystem,
			"name":        name,
			"version":     version,
			"risk_type":   "version_drift",
			"severity":    dependencyRiskSeverity(version),
			"reason":      "non-pinned or floating version",
			"source_file": file.Path,
		}, file, line, col, snippet, func() { c.report.DependencyRisks++ })
	}
}

func isDependencyDriftRisk(version string) bool {
	v := strings.ToLower(strings.TrimSpace(version))
	if v == "" || v == "unbounded" {
		return true
	}
	if strings.Contains(v, "latest") || strings.Contains(v, "snapshot") || strings.Contains(v, "*") {
		return true
	}
	prefixes := []string{"^", "~", ">", "<", ">=", "<=", "!="}
	for _, p := range prefixes {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

func dependencyRiskSeverity(version string) string {
	v := strings.ToLower(strings.TrimSpace(version))
	if v == "" || v == "unbounded" || strings.Contains(v, "latest") || strings.Contains(v, "*") {
		return "high"
	}
	return "medium"
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}

func toStringAnyMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func findLineForKey(lines []string, key string) int {
	for i, line := range lines {
		if strings.Contains(line, `"`+key+`"`) {
			return i + 1
		}
	}
	return -1
}

func extractXMLTagValue(line string, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(line, open)
	if start == -1 {
		return ""
	}
	start += len(open)
	end := strings.Index(line[start:], close)
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(line[start : start+end])
}
