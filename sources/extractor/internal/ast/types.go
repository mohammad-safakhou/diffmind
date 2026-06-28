// Package ast provides a language-agnostic static analysis engine built on
// tree-sitter. It parses source files for all supported languages, builds
// a cross-file call graph with full control-flow context, and resolves
// symbols across import/module boundaries.
//
// Supported languages: Go, Python, Java, Kotlin, C#, TypeScript, JavaScript,
// PHP, Ruby, Rust.
//
// The package intentionally avoids any language-specific logic in the core
// types. Language specialisation lives in the queries/ directory (tree-sitter
// S-expression patterns) and the framework/ directory (per-framework implicit
// invocation detectors). Adding a new language is:
//
//  1. Add a GetLanguage() binding in grammar.go.
//  2. Add query files under queries/<lang>/.
//  3. Optionally add a framework detector under framework/.
//  4. Register it in grammar.go's languageRegistry.
package ast

import "strings"

// Core data types

// ProjectIndex is the complete AST analysis of one repository. Built once,
// queried by every pipeline stage after ast_index runs.
type ProjectIndex struct {
	// RepoRoot is the absolute repository path used to build this index.
	// Deterministic resolvers use it to load repo-local configuration hints.
	RepoRoot string

	// Files is the per-file AST analysis, keyed by relative path.
	Files map[string]*FileAST

	// Symbols maps every qualified symbol name to its definitions across the
	// project. Multiple definitions can exist (method overloads, interface +
	// implementation).
	Symbols map[string][]SymbolDef

	// CallGraph maps every caller (qualified symbol) to the call sites inside
	// it. This is the primary structure used by the connection walker.
	CallGraph map[string][]CallSite

	// TypeMap maps a qualified type name to its concrete implementations.
	// For interfaces/abstract classes: TypeMap["IFoo"] = ["FooImpl", "MockFoo"].
	TypeMap map[string][]string

	// FieldTypes maps a receiver type + field name to the field's declared type.
	// Used for DI resolution: FieldTypes["UserController.repo"] = "UserRepository".
	FieldTypes map[string]string

	// LocalTypes maps a caller-qualified local variable to its declared type.
	// Used for local receiver resolution: LocalTypes["Listener.onMessage.processor"] = "Processor".
	LocalTypes map[string]string

	// Implements maps an interface/simple type name to classes that explicitly implement it.
	Implements map[string][]string

	// Frameworks is the list of framework patterns detected in this project.
	// Each binding captures an implicit invocation (e.g. @Scheduled triggers
	// a method that has no syntactic caller in user code).
	Frameworks []FrameworkBinding

	// RejectedFrameworks holds near-miss framework candidates with explicit
	// rejection reasons. These are for observability only and must not be used
	// as deterministic facts or prompt hints.
	RejectedFrameworks []FrameworkBinding

	// Configs holds the parsed configuration files (YAML, JSON, TOML, .env,
	// .properties). Used by discovery resource resolution and instance backfills.
	Configs map[string]*ConfigFile

	// Languages are the distinct source languages actually present in the
	// repository, derived from the files parsed (parsing is extension-driven,
	// so a polyglot repo lists every language it contains). There is no single
	// "primary" language: the index, framework detectors, and prompt scoping all
	// operate across all detected languages.
	Languages []string
}

// FileAST is the tree-sitter analysis of one source file.
type FileAST struct {
	Path     string
	Language string
	Imports  []ImportDecl
	Symbols  []SymbolDef
	Calls    []CallSite
	// FieldTypes maps class-qualified field names to declared types found in
	// source files, for example "UserController.repo" -> "UserRepository".
	FieldTypes map[string]string
	// LocalTypes maps method-qualified local variable names to declared types.
	LocalTypes map[string]string
	// Implements maps interfaces to class names declared with implements clauses.
	Implements map[string][]string
}

// SymbolDef is a function, method, class, or interface definition.
type SymbolDef struct {
	Name        string
	Qualified   string
	Kind        SymbolKind
	File        string
	Range       Range
	Receiver    string
	Modifiers   []string
	Annotations []Annotation
	// Parameters are the declared formal parameters of a function/method
	// (empty for classes/interfaces and for languages whose extractor does not
	// yet populate them). Used to recover an exposure's IO contract
	// deterministically without an LLM pass.
	Parameters []Param
}

// Param is one declared formal parameter of a function/method, with its
// parameter-level annotations (e.g. Spring @PathVariable/@RequestParam).
type Param struct {
	Name        string
	Type        string
	Annotations []Annotation
}

// CallSite is a single function/method invocation.
type CallSite struct {
	Caller         string
	CalleeRaw      string
	ReceiverRaw    string
	CalleeResolved []string
	File           string
	Range          Range
	Arguments      []ArgumentExpr
	EnclosingPath  []EnclosingNode
	IsImplicit     bool
}

// EnclosingNode represents one level of control-flow context wrapping a call.
type EnclosingNode struct {
	// Kind is the normalised control-flow type (language-agnostic).
	// Values: "if_guard", "else_branch", "loop", "try_block", "catch_block",
	// "finally_block", "closure", "goroutine", "async_block", "match_arm",
	// "ternary", "null_check", "optional_chain"
	Kind         string
	Range        Range
	Source       string
	IteratesOver string
}

// ArgumentExpr is one actual argument passed to a call.
type ArgumentExpr struct {
	Index  int
	Source string
	// Kind: "literal", "identifier", "call", "new", "other"
	Kind string
}

// ImportDecl represents one import/require/use statement.
type ImportDecl struct {
	Alias        string
	Path         string
	ResolvedFile string
}

// Annotation represents a decorator, annotation, or attribute.
type Annotation struct {
	Name      string
	Arguments string
	Range     Range
}

// FrameworkBinding captures an implicit invocation triggered by a framework.
type FrameworkBinding struct {
	Framework        string
	Kind             string
	Direction        string
	Symbol           string
	Trigger          string
	TriggerSource    string
	File             string
	Range            Range
	DetectorIDs      []string
	ConfidenceReason string
	RejectionReason  string
}

// ConfigFile holds key-value pairs from one configuration file.
type ConfigFile struct {
	Path    string
	Format  string
	Entries []ConfigEntry
}

// ConfigEntry is a single key-value pair from a configuration file. Profile is
// set when the entry comes from a profile-activated document inside a Spring
// multi-doc YAML file ("spring.config.activate.on-profile"); "" means the base
// document. File-level profiles (application-prod.yml) are derived from the
// path by consumers instead.
type ConfigEntry struct {
	Key     string
	Value   string
	Line    int
	Profile string
}

// Range is a source position (0-based, matching tree-sitter's convention).
type Range struct {
	StartByte   uint32
	EndByte     uint32
	StartLine   uint32
	StartColumn uint32
	EndLine     uint32
	EndColumn   uint32
}

// Enumerations

// SymbolKind classifies a symbol definition.
type SymbolKind int

const (
	SymbolKindFunction SymbolKind = iota
	SymbolKindMethod
	SymbolKindClass
	SymbolKindInterface
	SymbolKindConstructor
	SymbolKindProperty
)

func (k SymbolKind) String() string {
	switch k {
	case SymbolKindFunction:
		return "function"
	case SymbolKindMethod:
		return "method"
	case SymbolKindClass:
		return "class"
	case SymbolKindInterface:
		return "interface"
	case SymbolKindConstructor:
		return "constructor"
	case SymbolKindProperty:
		return "property"
	default:
		return "unknown"
	}
}

// Control-flow kind normalisation

// normaliseNodeKindMap maps tree-sitter node type strings (which are mostly
// shared across languages — tree-sitter uses the same names for the same
// constructs) to the canonical EnclosingNode.Kind strings the pipeline uses.
//
// This single map works because tree-sitter grammars deliberately use
// consistent naming (e.g. "if_statement" is the same in Go, Java, Python,
// C#, PHP grammars). Language-unique names are added where they differ.
var normaliseNodeKindMap = map[string]string{
	// Conditionals
	"if_statement":           "if_guard",
	"if_expression":          "if_guard", // Rust
	"if_let_expression":      "if_guard", // Rust
	"elif_clause":            "if_guard", // Python
	"else_clause":            "else_branch",
	"unless":                 "if_guard", // Ruby
	"switch_statement":       "match_arm",
	"switch_expression":      "match_arm",
	"match_statement":        "match_arm", // Python 3.10+
	"match_expression":       "match_arm", // Rust, PHP
	"when_expression":        "match_arm", // Kotlin
	"case_clause":            "match_arm",
	"ternary_expression":     "ternary",
	"conditional_expression": "ternary", // C#, TypeScript
	"optional_chain":         "optional_chain",
	"select_statement":       "if_guard", // Go select

	// Loops
	"for_statement":            "loop",
	"for_expression":           "loop", // Rust, Kotlin
	"enhanced_for_statement":   "loop", // Java
	"foreach_statement":        "loop", // C#, PHP
	"for_in_statement":         "loop", // JS/TS
	"for_of_statement":         "loop", // JS/TS
	"while_statement":          "loop",
	"while_expression":         "loop", // Rust
	"do_statement":             "loop",
	"loop_expression":          "loop", // Rust `loop`
	"list_comprehension":       "loop", // Python
	"set_comprehension":        "loop", // Python
	"dictionary_comprehension": "loop", // Python
	"generator_expression":     "loop", // Python
	"range_clause":             "loop", // Go `range`

	// Try/Except
	"try_statement":  "try_block",
	"try_expression": "try_block",
	"catch_clause":   "catch_block",
	"except_clause":  "catch_block", // Python
	"rescue":         "catch_block", // Ruby
	"finally_clause": "finally_block",
	"ensure":         "finally_block", // Ruby

	// Async / concurrent dispatch
	"go_statement":     "goroutine", // Go
	"await_expression": "async_block",
	"spawn_expression": "goroutine",
}

// NormaliseNodeKind converts a tree-sitter node type to the canonical
// EnclosingNode.Kind string. Returns "" when the node is not a recognised
// control-flow boundary.
func NormaliseNodeKind(rawKind string) string {
	if k, ok := normaliseNodeKindMap[rawKind]; ok {
		return k
	}
	// Closure / lambda detection by name pattern (applies across languages).
	if strings.HasSuffix(rawKind, "_function") ||
		strings.HasSuffix(rawKind, "_closure") ||
		rawKind == "lambda" ||
		rawKind == "arrow_function" ||
		rawKind == "closure_expression" ||
		rawKind == "anonymous_function" ||
		rawKind == "proc_literal" ||
		rawKind == "method_value" ||
		rawKind == "func_literal" {
		return "closure"
	}
	return ""
}

// Condition derivation

// ConnectionCondition describes the control-flow context of one call edge.
type ConnectionCondition struct {
	Kind        string `json:"kind"`
	Expression  string `json:"expression,omitempty"`
	Explanation string `json:"explanation,omitempty"`
}

// Repetition describes whether a call is made once or multiple times.
type Repetition struct {
	Kind         string `json:"kind"`
	IteratesOver string `json:"iterates_over,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
}

// DeriveConditionAndRepetition walks the EnclosingPath of a call site and
// returns the most significant condition and repetition context.
//
// The innermost relevant node takes priority: a loop inside an if wins over
// just the if, and a catch block wins over the surrounding try.
func DeriveConditionAndRepetition(enclosing []EnclosingNode) (ConnectionCondition, Repetition) {
	cond := ConnectionCondition{Kind: "unconditional"}
	rep := Repetition{Kind: "single"}

	for _, node := range enclosing {
		switch node.Kind {
		case "if_guard":
			cond = ConnectionCondition{
				Kind:        "if_guard",
				Expression:  node.Source,
				Explanation: "Call is inside a conditional branch",
			}
		case "else_branch":
			cond = ConnectionCondition{
				Kind:        "if_guard",
				Expression:  "!(" + node.Source + ")",
				Explanation: "Call is inside an else branch",
			}
		case "match_arm":
			cond = ConnectionCondition{
				Kind:        "if_guard",
				Expression:  node.Source,
				Explanation: "Call is inside a match/switch arm",
			}
		case "ternary":
			cond = ConnectionCondition{
				Kind:        "if_guard",
				Expression:  node.Source,
				Explanation: "Call is inside a ternary expression",
			}
		case "optional_chain":
			cond = ConnectionCondition{
				Kind:        "null_check",
				Expression:  node.Source,
				Explanation: "Call uses optional chaining; skipped if receiver is nil/null/undefined",
			}
		case "loop":
			rep = Repetition{
				Kind:         "loop",
				IteratesOver: node.IteratesOver,
				Evidence:     node.Source,
			}
		case "goroutine":
			if rep.Kind != "loop" { // loop takes priority over goroutine
				rep = Repetition{Kind: "fan_out", Evidence: node.Source}
			}
		case "async_block":
			if rep.Kind != "loop" { // loop takes priority over async
				rep = Repetition{Kind: "fan_out", Evidence: node.Source}
			}
		case "closure":
			if rep.Kind != "loop" {
				rep = Repetition{Kind: "fan_out", Evidence: node.Source}
			}
		case "catch_block":
			cond = ConnectionCondition{
				Kind:        "catch_block",
				Expression:  node.Source,
				Explanation: "Call is inside an exception handler",
			}
		case "finally_block":
			cond = ConnectionCondition{
				Kind:        "finally_block",
				Explanation: "Call is inside a finally/ensure block; always runs",
			}
		case "try_block":
			if cond.Kind == "unconditional" {
				cond = ConnectionCondition{
					Kind:        "try_block",
					Explanation: "Call is inside a try block; exceptions may prevent completion",
				}
			}
		}
	}
	return cond, rep
}
