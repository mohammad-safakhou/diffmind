package ast

import (
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// languageEntry describes one supported language.
type languageEntry struct {
	// Language returns the tree-sitter grammar for this language.
	Language func() *sitter.Language
	// Extensions is the list of file extensions (including the dot) that
	// should be parsed with this language.
	Extensions []string
	// ConfigExtensions is the list of extensions for configuration files
	// belonging to this language's ecosystem (e.g. ".properties" for Java).
	ConfigExtensions []string
}

// languageRegistry maps the canonical language name to its entry.
// The name matches the strings used by internal/langdetect.
var languageRegistry = map[string]languageEntry{
	"go": {
		Language:   golang.GetLanguage,
		Extensions: []string{".go"},
	},
	"python": {
		Language:   python.GetLanguage,
		Extensions: []string{".py", ".pyw"},
	},
	"java": {
		Language:         java.GetLanguage,
		Extensions:       []string{".java"},
		ConfigExtensions: []string{".properties"},
	},
	"kotlin": {
		Language:   kotlin.GetLanguage,
		Extensions: []string{".kt", ".kts"},
	},
	"csharp": {
		Language:   csharp.GetLanguage,
		Extensions: []string{".cs"},
	},
	"typescript": {
		Language:   typescript.GetLanguage,
		Extensions: []string{".ts"},
	},
	"tsx": {
		Language:   tsx.GetLanguage,
		Extensions: []string{".tsx"},
	},
	"javascript": {
		Language:   javascript.GetLanguage,
		Extensions: []string{".js", ".mjs", ".cjs"},
	},
	"jsx": {
		Language:   javascript.GetLanguage,
		Extensions: []string{".jsx"},
	},
	"php": {
		Language:   php.GetLanguage,
		Extensions: []string{".php"},
	},
	"ruby": {
		Language:   ruby.GetLanguage,
		Extensions: []string{".rb", ".rake"},
	},
	"rust": {
		Language:   rust.GetLanguage,
		Extensions: []string{".rs"},
	},
}

// extensionToLanguage is built from languageRegistry at init time.
var extensionToLanguage map[string]string

func init() {
	extensionToLanguage = make(map[string]string)
	for name, entry := range languageRegistry {
		for _, ext := range entry.Extensions {
			extensionToLanguage[ext] = name
		}
		for _, ext := range entry.ConfigExtensions {
			extensionToLanguage[ext] = name
		}
	}
}

// LanguageForExtension returns the canonical language name for a file
// extension (including the dot). Returns "" when unrecognised.
func LanguageForExtension(ext string) string {
	return extensionToLanguage[ext]
}

// GetLanguage returns the tree-sitter Language for the given canonical name.
// Returns nil when the language is not registered.
func GetLanguage(name string) *sitter.Language {
	if entry, ok := languageRegistry[name]; ok {
		return entry.Language()
	}
	return nil
}

// SupportedLanguages returns the canonical names of all registered languages.
func SupportedLanguages() []string {
	out := make([]string, 0, len(languageRegistry))
	for name := range languageRegistry {
		out = append(out, name)
	}
	return out
}

// IsConfigFile reports whether the given file extension belongs to a
// configuration file that the AST index should parse for discovery and
// resource-resolution context.
var configExtensions = map[string]string{
	".yml":        "yaml",
	".yaml":       "yaml",
	".json":       "json",
	".toml":       "toml",
	".tf":         "hcl",
	".tfvars":     "hcl",
	".hcl":        "hcl",
	".ini":        "ini",
	".env":        "env",
	".properties": "properties",
	".config":     "xml",
	".xml":        "xml",
}

// ConfigFormatForExtension returns the format name for a config file
// extension. Returns "" for non-config files.
func ConfigFormatForExtension(ext string) string {
	return configExtensions[ext]
}
