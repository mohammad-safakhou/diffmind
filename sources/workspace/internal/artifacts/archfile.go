package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	ArchfileSchema    = "diffmind.discovery.v1"
	RepoArchfileRunID = "repo:diffmind.yaml"
)

const maxIncludeDepth = 20

var archfileVarPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

type rawArchFile struct {
	Schema       string            `yaml:"schema"`
	Service      string            `yaml:"service,omitempty"`
	Team         string            `yaml:"team,omitempty"`
	Vars         map[string]string `yaml:"vars,omitempty"`
	Include      []string          `yaml:"include,omitempty"`
	Resources    []rawResource     `yaml:"resources,omitempty"`
	Exposures    []rawEntity       `yaml:"exposures,omitempty"`
	Dependencies []rawEntity       `yaml:"dependencies,omitempty"`
	Connections  []rawConn         `yaml:"connections,omitempty"`
}

type rawResource struct {
	ID       string         `yaml:"id"`
	Kind     string         `yaml:"kind"`
	Platform string         `yaml:"platform,omitempty"`
	Name     string         `yaml:"name"`
	Instance string         `yaml:"instance,omitempty"`
	Summary  string         `yaml:"summary,omitempty"`
	Tags     []string       `yaml:"tags,omitempty"`
	Details  map[string]any `yaml:"details,omitempty"`
	Status   string         `yaml:"status,omitempty"`
	Source   string         `yaml:"source,omitempty"`
}

type rawEntity struct {
	ID       string         `yaml:"id,omitempty"`
	Type     string         `yaml:"type"`
	Name     string         `yaml:"name"`
	Resource string         `yaml:"resource,omitempty"`
	Service  string         `yaml:"service,omitempty"`
	Summary  string         `yaml:"summary,omitempty"`
	Platform string         `yaml:"platform,omitempty"`
	Tags     []string       `yaml:"tags,omitempty"`
	Details  map[string]any `yaml:"details,omitempty"`
	Status   string         `yaml:"status,omitempty"`
	Source   string         `yaml:"source,omitempty"`
}

type rawConn struct {
	From      string `yaml:"from"`
	To        string `yaml:"to"`
	Condition string `yaml:"condition,omitempty"`
	Summary   string `yaml:"summary,omitempty"`
	Status    string `yaml:"status,omitempty"`
	Source    string `yaml:"source,omitempty"`
}

type resolvedArchFile struct {
	Service      string
	Team         string
	Resources    []model.Resource
	Exposures    []rawEntity
	Dependencies []rawEntity
	Connections  []rawConn
}

// RepoArchfilePath returns the conventional DiffMind discovery file path for a repo.
func RepoArchfilePath(repoPath string) string {
	return filepath.Join(repoPath, "diffmind.yaml")
}

// HasRepoArchfile reports whether a repo has a readable diffmind.yaml file.
func HasRepoArchfile(repoPath string) bool {
	st, err := os.Stat(RepoArchfilePath(repoPath))
	return err == nil && !st.IsDir()
}

func RepoArchfileTeam(repoPath string) string {
	resolved, err := resolveArchfile(RepoArchfilePath(repoPath))
	if err != nil {
		return "default"
	}
	return firstString(resolved.Team, "default")
}

// ReadDiffMindArchfile reads the latest DiffMind discovery-file format
// (diffmind.yaml) into the same ServiceArchitecture shape as run artifacts.
func ReadDiffMindArchfile(path string) (*model.ServiceArchitecture, error) {
	resolved, err := resolveArchfile(path)
	if err != nil {
		return nil, err
	}
	repoPath := filepath.Dir(path)
	arch := &model.ServiceArchitecture{
		ServiceName:  resolved.Service,
		RepoPath:     repoPath,
		Resources:    resolved.Resources,
		Exposures:    make([]model.Exposure, 0, len(resolved.Exposures)),
		Dependencies: make([]model.Dependency, 0, len(resolved.Dependencies)),
		Connections:  make([]model.Connection, 0, len(resolved.Connections)),
		Manifest: &model.RunManifest{
			RunID:         RepoArchfileRunID,
			RepoPath:      repoPath,
			Team:          firstString(resolved.Team, "default"),
			SchemaVersion: ArchfileSchema,
			Counts:        map[string]int{},
			Metadata:      map[string]string{"source": path},
		},
	}

	exposureRefs := map[string]model.BaseEntity{}
	dependencyRefs := map[string]model.BaseEntity{}
	for _, e := range resolved.Exposures {
		base := baseFromRawEntity(e, resolved.Service, "exposure")
		arch.Exposures = append(arch.Exposures, model.Exposure{BaseEntity: base})
		addEntityRefs(exposureRefs, e.ID, e.Name, base)
	}
	for _, d := range resolved.Dependencies {
		base := baseFromRawEntity(d, resolved.Service, "dependency")
		arch.Dependencies = append(arch.Dependencies, model.Dependency{BaseEntity: base})
		addEntityRefs(dependencyRefs, d.ID, d.Name, base)
	}
	for _, c := range resolved.Connections {
		from, ok := exposureRefs[strings.TrimSpace(c.From)]
		if !ok {
			continue
		}
		to, ok := dependencyRefs[strings.TrimSpace(c.To)]
		if !ok {
			continue
		}
		arch.Connections = append(arch.Connections, model.Connection{
			ID:             util.ContentHash("archfile-connection", from.ID, to.ID, strings.TrimSpace(c.Condition), strings.TrimSpace(c.Summary)),
			FromExposureID: from.ID,
			ToDependencyID: to.ID,
			FromType:       from.Type,
			ToType:         to.Type,
			Condition:      conditionFromShorthand(c.Condition),
			PathSignature:  pathSignature(c.Condition),
			Summary:        strings.TrimSpace(c.Summary),
			Confidence:     1,
		})
	}
	arch.Manifest.Counts["exposures"] = len(arch.Exposures)
	arch.Manifest.Counts["dependencies"] = len(arch.Dependencies)
	arch.Manifest.Counts["connections"] = len(arch.Connections)
	return arch, nil
}

// ReadDiffMindFileMaps returns raw entity maps grouped by type for the rich
// architecture graph endpoint. It accepts either a diffmind.yaml file or a run dir.
func ReadDiffMindFileMaps(path string) (map[string][]map[string]any, map[string][]map[string]any, map[string][]map[string]any) {
	if isYAMLFile(path) {
		return readArchfileMaps(path)
	}
	return readRunDirMaps(path)
}

func resolveArchfile(path string) (*resolvedArchFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	out := &resolvedArchFile{}
	if err := resolveArchfileInto(abs, map[string]string{}, "", map[string]bool{}, 0, out); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveArchfileInto(abs string, inheritedVars map[string]string, rootService string, visited map[string]bool, depth int, out *resolvedArchFile) error {
	if depth > maxIncludeDepth {
		return fmt.Errorf("include depth exceeded %d at %s", maxIncludeDepth, abs)
	}
	if visited[abs] {
		return fmt.Errorf("include cycle detected at %s", abs)
	}
	visited[abs] = true
	defer delete(visited, abs)

	doc, raw, err := parseArchfile(abs)
	if err != nil {
		return err
	}

	vars := make(map[string]string, len(inheritedVars)+len(raw.Vars))
	for k, v := range inheritedVars {
		vars[k] = v
	}
	for k, v := range raw.Vars {
		vars[k] = expandArchfileString(v, inheritedVars)
	}
	if err := interpolateArchfileMapping(doc.Content[0], vars); err != nil {
		return fmt.Errorf("interpolate %s: %w", abs, err)
	}
	if err := doc.Decode(&raw); err != nil {
		return fmt.Errorf("parse %s: %w", abs, err)
	}

	fileService := strings.TrimSpace(raw.Service)
	if rootService == "" {
		rootService = fileService
	}
	if out.Service == "" {
		out.Service = rootService
	}
	if out.Team == "" {
		out.Team = firstString(raw.Team, "default")
	}
	fallback := fileService
	if fallback == "" {
		fallback = rootService
	}

	for _, r := range raw.Resources {
		out.Resources = append(out.Resources, resourceFromRaw(r))
	}
	for _, e := range raw.Exposures {
		e.Service = firstString(e.Service, fallback)
		out.Exposures = append(out.Exposures, e)
	}
	for _, d := range raw.Dependencies {
		d.Service = firstString(d.Service, fallback)
		out.Dependencies = append(out.Dependencies, d)
	}
	for _, c := range raw.Connections {
		out.Connections = append(out.Connections, c)
	}

	dir := filepath.Dir(abs)
	for _, inc := range raw.Include {
		inc = strings.TrimSpace(inc)
		if inc == "" {
			continue
		}
		child := inc
		if !filepath.IsAbs(child) {
			child = filepath.Join(dir, inc)
		}
		if err := resolveArchfileInto(child, vars, rootService, visited, depth+1, out); err != nil {
			return err
		}
	}
	return nil
}

func parseArchfile(path string) (*yaml.Node, rawArchFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, rawArchFile{}, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, rawArchFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, rawArchFile{}, fmt.Errorf("parse %s: empty document", path)
	}
	var raw rawArchFile
	if err := doc.Decode(&raw); err != nil {
		return nil, rawArchFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if raw.Schema != "" && raw.Schema != ArchfileSchema {
		return nil, rawArchFile{}, fmt.Errorf("parse %s: unsupported schema %q (want %q)", path, raw.Schema, ArchfileSchema)
	}
	return &doc, raw, nil
}

func interpolateArchfileMapping(root *yaml.Node, vars map[string]string) error {
	if root == nil {
		return nil
	}
	if root.Kind != yaml.MappingNode {
		return interpolateArchfileNode(root, vars)
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "vars" {
			continue
		}
		if err := interpolateArchfileNode(root.Content[i+1], vars); err != nil {
			return err
		}
	}
	return nil
}

func interpolateArchfileNode(n *yaml.Node, vars map[string]string) error {
	switch n.Kind {
	case yaml.ScalarNode:
		n.Value = expandArchfileString(n.Value, vars)
	case yaml.MappingNode, yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			if err := interpolateArchfileNode(c, vars); err != nil {
				return err
			}
		}
	}
	return nil
}

func expandArchfileString(s string, vars map[string]string) string {
	return archfileVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := archfileVarPattern.FindStringSubmatch(match)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		return match
	})
}

func resourceFromRaw(r rawResource) model.Resource {
	return model.Resource{
		ID:       strings.TrimSpace(r.ID),
		Kind:     strings.TrimSpace(r.Kind),
		Platform: strings.TrimSpace(r.Platform),
		Name:     strings.TrimSpace(r.Name),
		Instance: strings.TrimSpace(r.Instance),
		Summary:  strings.TrimSpace(r.Summary),
		Tags:     r.Tags,
		Details:  nonNilMap(r.Details),
		Status:   strings.TrimSpace(r.Status),
		Source:   strings.TrimSpace(r.Source),
	}
}

func baseFromRawEntity(e rawEntity, fallbackService, kind string) model.BaseEntity {
	service := firstString(e.Service, fallbackService)
	details := nonNilMap(e.Details)
	if e.Resource != "" {
		details["resource"] = strings.TrimSpace(e.Resource)
	}
	base := model.BaseEntity{
		ID:         strings.TrimSpace(e.ID),
		Type:       strings.TrimSpace(e.Type),
		Name:       strings.TrimSpace(e.Name),
		Service:    service,
		Platform:   strings.TrimSpace(e.Platform),
		Summary:    strings.TrimSpace(e.Summary),
		Tags:       e.Tags,
		Details:    details,
		Confidence: 1,
		Locations:  []model.Location{},
		Evidence:   []model.Evidence{},
	}
	if base.ID == "" {
		base.ID = util.ContentHash("archfile", kind, service, base.Type, base.Name)
	}
	return base
}

func addEntityRefs(refs map[string]model.BaseEntity, alias, name string, base model.BaseEntity) {
	if alias = strings.TrimSpace(alias); alias != "" {
		refs[alias] = base
	}
	if name = strings.TrimSpace(name); name != "" {
		refs[name] = base
	}
}

func conditionFromShorthand(s string) model.Condition {
	s = strings.TrimSpace(s)
	if s == "" {
		return model.Condition{Kind: "unconditional", Expression: "true", Explanation: "Always"}
	}
	return model.Condition{Kind: "expression", Expression: s, Explanation: s}
}

func pathSignature(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "file"
	}
	return "file:" + s
}

func readArchfileMaps(path string) (map[string][]map[string]any, map[string][]map[string]any, map[string][]map[string]any) {
	resolved, err := resolveArchfile(path)
	if err != nil {
		return map[string][]map[string]any{}, map[string][]map[string]any{}, map[string][]map[string]any{}
	}
	resByID := map[string]model.Resource{}
	for _, r := range resolved.Resources {
		resByID[r.ID] = r
	}
	expMaps := map[string][]map[string]any{}
	depMaps := map[string][]map[string]any{}
	connMaps := map[string][]map[string]any{}
	for _, e := range resolved.Exposures {
		m := rawEntityMap(e, resByID)
		expMaps[e.Type] = append(expMaps[e.Type], m)
	}
	for _, d := range resolved.Dependencies {
		m := rawEntityMap(d, resByID)
		depMaps[d.Type] = append(depMaps[d.Type], m)
	}
	for _, c := range resolved.Connections {
		m := map[string]any{
			"from_exposure_id": strings.TrimSpace(c.From),
			"to_dependency_id": strings.TrimSpace(c.To),
			"condition":        strings.TrimSpace(c.Condition),
			"summary":          strings.TrimSpace(c.Summary),
			"confidence":       1,
		}
		connMaps["connections"] = append(connMaps["connections"], m)
	}
	return expMaps, depMaps, connMaps
}

func rawEntityMap(e rawEntity, resources map[string]model.Resource) map[string]any {
	details := nonNilMap(e.Details)
	platform := strings.TrimSpace(e.Platform)
	if e.Resource != "" {
		details["resource"] = strings.TrimSpace(e.Resource)
		if r, ok := resources[e.Resource]; ok {
			if platform == "" {
				platform = r.Platform
			}
			details["resource_kind"] = r.Kind
			details["resource_name"] = r.Name
			details["resource_instance"] = r.Instance
			if details["platform"] == nil && r.Platform != "" {
				details["platform"] = r.Platform
			}
			if details["database_name"] == nil && (r.Kind == "datastore" || r.Kind == "database") {
				details["database_name"] = firstString(r.Instance, r.Name)
			}
			if details["cache_name"] == nil && r.Kind == "cache" {
				details["cache_name"] = firstString(r.Instance, r.Name)
			}
			if details["queue"] == nil && r.Kind == "message_bus" {
				details["queue"] = firstString(r.Instance, r.Name)
			}
			if details["target_service"] == nil && r.Kind == "service" {
				details["target_service"] = firstString(r.Instance, r.Name)
			}
		}
	}
	m := map[string]any{
		"id":         strings.TrimSpace(e.ID),
		"type":       strings.TrimSpace(e.Type),
		"name":       strings.TrimSpace(e.Name),
		"service":    strings.TrimSpace(e.Service),
		"platform":   platform,
		"summary":    strings.TrimSpace(e.Summary),
		"tags":       e.Tags,
		"details":    details,
		"confidence": 1,
	}
	return m
}

func readRunDirMaps(path string) (map[string][]map[string]any, map[string][]map[string]any, map[string][]map[string]any) {
	return readJSONDirMaps(filepath.Join(path, "exposures")),
		readJSONDirMaps(filepath.Join(path, "dependencies")),
		readJSONDirMaps(filepath.Join(path, "connections"))
}

func readJSONDirMaps(dir string) map[string][]map[string]any {
	out := make(map[string][]map[string]any)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var items []map[string]any
		if json.Unmarshal(b, &items) == nil {
			out[strings.TrimSuffix(e.Name(), ".json")] = items
		}
	}
	return out
}

func isYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func firstString(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func nonNilMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
