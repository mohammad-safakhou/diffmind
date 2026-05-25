// Package scip is diffmind's interface to SCIP (Source Code Intelligence
// Protocol) indexes produced by the diffmind-indexer image. It provides:
//
//   - Streaming load of a SCIP index file (proto3 stream of varint-
//     length-delimited messages)
//   - Helpers for navigating the loaded index: looking up the symbol at
//     a file:line:column, walking call-graph occurrences, resolving
//     interface-to-implementation relationships
//
// The package is intentionally read-only: it never mutates a SCIP file
// and never re-emits one. Writing SCIP is the indexer's job; querying
// is ours.
//
// PERFORMANCE
//
// A real-world Java index for a medium service is typically 10-50 MB
// uncompressed proto. We deserialize it once into an in-memory map of
// Documents keyed by relative_path, plus a secondary symbol index. For
// huge monorepos (>500 MB indexes) we'll need an mmap-based loader; for
// Sprint 1 the simpler approach is fine.
//
// THREAD SAFETY
//
// An Index is read-only after Load returns; multiple goroutines may
// query it concurrently. The exposed types are immutable in practice
// (they alias the SCIP proto messages, which have unexported fields
// and Get*() accessors).
package scip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	pb "github.com/scip-code/scip/bindings/go/scip"
)

// Index is diffmind's high-level view of a loaded SCIP index. Wraps
// the raw proto Index with lookup tables built at load time.
//
// The exported fields are deliberately minimal; richer queries are
// available via the methods on this type. We do not re-export the
// raw documents/occurrences slices because callers should not rely
// on their order or contents — those are implementation details of
// whichever indexer produced the file.
type Index struct {
	// path is the on-disk path the index was loaded from. Kept for
	// debug logging and error attribution.
	path string

	// metadata holds the index's overall metadata: project root,
	// encoding, tool info. Useful for sanity checks (project_root
	// should equal the snapshot directory; tool_info tells us which
	// indexer produced this index).
	metadata *pb.Metadata

	// documentsByPath is the primary lookup table. Keyed by the
	// relative path (forward slashes, no leading '/') as emitted by
	// the indexer. SCIP guarantees one Document per source file.
	documentsByPath map[string]*pb.Document

	// symbolDefinitions maps a SCIP symbol string to the (document
	// path, occurrence index) pair where that symbol is defined. A
	// symbol can have multiple definitions in pathological cases
	// (overloaded methods with the same erased signature, for example);
	// we store all of them and let the caller decide.
	symbolDefinitions map[string][]symbolLocation

	// externalSymbols lists symbols this index references but whose
	// definitions live in another index (e.g. a Maven library). These
	// have docstrings but no occurrence; useful for hover but not for
	// call-graph walking. We retain the slice as-is.
	externalSymbols []*pb.SymbolInformation
}

// symbolLocation is the (document path, occurrence index) pair that
// identifies a single position where a symbol is referenced or defined.
//
// We use an index into Document.Occurrences instead of a pointer to
// the Occurrence so we don't have to reach back through the proto
// message for line/column data when callers ask for it.
type symbolLocation struct {
	DocumentPath    string
	OccurrenceIndex int
}

// Load reads a SCIP index from disk and returns an Index ready for
// querying. The file may be a streaming SCIP proto (the canonical
// format produced by `scip merge`) or a single Index message;
// ParseStreaming handles both.
//
// Memory footprint: roughly 1.5x the file size on disk, because we
// keep the parsed proto + lookup tables in memory. For Sprint 1 this
// is acceptable.
func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open scip index: %w", err)
	}
	defer f.Close()
	return LoadFromReader(path, f)
}

// LoadFromReader is the io.Reader-driven variant. Used by tests with
// in-memory fixtures and by future remote-loader code paths.
//
// The visitor callbacks may return a non-nil error to abort parsing;
// we use that to short-circuit on the first malformed Document.
func LoadFromReader(name string, r io.Reader) (*Index, error) {
	idx := &Index{
		path:              name,
		documentsByPath:   map[string]*pb.Document{},
		symbolDefinitions: map[string][]symbolLocation{},
	}

	visitor := &pb.IndexVisitor{
		VisitMetadata: func(_ context.Context, m *pb.Metadata) error {
			idx.metadata = m
			return nil
		},
		VisitDocument: func(_ context.Context, d *pb.Document) error {
			// Defensive: drop entries with an empty path.
			if d.GetRelativePath() == "" {
				return nil
			}
			path := d.GetRelativePath()
			idx.documentsByPath[path] = d
			for i, occ := range d.GetOccurrences() {
				if occ.GetSymbol() == "" {
					continue
				}
				if isDefinitionRole(occ.GetSymbolRoles()) {
					idx.symbolDefinitions[occ.GetSymbol()] = append(
						idx.symbolDefinitions[occ.GetSymbol()],
						symbolLocation{DocumentPath: path, OccurrenceIndex: i},
					)
				}
			}
			return nil
		},
		VisitExternalSymbol: func(_ context.Context, si *pb.SymbolInformation) error {
			idx.externalSymbols = append(idx.externalSymbols, si)
			return nil
		},
	}

	if err := visitor.ParseStreaming(context.Background(), r); err != nil {
		return nil, fmt.Errorf("parse scip stream: %w", err)
	}

	if idx.metadata == nil {
		return nil, errors.New("scip index missing metadata block")
	}
	return idx, nil
}

// Path returns the path the index was loaded from. Useful for log
// messages.
func (i *Index) Path() string { return i.path }

// ProjectRoot returns the indexer's reported project root URI. This
// should equal the snapshot directory the indexer ran against.
func (i *Index) ProjectRoot() string {
	if i == nil || i.metadata == nil {
		return ""
	}
	return i.metadata.GetProjectRoot()
}

// ToolInfo returns the indexer's name + version + invocation args.
// Used by the diffmind runner to record which indexer produced the
// index for debugging.
func (i *Index) ToolInfo() (name, version string, args []string) {
	if i == nil || i.metadata == nil {
		return "", "", nil
	}
	ti := i.metadata.GetToolInfo()
	if ti == nil {
		return "", "", nil
	}
	return ti.GetName(), ti.GetVersion(), ti.GetArguments()
}

// DocumentCount returns the number of source files in the index.
// Sanity-check signal: a well-indexed mid-size Java service yields
// hundreds to thousands of Documents.
func (i *Index) DocumentCount() int { return len(i.documentsByPath) }

// SymbolDefinitionCount is the count of unique symbol strings that
// have at least one definition in this index. Useful for size
// reporting in the index stage's event payload.
func (i *Index) SymbolDefinitionCount() int { return len(i.symbolDefinitions) }

// Document returns the Document for the given relative path, or nil
// if no such file is in the index. The returned *pb.Document is
// owned by the Index; callers must not modify it.
func (i *Index) Document(relativePath string) *pb.Document {
	if i == nil {
		return nil
	}
	return i.documentsByPath[relativePath]
}

// DocumentPaths returns the sorted list of file paths present in the
// index. Sorting is for deterministic iteration order in tests and
// logs; production callers don't usually iterate this directly.
func (i *Index) DocumentPaths() []string {
	if i == nil {
		return nil
	}
	out := make([]string, 0, len(i.documentsByPath))
	for p := range i.documentsByPath {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// isDefinitionRole returns true if the SymbolRole bitset includes the
// Definition flag. Pulled out so the loader stays readable.
func isDefinitionRole(roles int32) bool {
	return roles&int32(pb.SymbolRole_Definition) != 0
}
