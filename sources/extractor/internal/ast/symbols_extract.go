package ast

import sitter "github.com/smacker/go-tree-sitter"

// extractSymbols runs the functions/methods query and builds SymbolDef list.
func extractSymbols(root *sitter.Node, src []byte, lang string, sitterLang *sitter.Language, relPath string) []SymbolDef {
	q := queriesForLanguage(lang)
	if q == nil || q.functions == nil {
		return nil
	}
	query, err := sitter.NewQuery(q.functions, sitterLang)
	if err != nil {
		return nil
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	// Also run the annotations query to attach annotations.
	annotsByLine := extractAnnotations(root, src, lang, sitterLang)

	// We do a two-pass approach:
	//   Pass 1: collect all symbols as-is.
	//   Pass 2: for languages that don't have explicit receiver syntax
	//           (Java, Kotlin, C#, Python classes, etc.), infer the class
	//           name from the enclosing class symbol and prepend it to
	//           every method's Qualified name.
	// This turns ambiguous names like "run" → "CIPlannedTargetDailyJob.run",
	// which prevents the walker from conflating methods across classes.

	var out []SymbolDef
	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var name, receiver string
		var kind SymbolKind
		var r Range
		var nameLine uint32
		var node *sitter.Node

		// defRange stores the FULL range of the @def node (method body).
		// nameRange stores just the identifier position.
		// We use defRange.EndLine for enclosingSymbol lookup.
		var defNode *sitter.Node

		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "name":
				name = text
				node = c.Node
				r = nodeRange(c.Node)
				nameLine = r.StartLine
			case "receiver", "class", "type":
				receiver = text
			case "def":
				defNode = c.Node
				switch c.Node.Type() {
				case "function_declaration", "function_definition", "function",
					"func_literal", "function_item", "def":
					kind = SymbolKindFunction
				case "method_declaration", "method_definition", "method",
					"function_def":
					kind = SymbolKindMethod
				case "class_declaration", "class_definition", "class",
					"class_body", "struct_item":
					kind = SymbolKindClass
				case "interface_declaration", "interface", "trait_item",
					"protocol_declaration":
					kind = SymbolKindInterface
				case "constructor_declaration", "constructor_definition":
					kind = SymbolKindConstructor
				default:
					kind = SymbolKindFunction
				}
				_ = node // used below
			}
		}

		if name == "" {
			continue
		}

		defR := r
		// If we have the full @def node, use its EndLine so that
		// enclosingSymbol() can correctly identify which method body
		// a call site belongs to. Without this, methods whose identifier
		// is on line N but body extends to line M would never match calls
		// on lines N+1..M.
		if defNode != nil {
			defR = nodeRange(defNode)
			r.EndLine = defR.EndLine
			r.EndColumn = defR.EndColumn
			r.EndByte = defR.EndByte
			// Also use the def node's start so the range covers annotations too.
			if defR.StartLine < r.StartLine {
				r.StartLine = defR.StartLine
				r.StartColumn = defR.StartColumn
				r.StartByte = defR.StartByte
			}
		}

		qualified := qualifiedName(receiver, name, lang)

		annots := annotationsForSymbol(annotsByLine, src, defR, nameLine)

		out = append(out, SymbolDef{
			Name:        name,
			Qualified:   qualified,
			Kind:        kind,
			File:        relPath,
			Range:       r,
			Receiver:    receiver,
			Annotations: annots,
			Parameters:  extractParams(defNode, src, lang),
		})
	}

	// Pass 2: for methods that have no explicit receiver, infer it from
	// the enclosing class. This applies to Java, Kotlin, C#, Python, etc.
	// where class membership is determined by nesting, not by receiver syntax.
	//
	// We only qualify names for languages that don't already have receiver
	// syntax in the query (Go methods get "Receiver.method" from Pass 1).
	switch lang {
	case "java", "kotlin", "csharp", "python", "typescript", "tsx", "javascript", "jsx", "php", "ruby":
		out = qualifyMethodsWithClass(out)
	}

	return out
}

// qualifyMethodsWithClass infers the enclosing class for each method that
// has an empty Receiver and prepends the class name to its Qualified symbol.
// This prevents "run", "send", "save" etc. from being ambiguous across all
// classes in the project.
func qualifyMethodsWithClass(symbols []SymbolDef) []SymbolDef {
	// Collect class symbols sorted by StartLine ascending.
	type classRange struct {
		name  string
		start uint32
		end   uint32
	}
	var classes []classRange
	for _, sym := range symbols {
		if sym.Kind == SymbolKindClass || sym.Kind == SymbolKindInterface {
			classes = append(classes, classRange{
				name:  sym.Name,
				start: sym.Range.StartLine,
				end:   sym.Range.EndLine,
			})
		}
	}

	// For each method/function without a receiver, find the innermost class
	// that contains it.
	result := make([]SymbolDef, len(symbols))
	copy(result, symbols)

	for i := range result {
		sym := &result[i]
		if sym.Kind != SymbolKindMethod && sym.Kind != SymbolKindFunction && sym.Kind != SymbolKindConstructor {
			continue
		}
		if sym.Receiver != "" {
			// Already has receiver (Go style).
			continue
		}
		// Find the innermost enclosing class.
		best := ""
		bestStart := uint32(0)
		for _, cls := range classes {
			if cls.start <= sym.Range.StartLine && cls.end >= sym.Range.EndLine {
				if cls.start >= bestStart {
					bestStart = cls.start
					best = cls.name
				}
			}
		}
		if best != "" && best != sym.Name {
			sym.Receiver = best
			sym.Qualified = best + "." + sym.Name
		}
	}
	return result
}
