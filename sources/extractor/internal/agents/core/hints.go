package core

// ObjectiveHints is the compact, token-bounded AST context for one objective,
// rendered into the discovery/reexamine/detail prompts. The hints are advisory
// only: the LLM remains the authority on what is a real entity.
type ObjectiveHints struct {
	Symbols   []SymbolHint
	Bindings  []BindingHint
	Configs   []ConfigHint
	Truncated bool // true if any list was capped
}

type SymbolHint struct {
	Qualified   string
	File        string
	Line        uint32
	Annotations []string
}

type BindingHint struct {
	Framework string
	Kind      string
	Symbol    string
	Trigger   string
	File      string
	Line      uint32
}

type ConfigHint struct {
	File  string
	Key   string
	Value string
}

// Empty reports whether there is nothing worth rendering.
func (h ObjectiveHints) Empty() bool {
	return len(h.Symbols) == 0 && len(h.Bindings) == 0 && len(h.Configs) == 0
}
