package ast

// languageQueries holds the compiled tree-sitter S-expression query strings
// for one language.
type languageQueries struct {
	// imports matches import/require/use statements.
	// Captures: @path (the import path), @alias (the local name, optional).
	imports []byte

	// functions matches function/method definitions.
	// Captures: @def (the definition node), @name (the identifier),
	// @receiver or @class (the containing type, optional).
	functions []byte

	// calls matches function/method call expressions.
	// Captures: @callee (the called identifier/member expression),
	// @args (the argument list node).
	calls []byte

	// annotations matches decorators, annotations, attributes.
	// Captures: @name (annotation name), @args (arguments, optional).
	annotations []byte
}

// queryRegistry maps canonical language name → query set.
var queryRegistry = map[string]*languageQueries{
	"go":         goQueries,
	"python":     pythonQueries,
	"java":       javaQueries,
	"kotlin":     kotlinQueries,
	"csharp":     csharpQueries,
	"typescript": typescriptQueries,
	"tsx":        typescriptQueries,
	"javascript": javascriptQueries,
	"jsx":        javascriptQueries,
	"php":        phpQueries,
	"ruby":       rubyQueries,
	"rust":       rustQueries,
}

// queriesForLanguage returns the query set for a language, or nil.
func queriesForLanguage(lang string) *languageQueries {
	return queryRegistry[lang]
}

// ── Go ────────────────────────────────────────────────────────────────────────

var goQueries = &languageQueries{
	imports: []byte(`
(import_spec
  path: (interpreted_string_literal) @path
  name: (package_identifier)? @alias)

(import_spec
  path: (interpreted_string_literal) @path)
`),

	functions: []byte(`
(function_declaration
  name: (identifier) @name) @def

(method_declaration
  receiver: (parameter_list
    (parameter_declaration
      type: [
        (type_identifier) @receiver
        (pointer_type (type_identifier) @receiver)
      ]))
  name: (field_identifier) @name) @def
`),

	calls: []byte(`
(call_expression
  function: [
    (identifier) @callee
    (selector_expression
      field: (field_identifier) @callee)
  ]
  arguments: (argument_list) @args) @call
`),

	annotations: nil, // Go has no annotations/decorators
}

// ── Python ────────────────────────────────────────────────────────────────────

var pythonQueries = &languageQueries{
	imports: []byte(`
(import_statement
  name: (dotted_name) @path)

(import_from_statement
  module_name: (dotted_name) @path
  name: (dotted_name) @alias)

(import_from_statement
  module_name: (dotted_name) @path
  name: (aliased_import alias: (identifier) @alias))

(import_from_statement
  module_name: (dotted_name) @path)
`),

	functions: []byte(`
(function_definition
  name: (identifier) @name) @def

(class_definition
  name: (identifier) @name) @def
`),

	calls: []byte(`
(call
  function: [
    (identifier) @callee
    (attribute
      attribute: (identifier) @callee)
  ]
  arguments: (argument_list)? @args) @call
`),

	annotations: []byte(`
(decorator
  (identifier) @name)

(decorator
  (call
    function: (identifier) @name
    arguments: (argument_list) @args))

(decorator
  (attribute
    attribute: (identifier) @name))

(decorator
  (call
    function: (attribute attribute: (identifier) @name)
    arguments: (argument_list) @args))
`),
}

// ── Java ─────────────────────────────────────────────────────────────────────

var javaQueries = &languageQueries{
	imports: []byte(`
(import_declaration
  (scoped_identifier) @path)

(import_declaration
  (identifier) @path)
`),

	functions: []byte(`
(method_declaration
  name: (identifier) @name
  (modifiers)? @modifiers) @def

(class_declaration
  name: (identifier) @name) @def

(interface_declaration
  name: (identifier) @name) @def

(constructor_declaration
  name: (identifier) @name) @def
`),

	calls: []byte(`
(method_invocation
  name: (identifier) @callee
  arguments: (argument_list) @args) @call

(method_invocation
  object: (_) @receiver
  name: (identifier) @callee
  arguments: (argument_list) @args) @call

(object_creation_expression
  type: (type_identifier) @callee
  arguments: (argument_list) @args) @call
`),

	annotations: []byte(`
(marker_annotation
  name: (identifier) @name)

(annotation
  name: (identifier) @name
  arguments: (annotation_argument_list) @args)
`),
}

// ── Kotlin ────────────────────────────────────────────────────────────────────

var kotlinQueries = &languageQueries{
	imports: []byte(`
(import_header
  (identifier) @path)
`),

	functions: []byte(`
(function_declaration
  (simple_identifier) @name) @def

(class_declaration
  (type_identifier) @name) @def

(object_declaration
  (type_identifier) @name) @def
`),

	calls: []byte(`
(call_expression
  (navigation_expression
    (navigation_suffix
      (simple_identifier) @callee))
  (value_arguments) @args) @call

(call_expression
  (simple_identifier) @callee
  (value_arguments) @args) @call
`),

	annotations: []byte(`
(annotation
  (use_site_target)? 
  (unescaped_annotation
    (constructor_invocation
      (user_type (simple_identifier) @name)
      (value_arguments) @args)))

(annotation
  (use_site_target)?
  (unescaped_annotation
    (user_type (simple_identifier) @name)))
`),
}

// ── C# ───────────────────────────────────────────────────────────────────────

var csharpQueries = &languageQueries{
	imports: []byte(`
(using_directive
  (identifier) @path)

(using_directive
  (qualified_name) @path)
`),

	functions: []byte(`
(method_declaration
  name: (identifier) @name) @def

(class_declaration
  name: (identifier) @name) @def

(interface_declaration
  name: (identifier) @name) @def

(constructor_declaration
  name: (identifier) @name) @def
`),

	calls: []byte(`
(invocation_expression
  function: [
    (identifier) @callee
    (member_access_expression
      name: (identifier) @callee)
  ]
  arguments: (argument_list) @args) @call

(object_creation_expression
  type: (identifier) @callee
  arguments: (argument_list)? @args) @call
`),

	annotations: []byte(`
(attribute
  name: (identifier) @name
  (attribute_argument_list)? @args)
`),
}

// ── TypeScript / JavaScript ───────────────────────────────────────────────────

var typescriptQueries = &languageQueries{
	imports: []byte(`
(import_statement
  source: (string) @path
  (import_clause
    (named_imports
      (import_specifier
        name: (identifier) @alias))))

(import_statement
  source: (string) @path
  (import_clause
    (identifier) @alias))

(import_statement
  source: (string) @path)
`),

	functions: []byte(`
(function_declaration
  name: (identifier) @name) @def

(method_definition
  name: (property_identifier) @name) @def

(class_declaration
  name: (type_identifier) @name) @def

(arrow_function) @def

(variable_declarator
  name: (identifier) @name
  value: (arrow_function) @def)

(variable_declarator
  name: (identifier) @name
  value: (function) @def)
`),

	calls: []byte(`
(call_expression
  function: [
    (identifier) @callee
    (member_expression
      property: (property_identifier) @callee)
  ]
  arguments: (arguments) @args) @call

(new_expression
  constructor: (identifier) @callee
  arguments: (arguments)? @args) @call
`),

	annotations: []byte(`
(decorator
  (identifier) @name)

(decorator
  (call_expression
    function: (identifier) @name
    arguments: (arguments) @args))

(decorator
  (member_expression
    property: (property_identifier) @name))
`),
}

var javascriptQueries = typescriptQueries

// ── PHP ───────────────────────────────────────────────────────────────────────

var phpQueries = &languageQueries{
	imports: []byte(`
(namespace_use_clause
  (qualified_name) @path
  (namespace_aliasing_clause (name) @alias)?)

(namespace_use_clause
  (qualified_name) @path)
`),

	functions: []byte(`
(function_definition
  name: (name) @name) @def

(method_declaration
  name: (name) @name) @def

(class_declaration
  name: (name) @name) @def
`),

	calls: []byte(`
(function_call_expression
  function: [
    (name) @callee
    (qualified_name) @callee
  ]
  arguments: (arguments) @args) @call

(member_call_expression
  name: (name) @callee
  arguments: (arguments) @args) @call

(object_creation_expression
  class: (name) @callee
  arguments: (arguments)? @args) @call
`),

	annotations: []byte(`
(attribute
  (attribute_group
    (attribute
      (name) @name
      (arguments)? @args)))
`),
}

// ── Ruby ─────────────────────────────────────────────────────────────────────

var rubyQueries = &languageQueries{
	imports: []byte(`
(call
  method: (identifier) @method
  arguments: (argument_list (string) @path)
  (#match? @method "^require"))

(call
  method: (identifier) @method
  arguments: (argument_list (string) @path)
  (#match? @method "^require_relative"))
`),

	functions: []byte(`
(method
  name: (identifier) @name) @def

(singleton_method
  name: (identifier) @name) @def

(class
  name: (constant) @name) @def

(module
  name: (constant) @name) @def
`),

	calls: []byte(`
(call
  receiver: (_)? @receiver
  method: (identifier) @callee
  arguments: (argument_list)? @args) @call
`),

	annotations: nil, // Ruby doesn't use annotations; framework detection handles routes
}

// ── Rust ─────────────────────────────────────────────────────────────────────

var rustQueries = &languageQueries{
	imports: []byte(`
(use_declaration
  argument: (scoped_identifier) @path)

(use_declaration
  argument: (identifier) @path)

(use_declaration
  argument: (scoped_use_list
    path: (scoped_identifier) @path))
`),

	functions: []byte(`
(function_item
  name: (identifier) @name) @def

(impl_item
  type: (type_identifier) @receiver
  body: (declaration_list
    (function_item name: (identifier) @name) @def))

(struct_item
  name: (type_identifier) @name) @def

(trait_item
  name: (type_identifier) @name) @def
`),

	calls: []byte(`
(call_expression
  function: [
    (identifier) @callee
    (field_expression field: (field_identifier) @callee)
    (scoped_identifier name: (identifier) @callee)
  ]
  arguments: (arguments) @args) @call

(struct_expression
  name: (type_identifier) @callee) @call
`),

	annotations: []byte(`
(attribute_item
  (attribute
    (identifier) @name
    (token_tree)? @args))

(inner_attribute_item
  (attribute
    (identifier) @name
    (token_tree)? @args))
`),
}
