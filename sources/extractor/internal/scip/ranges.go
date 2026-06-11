package scip

import (
	"strings"

	pb "github.com/scip-code/scip/bindings/go/scip"
)

type rng struct {
	startLine, startCol, endLine, endCol int32
}

func occurrenceRange(occ *pb.Occurrence) *rng {
	return parseRange(occ.GetRange())
}

func occurrenceEnclosing(occ *pb.Occurrence) *rng {
	r := occ.GetEnclosingRange()
	if len(r) == 0 {
		return nil
	}
	return parseRange(r)
}

// inferBodyRange is the fallback for indexers that omit enclosing_range. It
// ends a callable body at the next sibling definition, ignoring locals,
// parameters, and synthetic constructor symbols.
func inferBodyRange(occurrences []*pb.Occurrence, definitionIndex int) *rng {
	definitionRange := occurrenceRange(occurrences[definitionIndex])
	if definitionRange == nil {
		return nil
	}
	const sentinel = int32(1<<30 - 1)
	nextLine := sentinel
	for index, occurrence := range occurrences {
		if index == definitionIndex ||
			!isDefinitionRole(occurrence.GetSymbolRoles()) ||
			!isSiblingDefinitionSymbol(occurrence.GetSymbol()) {
			continue
		}
		r := occurrenceRange(occurrence)
		if r != nil && r.startLine > definitionRange.startLine && r.startLine < nextLine {
			nextLine = r.startLine
		}
	}
	end := nextLine - 1
	if end < definitionRange.startLine {
		end = definitionRange.startLine
	}
	return &rng{
		startLine: definitionRange.startLine,
		startCol:  0,
		endLine:   end,
		endCol:    sentinel,
	}
}

func isCallableSymbol(symbol string) bool {
	if symbol == "" || strings.HasPrefix(symbol, "local ") {
		return false
	}
	if strings.HasSuffix(symbol, ")") {
		return false
	}
	if strings.HasSuffix(symbol, ".") && !strings.Contains(symbol, "()") {
		return false
	}
	return true
}

func isSiblingDefinitionSymbol(symbol string) bool {
	if symbol == "" || strings.HasPrefix(symbol, "local ") {
		return false
	}
	if strings.HasSuffix(symbol, ")") || strings.Contains(symbol, "().(") {
		return false
	}
	return !strings.Contains(symbol, "#`<")
}

func parseRange(r []int32) *rng {
	switch len(r) {
	case 3:
		return &rng{startLine: r[0], startCol: r[1], endLine: r[0], endCol: r[2]}
	case 4:
		return &rng{startLine: r[0], startCol: r[1], endLine: r[2], endCol: r[3]}
	default:
		return nil
	}
}

func rangeContains(r *rng, line, col int32) bool {
	if r == nil || line < r.startLine || line > r.endLine {
		return false
	}
	if line == r.startLine && col < r.startCol {
		return false
	}
	return line != r.endLine || col < r.endCol
}

func rangeContainedIn(inner, outer *rng) bool {
	if inner == nil || outer == nil {
		return false
	}
	if inner.startLine < outer.startLine ||
		(inner.startLine == outer.startLine && inner.startCol < outer.startCol) {
		return false
	}
	if inner.endLine > outer.endLine ||
		(inner.endLine == outer.endLine && inner.endCol > outer.endCol) {
		return false
	}
	return true
}

func rangeArea(r *rng) int64 {
	if r == nil {
		return 1 << 62
	}
	lines := int64(r.endLine - r.startLine + 1)
	if lines <= 0 {
		lines = 1
	}
	columns := int64(r.endCol - r.startCol)
	if r.startLine != r.endLine {
		columns = 0
	}
	if columns < 0 {
		columns = 0
	}
	return lines*4096 + columns
}

func occurrenceToLocation(file string, occurrence *pb.Occurrence) Location {
	r := occurrenceRange(occurrence)
	if r == nil {
		return Location{File: file}
	}
	return Location{
		File: file, StartLine: r.startLine, StartCol: r.startCol,
		EndLine: r.endLine, EndCol: r.endCol,
	}
}

func enclosingLocation(file string, occurrence *pb.Occurrence) Location {
	r := occurrenceEnclosing(occurrence)
	if r == nil {
		return Location{}
	}
	return Location{
		File: file, StartLine: r.startLine, StartCol: r.startCol,
		EndLine: r.endLine, EndCol: r.endCol,
	}
}
