package archfile

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxIncludeDepth = 20

// varPattern matches ${name} variables. It deliberately excludes ${ENV:default}
// runtime placeholders (which contain a colon) so those pass through verbatim —
// they belong to a different layer (InstanceRef.URLTemplate), not authoring vars.
var varPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// Resolve reads a discovery file, expands its ${vars}, inlines its includes, and
// applies service defaults, returning a flat resolved File.
func Resolve(path string) (*File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	out := &File{}
	if err := resolveFile(abs, map[string]string{}, "", map[string]bool{}, 0, out); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveFile(abs string, inheritedVars map[string]string, rootService string, visited map[string]bool, depth int, out *File) error {
	if depth > maxIncludeDepth {
		return fmt.Errorf("include depth exceeded %d at %s", maxIncludeDepth, abs)
	}
	if visited[abs] {
		return fmt.Errorf("include cycle detected at %s", abs)
	}
	visited[abs] = true
	defer delete(visited, abs) // per-branch: a diamond re-include is allowed, a cycle is not

	p, err := parse(abs)
	if err != nil {
		return err
	}

	// Merge variables: inherited first, then this file's own (which shadow).
	vars := make(map[string]string, len(inheritedVars)+len(p.raw.Vars))
	for k, v := range inheritedVars {
		vars[k] = v
	}
	for k, v := range p.raw.Vars {
		// A var value may reference an inherited var; expand best-effort.
		expanded, verr := expandString(v, inheritedVars)
		if verr != nil {
			return fmt.Errorf("%s: var %q: %w", abs, k, verr)
		}
		vars[k] = expanded
	}

	// Interpolate the node tree (skipping the vars block), then re-decode.
	if err := interpolateMapping(p.rootMapping(), vars, abs); err != nil {
		return err
	}
	var raw rawFile
	if err := p.doc.Decode(&raw); err != nil {
		return fmt.Errorf("%s: %w", abs, err)
	}

	fileService := strings.TrimSpace(raw.Service)
	if rootService == "" {
		rootService = fileService
	}
	if out.Service == "" {
		out.Service = rootService
	}
	fallback := fileService
	if fallback == "" {
		fallback = rootService
	}

	for _, r := range raw.Resources {
		out.Resources = append(out.Resources, toResource(r))
	}
	for _, e := range raw.Exposures {
		out.Exposures = append(out.Exposures, toEntity(e, fallback))
	}
	for _, d := range raw.Dependencies {
		out.Dependencies = append(out.Dependencies, toEntity(d, fallback))
	}
	for _, c := range raw.Connections {
		out.Connections = append(out.Connections, Conn{
			From:      strings.TrimSpace(c.From),
			To:        strings.TrimSpace(c.To),
			Condition: strings.TrimSpace(c.Condition),
			Summary:   strings.TrimSpace(c.Summary),
		})
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
		if err := resolveFile(child, vars, rootService, visited, depth+1, out); err != nil {
			return err
		}
	}
	return nil
}

func toResource(r rawResource) Resource {
	details := r.Details
	if details == nil {
		details = map[string]any{}
	}
	return Resource{
		ID:       strings.TrimSpace(r.ID),
		Kind:     strings.TrimSpace(r.Kind),
		Platform: strings.TrimSpace(r.Platform),
		Name:     strings.TrimSpace(r.Name),
		Instance: strings.TrimSpace(r.Instance),
		Summary:  strings.TrimSpace(r.Summary),
		Tags:     r.Tags,
		Details:  details,
	}
}

func toEntity(e rawEntity, fallbackService string) Entity {
	service := strings.TrimSpace(e.Service)
	if service == "" {
		service = fallbackService
	}
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	return Entity{
		Alias:    strings.TrimSpace(e.ID),
		Type:     strings.TrimSpace(e.Type),
		Name:     strings.TrimSpace(e.Name),
		Resource: strings.TrimSpace(e.Resource),
		Service:  service,
		Summary:  strings.TrimSpace(e.Summary),
		Platform: strings.TrimSpace(e.Platform),
		Tags:     e.Tags,
		Details:  details,
	}
}

// interpolateMapping walks the top-level mapping and interpolates every scalar
// except the values under the `vars:` key (variable definitions are literal).
func interpolateMapping(root *yaml.Node, vars map[string]string, file string) error {
	if root.Kind != yaml.MappingNode {
		return interpolateNode(root, vars, file)
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		if key.Value == "vars" {
			continue
		}
		if err := interpolateNode(val, vars, file); err != nil {
			return err
		}
	}
	return nil
}

func interpolateNode(n *yaml.Node, vars map[string]string, file string) error {
	switch n.Kind {
	case yaml.ScalarNode:
		expanded, err := expandScalar(n.Value, vars, file, n.Line)
		if err != nil {
			return err
		}
		n.Value = expanded
		return nil
	case yaml.MappingNode, yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			if err := interpolateNode(c, vars, file); err != nil {
				return err
			}
		}
	}
	return nil
}

func expandScalar(s string, vars map[string]string, file string, line int) (string, error) {
	var missing string
	out := varPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := varPattern.FindStringSubmatch(match)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		if missing == "" {
			missing = name
		}
		return match
	})
	if missing != "" {
		return "", fmt.Errorf("%s:%d: unknown variable ${%s}", file, line, missing)
	}
	return out, nil
}

// expandString is the value-only variant used for variable definitions, where no
// node position is available.
func expandString(s string, vars map[string]string) (string, error) {
	var missing string
	out := varPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := varPattern.FindStringSubmatch(match)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		if missing == "" {
			missing = name
		}
		return match
	})
	if missing != "" {
		return "", fmt.Errorf("unknown variable ${%s}", missing)
	}
	return out, nil
}
