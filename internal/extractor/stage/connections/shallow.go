package connections

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

func dedupeLocations(in []model.Location) []model.Location {
	seen := map[string]struct{}{}
	out := []model.Location{}
	for _, l := range in {
		key := fmt.Sprintf("%s:%d-%d", l.File, l.StartLine, l.EndLine)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, l)
	}
	return out
}

func formatConnectionSummary(exp model.Exposure, dep model.Dependency, pathCount int) string {
	if pathCount == 1 {
		return fmt.Sprintf("%s → %s (1 path)", exp.Name, dep.Name)
	}
	return fmt.Sprintf("%s → %s (%d paths)", exp.Name, dep.Name, pathCount)
}

func buildShallowConnections(exposures []model.Exposure, deps []model.Dependency, minConfidence float64) ([]model.Connection, []model.UnresolvedItem) {
	depIndex := indexDependenciesByKey(deps)
	conns := []model.Connection{}
	for _, exp := range exposures {
		for _, dep := range matchShallow(exp, depIndex) {
			pathSig := "shallow:" + exp.ID + "->" + dep.ID
			conns = append(conns, model.Connection{
				ID:             util.StableID(exp.ID, dep.ID, pathSig),
				FromExposureID: exp.ID,
				ToDependencyID: dep.ID,
				Source:         model.ConnectionSourceShallow,
				Summary:        formatConnectionSummary(exp, dep, 0),
				Locations:      exp.Locations,
				Evidence: []model.Evidence{{
					Location: firstLocation(exp.Locations),
					Snippet:  fmt.Sprintf("name-based match (no SCIP paths) to %s", dep.Name),
					Source:   "shallow",
				}},
				Confidence:    minConfidence,
				FromType:      exp.Type,
				ToType:        dep.Type,
				PathSignature: pathSig,
			})
		}
	}
	return conns, nil
}

type depIndexT struct {
	byName       map[string][]model.Dependency
	byRepository map[string][]model.Dependency
}

func indexDependenciesByKey(deps []model.Dependency) *depIndexT {
	idx := &depIndexT{
		byName:       map[string][]model.Dependency{},
		byRepository: map[string][]model.Dependency{},
	}
	for _, d := range deps {
		idx.byName[strings.ToLower(d.Name)] = append(idx.byName[strings.ToLower(d.Name)], d)
		if repo, ok := d.Details["repository"].(string); ok && repo != "" {
			idx.byRepository[strings.ToLower(repo)] = append(idx.byRepository[strings.ToLower(repo)], d)
		}
		if cls, ok := d.Details["class"].(string); ok && cls != "" {
			idx.byRepository[strings.ToLower(cls)] = append(idx.byRepository[strings.ToLower(cls)], d)
		}
	}
	return idx
}

func matchShallow(exp model.Exposure, idx *depIndexT) []model.Dependency {
	matched := map[string]model.Dependency{}
	add := func(deps []model.Dependency) {
		for _, d := range deps {
			matched[d.ID] = d
		}
	}
	if ops, ok := exp.Details["db_operations"].([]any); ok {
		for _, raw := range ops {
			op, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if repo, ok := op["repository"].(string); ok && repo != "" {
				add(idx.byRepository[strings.ToLower(repo)])
			}
			if method, ok := op["method"].(string); ok && method != "" {
				if p := strings.Index(method, "("); p > 0 {
					method = method[:p]
				}
				add(idx.byName[strings.ToLower(method)])
			}
		}
	}
	if depList, ok := exp.Details["dependencies"].([]any); ok {
		for _, raw := range depList {
			d, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := d["name"].(string); ok && name != "" {
				add(idx.byName[strings.ToLower(name)])
				add(idx.byRepository[strings.ToLower(name)])
			}
		}
	}
	out := make([]model.Dependency, 0, len(matched))
	for _, d := range matched {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func firstLocation(in []model.Location) model.Location {
	if len(in) == 0 {
		return model.Location{}
	}
	return in[0]
}
