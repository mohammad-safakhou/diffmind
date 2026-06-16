package archfile

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

type EditSet struct {
	Resources    []ResourceEdit   `json:"resources"`
	Exposures    []EntityEdit     `json:"exposures"`
	Dependencies []EntityEdit     `json:"dependencies"`
	Connections  []ConnectionEdit `json:"connections"`
}

type ResourceEdit struct {
	ID       string  `json:"id"`
	Kind     *string `json:"kind,omitempty"`
	Platform *string `json:"platform,omitempty"`
	Name     *string `json:"name,omitempty"`
	Instance *string `json:"instance,omitempty"`
	Summary  *string `json:"summary,omitempty"`
}

type EntityEdit struct {
	ID       string         `json:"id"`
	Type     *string        `json:"type,omitempty"`
	Name     *string        `json:"name,omitempty"`
	Resource *string        `json:"resource,omitempty"`
	Platform *string        `json:"platform,omitempty"`
	Summary  *string        `json:"summary,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

type ConnectionEdit struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	FromID    string  `json:"from_id"`
	ToID      string  `json:"to_id"`
	Condition *string `json:"condition,omitempty"`
	Summary   *string `json:"summary,omitempty"`
}

type DraftResult struct {
	YAML    string       `json:"yaml"`
	Graph   Graph        `json:"graph"`
	Summary DraftSummary `json:"summary"`
}

type DraftSummary struct {
	Resources    int `json:"resources"`
	Exposures    int `json:"exposures"`
	Dependencies int `json:"dependencies"`
	Connections  int `json:"connections"`
}

func Draft(path string, edits EditSet) (DraftResult, error) {
	p, err := parse(path)
	if err != nil {
		return DraftResult{}, err
	}
	raw := p.raw
	summary := applyEdits(&raw, edits)
	body, err := Marshal(&raw)
	if err != nil {
		return DraftResult{}, err
	}
	graph, err := graphForDraft(path, body)
	if err != nil {
		return DraftResult{}, err
	}
	return DraftResult{YAML: string(body), Graph: graph, Summary: summary}, nil
}

func applyEdits(raw *rawFile, edits EditSet) DraftSummary {
	var summary DraftSummary
	for _, edit := range edits.Resources {
		if applyResourceEdit(raw, edit) {
			summary.Resources++
		}
	}
	for _, edit := range edits.Exposures {
		if applyEntityEdit(raw, false, edit) {
			summary.Exposures++
		}
	}
	for _, edit := range edits.Dependencies {
		if applyEntityEdit(raw, true, edit) {
			summary.Dependencies++
		}
	}
	for _, edit := range edits.Connections {
		if applyConnectionEdit(raw, edit) {
			summary.Connections++
		}
	}
	return summary
}

func applyResourceEdit(raw *rawFile, edit ResourceEdit) bool {
	id := strings.TrimSpace(edit.ID)
	if id == "" {
		return false
	}
	for i := range raw.Resources {
		if raw.Resources[i].ID != id {
			continue
		}
		patchResource(&raw.Resources[i], edit)
		return true
	}
	r := rawResource{ID: id, Kind: "resource", Name: id}
	patchResource(&r, edit)
	raw.Resources = append(raw.Resources, r)
	return true
}

func patchResource(r *rawResource, edit ResourceEdit) {
	if edit.Kind != nil {
		r.Kind = strings.TrimSpace(*edit.Kind)
	}
	if edit.Platform != nil {
		r.Platform = strings.TrimSpace(*edit.Platform)
	}
	if edit.Name != nil {
		r.Name = strings.TrimSpace(*edit.Name)
	}
	if edit.Instance != nil {
		r.Instance = strings.TrimSpace(*edit.Instance)
	}
	if edit.Summary != nil {
		r.Summary = strings.TrimSpace(*edit.Summary)
	}
	if r.Kind == "" {
		r.Kind = "resource"
	}
	if r.Name == "" {
		r.Name = r.ID
	}
}

func applyEntityEdit(raw *rawFile, dependency bool, edit EntityEdit) bool {
	items := raw.Exposures
	if dependency {
		items = raw.Dependencies
	}
	idx := rawEntityIndex(raw, dependency, edit.ID)
	if idx < 0 {
		return false
	}
	patchEntity(&items[idx], edit, dependency)
	if dependency {
		raw.Dependencies = items
	} else {
		raw.Exposures = items
	}
	return true
}

func patchEntity(e *rawEntity, edit EntityEdit, dependency bool) {
	if edit.Type != nil {
		e.Type = strings.TrimSpace(*edit.Type)
	}
	if edit.Name != nil {
		e.Name = strings.TrimSpace(*edit.Name)
	}
	if dependency && edit.Resource != nil {
		e.Resource = strings.TrimSpace(*edit.Resource)
	}
	if edit.Platform != nil {
		e.Platform = strings.TrimSpace(*edit.Platform)
	}
	if edit.Summary != nil {
		e.Summary = strings.TrimSpace(*edit.Summary)
	}
	if edit.Details != nil {
		if e.Details == nil {
			e.Details = map[string]any{}
		}
		for k, v := range edit.Details {
			if v == nil {
				delete(e.Details, k)
				continue
			}
			e.Details[k] = v
		}
	}
}

func rawEntityIndex(raw *rawFile, dependency bool, id string) int {
	id = strings.TrimSpace(id)
	if id == "" {
		return -1
	}
	items := raw.Exposures
	kind := model.KindExposure
	if dependency {
		items = raw.Dependencies
		kind = model.KindDependency
	}
	fallback := strings.TrimSpace(raw.Service)
	for i, item := range items {
		if strings.TrimSpace(item.ID) == id || strings.TrimSpace(item.Name) == id {
			return i
		}
		base, err := toBase(toEntity(item, fallback), kind)
		if err == nil && base.ID == id {
			return i
		}
	}
	return -1
}

func applyConnectionEdit(raw *rawFile, edit ConnectionEdit) bool {
	from := strings.TrimSpace(edit.From)
	to := strings.TrimSpace(edit.To)
	if from == "" && edit.FromID != "" {
		from = rawEntityToken(raw, false, edit.FromID)
	}
	if to == "" && edit.ToID != "" {
		to = rawEntityToken(raw, true, edit.ToID)
	}
	if from == "" || to == "" {
		return false
	}
	for i := range raw.Connections {
		if raw.Connections[i].From != from || raw.Connections[i].To != to {
			continue
		}
		if edit.Condition != nil {
			raw.Connections[i].Condition = strings.TrimSpace(*edit.Condition)
		}
		if edit.Summary != nil {
			raw.Connections[i].Summary = strings.TrimSpace(*edit.Summary)
		}
		return true
	}
	return false
}

func rawEntityToken(raw *rawFile, dependency bool, id string) string {
	idx := rawEntityIndex(raw, dependency, id)
	if idx < 0 {
		return ""
	}
	item := raw.Exposures[idx]
	if dependency {
		item = raw.Dependencies[idx]
	}
	if strings.TrimSpace(item.ID) != "" {
		return strings.TrimSpace(item.ID)
	}
	return strings.TrimSpace(item.Name)
}

func graphForDraft(path string, body []byte) (Graph, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".diffmind-draft-*.yaml")
	if err != nil {
		return Graph{}, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return Graph{}, err
	}
	if err := tmp.Close(); err != nil {
		return Graph{}, err
	}
	resolved, err := Resolve(name)
	if err != nil {
		return Graph{}, err
	}
	return ToGraph(resolved, "draft:"+filepath.Base(path))
}
