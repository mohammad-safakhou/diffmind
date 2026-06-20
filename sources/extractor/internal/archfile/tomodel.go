package archfile

import (
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/catalog"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// objectiveKinds maps each known objective type to its entity kind, built once
// from the registry so the file format and the pipeline agree on the taxonomy.
var objectiveKinds = func() map[string]model.EntityKind {
	m := map[string]model.EntityKind{}
	for _, o := range objectives.Default() {
		m[o.Type] = o.Kind
	}
	return m
}()

// ToModel maps a resolved File to a catalog.ImportInput suitable for
// Store.ImportManual. Identity-bearing fields are populated exactly as the run
// importer populates them (extraction.EnrichEntityGrouping), so a file-authored
// fact and the same run-discovered fact collapse to one durable record.
func ToModel(f *File, source string) (catalog.ImportInput, error) {
	in := catalog.ImportInput{RunID: source}

	type ref struct {
		id  string
		typ string
	}
	exposureTokens := map[string]ref{}
	dependencyTokens := map[string]ref{}

	addTokens := func(tokens map[string]ref, alias, name string, r ref) {
		if alias != "" {
			tokens[alias] = r
		}
		if name != "" {
			tokens[name] = r
		}
	}

	for _, e := range f.Exposures {
		base, err := toBase(e, model.KindExposure)
		if err != nil {
			return catalog.ImportInput{}, err
		}
		in.Exposures = append(in.Exposures, model.Exposure{BaseEntity: base})
		addTokens(exposureTokens, e.Alias, e.Name, ref{id: base.ID, typ: base.Type})
	}
	for _, d := range f.Dependencies {
		base, err := toBase(d, model.KindDependency)
		if err != nil {
			return catalog.ImportInput{}, err
		}
		in.Dependencies = append(in.Dependencies, model.Dependency{BaseEntity: base})
		addTokens(dependencyTokens, d.Alias, d.Name, ref{id: base.ID, typ: base.Type})
	}

	for _, c := range f.Connections {
		// A connection whose endpoint isn't present is dropped, not fatal. This
		// happens routinely after a run import: the run's dependency may collapse
		// (by identity) onto an already-present fact and get skipped, leaving its
		// connection referencing a name the merged file no longer carries. One
		// dangling edge must not blank the entire graph — the resolved file simply
		// omits that edge. Genuine authoring typos surface the same way (a missing
		// edge), which the graph and inline editor make visible.
		from, ok := exposureTokens[c.From]
		if !ok {
			continue
		}
		to, ok := dependencyTokens[c.To]
		if !ok {
			continue
		}
		source := strings.TrimSpace(c.Source)
		if source == "" {
			source = "manual"
		}
		in.Connections = append(in.Connections, model.Connection{
			FromExposureID: from.id,
			ToDependencyID: to.id,
			FromType:       from.typ,
			ToType:         to.typ,
			Source:         source,
			Status:         strings.TrimSpace(c.Status),
			Confidence:     1,
			Condition:      conditionFromShorthand(c.Condition),
			PathSignature:  pathSignature(c.Condition),
			Summary:        strings.TrimSpace(c.Summary),
		})
	}
	return in, nil
}

func toBase(e Entity, kind model.EntityKind) (model.BaseEntity, error) {
	if e.Type == "" {
		return model.BaseEntity{}, fmt.Errorf("%s entry %q is missing a type", kind, e.Name)
	}
	if e.Name == "" {
		return model.BaseEntity{}, fmt.Errorf("%s entry of type %q is missing a name", kind, e.Type)
	}
	got, known := objectiveKinds[e.Type]
	if !known {
		return model.BaseEntity{}, fmt.Errorf("unknown type %q (entry %q)", e.Type, e.Name)
	}
	if got != kind {
		return model.BaseEntity{}, fmt.Errorf("type %q is a %s but is listed under %ss (entry %q)", e.Type, got, kind, e.Name)
	}

	details := map[string]any{}
	for k, v := range e.Details {
		details[k] = v
	}
	if e.Resource != "" {
		details["resource"] = e.Resource
	}
	base := model.BaseEntity{
		Type:       e.Type,
		Name:       e.Name,
		Service:    e.Service,
		Platform:   e.Platform,
		Summary:    e.Summary,
		Tags:       e.Tags,
		Details:    details,
		Confidence: 1,
		Locations:  []model.Location{},
		Evidence:   []model.Evidence{},
	}
	// Identity parity: derive Platform/Instance/Operation exactly as the run does
	// before computing the durable ID, so a file fact keys identically to a run
	// fact for the same real entity.
	extraction.EnrichEntityGrouping(&base)
	base.ID = util.StableID("archfile", string(kind), catalog.EntityCatalogKey(string(kind), base))
	return base, nil
}

func conditionFromShorthand(s string) model.Condition {
	s = strings.TrimSpace(s)
	if s == "" {
		return model.Condition{Kind: "unconditional", Expression: "true", Explanation: "Always"}
	}
	return model.Condition{Kind: "expression", Expression: s, Explanation: s}
}

// pathSignature is derived from authored content (the condition), independent of
// durable IDs, so it is identical on every re-import — keeping connection
// identity stable across the round trip.
func pathSignature(condition string) string {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return "file"
	}
	return "file:" + condition
}
