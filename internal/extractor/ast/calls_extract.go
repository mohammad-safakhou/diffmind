package ast

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// extractCalls runs the calls query and builds CallSite list.
// symbolsInFile is used to determine the enclosing function for each call.
func extractCalls(root *sitter.Node, src []byte, lang string, sitterLang *sitter.Language, relPath string, symbolsInFile []SymbolDef) []CallSite {
	q := queriesForLanguage(lang)
	if q == nil || q.calls == nil {
		return nil
	}
	query, err := sitter.NewQuery(q.calls, sitterLang)
	if err != nil {
		return nil
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	// seen deduplicates on (callee, startByte) so the same call expression
	// is not emitted twice when multiple query patterns match it.
	seen := map[uint64]struct{}{}
	deupKey := func(callee string, startByte uint32) uint64 {
		// Simple hash: combine startByte with a hash of the callee string.
		h := uint64(startByte) * 2654435761
		for _, ch := range callee {
			h = h*31 + uint64(ch)
		}
		return h
	}

	var out []CallSite
	for m, ok := cursor.NextMatch(); ok; m, ok = cursor.NextMatch() {
		var calleeRaw, receiverRaw, argsText string
		var callNode *sitter.Node
		var isMethodRef bool
		var methodRefNode *sitter.Node

		for _, c := range m.Captures {
			capName := query.CaptureNameForId(c.Index)
			text := c.Node.Content(src)
			switch capName {
			case "callee":
				calleeRaw = text
				callNode = c.Node
			case "receiver":
				receiverRaw = text
			case "args":
				argsText = text
			case "method_ref":
				// Java/Kotlin method reference: extract the method name after "::"
				isMethodRef = true
				methodRefNode = c.Node
				full := text // e.g. "service::processItem" or "CampaignMapper::map"
				if idx := strings.LastIndex(full, "::"); idx >= 0 {
					calleeRaw = full[idx+2:]
				} else {
					calleeRaw = full
				}
				callNode = c.Node
			}
		}

		if calleeRaw == "" || callNode == nil {
			continue
		}
		if receiverRaw != "" && !strings.Contains(calleeRaw, ".") {
			calleeRaw = strings.TrimSpace(receiverRaw) + "." + strings.TrimSpace(calleeRaw)
		}
		_ = isMethodRef
		_ = methodRefNode

		r := nodeRange(callNode)

		// Deduplicate: the same call expression should only be emitted once
		// even if multiple query patterns both match it.
		key := deupKey(calleeRaw, r.StartByte)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		// Find the enclosing function.
		caller := enclosingSymbol(r.StartLine, symbolsInFile)

		// Build the enclosing control-flow path.
		enclosing := buildEnclosingPath(callNode, src)

		// Parse arguments.
		args := parseArguments(argsText)

		out = append(out, CallSite{
			Caller:        caller,
			CalleeRaw:     calleeRaw,
			ReceiverRaw:   receiverRaw,
			File:          relPath,
			Range:         r,
			Arguments:     args,
			EnclosingPath: enclosing,
		})
	}

	// Post-process: scan all calls for lambda arguments that contain method
	// references (`obj::method` in the source text). This handles cases like:
	//   list.forEach(service::processItem)
	//   list.stream().map(Mapper::convert)
	// where the tree-sitter query captures `forEach`/`map` as the callee but
	// the actual callable passed as an argument is `service::processItem`.
	// We synthesise a CallSite for the method reference so the walker can
	// follow that edge.
	out = appendMethodRefArgCalls(out, src, relPath, symbolsInFile, seen, deupKey)

	return out
}

// appendMethodRefArgCalls scans the already-extracted calls for arguments that
// look like method references ("Class::method" or "obj::method") and emits
// synthetic CallSites for the referenced method. This handles:
//
//	list.forEach(service::processItem)  →  synthetic call to processItem
//	stream.map(Mapper::convert)         →  synthetic call to convert
//
// The synthetic site has the same caller/enclosing context as the surrounding
// forEach/map/filter call, so conditions and repetitions are correctly attributed.
func appendMethodRefArgCalls(
	calls []CallSite,
	src []byte, relPath string,
	symbolsInFile []SymbolDef,
	seen map[uint64]struct{},
	deupKey func(string, uint32) uint64,
) []CallSite {
	var extra []CallSite
	for _, cs := range calls {
		for _, arg := range cs.Arguments {
			s := strings.TrimSpace(arg.Source)
			if !strings.Contains(s, "::") {
				continue
			}
			// Extract the method name after "::".
			idx := strings.LastIndex(s, "::")
			if idx < 0 || idx+2 >= len(s) {
				continue
			}
			methodName := strings.TrimSpace(s[idx+2:])
			if methodName == "" || methodName == "new" {
				continue
			}
			key := deupKey(methodName, cs.Range.StartByte+uint32(arg.Index)+0xdeadbeef)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			extra = append(extra, CallSite{
				Caller:        cs.Caller,
				CalleeRaw:     methodName,
				File:          relPath,
				Range:         cs.Range,
				Arguments:     nil,
				EnclosingPath: cs.EnclosingPath,
				IsImplicit:    true,
			})
		}
	}
	return append(calls, extra...)
}

// buildEnclosingPath walks from the call node up to the enclosing function
// body and collects control-flow boundary nodes.
func buildEnclosingPath(n *sitter.Node, src []byte) []EnclosingNode {
	var path []EnclosingNode
	// Walk up the tree collecting control-flow boundaries.
	cur := n.Parent()
	for cur != nil {
		kind := NormaliseNodeKind(cur.Type())
		if kind != "" {
			source := ""
			iterates := ""
			// Extract condition text or loop header.
			source, iterates = extractNodeSource(cur, src)
			path = append(path, EnclosingNode{
				Kind:         kind,
				Range:        nodeRange(cur),
				Source:       source,
				IteratesOver: iterates,
			})
		}
		// Stop when we hit a function/method definition (we don't want
		// to capture conditions from the *calling* function's scope).
		t := cur.Type()
		if isFunctionNode(t) {
			break
		}
		cur = cur.Parent()
	}
	// Reverse so outermost is first.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// extractNodeSource returns the condition/header text and the iterated
// collection (for loops) from a control-flow node.
func extractNodeSource(n *sitter.Node, src []byte) (source, iteratesOver string) {
	// For loops and comprehensions: try to find the iterable.
	switch n.Type() {
	case "for_statement", "enhanced_for_statement", "foreach_statement",
		"for_in_statement", "for_of_statement", "range_clause",
		"list_comprehension", "for_expression":
		// The iterable is typically the last named child before the body.
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			t := child.Type()
			// Skip the body block.
			if t == "block" || t == "statement_block" || t == "body" {
				break
			}
			iteratesOver = child.Content(src)
		}
		source = extractFirstLine(n.Content(src))
		return

	case "if_statement", "if_expression", "if_let_expression", "elif_clause":
		// Condition is the first named child that isn't the consequence.
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			t := child.Type()
			if t == "block" || t == "then" || t == "consequence" ||
				t == "statement_block" || t == "body" {
				break
			}
			source = child.Content(src)
			break
		}
		return

	case "while_statement", "while_expression":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			t := child.Type()
			if t == "block" || t == "body" || t == "statement_block" {
				break
			}
			source = child.Content(src)
			break
		}
		return

	case "catch_clause", "except_clause", "rescue":
		source = extractFirstLine(n.Content(src))
		return

	case "match_arm", "case_clause", "when_expression":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			t := child.Type()
			if t == "block" || t == "body" || t == "statement_block" ||
				t == "=>" || t == ":" {
				break
			}
			source = child.Content(src)
			break
		}
		return

	default:
		source = extractFirstLine(n.Content(src))
	}
	return
}

// enclosingSymbol finds the qualified name of the innermost symbol definition
// that contains lineNum (0-based).
func enclosingSymbol(lineNum uint32, symbols []SymbolDef) string {
	best := ""
	bestStart := uint32(0)
	for _, sym := range symbols {
		if sym.Range.StartLine <= lineNum && sym.Range.EndLine >= lineNum {
			if sym.Range.StartLine >= bestStart {
				bestStart = sym.Range.StartLine
				best = sym.Qualified
			}
		}
	}
	return best
}
