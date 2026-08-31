package ast

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// extractAnnotations returns a map from line number to annotations at that line.
func extractAnnotations(root *sitter.Node, src []byte, lang string, sitterLang *sitter.Language) map[uint32][]Annotation {
	q := queriesForLanguage(lang)
	out := map[uint32][]Annotation{}
	if q == nil || q.annotations == nil {
		return out
	}
	query, err := sitter.NewQuery(q.annotations, sitterLang)
	if err != nil {
		return out
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var annotName, args string
		var r Range
		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "name":
				annotName = text
				r = nodeRange(c.Node)
			case "args":
				args = trimOuterParens(text)
				argRange := nodeRange(c.Node)
				if r.StartLine == 0 && r.EndLine == 0 {
					r = argRange
				} else {
					r.EndLine = argRange.EndLine
					r.EndColumn = argRange.EndColumn
					r.EndByte = argRange.EndByte
				}
			}
		}
		if annotName != "" {
			out[r.StartLine] = append(out[r.StartLine], Annotation{Name: annotName, Arguments: args, Range: r})
		}
	}
	return out
}

func annotationsForSymbol(annotsByLine map[uint32][]Annotation, src []byte, defR Range, nameLine uint32) []Annotation {
	var out []Annotation
	seen := map[string]struct{}{}
	add := func(anns []Annotation) {
		for _, ann := range anns {
			key := ann.Name + "\x00" + ann.Arguments + "\x00" + fmt.Sprint(ann.Range.StartLine)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ann)
		}
	}

	// Prefer annotations that tree-sitter includes in the declaration node. This
	// keeps class annotations attached to classes and method annotations attached
	// only to the method they decorate.
	for line := defR.StartLine; line <= nameLine; line++ {
		add(annotsByLine[line])
	}
	if len(out) > 0 {
		return out
	}

	// Fallback for grammars whose declaration node starts at the identifier:
	// walk upward only through immediately adjacent annotation lines. Stop at
	// blanks or code so class-level annotations cannot leak into methods.
	lines := strings.Split(string(src), "\n")
	for line := int(nameLine) - 1; line >= 0 && line < len(lines); line-- {
		if anns := annotsByLine[uint32(line)]; len(anns) > 0 {
			out = append(append([]Annotation(nil), anns...), out...)
			continue
		}
		break
	}
	return out
}
