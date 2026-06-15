package ast

import sitter "github.com/smacker/go-tree-sitter"

// params_extract.go pulls the formal parameters (name, type, parameter-level
// annotations) off a function/method def node. These feed the deterministic
// IO-contract backfill that replaces the detail stage's `inputs` extraction.
//
// Implemented per-language and staged: Java (the primary stack) first; other
// languages return nil until their formal-parameter shapes are added, so the
// feature degrades to "no inputs" rather than wrong inputs.
func extractParams(defNode *sitter.Node, src []byte, lang string) []Param {
	if defNode == nil {
		return nil
	}
	switch lang {
	case "java":
		return extractJavaParams(defNode, src)
	}
	return nil
}

func extractJavaParams(defNode *sitter.Node, src []byte) []Param {
	pl := defNode.ChildByFieldName("parameters") // (formal_parameters ...)
	if pl == nil {
		return nil
	}
	var out []Param
	for i := 0; i < int(pl.NamedChildCount()); i++ {
		c := pl.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "formal_parameter", "spread_parameter":
		default:
			continue
		}
		p := Param{}
		if nameNode := c.ChildByFieldName("name"); nameNode != nil {
			p.Name = nameNode.Content(src)
		}
		if typeNode := c.ChildByFieldName("type"); typeNode != nil {
			p.Type = typeNode.Content(src)
		}
		p.Annotations = paramAnnotations(c, src)
		if p.Name == "" && p.Type == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// paramAnnotations collects the annotation nodes nested inside a formal
// parameter (Java keeps them under a (modifiers) child). Walking the whole
// parameter subtree is simplest and safe: only annotation nodes are collected.
func paramAnnotations(paramNode *sitter.Node, src []byte) []Annotation {
	var anns []Annotation
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Type() {
		case "annotation", "marker_annotation":
			a := Annotation{Range: nodeRange(n)}
			if nameNode := n.ChildByFieldName("name"); nameNode != nil {
				a.Name = nameNode.Content(src)
			}
			if argsNode := n.ChildByFieldName("arguments"); argsNode != nil {
				a.Arguments = trimOuterParens(argsNode.Content(src))
			}
			if a.Name != "" {
				anns = append(anns, a)
			}
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(paramNode)
	return anns
}
