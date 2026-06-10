// Package lang is the registry of languages lint understands. It maps
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
	"github.com/smacker/go-tree-sitter/css"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/html"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/swift"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
	"github.com/smacker/go-tree-sitter/yaml"

	"github.com/ichigo-labs/lint/internal/grammars/json"
	"github.com/ichigo-labs/lint/internal/grammars/scss"
	"github.com/ichigo-labs/lint/internal/grammars/xml"
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

	// Group names the language family a language belongs to, for presentation
	// (README table, `lint langs`). Languages sharing a Group are listed
	// together; an empty Group means the language stands alone.
	Group string

	// ExplicitOnly marks languages that rules target only via an explicit
	// `in` clause. Markup/style grammars (HTML especially) parse nearly any
	// text without error, so rules that omit `in` would otherwise silently
	// compile for them and produce nonsense candidates.
	ExplicitOnly bool

	// MetaInStrings makes pattern rewriting interpret $X inside quoted
	// literals. Markup languages need it because attribute values must be
	// quoted (`<a href="$URL">`); code languages keep $ literal in strings.
	MetaInStrings bool

	// MetaUppercaseOnly restricts metavariables to names starting with an
	// uppercase letter, so SCSS's own $lowercase-variables stay literal
	// pattern text.
	MetaUppercaseOnly bool

	// MetaVariadicTagAttrs emits a variadic metavariable appearing inside a
	// tag (between < and >) as `$$$X=""`, because the grammar cannot parse a
	// bare attribute name (XML; HTML parses bare names as boolean attributes).
	MetaVariadicTagAttrs bool

	// AllowMissingNodes accepts pattern parses that contain MISSING (zero
	// width) nodes. HTML parses an unclosed `<div …>` as element(start_tag,
	// MISSING end_tag); tolerating the phantom node lets an open-tag pattern
	// compile — and, with the missing node filtered from matching, mean
	// "match the start tag".
	AllowMissingNodes bool

	// MetaQuoteBare emits a metavariable that is not already inside quotes
	// as a quoted token (`"SENT"`), because the grammar has no bare-token
	// form at all (JSON). Combined with a transparent string wrapper, the
	// quoted sentinel then matches a value of any kind.
	MetaQuoteBare bool

	// sentinelSep is the separator used inside metavariable sentinel
	// identifiers (default "_"). HTML tag names cannot contain '_', so html
	// overrides it with '-'.
	sentinelSep string

	// identifierKinds are node types that name things (used to recognise
	// metavariables and to give "$x kind identifier" sensible defaults).
	identifierKinds map[string]bool
	// literalKinds are node types that represent literal values. The matcher
	// compares them by their full source text, because several grammars
	// (Go strings, CSS colors, XML attribute values) do not expose the
	// literal's content as a child node.
	literalKinds map[string]bool
	// commentKinds are node types representing comments (skipped in matching).
	commentKinds map[string]bool
	// foldKinds are leaf node types whose text compares case-insensitively
	// (HTML tag and attribute names).
	foldKinds map[string]bool
	// interpolationKinds are node types for code embedded in string literals
	// (template substitutions, f-string interpolations). A literal containing
	// one keeps structural matching instead of full-text comparison.
	interpolationKinds map[string]bool
	// transparentKinds are language-specific single-child wrappers matching
	// looks through, beyond the engine's global set. JSON's "string" wraps
	// string_content, and looking through it lets a quoted metavariable
	// (`"$V"`) bind a value of any kind; the kind name collides with other
	// grammars (TypeScript), so it cannot be global.
	transparentKinds map[string]bool
}

// IsIdentifier reports whether kind is an identifier-like node in this language.
func (l *Language) IsIdentifier(kind string) bool { return l.identifierKinds[kind] }

// IsLiteral reports whether kind is a literal node in this language.
func (l *Language) IsLiteral(kind string) bool { return l.literalKinds[kind] }

// IsComment reports whether kind is a comment node in this language.
func (l *Language) IsComment(kind string) bool { return l.commentKinds[kind] }

// FoldsCase reports whether leaf nodes of this kind compare case-insensitively.
func (l *Language) FoldsCase(kind string) bool { return l.foldKinds[kind] }

// IsInterpolation reports whether kind embeds code inside a string literal.
func (l *Language) IsInterpolation(kind string) bool { return l.interpolationKinds[kind] }

// IsTransparent reports whether kind is a language-specific single-child
// wrapper that matching looks through.
func (l *Language) IsTransparent(kind string) bool { return l.transparentKinds[kind] }

// HasCaseFolding reports whether any node kind compares case-insensitively,
// which makes byte-exact content prefilters unsound for this language.
func (l *Language) HasCaseFolding() bool { return len(l.foldKinds) > 0 }

// SentinelSep returns the separator used in metavariable sentinel identifiers.
func (l *Language) SentinelSep() string {
	if l.sentinelSep == "" {
		return "_"
	}
	return l.sentinelSep
}

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
		Name:               "python",
		Aliases:            []string{"py"},
		Extensions:         []string{".py", ".pyi"},
		Grammar:            python.GetLanguage,
		identifierKinds:    set("identifier"),
		literalKinds:       set("integer", "float", "string", "true", "false", "none"),
		commentKinds:       set("comment"),
		interpolationKinds: set("interpolation"),
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
		Name:               "kotlin",
		Aliases:            []string{"kt", "kts"},
		Extensions:         []string{".kt", ".kts"},
		Grammar:            kotlin.GetLanguage,
		identifierKinds:    set("simple_identifier", "type_identifier"),
		literalKinds:       set("integer_literal", "real_literal", "hex_literal", "bin_literal", "long_literal", "unsigned_literal", "string_literal", "character_literal", "boolean_literal"),
		commentKinds:       set("line_comment", "multiline_comment"),
		interpolationKinds: set("interpolated_expression", "interpolated_identifier"),
	})
	register(&Language{
		Name:               "swift",
		Extensions:         []string{".swift"},
		Grammar:            swift.GetLanguage,
		identifierKinds:    set("simple_identifier", "type_identifier"),
		literalKinds:       set("integer_literal", "real_literal", "hex_literal", "oct_literal", "bin_literal", "line_string_literal", "multi_line_string_literal", "boolean_literal"),
		commentKinds:       set("comment", "multiline_comment"),
		interpolationKinds: set("interpolated_expression"),
	})
	register(&Language{
		Name:               "typescript",
		Aliases:            []string{"ts"},
		Extensions:         []string{".ts", ".mts", ".cts"},
		Grammar:            typescript.GetLanguage,
		Group:              "typescript",
		identifierKinds:    set("identifier", "property_identifier", "type_identifier", "shorthand_property_identifier"),
		literalKinds:       set("number", "string", "template_string", "regex", "true", "false", "null", "undefined"),
		commentKinds:       set("comment"),
		interpolationKinds: set("template_substitution"),
	})
	register(&Language{
		Name:               "tsx",
		Aliases:            []string{"javascript", "js", "jsx"},
		Extensions:         []string{".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		Grammar:            tsx.GetLanguage,
		Group:              "typescript",
		identifierKinds:    set("identifier", "property_identifier", "type_identifier", "shorthand_property_identifier"),
		literalKinds:       set("number", "string", "template_string", "regex", "true", "false", "null", "undefined"),
		commentKinds:       set("comment"),
		interpolationKinds: set("template_substitution"),
	})

	// Markup and style languages. These are ExplicitOnly: their grammars
	// (HTML especially) parse nearly any text without error, so a rule
	// without an `in` clause would otherwise silently compile for them.
	register(&Language{
		Name:              "html",
		Extensions:        []string{".html", ".htm"},
		Grammar:           html.GetLanguage,
		ExplicitOnly:      true,
		MetaInStrings:     true,
		AllowMissingNodes: true,
		sentinelSep:       "-", // html tag names cannot contain '_'
		identifierKinds:   set("tag_name", "attribute_name"),
		literalKinds:      set("attribute_value", "quoted_attribute_value", "text", "raw_text"),
		commentKinds:      set("comment"),
		foldKinds:         set("tag_name", "attribute_name"),
	})
	register(&Language{
		Name:            "css",
		Extensions:      []string{".css"},
		Grammar:         css.GetLanguage,
		Group:           "css",
		ExplicitOnly:    true,
		MetaInStrings:   true,
		identifierKinds: set("tag_name", "class_name", "id_name", "property_name", "attribute_name", "function_name", "feature_name", "keyword_query", "plain_value", "unit", "namespace_name"),
		literalKinds:    set("string_value", "integer_value", "float_value", "color_value"),
		commentKinds:    set("comment"),
	})
	register(&Language{
		Name:              "scss",
		Extensions:        []string{".scss"},
		Grammar:           scss.GetLanguage,
		Group:             "css",
		ExplicitOnly:      true,
		MetaInStrings:     true,
		MetaUppercaseOnly: true, // $lowercase is an SCSS variable, not a metavariable
		identifierKinds:   set("tag_name", "class_name", "id_name", "property_name", "attribute_name", "function_name", "feature_name", "keyword_query", "plain_value", "unit", "namespace_name", "variable", "identifier"),
		literalKinds:      set("string_value", "integer_value", "float_value", "color_value"),
		commentKinds:      set("comment", "js_comment"),
	})
	// Data languages, ExplicitOnly for the same reason as markup: a Python
	// dict pattern is valid JSON, and nearly anything is valid YAML, so
	// implicit targeting would misfire.
	register(&Language{
		Name:          "json",
		Aliases:       []string{"jsonc"},
		Extensions:    []string{".json", ".jsonc"},
		Grammar:       json.GetLanguage,
		ExplicitOnly:  true,
		MetaInStrings: true,
		MetaQuoteBare: true,
		// string wraps string_content; looking through it lets "$V" (the
		// only way to write a metavariable in JSON) bind any value kind.
		transparentKinds: set("string"),
		identifierKinds:  set("string_content"),
		literalKinds:     set("number", "true", "false", "null"),
		commentKinds:     set("comment"),
	})
	register(&Language{
		Name:            "yaml",
		Aliases:         []string{"yml"},
		Extensions:      []string{".yaml", ".yml"},
		Grammar:         yaml.GetLanguage,
		ExplicitOnly:    true,
		identifierKinds: set("string_scalar"),
		literalKinds:    set("integer_scalar", "float_scalar", "boolean_scalar", "null_scalar", "double_quote_scalar", "single_quote_scalar", "block_scalar", "timestamp"),
		commentKinds:    set("comment"),
	})
	register(&Language{
		Name:                 "xml",
		Extensions:           []string{".xml", ".svg", ".xsd", ".xsl", ".xslt"},
		Grammar:              xml.GetLanguage,
		ExplicitOnly:         true,
		MetaInStrings:        true,
		MetaVariadicTagAttrs: true,
		identifierKinds:      set("Name"),
		literalKinds:         set("AttValue", "CharData", "CData"),
		commentKinds:         set("Comment"),
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
