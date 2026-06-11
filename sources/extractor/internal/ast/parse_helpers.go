package ast

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// parseArguments splits a raw argument list text into individual ArgumentExprs.
func parseArguments(argsText string) []ArgumentExpr {
	argsText = trimOuterParens(argsText)
	if strings.TrimSpace(argsText) == "" {
		return nil
	}
	// Simple split on commas at depth 0.
	parts := splitArgs(argsText)
	out := make([]ArgumentExpr, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, ArgumentExpr{
			Index:  i,
			Source: p,
			Kind:   classifyArgument(p),
		})
	}
	return out
}

// splitArgs splits a comma-separated argument list, respecting nested
// brackets/parens/braces.
func splitArgs(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, ch := range s {
		switch ch {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

// classifyArgument classifies an argument expression text.
func classifyArgument(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "other"
	}
	// String literal.
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) ||
		(strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`")) {
		return "literal"
	}
	// Number literal.
	if len(s) > 0 && (s[0] >= '0' && s[0] <= '9') {
		return "literal"
	}
	// Boolean / null literals.
	switch strings.ToLower(s) {
	case "true", "false", "null", "nil", "none", "undefined":
		return "literal"
	}
	// Constructor call.
	if strings.HasPrefix(s, "new ") {
		return "new"
	}
	// Nested call expression (contains parentheses).
	if strings.Contains(s, "(") {
		return "call"
	}
	// Plain identifier.
	if !strings.ContainsAny(s, " \t.+*/%-&|^!<>=") {
		return "identifier"
	}
	return "other"
}

func nodeRange(n *sitter.Node) Range {
	sp := n.StartPoint()
	ep := n.EndPoint()
	return Range{
		StartByte:   n.StartByte(),
		EndByte:     n.EndByte(),
		StartLine:   sp.Row,
		StartColumn: sp.Column,
		EndLine:     ep.Row,
		EndColumn:   ep.Column,
	}
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') ||
			(s[0] == '`' && s[len(s)-1] == '`') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func trimOuterParens(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' {
		return s[1 : len(s)-1]
	}
	return s
}

func extractFirstLine(s string) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

// qualifiedName builds a stable qualified symbol name from receiver + method
// name, using the dot-separated convention shared across languages.
func qualifiedName(receiver, name, lang string) string {
	if receiver == "" {
		return name
	}
	return receiver + "." + name
}

// isFunctionNode reports whether the tree-sitter node type represents a
// function or method body boundary.
func isFunctionNode(t string) bool {
	switch t {
	case "function_declaration", "function_definition", "function",
		"method_declaration", "method_definition", "method",
		"func_literal", "function_item", "arrow_function",
		"anonymous_function", "closure_expression", "lambda",
		"proc_literal", "def", "constructor_declaration",
		"constructor_definition", "function_def":
		return true
	}
	return false
}
