package agents

import (
	"path/filepath"
	"strings"
)

// pathMapper rewrites file paths returned by an OpenCode session bound to
// the snapshot directory so that artifacts published to the user reference
// the original (source) paths. This is purely a string transformation: we
// never touch the snapshot or source filesystems here.
type pathMapper struct {
	snapshotPath string
	sourcePath   string
}

func newPathMapper(snapshotPath, sourcePath string) *pathMapper {
	return &pathMapper{snapshotPath: filepath.Clean(snapshotPath), sourcePath: filepath.Clean(sourcePath)}
}

// MapFile rewrites a single file path. It handles three shapes:
//  1. absolute path inside the snapshot           -> source-relative path
//  2. relative path that begins with the snapshot
//     directory's basename (some agents echo it)  -> stripped to source-relative
//  3. anything else                                -> returned as-is
//
// The result is always relative to the source tree (forward-slashes), to
// match the convention the rest of the pipeline uses for artifact paths.
func (m *pathMapper) MapFile(p string) string {
	if m == nil {
		return p
	}
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return p
	}
	if filepath.IsAbs(trimmed) {
		clean := filepath.Clean(trimmed)
		if rel, err := filepath.Rel(m.snapshotPath, clean); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
		// Absolute path that is not under the snapshot: leave it alone.
		return trimmed
	}
	clean := filepath.ToSlash(filepath.Clean(trimmed))
	snapBase := filepath.Base(m.snapshotPath)
	if strings.HasPrefix(clean, snapBase+"/") {
		clean = strings.TrimPrefix(clean, snapBase+"/")
	}
	return clean
}

// applyToEntity rewrites all path-bearing fields on an llmEntity in place.
func (m *pathMapper) applyToEntity(e *llmEntity) {
	if e == nil || m == nil {
		return
	}
	for i := range e.Locations {
		e.Locations[i].File = m.MapFile(e.Locations[i].File)
	}
	for i := range e.Evidence {
		e.Evidence[i].File = m.MapFile(e.Evidence[i].File)
	}
}

// applyToEntities is a convenience for slices of llmEntity.
func (m *pathMapper) applyToEntities(es []llmEntity) {
	for i := range es {
		m.applyToEntity(&es[i])
	}
}

// applyToConnection / applyToConnections were the LLM-era helpers that
// rewrote file paths inside an `llmConnection` returned by the model.
// The connections stage is now deterministic and never produces an
// `llmConnection`; these helpers are therefore obsolete and have been
// removed. If you need to rewrite file paths inside a model.Connection,
// call MapFile on each Location yourself — the new connections stage
// already emits paths relative to the snapshot root, so this is
// almost never needed.
