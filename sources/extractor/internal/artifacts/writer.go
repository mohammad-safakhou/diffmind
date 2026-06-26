package artifacts

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/provenance"
	"github.com/mohammad-safakhou/diffmind/internal/serviceconfig"
	"github.com/mohammad-safakhou/diffmind/internal/util"
	"github.com/mohammad-safakhou/protocol"
	"gopkg.in/yaml.v3"
)

const SchemaVersion = "v1alpha1"

// DiffMindVersion identifies the extractor build that produced a run. Override
// at build time with -ldflags "-X .../internal/artifacts.DiffMindVersion=<sha>".
var DiffMindVersion = "dev"

// gitHeadSHA returns the HEAD commit of the repo at path, or "" if it is not a
// git working tree. Used to pin a run to the exact analyzed revision.
func gitHeadSHA(path string) string {
	cmd := exec.Command("git", "-C", path, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitOutput(path string, args ...string) string {
	cmdArgs := append([]string{"-C", path}, args...)
	out, err := exec.Command("git", cmdArgs...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitDirty(path string, deterministic bool, importLegacyArchfile bool) bool {
	out := gitOutput(path, "status", "--porcelain")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if deterministic && ignoredDeterministicDirtyPath(line, importLegacyArchfile) {
			continue
		}
		return true
	}
	return false
}

func ignoredDeterministicDirtyPath(statusLine string, importLegacyArchfile bool) bool {
	path := strings.TrimSpace(statusLine)
	if len(path) > 3 {
		path = strings.TrimSpace(path[3:])
	}
	if i := strings.Index(path, " -> "); i >= 0 {
		path = strings.TrimSpace(path[i+4:])
	}
	path = filepath.ToSlash(strings.Trim(path, `"`))
	if path == "" {
		return false
	}
	base := filepath.Base(path)
	switch {
	case strings.HasPrefix(path, ".diffmind/"), strings.HasPrefix(path, ".diffmind/"):
		return true
	case path == ".project", path == ".settings" || strings.HasPrefix(path, ".settings/") || strings.Contains(path, "/.settings/"):
		return true
	case base == ".classpath" || base == ".factorypath" || base == ".project" || base == ".settings":
		return true
	case !importLegacyArchfile && (path == "diffmind.yaml" || path == "diffmind.curated.yaml"):
		return true
	default:
		return false
	}
}

type WriteInput struct {
	RunID                string
	BaseDir              string
	RepoPath             string
	OpenCodeURL          string
	MinConfidence        float64
	Exposures            []model.Exposure
	Dependencies         []model.Dependency
	Connections          []model.Connection
	Unresolved           []model.UnresolvedItem
	Warnings             []string
	Pipeline             string
	ImportLegacyArchfile bool
	StartedAt            time.Time
	FinishedAt           time.Time
	// TokenTotals — when present — get persisted under the
	// manifest's `token_totals` field so the `validate` command and
	// the dashboard's runs sidebar can show per-stage cost summaries
	// without re-reading the events log.
	TokenTotals map[string]model.TokenBucket
	RepoFacts   *extraction.RepoFacts
}

func Write(in WriteInput) (string, error) {
	runDir := filepath.Join(in.BaseDir, in.RunID)
	util.Info("artifacts", "writing artifacts", map[string]any{"run_dir": runDir, "run_id": in.RunID})
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		util.Error("artifacts", "failed creating run dir", map[string]any{"run_dir": runDir, "error": err})
		return "", err
	}
	pipeline := pipelineName(in)
	deterministic := pipeline == "deterministic"
	if deterministic {
		provenance.NormalizeDeterministic(in.Exposures, in.Dependencies, in.Connections)
	}
	if err := writeEntities(filepath.Join(runDir, "exposures"), exposuresByType(in.Exposures)); err != nil {
		return "", err
	}
	if err := writeDependencies(filepath.Join(runDir, "dependencies"), depsByType(in.Dependencies)); err != nil {
		return "", err
	}
	if err := writeConnections(filepath.Join(runDir, "connections"), connsByType(in.Connections)); err != nil {
		return "", err
	}
	if err := writeUnresolved(filepath.Join(runDir, "unresolved"), unresolvedByType(in.Unresolved)); err != nil {
		return "", err
	}
	schemaVersion := SchemaVersion
	if deterministic {
		schemaVersion = protocol.SchemaServiceV1
	}
	manifest := model.RunManifest{
		RunID:             in.RunID,
		StartedAt:         in.StartedAt,
		FinishedAt:        in.FinishedAt,
		RepoPath:          in.RepoPath,
		Team:              repoTeam(in.RepoPath),
		RepoGitSHA:        gitHeadSHA(in.RepoPath),
		RepoGitBranch:     gitOutput(in.RepoPath, "rev-parse", "--abbrev-ref", "HEAD"),
		RepoGitRemoteURL:  gitOutput(in.RepoPath, "remote", "get-url", "origin"),
		RepoGitDirty:      gitDirty(in.RepoPath, deterministic, in.ImportLegacyArchfile),
		DiffMindVersion:   DiffMindVersion,
		SchemaVersion:     schemaVersion,
		Pipeline:          pipeline,
		OpenCodeURL:       in.OpenCodeURL,
		ConfidenceMinimum: in.MinConfidence,
		Counts: map[string]int{
			"exposures":    len(in.Exposures),
			"dependencies": len(in.Dependencies),
			"connections":  len(in.Connections),
			"unresolved":   len(in.Unresolved),
		},
		RepoMetrics:   CollectRepoMetrics(in.RepoPath, in.RepoFacts),
		Warnings:      in.Warnings,
		StageFailures: stageFailures(in.Unresolved),
		TokenTotals:   in.TokenTotals,
	}
	if err := writeJSON(filepath.Join(runDir, "run_manifest.json"), manifest); err != nil {
		return "", err
	}
	if err := writeDiffMind protocolArtifacts(runDir, in, manifest); err != nil {
		return "", err
	}
	util.Info("artifacts", "artifact write complete", map[string]any{
		"run_dir":      runDir,
		"exposures":    len(in.Exposures),
		"dependencies": len(in.Dependencies),
		"connections":  len(in.Connections),
		"unresolved":   len(in.Unresolved),
	})
	return runDir, nil
}

func pipelineName(in WriteInput) string {
	if p := strings.TrimSpace(in.Pipeline); p != "" {
		return p
	}
	if strings.TrimSpace(in.OpenCodeURL) == "" {
		return "deterministic"
	}
	return "llm"
}

func writeDiffMind protocolArtifacts(runDir string, in WriteInput, manifest model.RunManifest) error {
	doc, err := buildDiffMind protocol(in, manifest)
	if err != nil {
		return err
	}
	jsonPath := filepath.Join(runDir, DiffMind protocolServiceJSON)
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		return err
	}
	jf, err := os.Create(jsonPath)
	if err != nil {
		return err
	}
	if err := protocolEncodeJSON(jf, doc); err != nil {
		_ = jf.Close()
		return err
	}
	if err := jf.Close(); err != nil {
		return err
	}
	yf, err := os.Create(filepath.Join(runDir, DiffMind protocolServiceYAML))
	if err != nil {
		return err
	}
	if err := protocolEncodeYAML(yf, doc); err != nil {
		_ = yf.Close()
		return err
	}
	return yf.Close()
}

func repoTeam(repoPath string) string {
	if cfg, err := serviceconfig.Load(repoPath); err == nil {
		if team := strings.TrimSpace(cfg.Service.Team); team != "" {
			return team
		}
	}
	if team := catalogInfoOwner(repoPath); team != "" {
		return team
	}
	return "default"
}

func catalogInfoOwner(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, "catalog-info.yaml"))
	if err != nil {
		return ""
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Owner string `yaml:"owner"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if strings.EqualFold(strings.TrimSpace(doc.Kind), "Component") {
			if owner := strings.TrimSpace(doc.Spec.Owner); owner != "" {
				return owner
			}
		}
	}
	return ""
}

func CollectRepoMetrics(repoPath string, facts *extraction.RepoFacts) *model.RepoMetrics {
	m := &model.RepoMetrics{}
	byLang := map[string]*model.LanguageMetric{}
	_ = filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipMetricsDir(name) && path != repoPath {
				return filepath.SkipDir
			}
			return nil
		}
		lang := languageForPath(path)
		if lang == "" {
			return nil
		}
		loc := countLOC(path)
		if loc < 0 {
			return nil
		}
		lm := byLang[lang]
		if lm == nil {
			lm = &model.LanguageMetric{Language: lang}
			byLang[lang] = lm
		}
		lm.Files++
		lm.LOC += loc
		m.FileCount++
		m.TotalLOC += loc
		return nil
	})
	for _, lm := range byLang {
		m.Languages = append(m.Languages, *lm)
	}
	sort.Slice(m.Languages, func(i, j int) bool {
		if m.Languages[i].LOC != m.Languages[j].LOC {
			return m.Languages[i].LOC > m.Languages[j].LOC
		}
		return m.Languages[i].Language < m.Languages[j].Language
	})
	if facts != nil {
		m.Frameworks = uniqueStrings(facts.Frameworks)
		m.DetectedServiceName = strings.TrimSpace(facts.ServiceName)
		tools := map[string]bool{}
		for _, lf := range facts.LanguageFacts {
			if t := strings.TrimSpace(lf.BuildTool); t != "" {
				tools[t] = true
			}
		}
		for t := range tools {
			m.BuildTools = append(m.BuildTools, t)
		}
		sort.Strings(m.BuildTools)
	}
	return m
}

func skipMetricsDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "target", "build", "dist", ".gradle", ".idea", ".gocache", ".diffmind", "coverage", ".cache":
		return true
	default:
		return false
	}
}

func languageForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala":
		return "scala"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py":
		return "python"
	case ".rb":
		return "ruby"
	case ".cs":
		return "csharp"
	case ".fs":
		return "fsharp"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp":
		return "cpp"
	case ".rs":
		return "rust"
	case ".php":
		return "php"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".xml":
		return "xml"
	default:
		return ""
	}
}

func countLOC(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	lines := 0
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			lines++
		}
	}
	if sc.Err() != nil {
		return -1
	}
	return lines
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func writeEntities(dir string, grouped map[string][]model.Exposure) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := sortedKeys(grouped)
	used := map[string]struct{}{}
	for _, k := range keys {
		util.Trace("artifacts", "writing exposure file", map[string]any{"type": k, "count": len(grouped[k])})
		filename := safeFileBase(k, used) + ".json"
		if err := writeJSON(filepath.Join(dir, filename), grouped[k]); err != nil {
			return err
		}
	}
	return nil
}

func writeDependencies(dir string, grouped map[string][]model.Dependency) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := sortedKeys(grouped)
	used := map[string]struct{}{}
	for _, k := range keys {
		util.Trace("artifacts", "writing dependency file", map[string]any{"type": k, "count": len(grouped[k])})
		filename := safeFileBase(k, used) + ".json"
		if err := writeJSON(filepath.Join(dir, filename), grouped[k]); err != nil {
			return err
		}
	}
	return nil
}

func writeConnections(dir string, grouped map[string][]model.Connection) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := sortedKeys(grouped)
	used := map[string]struct{}{}
	for _, k := range keys {
		util.Trace("artifacts", "writing connection file", map[string]any{"type": k, "count": len(grouped[k])})
		filename := safeFileBase(k, used) + ".json"
		if err := writeJSON(filepath.Join(dir, filename), grouped[k]); err != nil {
			return err
		}
	}
	return nil
}

func writeUnresolved(dir string, grouped map[string][]model.UnresolvedItem) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := sortedKeys(grouped)
	used := map[string]struct{}{}
	for _, k := range keys {
		util.Trace("artifacts", "writing unresolved file", map[string]any{"type": k, "count": len(grouped[k])})
		filename := safeFileBase(k, used) + ".json"
		if err := writeJSON(filepath.Join(dir, filename), grouped[k]); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, data any) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func exposuresByType(in []model.Exposure) map[string][]model.Exposure {
	out := map[string][]model.Exposure{}
	for _, v := range in {
		out[v.Type] = append(out[v.Type], v)
	}
	return out
}

func depsByType(in []model.Dependency) map[string][]model.Dependency {
	out := map[string][]model.Dependency{}
	for _, v := range in {
		out[v.Type] = append(out[v.Type], v)
	}
	return out
}

func connsByType(in []model.Connection) map[string][]model.Connection {
	out := map[string][]model.Connection{}
	for _, v := range in {
		key := v.FromType + "__to__" + v.ToType
		out[key] = append(out[key], v)
	}
	return out
}

func unresolvedByType(in []model.UnresolvedItem) map[string][]model.UnresolvedItem {
	out := map[string][]model.UnresolvedItem{}
	for _, v := range in {
		key := string(v.Kind) + "_" + v.Type
		out[key] = append(out[key], v)
	}
	return out
}

// stageFailures groups unresolved diagnostics by the pipeline stage
// where they originated, using the ReasonCode as a coarse tag. The
// resulting map is what the dashboard's "stage health" badge reads to
// decide whether to flag a stage as "degraded".
//
// Reason codes that don't correspond to a stage (e.g.
// "missing_required_details" written by the assembler before any LLM
// call) are filed under "validation".
func stageFailures(in []model.UnresolvedItem) map[string]int {
	if len(in) == 0 {
		return nil
	}
	stageOf := map[string]string{
		// Hard agent failures (the LLM call itself errored after
		// retries) are filed under the stage that ran them.
		"discovery_failure":         "discovery",
		"connections_failure":       "connections",
		"reexamine_failure":         "reexamination",
		"rejected_on_reexamination": "reexamination",

		// Quality / validation diagnostics from the assembler.
		"missing_required_details": "validation",
		"low_confidence":           "validation",
		"no_source_location":       "validation",
		"invalid_entity":           "validation",
		"orphan_connection":        "reconcile",
		"unmatched_reference":      "connections",
	}
	out := map[string]int{}
	for _, u := range in {
		stage, ok := stageOf[u.ReasonCode]
		if !ok {
			stage = "other"
		}
		out[stage]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func safeFileBase(key string, used map[string]struct{}) string {
	base := sanitizeFileBase(key)
	if base == "" {
		base = "unknown"
	}
	candidate := base
	if candidate != key {
		candidate = base + "__" + util.StableID(key)[:8]
	}
	if _, exists := used[candidate]; exists {
		candidate = base + "__" + util.StableID(key)[:8]
	}
	used[candidate] = struct{}{}
	return candidate
}

func sanitizeFileBase(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(in))
	lastUnderscore := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		isAlpha := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		isSafePunct := c == '.' || c == '_' || c == '-'
		if isAlpha || isDigit || isSafePunct {
			b.WriteByte(c)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "._-")
	return out
}
