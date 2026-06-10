package engine

import (
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
)

// compileRule parses a single-rule source and returns Compile's result, so
// tests can assert that a pattern does or does not compile for a grammar.
func compileRule(t *testing.T, ruleSrc string) (*CompiledRule, error) {
	t.Helper()
	rules, err := dsl.Parse("test.lint", ruleSrc)
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	return Compile(rules[0])
}

func TestCSSDeclarationMatchesAnywhere(t *testing.T) {
	// A declaration pattern matches at any nesting depth: top-level rule sets
	// and rule sets inside @media blocks alike.
	rule := "rule r { in css\n pattern { color: $V } }"
	tests := []struct {
		name string
		code string
		n    int
		v    string
	}{
		{"top level", "a { color: red; }", 1, "red"},
		{"nested in media query", "@media (max-width: 600px) { a { color: red } }", 1, "red"},
		{"other property does not match", "a { margin: 0; }", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "css", rule, tt.code)
			wantN(t, fs, tt.n)
			if tt.n > 0 && fs[0].Bindings["V"] != tt.v {
				t.Fatalf("$V = %q, want %q", fs[0].Bindings["V"], tt.v)
			}
		})
	}
}

func TestCSSImportantVariadic(t *testing.T) {
	// $$$V absorbs the multi-token value run; the literal !important must
	// still be present in the target for a match.
	rule := "rule r { in css\n pattern { $P: $$$V !important } }"
	fs := run(t, "css", rule, `
a { margin: 0 1px !important; }
b { margin: 0 1px; }
c { color: red; }`)
	wantN(t, fs, 1)
	if fs[0].Bindings["P"] != "margin" {
		t.Fatalf("$P = %q, want %q", fs[0].Bindings["P"], "margin")
	}
}

func TestCSSMultiValueVariadic(t *testing.T) {
	// A variadic value matches declarations with any number of value tokens.
	rule := "rule r { in css\n pattern { margin: $$$V } }"
	tests := []struct {
		name string
		code string
		n    int
	}{
		{"two values", "a { margin: 0 auto; }", 1},
		{"one value", "a { margin: 0; }", 1},
		{"other property", "a { padding: 0 auto; }", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "css", rule, tt.code)
			wantN(t, fs, tt.n)
		})
	}
}

func TestCSSBlockVariadicDoesNotCompile(t *testing.T) {
	// Documented limitation: a bare variadic as a rule-set body does not
	// parse under the css or scss grammar (the sentinel identifier is not a
	// valid block item), so the pattern fails to compile.
	tests := []struct {
		name string
		rule string
	}{
		{"css class selector block", "rule r { in css\n pattern { .btn { $$$ } } }"},
		{"scss class selector block", "rule r { in scss\n pattern { .btn { $$$ } } }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := compileRule(t, tt.rule); err == nil {
				t.Fatal("expected compile error for block variadic, got none")
			}
		})
	}
}

func TestCSSClassSelectorExactArity(t *testing.T) {
	// Without a variadic, a rule-set pattern requires the exact declaration
	// count, and the class selector must match.
	rule := "rule r { in css\n pattern { .legacy { color: $_ } } }"
	tests := []struct {
		name string
		code string
		n    int
	}{
		{"exact rule set", ".legacy { color: blue; }", 1},
		{"different class", ".modern { color: blue; }", 0},
		{"extra declaration", ".legacy { color: blue; margin: 0; }", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "css", rule, tt.code)
			wantN(t, fs, tt.n)
		})
	}
}

func TestCSSColorValueComparesFullText(t *testing.T) {
	// Regression: color_value exposes no named children (the hex digits are
	// not a node), so literal colors must compare by full source text.
	// Previously 'color: #fff' matched any color.
	rule := "rule r { in css\n pattern { color: #fff } }"
	tests := []struct {
		name string
		code string
		n    int
	}{
		{"same color", "a { color: #fff; }", 1},
		{"different color", "a { color: #000; }", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "css", rule, tt.code)
			wantN(t, fs, tt.n)
		})
	}
}

func TestCSSStringValueComparesFullText(t *testing.T) {
	// Regression: string_value's content is not a child node (only the two
	// quote tokens are), so string literals must compare by full text.
	rule := "rule r { in css\n pattern { content: \"a\" } }"
	tests := []struct {
		name string
		code string
		n    int
	}{
		{"same string", `x::before { content: "a"; }`, 1},
		{"different string", `x::before { content: "b"; }`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "css", rule, tt.code)
			wantN(t, fs, tt.n)
		})
	}
}

func TestSCSSVariableAsValue(t *testing.T) {
	// An uppercase metavariable binds an SCSS variable value node whole,
	// including its '$' sigil.
	rule := "rule r { in scss\n pattern { color: $V } }"
	fs := run(t, "scss", rule, ".x { color: $primary; }")
	wantN(t, fs, 1)
	if fs[0].Bindings["V"] != "$primary" {
		t.Fatalf("$V = %q, want %q", fs[0].Bindings["V"], "$primary")
	}
}

func TestSCSSLowercaseDollarIsLiteral(t *testing.T) {
	// In scss, $lowercase is the language's own variable syntax, not a
	// metavariable: 'color: $primary' matches only that exact variable.
	rule := "rule r { in scss\n pattern { color: $primary } }"
	tests := []struct {
		name string
		code string
		n    int
	}{
		{"same variable", ".x { color: $primary; }", 1},
		{"different variable", ".x { color: $secondary; }", 0},
		{"plain value", ".x { color: red; }", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "scss", rule, tt.code)
			wantN(t, fs, tt.n)
		})
	}
}

func TestSCSSNestedHoverRule(t *testing.T) {
	// '&:hover { $$$ }' cannot compile (block variadic, see
	// TestCSSBlockVariadicDoesNotCompile); an exact-arity nested rule pattern
	// works and matches the rule set nested inside another rule set.
	if _, err := compileRule(t, "rule r { in scss\n pattern { &:hover { $$$ } } }"); err == nil {
		t.Fatal("expected compile error for '&:hover { $$$ }', got none")
	}
	rule := "rule r { in scss\n pattern { &:hover { color: $V } } }"
	fs := run(t, "scss", rule, `
.x {
  &:hover { color: red; }
  &:focus { color: blue; }
  .y { color: green; }
}`)
	wantN(t, fs, 1)
	if fs[0].Bindings["V"] != "red" {
		t.Fatalf("$V = %q, want %q", fs[0].Bindings["V"], "red")
	}
}

func TestSCSSInclude(t *testing.T) {
	// include_statement requires its ';' to parse, so the pattern must spell
	// it: '@include rounded' (no semicolon) fails to compile.
	if _, err := compileRule(t, "rule r { in scss\n pattern { @include rounded } }"); err == nil {
		t.Fatal("expected compile error for '@include rounded' without semicolon, got none")
	}

	literal := "rule r { in scss\n pattern { @include rounded; } }"
	fs := run(t, "scss", literal, ".x { @include rounded; @include square; }")
	wantN(t, fs, 1)

	meta := "rule r { in scss\n pattern { @include $NAME; } }"
	fs = run(t, "scss", meta, ".x { @include rounded; @include square; }")
	wantN(t, fs, 2)
	if fs[0].Bindings["NAME"] != "rounded" || fs[1].Bindings["NAME"] != "square" {
		t.Fatalf("$NAME bindings = %q, %q; want rounded, square",
			fs[0].Bindings["NAME"], fs[1].Bindings["NAME"])
	}
}

func TestCSSAndSCSSCompileUnderOwnGrammar(t *testing.T) {
	// css and scss are separate languages: an explicit `in` clause compiles
	// the rule for exactly that grammar (file routing is runner-level).
	tests := []struct {
		name string
		rule string
		lang string
	}{
		{"css rule", "rule r { in css\n pattern { color: $V } }", "css"},
		{"scss rule", "rule r { in scss\n pattern { color: $V } }", "scss"},
		{"scss-only value syntax", "rule r { in scss\n pattern { color: $primary } }", "scss"},
		{"scss-only include syntax", "rule r { in scss\n pattern { @include $NAME; } }", "scss"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := compileRule(t, tt.rule)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			langs := cr.Languages()
			if len(langs) != 1 || langs[0].Name != tt.lang {
				names := make([]string, len(langs))
				for i, l := range langs {
					names[i] = l.Name
				}
				t.Fatalf("compiled for %v, want exactly [%s]", names, tt.lang)
			}
		})
	}

	// Both languages target one rule when named together.
	cr, err := compileRule(t, "rule r { in css, scss\n pattern { color: $V } }")
	if err != nil {
		t.Fatalf("compile css+scss: %v", err)
	}
	if len(cr.Languages()) != 2 {
		t.Fatalf("expected 2 languages for 'in css, scss', got %d", len(cr.Languages()))
	}
}
