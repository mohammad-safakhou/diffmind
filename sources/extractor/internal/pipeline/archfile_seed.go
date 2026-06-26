package pipeline

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/archfile"
	"github.com/mohammad-safakhou/diffmind/internal/catalog"
	"github.com/mohammad-safakhou/diffmind/internal/entitykey"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	reconcile "github.com/mohammad-safakhou/diffmind/internal/stage/reconcile"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// loadDeterministicArchfileSeed imports an in-repo DiffMind discovery file as
// a zero-token deterministic seed. The curated file wins when present; the
// generated/default file is the fallback. This is deliberately generic: it is
// DiffMind consuming its own versioned architecture-file format, not a
// repository-specific shortcut.
func (o *orchestrator) loadDeterministicArchfileSeed() (catalog.ImportInput, string, error) {
	for _, name := range []string{"diffmind.curated.yaml", "diffmind.yaml"} {
		path := filepath.Join(o.repoPath, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		f, err := archfile.Resolve(path)
		if err != nil {
			return catalog.ImportInput{}, path, err
		}
		if strings.TrimSpace(f.Service) == "" {
			f.Service = o.repoPath
		}
		in, err := archfile.ToModel(f, "archfile:"+name)
		if err != nil {
			return catalog.ImportInput{}, path, err
		}
		for i := range in.Exposures {
			in.Exposures[i].PluginSource = "archfile"
			in.Exposures[i].Tags = appendUnique(in.Exposures[i].Tags, "deterministic", "archfile")
		}
		for i := range in.Dependencies {
			in.Dependencies[i].PluginSource = "archfile"
			in.Dependencies[i].Tags = appendUnique(in.Dependencies[i].Tags, "deterministic", "archfile")
		}
		for i := range in.Connections {
			if in.Connections[i].Source == "" || in.Connections[i].Source == "manual" {
				in.Connections[i].Source = "archfile"
			}
			if in.Connections[i].ID == "" {
				in.Connections[i].ID = util.StableID("architecture-connection", in.Connections[i].FromExposureID, in.Connections[i].ToDependencyID, in.Connections[i].PathSignature)
			}
		}
		return in, path, nil
	}
	return catalog.ImportInput{}, "", nil
}

func mergeArchfileSeed(
	arch catalog.ImportInput,
	detectedExposures []model.Exposure,
	detectedDependencies []model.Dependency,
	detectedConnections []model.Connection,
) ([]model.Exposure, []model.Dependency, []model.Connection) {
	exposures, expID := mergeExposuresPreferArchfile(arch.Exposures, detectedExposures)
	dependencies, depID := mergeDependenciesPreferArchfile(arch.Dependencies, detectedDependencies)
	connections := mergeConnectionsPreferArchfile(arch.Connections, detectedConnections, detectedExposures, detectedDependencies, expID, depID)
	return exposures, dependencies, connections
}

func mergeExposuresPreferArchfile(arch, detected []model.Exposure) ([]model.Exposure, map[string]string) {
	out := append([]model.Exposure(nil), arch...)
	keyToID := map[string]string{}
	origToCanonical := map[string]string{}
	for _, e := range arch {
		key := exposureKey(e)
		keyToID[key] = e.ID
		origToCanonical[e.ID] = e.ID
	}
	for _, e := range detected {
		key := exposureKey(e)
		if id := keyToID[key]; id != "" {
			origToCanonical[e.ID] = id
			continue
		}
		keyToID[key] = e.ID
		origToCanonical[e.ID] = e.ID
		out = append(out, e)
	}
	return reconcile.DedupeExposures(out), origToCanonical
}

func mergeDependenciesPreferArchfile(arch, detected []model.Dependency) ([]model.Dependency, map[string]string) {
	out := append([]model.Dependency(nil), arch...)
	keyToID := map[string]string{}
	fallbackToID := uniqueArchDependencyFallbacks(arch)
	origToCanonical := map[string]string{}
	for _, d := range arch {
		key := dependencyKey(d)
		keyToID[key] = d.ID
		origToCanonical[d.ID] = d.ID
	}
	for _, d := range detected {
		key := dependencyKey(d)
		if id := keyToID[key]; id != "" {
			origToCanonical[d.ID] = id
			continue
		}
		if id := fallbackToID[dependencyArchFallbackKey(d)]; id != "" {
			origToCanonical[d.ID] = id
			continue
		}
		if id := fallbackToID[dependencyArchFallbackKeyWithoutPlatform(d)]; id != "" {
			origToCanonical[d.ID] = id
			continue
		}
		keyToID[key] = d.ID
		origToCanonical[d.ID] = d.ID
		out = append(out, d)
	}
	return reconcile.DedupeDependencies(out), origToCanonical
}

func mergeConnectionsPreferArchfile(
	arch []model.Connection,
	detected []model.Connection,
	detectedExposures []model.Exposure,
	detectedDependencies []model.Dependency,
	expID map[string]string,
	depID map[string]string,
) []model.Connection {
	out := append([]model.Connection(nil), arch...)
	seen := map[string]struct{}{}
	for _, c := range out {
		seen[connectionMergeKey(c)] = struct{}{}
	}
	expType := map[string]string{}
	for _, e := range detectedExposures {
		expType[expID[e.ID]] = e.Type
	}
	depType := map[string]string{}
	for _, d := range detectedDependencies {
		depType[depID[d.ID]] = d.Type
	}
	for _, c := range detected {
		from := expID[c.FromExposureID]
		to := depID[c.ToDependencyID]
		if from == "" || to == "" {
			continue
		}
		c.FromExposureID = from
		c.ToDependencyID = to
		if c.FromType == "" {
			c.FromType = expType[from]
		}
		if c.ToType == "" {
			c.ToType = depType[to]
		}
		if c.Source == "" {
			c.Source = model.ConnectionSourceAST
		}
		c.ID = util.StableID(c.FromExposureID, c.ToDependencyID, c.PathSignature)
		key := connectionMergeKey(c)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}

func exposureKey(e model.Exposure) string {
	if k := reconcile.SemanticKeyLoose(e.BaseEntity); k != "" {
		return k
	}
	return e.ID
}

func dependencyKey(d model.Dependency) string {
	if k := reconcile.SemanticKeyLoose(d.BaseEntity); k != "" {
		return k
	}
	return d.ID
}

func uniqueArchDependencyFallbacks(deps []model.Dependency) map[string]string {
	counts := map[string]int{}
	ids := map[string]string{}
	for _, d := range deps {
		seenForDep := map[string]struct{}{}
		for _, key := range []string{dependencyArchFallbackKey(d), dependencyArchFallbackKeyWithoutPlatform(d)} {
			if key == "" {
				continue
			}
			if _, seen := seenForDep[key]; seen {
				continue
			}
			seenForDep[key] = struct{}{}
			counts[key]++
			ids[key] = d.ID
		}
	}
	out := map[string]string{}
	for key, count := range counts {
		if count == 1 {
			out[key] = ids[key]
		}
	}
	return out
}

func dependencyArchFallbackKey(d model.Dependency) string {
	if d.Type != "db_operation" && d.Type != "cache_operation" {
		return ""
	}
	return strings.Join([]string{d.Type, dependencyArchOperation(d.BaseEntity), entitykey.PlatformClass(d.BaseEntity)}, "|")
}

func dependencyArchFallbackKeyWithoutPlatform(d model.Dependency) string {
	if d.Type != "db_operation" && d.Type != "cache_operation" {
		return ""
	}
	return strings.Join([]string{d.Type, dependencyArchOperation(d.BaseEntity), ""}, "|")
}

func dependencyArchOperation(b model.BaseEntity) string {
	op := entitykey.DataOperation(b)
	switch op {
	case "evict", "flush", "purge":
		return "write"
	default:
		return op
	}
}

func connectionMergeKey(c model.Connection) string {
	return strings.Join([]string{c.FromExposureID, c.ToDependencyID, strings.ToLower(strings.TrimSpace(c.PathSignature))}, "|")
}

func appendUnique(in []string, values ...string) []string {
	seen := map[string]struct{}{}
	for _, v := range in {
		seen[v] = struct{}{}
	}
	out := append([]string(nil), in...)
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
