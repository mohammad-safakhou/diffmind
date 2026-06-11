package ast

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// extractImports runs the imports query and returns the resolved import list.
func extractImports(root *sitter.Node, src []byte, lang string, sitterLang *sitter.Language) []ImportDecl {
	q := queriesForLanguage(lang)
	if q == nil || q.imports == nil {
		return nil
	}
	query, err := sitter.NewQuery(q.imports, sitterLang)
	if err != nil {
		return nil
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	var out []ImportDecl
	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var path, alias string
		for _, c := range m.Captures {
			name := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch name {
			case "path":
				path = trimQuotes(text)
			case "alias":
				alias = text
			}
		}
		if path == "" {
			continue
		}
		if alias == "" {
			// Derive alias from the last path segment.
			parts := strings.Split(path, "/")
			alias = parts[len(parts)-1]
			// Handle dots: "com.example.Foo" → "Foo"
			if dotParts := strings.Split(alias, "."); len(dotParts) > 0 {
				alias = dotParts[len(dotParts)-1]
			}
		}
		out = append(out, ImportDecl{Alias: alias, Path: path})
	}
	return out
}
