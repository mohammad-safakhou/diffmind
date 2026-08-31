package scip

import (
	"bytes"
	"encoding/binary"
	"sort"
	"testing"

	pb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// TestLoadEmptyIndexFailsWithoutMetadata: a SCIP stream MUST start
// with a Metadata message. We refuse anything that doesn't.
func TestLoadEmptyIndexFailsWithoutMetadata(t *testing.T) {
	_, err := LoadFromReader("empty", bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected error for empty stream, got nil")
	}
}

// TestLoadMinimalIndex: build a real SCIP index in-memory, encode it
// to bytes the way ParseStreaming expects, and assert Load returns it.
// This is the foundational integration test for the loader.
func TestLoadMinimalIndex(t *testing.T) {
	idx := &pb.Index{
		Metadata: &pb.Metadata{
			Version:      pb.ProtocolVersion_UnspecifiedProtocolVersion,
			ProjectRoot:  "file:///snap/X",
			TextDocumentEncoding: pb.TextEncoding_UTF8,
			ToolInfo: &pb.ToolInfo{
				Name:    "scip-test",
				Version: "1.2.3",
			},
		},
		Documents: []*pb.Document{
			{
				Language:     "java",
				RelativePath: "src/Foo.java",
				Occurrences: []*pb.Occurrence{
					{
						Range:       []int32{0, 0, 0, 3},
						Symbol:      "scip-java maven com.ex/Foo#",
						SymbolRoles: int32(pb.SymbolRole_Definition),
					},
					{
						Range:       []int32{5, 0, 5, 6},
						Symbol:      "scip-java maven com.ex/Bar#m().",
						SymbolRoles: int32(pb.SymbolRole_ReadAccess),
					},
				},
				Symbols: []*pb.SymbolInformation{
					{
						Symbol:      "scip-java maven com.ex/Foo#",
						DisplayName: "Foo",
						Kind:        pb.SymbolInformation_Class,
					},
				},
			},
		},
	}

	stream := encodeStream(t, idx)
	loaded, err := LoadFromReader("test", bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := loaded.ProjectRoot(); got != "file:///snap/X" {
		t.Errorf("ProjectRoot = %q", got)
	}
	if n, v, _ := loaded.ToolInfo(); n != "scip-test" || v != "1.2.3" {
		t.Errorf("ToolInfo = (%q, %q)", n, v)
	}
	if got := loaded.DocumentCount(); got != 1 {
		t.Errorf("DocumentCount = %d, want 1", got)
	}
	if got := loaded.SymbolDefinitionCount(); got != 1 {
		t.Errorf("SymbolDefinitionCount = %d, want 1 (only Foo# is a Definition)", got)
	}
	if d := loaded.Document("src/Foo.java"); d == nil {
		t.Errorf("Document(src/Foo.java) = nil")
	} else if len(d.GetOccurrences()) != 2 {
		t.Errorf("expected 2 occurrences, got %d", len(d.GetOccurrences()))
	}
	if d := loaded.Document("does/not/exist"); d != nil {
		t.Errorf("expected nil for missing doc, got %+v", d)
	}
}

// TestDocumentPathsSorted verifies the iteration order helper returns
// stable, sorted paths regardless of insertion order.
func TestDocumentPathsSorted(t *testing.T) {
	idx := &pb.Index{
		Metadata: &pb.Metadata{
			ProjectRoot: "file:///snap",
			ToolInfo:    &pb.ToolInfo{Name: "t"},
		},
		Documents: []*pb.Document{
			{RelativePath: "zeta.go"},
			{RelativePath: "alpha.go"},
			{RelativePath: "mu.go"},
		},
	}
	stream := encodeStream(t, idx)
	loaded, err := LoadFromReader("test", bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.DocumentPaths()
	want := []string{"alpha.go", "mu.go", "zeta.go"}
	if !sort.StringsAreSorted(got) {
		t.Errorf("DocumentPaths() not sorted: %v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("path[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestEmptyRelativePathDropped: SCIP technically allows a Document with
// an empty relative_path (rare, but possible from sloppy indexers); we
// drop those at load time so callers don't trip on "" lookups.
func TestEmptyRelativePathDropped(t *testing.T) {
	idx := &pb.Index{
		Metadata: &pb.Metadata{ToolInfo: &pb.ToolInfo{Name: "t"}, ProjectRoot: "file:///x"},
		Documents: []*pb.Document{
			{RelativePath: ""},
			{RelativePath: "good.go"},
		},
	}
	stream := encodeStream(t, idx)
	loaded, err := LoadFromReader("test", bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.DocumentCount(); got != 1 {
		t.Errorf("expected 1 document after dropping empty path, got %d", got)
	}
}

// ---- helpers ----

// encodeStream serializes an Index into the streaming SCIP format
// IndexVisitor.ParseStreaming expects: a sequence of length-delimited
// proto messages, each preceded by a single byte indicating which
// field of Index it represents.
//
// The wire format produced by the scip CLI is the canonical proto
// encoding of `Index` itself. ParseStreaming knows how to walk through
// the outer Index message and feed each Document/Metadata/etc. to the
// visitor callbacks. So we can just Marshal the whole Index here.
func encodeStream(t *testing.T, idx *pb.Index) []byte {
	t.Helper()
	buf, err := proto.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal Index: %v", err)
	}
	return buf
}

// keep binary referenced if a future test wants to hand-craft a
// length-delimited stream. The protobuf library handles the encoding
// for the in-memory Index path above, but if we ever need to test
// chunked / interleaved messages we'll use binary.PutUvarint here.
var _ = binary.PutUvarint
