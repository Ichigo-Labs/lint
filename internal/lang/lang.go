// Package lang is the registry of languages lintel understands. It maps
// human language names and file extensions onto Tree-sitter grammars, and
// records a small amount of per-language metadata the engine uses to reason
// about patterns (which node kinds count as identifiers, literals, comments).
package lang

import (
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// Language describes one supported programming language.
type Language struct {
	// Name is the canonical identifier used in `.lint` files, e.g. "typescript".
	Name string
	// Aliases are alternate spellings accepted in `in`/`language` clauses.
	Aliases []string
	// Extensions are file extensions (with leading dot) mapped to this language.
	Extensions []string
	// Grammar returns the Tree-sitter grammar. It is a func so grammars are
	// only realized on demand.
	Grammar func() *sitter.Language

	// identifierKinds are node types that name things (used to recognise
	// metavariables and to give "$x kind identifier" sensible defaults).
	identifierKinds map[string]bool
	// literalKinds are node types that represent literal values.
	literalKinds map[string]bool
	// commentKinds are node types representing comments (skipped in matching).
	commentKinds map[string]bool
}

// IsIdentifier reports whether kind is an identifier-like node in this language.
func (l *Language) IsIdentifier(kind string) bool { return l.identifierKinds[kind] }

// IsLiteral reports whether kind is a literal node in this language.
func (l *Language) IsLiteral(kind string) bool { return l.literalKinds[kind] }

// IsComment reports whether kind is a comment node in this language.
func (l *Language) IsComment(kind string) bool { return l.commentKinds[kind] }

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

// registry holds every language by canonical name.
var registry = map[string]*Language{}

// byExt maps a lowercased extension (with dot) to a language.
var byExt = map[string]*Language{}

func register(l *Language) {
	registry[l.Name] = l
	for _, a := range l.Aliases {
		registry[a] = l
	}
	for _, e := range l.Extensions {
		byExt[strings.ToLower(e)] = l
	}
}

func init() {
	register(&Language{
		Name:            "go",
		Aliases:         []string{"golang"},
		Extensions:      []string{".go"},
		Grammar:         golang.GetLanguage,
		identifierKinds: set("identifier", "field_identifier", "package_identifier", "type_identifier", "label_name"),
		literalKinds:    set("int_literal", "float_literal", "imaginary_literal", "rune_literal", "interpreted_string_literal", "raw_string_literal", "true", "false", "nil"),
		commentKinds:    set("comment"),
	})
	register(&Language{
		Name:            "python",
		Aliases:         []string{"py"},
		Extensions:      []string{".py", ".pyi"},
		Grammar:         python.GetLanguage,
		identifierKinds: set("identifier"),
		literalKinds:    set("integer", "float", "string", "true", "false", "none"),
		commentKinds:    set("comment"),
	})
	register(&Language{
		Name:            "java",
		Extensions:      []string{".java"},
		Grammar:         java.GetLanguage,
		identifierKinds: set("identifier", "type_identifier"),
		literalKinds:    set("decimal_integer_literal", "hex_integer_literal", "decimal_floating_point_literal", "string_literal", "character_literal", "true", "false", "null_literal"),
		commentKinds:    set("line_comment", "block_comment"),
	})
	register(&Language{
		Name:            "csharp",
		Aliases:         []string{"cs", "c#"},
		Extensions:      []string{".cs"},
		Grammar:         csharp.GetLanguage,
		identifierKinds: set("identifier"),
		literalKinds:    set("integer_literal", "real_literal", "string_literal", "character_literal", "boolean_literal", "null_literal", "verbatim_string_literal"),
		commentKinds:    set("comment"),
	})
	register(&Language{
		Name:            "c",
		Extensions:      []string{".c", ".h"},
		Grammar:         c.GetLanguage,
		identifierKinds: set("identifier", "field_identifier", "type_identifier"),
		literalKinds:    set("number_literal", "string_literal", "char_literal", "true", "false", "null"),
		commentKinds:    set("comment"),
	})
	register(&Language{
		Name:            "cpp",
		Aliases:         []string{"c++", "cc"},
		Extensions:      []string{".cpp", ".cxx", ".cc", ".hpp", ".hxx", ".hh"},
		Grammar:         cpp.GetLanguage,
		identifierKinds: set("identifier", "field_identifier", "type_identifier", "namespace_identifier"),
		literalKinds:    set("number_literal", "string_literal", "char_literal", "true", "false", "nullptr"),
		commentKinds:    set("comment"),
	})
	register(&Language{
		Name:            "rust",
		Aliases:         []string{"rs"},
		Extensions:      []string{".rs"},
		Grammar:         rust.GetLanguage,
		identifierKinds: set("identifier", "field_identifier", "type_identifier", "shorthand_field_identifier"),
		literalKinds:    set("integer_literal", "float_literal", "string_literal", "raw_string_literal", "char_literal", "boolean_literal"),
		commentKinds:    set("line_comment", "block_comment"),
	})
	register(&Language{
		Name:            "typescript",
		Aliases:         []string{"ts"},
		Extensions:      []string{".ts", ".mts", ".cts"},
		Grammar:         typescript.GetLanguage,
		identifierKinds: set("identifier", "property_identifier", "type_identifier", "shorthand_property_identifier"),
		literalKinds:    set("number", "string", "template_string", "regex", "true", "false", "null", "undefined"),
		commentKinds:    set("comment"),
	})
	register(&Language{
		Name:            "tsx",
		Aliases:         []string{"javascript", "js", "jsx"},
		Extensions:      []string{".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		Grammar:         tsx.GetLanguage,
		identifierKinds: set("identifier", "property_identifier", "type_identifier", "shorthand_property_identifier"),
		literalKinds:    set("number", "string", "template_string", "regex", "true", "false", "null", "undefined"),
		commentKinds:    set("comment"),
	})
}

// Get returns the language registered under name (canonical or alias).
func Get(name string) (*Language, bool) {
	l, ok := registry[strings.ToLower(strings.TrimSpace(name))]
	return l, ok
}

// ForExt returns the language associated with a file extension (with or
// without a leading dot).
func ForExt(ext string) (*Language, bool) {
	if ext == "" {
		return nil, false
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	l, ok := byExt[strings.ToLower(ext)]
	return l, ok
}

// ForPath returns the language for a file path based on its extension.
func ForPath(path string) (*Language, bool) {
	return ForExt(filepath.Ext(path))
}

// Names returns the canonical names of all registered languages, sorted.
func Names() []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range registry {
		if !seen[l.Name] {
			seen[l.Name] = true
			out = append(out, l.Name)
		}
	}
	sort.Strings(out)
	return out
}
