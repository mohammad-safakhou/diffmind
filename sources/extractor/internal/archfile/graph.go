package archfile

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// Graph is the resource-oriented projection consumed by the repo graph UI.
// It keeps file-authored resources when present, and derives compatible
// resources from dependency identity for older diffmind.yaml files.
type Graph struct {
	Service      string             `json:"service"`
	Resources    []GraphResource    `json:"resources"`
	Exposures    []model.Exposure   `json:"exposures"`
	Dependencies []GraphDependency  `json:"dependencies"`
	Connections  []model.Connection `json:"connections"`
}

type GraphResource struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Platform string         `json:"platform,omitempty"`
	Name     string         `json:"name"`
	Instance string         `json:"instance,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
	Derived  bool           `json:"derived,omitempty"`
}

type GraphDependency struct {
	model.Dependency
	ResourceID string `json:"resource_id,omitempty"`
}

func ToGraph(f *File, source string) (Graph, error) {
	input, err := ToModel(f, source)
	if err != nil {
		return Graph{}, err
	}
	g := Graph{
		Service:     strings.TrimSpace(f.Service),
		Exposures:   input.Exposures,
		Connections: input.Connections,
	}

	resources := map[string]GraphResource{}
	for _, r := range f.Resources {
		gr, err := graphResource(r)
		if err != nil {
			return Graph{}, err
		}
		resources[gr.ID] = gr
	}

	for i, d := range input.Dependencies {
		var authored Entity
		if i < len(f.Dependencies) {
			authored = f.Dependencies[i]
		}
		resourceID := strings.TrimSpace(authored.Resource)
		if resourceID == "" {
			resourceID = detailString(d.Details, "resource")
		}
		if resourceID == "" || resources[resourceID].ID == "" {
			derived := derivedResource(d.BaseEntity)
			if resourceID == "" {
				resourceID = derived.ID
			}
			if resources[resourceID].ID == "" {
				derived.ID = resourceID
				resources[resourceID] = derived
			}
		}
		g.Dependencies = append(g.Dependencies, GraphDependency{Dependency: d, ResourceID: resourceID})
	}

	for _, r := range resources {
		g.Resources = append(g.Resources, r)
	}
	sort.Slice(g.Resources, func(i, j int) bool {
		if g.Resources[i].Kind != g.Resources[j].Kind {
			return g.Resources[i].Kind < g.Resources[j].Kind
		}
		return g.Resources[i].Name < g.Resources[j].Name
	})
	return g, nil
}

func graphResource(r Resource) (GraphResource, error) {
	id := strings.TrimSpace(r.ID)
	if id == "" {
		return GraphResource{}, fmt.Errorf("resource %q is missing id", r.Name)
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return GraphResource{}, fmt.Errorf("resource %q is missing name", id)
	}
	kind := strings.TrimSpace(r.Kind)
	if kind == "" {
		kind = "resource"
	}
	return GraphResource{
		ID:       id,
		Kind:     kind,
		Platform: strings.TrimSpace(r.Platform),
		Name:     name,
		Instance: strings.TrimSpace(r.Instance),
		Summary:  strings.TrimSpace(r.Summary),
		Tags:     r.Tags,
		Details:  r.Details,
	}, nil
}

func derivedResource(b model.BaseEntity) GraphResource {
	kind := resourceKind(b.Type)
	platform := firstNonEmpty(b.Platform, detailString(b.Details, "platform"), kind)
	instance := firstNonEmpty(
		b.Instance,
		detailString(b.Details, "database"),
		detailString(b.Details, "database_name"),
		detailString(b.Details, "datasource"),
		detailString(b.Details, "table"),
		detailString(b.Details, "entity"),
		detailString(b.Details, "queue"),
		detailString(b.Details, "queue_name"),
		detailString(b.Details, "topic"),
		detailString(b.Details, "stream"),
		detailString(b.Details, "target_service"),
		detailString(b.Details, "service"),
		detailString(b.Details, "host"),
		b.Name,
	)
	id := "res_" + slug(strings.Join([]string{kind, platform, instance}, "_"))
	name := resourceName(platform, instance)
	return GraphResource{
		ID:       id,
		Kind:     kind,
		Platform: platform,
		Name:     name,
		Instance: instance,
		Derived:  true,
	}
}

func resourceKind(entityType string) string {
	switch entityType {
	case "db_operation":
		return "datastore"
	case "cache_operation":
		return "cache"
	case "queue_publish", "queue_consumer", "stream_consume":
		return "message_bus"
	case "outbound_http", "outbound_rpc":
		return "service"
	case "command_exec":
		return "process"
	default:
		return "resource"
	}
}

func resourceName(platform, instance string) string {
	switch {
	case platform != "" && instance != "":
		return strings.TrimSpace(platform + " / " + instance)
	case instance != "":
		return instance
	case platform != "":
		return platform
	default:
		return "Resource"
	}
}

func detailString(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	v, ok := details[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugPattern.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "resource"
	}
	return s
}

func resourceIDForBase(b model.BaseEntity) string {
	if id := detailString(b.Details, "resource"); id != "" {
		return id
	}
	return derivedResource(b).ID
}

func resourceForBase(b model.BaseEntity) rawResource {
	r := derivedResource(b)
	r.ID = resourceIDForBase(b)
	return rawResource{
		ID:       r.ID,
		Kind:     r.Kind,
		Platform: r.Platform,
		Name:     r.Name,
		Instance: r.Instance,
		Summary:  r.Summary,
		Tags:     r.Tags,
		Details:  r.Details,
	}
}
